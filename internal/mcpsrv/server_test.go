package mcpsrv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gshepptech/gloop/internal/gum"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// stubCLI writes a fake `gumroad` that records the argv it was called with and
// prints a success envelope. Tests assert on the recorded argv, so nothing
// reaches the real Gumroad API.
func stubCLI(t *testing.T) (*gum.Runner, func() []string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	bin := filepath.Join(dir, "gumroad")

	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + log + "\n" +
		"echo '{\"success\":true,\"products\":[]}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	calls := func() []string {
		b, err := os.ReadFile(log)
		if err != nil {
			return nil // never invoked
		}
		var out []string
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if l != "" {
				out = append(out, l)
			}
		}
		return out
	}
	return &gum.Runner{Path: bin}, calls
}

func connect(t *testing.T, r *gum.Runner, opts Options) *mcp.ClientSession {
	t.Helper()
	srv := New(r, nil, opts)
	ct, st := mcp.NewInMemoryTransports()

	ctx := context.Background()
	go func() {
		if _, err := srv.Connect(ctx, st, nil); err != nil {
			panic(err)
		}
	}()

	cli := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	sess, err := cli.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess
}

func call(t *testing.T, s *mcp.ClientSession, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: transport error: %v", tool, err)
	}
	return res
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestToolsAreRegistered(t *testing.T) {
	r, _ := stubCLI(t)
	sess := connect(t, r, Options{})

	got := map[string]bool{}
	for tool, err := range sess.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(tool.Description) == "" {
			t.Errorf("tool %q has no description", tool.Name)
		}
		got[tool.Name] = true
	}
	for _, want := range []string{"gumroad_run", "gumroad_commands", "gumroad_skill", "gumroad_help"} {
		if !got[want] {
			t.Errorf("tool %q was not registered", want)
		}
	}
}

// TestDangerNeverReachesTheCLIWithoutConfirm is the central safety property:
// the refusal happens before the subprocess is spawned, so nothing can have
// half-happened.
func TestDangerNeverReachesTheCLIWithoutConfirm(t *testing.T) {
	r, calls := stubCLI(t)
	sess := connect(t, r, Options{AllowDanger: true})

	for _, argv := range [][]string{
		{"sales", "refund", "S1"},
		{"products", "delete", "P1"},
		{"emails", "send", "E1"},
		{"licenses", "rotate", "--product", "P1"},
		{"products", "page", "clear", "P1"},
	} {
		res := call(t, sess, "gumroad_run", map[string]any{"command": argv})
		if !res.IsError {
			t.Errorf("%v ran without confirm=true", argv)
		}
		if !strings.Contains(text(res), "confirm=true") {
			t.Errorf("%v refusal did not name the confirm flag: %s", argv, text(res))
		}
	}
	if c := calls(); len(c) > 0 {
		t.Errorf("the CLI was invoked despite every call being refused: %v", c)
	}
}

func TestConfirmedDangerRunsWithYes(t *testing.T) {
	r, calls := stubCLI(t)
	sess := connect(t, r, Options{AllowDanger: true})

	res := call(t, sess, "gumroad_run", map[string]any{
		"command": []string{"sales", "refund", "S1", "--amount", "5"},
		"confirm": true,
	})
	if res.IsError {
		t.Fatalf("confirmed refund was refused: %s", text(res))
	}

	c := calls()
	if len(c) != 1 {
		t.Fatalf("expected one invocation, got %v", c)
	}
	if !strings.Contains(c[0], "sales refund S1 --amount 5") {
		t.Errorf("argv lost the caller's arguments: %q", c[0])
	}
	for _, want := range []string{"--yes", "--json", "--no-input", "--quiet"} {
		if !strings.Contains(c[0], want) {
			t.Errorf("argv missing %s: %q", want, c[0])
		}
	}
}

// TestReadsAreNotAutoConfirmed: --yes on a read would be noise, and its
// presence would suggest the gate ran when it did not.
func TestWriteIsAutoConfirmedButReadIsNot(t *testing.T) {
	r, calls := stubCLI(t)
	sess := connect(t, r, Options{})

	call(t, sess, "gumroad_run", map[string]any{"command": []string{"products", "list"}})
	call(t, sess, "gumroad_run", map[string]any{"command": []string{"products", "create", "--name", "X"}})

	c := calls()
	if len(c) != 2 {
		t.Fatalf("expected two invocations, got %v", c)
	}
	if strings.Contains(c[0], "--yes") {
		t.Errorf("a read command was sent --yes: %q", c[0])
	}
	if !strings.Contains(c[1], "--yes") {
		t.Errorf("a write command was not auto-confirmed, so --no-input would dead-end it: %q", c[1])
	}
}

func TestReadOnlyRefusesWrites(t *testing.T) {
	r, calls := stubCLI(t)
	sess := connect(t, r, Options{ReadOnly: true})

	if res := call(t, sess, "gumroad_run", map[string]any{"command": []string{"products", "list"}}); res.IsError {
		t.Errorf("read-only refused a read: %s", text(res))
	}
	res := call(t, sess, "gumroad_run", map[string]any{"command": []string{"products", "create", "--name", "X"}})
	if !res.IsError {
		t.Error("read-only allowed a write")
	}
	if !strings.Contains(text(res), "read-only") {
		t.Errorf("refusal did not explain the posture: %s", text(res))
	}
	if c := calls(); len(c) != 1 {
		t.Errorf("expected only the read to reach the CLI, got %v", c)
	}
}

// TestCallerCannotSmuggleFlags: gloop owns --json/--no-input/--quiet, and --yes
// is the whole point of the gate, so accepting either from the model would
// hand it the bypass.
func TestCallerCannotSmuggleFlags(t *testing.T) {
	r, calls := stubCLI(t)
	sess := connect(t, r, Options{AllowDanger: true})

	for _, argv := range [][]string{
		{"products", "delete", "P1", "--yes"},
		{"products", "list", "-y"},
		{"products", "list", "--json"},
		{"products", "list", "--no-input"},
	} {
		res := call(t, sess, "gumroad_run", map[string]any{"command": argv})
		if !res.IsError {
			t.Errorf("%v was accepted", argv)
		}
	}
	if c := calls(); len(c) > 0 {
		t.Errorf("a smuggled flag reached the CLI: %v", c)
	}
}

func TestAdminIsUnreachable(t *testing.T) {
	r, calls := stubCLI(t)
	sess := connect(t, r, Options{AllowDanger: true})

	res := call(t, sess, "gumroad_run", map[string]any{
		"command": []string{"admin", "users", "suspend", "--email", "x@y.com"},
		"confirm": true,
	})
	if !res.IsError {
		t.Fatal("admin was reachable with confirm=true")
	}
	if c := calls(); len(c) > 0 {
		t.Errorf("admin reached the CLI: %v", c)
	}

	// Nor through the help tool.
	res = call(t, sess, "gumroad_help", map[string]any{"command": []string{"admin", "users"}})
	if !res.IsError {
		t.Error("gumroad_help exposed the admin surface")
	}
}

func TestCommandsListingReflectsPosture(t *testing.T) {
	r, _ := stubCLI(t)

	ro := text(call(t, connect(t, r, Options{ReadOnly: true}), "gumroad_commands", map[string]any{}))
	if strings.Contains(ro, "sales refund") {
		t.Error("read-only listing advertised sales refund")
	}
	if !strings.Contains(ro, "products list") {
		t.Error("read-only listing omitted products list")
	}

	full := text(call(t, connect(t, r, Options{AllowDanger: true}), "gumroad_commands", map[string]any{}))
	if !strings.Contains(full, "sales refund") {
		t.Error("full listing omitted sales refund")
	}
	if !strings.Contains(full, "confirm=true") {
		t.Error("full listing did not flag which commands need confirmation")
	}
}

func TestEmptyCommandIsRejected(t *testing.T) {
	r, _ := stubCLI(t)
	sess := connect(t, r, Options{})
	res := call(t, sess, "gumroad_run", map[string]any{"command": []string{}})
	if !res.IsError {
		t.Error("an empty command was accepted")
	}
}

// TestCLIFailureSurfacesAsToolError keeps the CLI's own message readable to the
// model rather than collapsing into a transport failure.
func TestCLIFailureSurfacesAsToolError(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "gumroad")
	script := "#!/bin/sh\n" +
		"echo '{\"success\":false,\"error\":{\"type\":\"auth_error\",\"code\":\"not_authenticated\"," +
		"\"message\":\"You are not logged in.\"}}'\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	sess := connect(t, &gum.Runner{Path: bin}, Options{})
	res := call(t, sess, "gumroad_run", map[string]any{"command": []string{"products", "list"}})
	if !res.IsError {
		t.Fatal("a failing CLI call was reported as success")
	}
	body := text(res)
	if !strings.Contains(body, "You are not logged in.") {
		t.Errorf("lost the CLI's message: %s", body)
	}
	if !strings.Contains(body, "gumroad auth login") {
		t.Errorf("auth failure did not tell the user how to fix it: %s", body)
	}
}

// TestDryRunRunsWithoutConfirmAndWithoutYes covers the fix for a real defect:
// gloop used to refuse `products content set --dry-run` outright, even though a
// dry run sends nothing to Gumroad.
func TestDryRunRunsWithoutConfirmAndWithoutYes(t *testing.T) {
	r, calls := stubCLI(t)
	sess := connect(t, r, Options{}) // no --allow-danger

	res := call(t, sess, "gumroad_run", map[string]any{
		"command": []string{"products", "content", "set", "P1", "c.json", "--dry-run"},
	})
	if res.IsError {
		t.Fatalf("a dry run of an irreversible command was refused: %s", text(res))
	}

	c := calls()
	if len(c) != 1 {
		t.Fatalf("expected one invocation, got %v", c)
	}
	if !strings.Contains(c[0], "--dry-run") {
		t.Errorf("--dry-run was dropped: %q", c[0])
	}
	if strings.Contains(c[0], "--yes") {
		t.Errorf("a dry run was auto-confirmed, which muddies what actually ran: %q", c[0])
	}
}

func TestDryRunWorksOnAReadOnlyServer(t *testing.T) {
	r, calls := stubCLI(t)
	sess := connect(t, r, Options{ReadOnly: true})

	res := call(t, sess, "gumroad_run", map[string]any{
		"command": []string{"products", "update", "P1", "--name", "X", "--dry-run"},
	})
	if res.IsError {
		t.Fatalf("read-only server refused a dry run: %s", text(res))
	}
	if len(calls()) != 1 {
		t.Error("the dry run did not reach the CLI")
	}
}

func TestDryRunStillCannotReachAdmin(t *testing.T) {
	r, calls := stubCLI(t)
	sess := connect(t, r, Options{AllowDanger: true})

	res := call(t, sess, "gumroad_run", map[string]any{
		"command": []string{"admin", "users", "suspend", "--dry-run"},
	})
	if !res.IsError {
		t.Error("--dry-run opened the admin surface")
	}
	if c := calls(); len(c) > 0 {
		t.Errorf("admin reached the CLI: %v", c)
	}
}

// TestPromotedCommandNeedsConfirm checks the MCP gate reads the EFFECTIVE tier.
// `products update` is a write, but with --file it is irreversible
// (antiwork/gumroad-cli#205), and gating on the command's own tier would let it
// straight through.
func TestPromotedCommandNeedsConfirm(t *testing.T) {
	r, calls := stubCLI(t)
	sess := connect(t, r, Options{AllowDanger: true})

	// Ordinary edit: runs, auto-confirmed.
	res := call(t, sess, "gumroad_run", map[string]any{
		"command": []string{"products", "update", "P1", "--name", "X"},
	})
	if res.IsError {
		t.Fatalf("an ordinary product edit was refused: %s", text(res))
	}

	// Same command with the destructive flag: refused without confirm.
	res = call(t, sess, "gumroad_run", map[string]any{
		"command": []string{"products", "update", "P1", "--file", "./a.zip"},
	})
	if !res.IsError {
		t.Error("products update --file ran without confirm=true")
	}
	if !strings.Contains(text(res), "--file") {
		t.Errorf("refusal does not name the flag that caused it: %s", text(res))
	}

	// And with confirm it runs.
	res = call(t, sess, "gumroad_run", map[string]any{
		"command": []string{"products", "update", "P1", "--file", "./a.zip"},
		"confirm": true,
	})
	if res.IsError {
		t.Fatalf("confirmed call was refused: %s", text(res))
	}

	c := calls()
	if len(c) != 2 {
		t.Fatalf("expected two invocations (the refused one must not run), got %v", c)
	}
}
