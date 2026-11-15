package gumroadcli

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/gshepptech/gumroad-mcp/pkg/gate"
)

func TestClassifyTiers(t *testing.T) {
	cases := []struct {
		argv []string
		want gate.Tier
	}{
		{[]string{"products", "list"}, gate.Read},
		{[]string{"products", "create"}, gate.Write},
		{[]string{"products", "delete"}, gate.Danger},
		{[]string{"sales", "refund"}, gate.Danger},
		{[]string{"sales", "list"}, gate.Read},
		{[]string{"emails", "create"}, gate.Write},
		{[]string{"emails", "send"}, gate.Danger},
		{[]string{"licenses", "verify"}, gate.Write}, // consumes a seat unless --no-increment
		{[]string{"licenses", "rotate"}, gate.Danger},
		{[]string{"workflows", "add-email"}, gate.Danger},
		{[]string{"webhooks", "delete"}, gate.Write},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.argv, " "), func(t *testing.T) {
			d := Policy.Classify(tc.argv, false, true)
			if d.Command.Tier != tc.want {
				t.Errorf("classified as %s, want %s", d.Command.Tier, tc.want)
			}
		})
	}
}

// TestLongestPrefixWins guards the three-word commands: `products page clear` is
// irreversible while `products page publish` is an ordinary write, so a
// shortest-match would misclassify one of them.
func TestLongestPrefixWins(t *testing.T) {
	cases := map[string]gate.Tier{
		"products page preview":     gate.Read,
		"products page publish":     gate.Danger,
		"products page clear":       gate.Danger,
		"products covers add":       gate.Write,
		"products covers remove":    gate.Danger,
		"products content get":      gate.Read,
		"products content set":      gate.Danger,
		"products thumbnail set":    gate.Write,
		"products thumbnail remove": gate.Danger,
		"user page publish":         gate.Danger,
		"user page clear":           gate.Danger,
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			d := Policy.Classify(strings.Fields(name), false, true)
			if !d.Allowed {
				t.Fatalf("refused: %s", d.Reason)
			}
			if d.Command.Tier != want {
				t.Errorf("classified as %s, want %s", d.Command.Tier, want)
			}
			if d.Command.Name() != name {
				t.Errorf("matched the shorter command %q", d.Command.Name())
			}
		})
	}
}

// TestFlagsAreNotPartOfTheCommand: arguments must not shift classification.
func TestFlagsAreNotPartOfTheCommand(t *testing.T) {
	d := Policy.Classify([]string{"sales", "list", "--after", "2026-01-01", "--all"}, false, false)
	if !d.Allowed || d.Command.Name() != "sales list" {
		t.Fatalf("flags broke classification: matched %q, allowed=%v", d.Command.Name(), d.Allowed)
	}

	// A danger command stays danger however it is dressed up.
	d = Policy.Classify([]string{"sales", "refund", "SALE", "--amount", "5"}, false, false)
	if d.Allowed {
		t.Error("sales refund was allowed without --allow-danger")
	}
}

// TestAdminIsBlockedAtEveryDepth: only `admin` is listed, but nothing beneath it
// may run either.
func TestAdminIsBlockedAtEveryDepth(t *testing.T) {
	for _, argv := range [][]string{
		{"admin"},
		{"admin", "users", "suspend"},
		{"admin", "users", "refund-all-for-fraud"},
		{"admin", "payouts", "scheduled", "create"},
	} {
		d := Policy.Classify(argv, false, true)
		if d.Allowed {
			t.Errorf("%v was allowed; the admin surface must never be proxied", argv)
		}
		if !strings.Contains(d.Reason, "never available") {
			t.Errorf("%v refusal did not say it is permanently blocked: %s", argv, d.Reason)
		}
	}
}

// TestCredentialCommandsAreBlocked keeps the token out of an agent's reach.
func TestCredentialCommandsAreBlocked(t *testing.T) {
	for _, argv := range [][]string{
		{"auth", "token"}, {"auth", "login"}, {"auth", "logout"},
	} {
		if d := Policy.Classify(argv, false, true); d.Allowed {
			t.Errorf("%v was allowed", argv)
		}
	}
	if d := Policy.Classify([]string{"auth", "status"}, true, false); !d.Allowed {
		t.Errorf("auth status should be readable: %s", d.Reason)
	}
}

func TestReadOnlyRefusesEveryMutation(t *testing.T) {
	for _, c := range Commands {
		if c.Tier == gate.Forbidden {
			continue
		}
		d := Policy.Classify(c.Path, true, true)
		if c.Tier == gate.Read && !d.Allowed {
			t.Errorf("read-only refused the read command %s: %s", c.Name(), d.Reason)
		}
		if c.Tier != gate.Read && d.Allowed {
			t.Errorf("read-only allowed the %s command %s", c.Tier, c.Name())
		}
	}
}

func TestDangerNeedsExplicitOptIn(t *testing.T) {
	for _, c := range Commands {
		if c.Tier != gate.Danger {
			continue
		}
		if d := Policy.Classify(c.Path, false, false); d.Allowed {
			t.Errorf("%s was allowed without --allow-danger", c.Name())
		}
		if d := Policy.Classify(c.Path, false, true); !d.Allowed {
			t.Errorf("%s was refused even with --allow-danger: %s", c.Name(), d.Reason)
		}
	}
}

// TestEveryDangerEntryExplainsItself: the refusal text is shown to the model
// and to the user, so an unexplained danger classification is a defect.
func TestEveryDangerEntryExplainsItself(t *testing.T) {
	for _, c := range Commands {
		if (c.Tier == gate.Danger || c.Tier == gate.Forbidden) && strings.TrimSpace(c.Why) == "" {
			t.Errorf("%s is %s but carries no explanation", c.Name(), c.Tier)
		}
	}
}

func TestUnknownCommandsAreRefused(t *testing.T) {
	for _, argv := range [][]string{
		{"frobnicate"},
		{"products", "frobnicate"},
		{},
		{"--json"},
	} {
		if d := Policy.Classify(argv, false, true); d.Allowed {
			t.Errorf("%v was allowed", argv)
		}
	}
}

func TestAvailableRespectsPosture(t *testing.T) {
	ro := Policy.Available(true, false)
	for _, c := range ro {
		if c.Tier != gate.Read {
			t.Errorf("read-only listing included %s (%s)", c.Name(), c.Tier)
		}
	}
	def := Policy.Available(false, false)
	full := Policy.Available(false, true)
	if len(ro) >= len(def) || len(def) >= len(full) {
		t.Errorf("posture should widen the listing: read-only=%d default=%d full=%d",
			len(ro), len(def), len(full))
	}
}

// TestPolicyCoversTheInstalledCLI reports commands the installed `gumroad` has
// that the policy does not classify. An unclassified command is refused, which
// is safe but invisible. This test is how a maintainer notices upstream grew.
//
// It skips when the CLI is absent, so it does not fail on a machine without it.
func TestPolicyCoversTheInstalledCLI(t *testing.T) {
	if _, err := exec.LookPath(Binary); err != nil {
		t.Skip("gumroad CLI not installed")
	}

	var missing []string
	var walk func(path []string, depth int)
	walk = func(path []string, depth int) {
		if depth > 3 {
			return
		}
		subs := subcommands(t, path)
		if len(subs) == 0 {
			if len(path) == 0 {
				return
			}
			if d := Policy.Classify(path, false, true); d.Matched == 0 {
				missing = append(missing, strings.Join(path, " "))
			}
			return
		}
		for _, s := range subs {
			walk(append(append([]string{}, path...), s), depth+1)
		}
	}
	walk(nil, 0)

	if len(missing) > 0 {
		t.Errorf("the installed Gumroad CLI has %d command(s) gumroad-mcp does not classify, so they are "+
			"silently refused. Add them to Commands with a tier:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// subcommands lists a command's children by parsing its --help.
func subcommands(t *testing.T, path []string) []string {
	t.Helper()
	out, err := exec.Command(Binary, append(path, "--help")...).Output()
	if err != nil {
		return nil
	}
	var subs []string
	inBlock := false
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "Available Commands:") {
			inBlock = true
			continue
		}
		if inBlock {
			if strings.TrimSpace(line) == "" {
				break
			}
			f := strings.Fields(line)
			if len(f) == 0 {
				break
			}
			switch f[0] {
			case "help", "completion":
				continue
			}
			subs = append(subs, f[0])
		}
	}
	return subs
}

// TestDryRunDowngradesToRead: a preview prints the request and stops, so
// refusing it only pushes a caller towards running the real thing blind.
func TestDryRunDowngradesToRead(t *testing.T) {
	for _, c := range Commands {
		if c.Tier == gate.Forbidden {
			continue
		}
		argv := append(append([]string{}, c.Path...), "--dry-run")

		// Allowed under every posture, including read-only.
		for _, posture := range []struct{ readOnly, danger bool }{
			{false, false}, {false, true}, {true, false},
		} {
			d := Policy.Classify(argv, posture.readOnly, posture.danger)
			if !d.Allowed {
				t.Errorf("--dry-run on %s was refused (readOnly=%v danger=%v): %s",
					c.Name(), posture.readOnly, posture.danger, d.Reason)
			}
			if !d.Preview {
				t.Errorf("%s: DryRun flag not set on the decision", c.Name())
			}
		}
	}
}

// TestDryRunDoesNotOpenTheForbiddenTier: previewing an admin command still
// exposes the surface gumroad-mcp refuses to expose at all.
func TestDryRunDoesNotOpenTheForbiddenTier(t *testing.T) {
	for _, argv := range [][]string{
		{"admin", "users", "suspend", "--dry-run"},
		{"auth", "token", "--dry-run"},
		{"admin", "--dry-run"},
	} {
		if d := Policy.Classify(argv, false, true); d.Allowed {
			t.Errorf("%v was allowed by adding --dry-run", argv)
		}
	}
}

// TestRefusalsPointAtDryRun: a refusal that does not name the safe alternative
// leaves the caller guessing.
func TestRefusalsPointAtDryRun(t *testing.T) {
	d := Policy.Classify([]string{"sales", "refund", "S1"}, false, false)
	if d.Allowed {
		t.Fatal("sales refund was allowed without --allow-danger")
	}
	if !strings.Contains(d.Reason, "--dry-run") {
		t.Errorf("danger refusal does not mention --dry-run: %s", d.Reason)
	}

	d = Policy.Classify([]string{"products", "create"}, true, false)
	if !strings.Contains(d.Reason, "--dry-run") {
		t.Errorf("read-only refusal does not mention --dry-run: %s", d.Reason)
	}
}

// TestFlagPromotesToDanger: `products update` is reversible, but
// `products update --file` orphans content that has no version history
// (antiwork/gumroad-cli#205). The command is not the unit of risk; the flag is.
func TestFlagPromotesToDanger(t *testing.T) {
	ordinary := Policy.Classify([]string{"products", "update", "P1", "--name", "X"}, false, false)
	if !ordinary.Allowed {
		t.Errorf("an ordinary product edit was refused: %s", ordinary.Reason)
	}
	if ordinary.Tier != gate.Write {
		t.Errorf("ordinary edit is tier %s, want write", ordinary.Tier)
	}

	for _, argv := range [][]string{
		{"products", "update", "P1", "--file", "./a.zip"},
		{"products", "update", "P1", "--file=./a.zip"},
		{"products", "update", "P1", "--custom-html", "./p.html"},
	} {
		d := Policy.Classify(argv, false, false)
		if d.Allowed {
			t.Errorf("%v was allowed without --allow-danger", argv)
		}
		if d.Tier != gate.Danger {
			t.Errorf("%v is tier %s, want danger", argv, d.Tier)
		}
		if d.PromotedBy == "" {
			t.Errorf("%v did not record which flag promoted it", argv)
		}
		if !strings.Contains(d.Reason, d.PromotedBy) {
			t.Errorf("%v refusal does not name the flag: %s", argv, d.Reason)
		}
	}

	// With the tier enabled it is reachable again, and still marked danger so
	// the caller is made to confirm.
	d := Policy.Classify([]string{"products", "update", "P1", "--file", "./a.zip"}, false, true)
	if !d.Allowed || d.Tier != gate.Danger {
		t.Errorf("with --allow-danger: allowed=%v tier=%s, want true/danger", d.Allowed, d.Tier)
	}
}

func TestPromotedCommandsStillPreviewable(t *testing.T) {
	d := Policy.Classify([]string{"products", "update", "P1", "--file", "./a.zip", "--dry-run"}, true, false)
	if !d.Allowed {
		t.Errorf("a dry run of a promoted command was refused: %s", d.Reason)
	}
}

// TestPairedCommandsShareATier: commands that replace or remove a live public
// page with no version history must carry the same tier.
func TestPairedCommandsShareATier(t *testing.T) {
	pairs := [][2]string{
		{"products page publish", "products page clear"},
		{"user page publish", "user page clear"},
		{"pages push", "products page clear"},
	}
	for _, p := range pairs {
		a, aok := byName(p[0])
		b, bok := byName(p[1])
		if !aok || !bok {
			t.Fatalf("policy is missing %q or %q", p[0], p[1])
		}
		if a.Tier != b.Tier {
			t.Errorf("%q is %s but %q is %s. Both replace or remove a live page "+
				"with no version history, so they cannot differ", p[0], a.Tier, p[1], b.Tier)
		}
	}
}

// TestFlagPromotionsAreExplained: an unexplained promotion is a refusal the
// caller cannot act on.
func TestFlagPromotionsAreExplained(t *testing.T) {
	for _, c := range Commands {
		if len(c.DangerFlags) > 0 && strings.TrimSpace(c.DangerFlagWhy) == "" {
			t.Errorf("%s promotes on %v but carries no explanation", c.Name(), c.DangerFlags)
		}
	}
}

// byName finds a classified command by its space-joined name.
func byName(name string) (gate.Command, bool) {
	for _, c := range Commands {
		if c.Name() == name {
			return c, true
		}
	}
	return gate.Command{}, false
}
