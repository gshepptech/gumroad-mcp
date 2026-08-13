package gum

import (
	"os/exec"
	"strings"
	"testing"
)

func TestClassifyTiers(t *testing.T) {
	cases := []struct {
		argv []string
		want Tier
	}{
		{[]string{"products", "list"}, Read},
		{[]string{"products", "create"}, Write},
		{[]string{"products", "delete"}, Danger},
		{[]string{"sales", "refund"}, Danger},
		{[]string{"sales", "list"}, Read},
		{[]string{"emails", "create"}, Write},
		{[]string{"emails", "send"}, Danger},
		{[]string{"licenses", "verify"}, Write}, // consumes a seat unless --no-increment
		{[]string{"licenses", "rotate"}, Danger},
		{[]string{"workflows", "add-email"}, Danger},
		{[]string{"webhooks", "delete"}, Write},
	}
	for _, tc := range cases {
		d := Classify(tc.argv, false, true)
		if d.Command.Tier != tc.want {
			t.Errorf("%v classified as %s, want %s", tc.argv, d.Command.Tier, tc.want)
		}
	}
}

// TestLongestPrefixWins guards the three-word commands: `products page clear` is
// irreversible while `products page publish` is an ordinary write, so a
// shortest-match would misclassify one of them.
func TestLongestPrefixWins(t *testing.T) {
	cases := map[string]Tier{
		"products page preview":     Read,
		"products page publish":     Write,
		"products page clear":       Danger,
		"products covers add":       Write,
		"products covers remove":    Danger,
		"products content get":      Read,
		"products content set":      Danger,
		"products thumbnail set":    Write,
		"products thumbnail remove": Danger,
		"user page publish":         Write,
		"user page clear":           Danger,
	}
	for name, want := range cases {
		d := Classify(strings.Fields(name), false, true)
		if !d.Allowed {
			t.Errorf("%s was refused: %s", name, d.Reason)
			continue
		}
		if d.Command.Tier != want {
			t.Errorf("%s classified as %s, want %s", name, d.Command.Tier, want)
		}
		if d.Command.Name() != name {
			t.Errorf("%s matched the shorter command %q", name, d.Command.Name())
		}
	}
}

// TestFlagsAreNotPartOfTheCommand: arguments must not shift classification.
func TestFlagsAreNotPartOfTheCommand(t *testing.T) {
	d := Classify([]string{"sales", "list", "--after", "2026-01-01", "--all"}, false, false)
	if !d.Allowed || d.Command.Name() != "sales list" {
		t.Fatalf("flags broke classification: matched %q, allowed=%v", d.Command.Name(), d.Allowed)
	}

	// A danger command stays danger however it is dressed up.
	d = Classify([]string{"sales", "refund", "SALE", "--amount", "5"}, false, false)
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
		d := Classify(argv, false, true)
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
		if d := Classify(argv, false, true); d.Allowed {
			t.Errorf("%v was allowed", argv)
		}
	}
	if d := Classify([]string{"auth", "status"}, true, false); !d.Allowed {
		t.Errorf("auth status should be readable: %s", d.Reason)
	}
}

func TestReadOnlyRefusesEveryMutation(t *testing.T) {
	for _, c := range policy {
		if c.Tier == Forbidden {
			continue
		}
		d := Classify(c.Path, true, true)
		if c.Tier == Read && !d.Allowed {
			t.Errorf("read-only refused the read command %s: %s", c.Name(), d.Reason)
		}
		if c.Tier != Read && d.Allowed {
			t.Errorf("read-only allowed the %s command %s", c.Tier, c.Name())
		}
	}
}

func TestDangerNeedsExplicitOptIn(t *testing.T) {
	for _, c := range policy {
		if c.Tier != Danger {
			continue
		}
		if d := Classify(c.Path, false, false); d.Allowed {
			t.Errorf("%s was allowed without --allow-danger", c.Name())
		}
		if d := Classify(c.Path, false, true); !d.Allowed {
			t.Errorf("%s was refused even with --allow-danger: %s", c.Name(), d.Reason)
		}
	}
}

// TestEveryDangerEntryExplainsItself: the refusal text is shown to the model
// and to the user, so an unexplained danger classification is a defect.
func TestEveryDangerEntryExplainsItself(t *testing.T) {
	for _, c := range policy {
		if (c.Tier == Danger || c.Tier == Forbidden) && strings.TrimSpace(c.Why) == "" {
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
		if d := Classify(argv, false, true); d.Allowed {
			t.Errorf("%v was allowed", argv)
		}
	}
}

func TestAvailableRespectsPosture(t *testing.T) {
	ro := Available(true, false)
	for _, c := range ro {
		if c.Tier != Read {
			t.Errorf("read-only listing included %s (%s)", c.Name(), c.Tier)
		}
	}
	def := Available(false, false)
	full := Available(false, true)
	if !(len(ro) < len(def) && len(def) < len(full)) {
		t.Errorf("posture should widen the listing: read-only=%d default=%d full=%d",
			len(ro), len(def), len(full))
	}
}

// TestPolicyCoversTheInstalledCLI reports commands the installed `gumroad` has
// that the policy does not classify. An unclassified command is refused, which
// is safe but invisible — this test is how a maintainer notices upstream grew.
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
			if d := Classify(path, false, true); d.Matched == 0 {
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
		t.Errorf("the installed Gumroad CLI has %d command(s) gloop does not classify, so they are "+
			"silently refused. Add them to policy.go with a tier:\n  %s",
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
	for _, c := range policy {
		if c.Tier == Forbidden {
			continue
		}
		argv := append(append([]string{}, c.Path...), "--dry-run")

		// Allowed under every posture, including read-only.
		for _, posture := range []struct{ readOnly, danger bool }{
			{false, false}, {false, true}, {true, false},
		} {
			d := Classify(argv, posture.readOnly, posture.danger)
			if !d.Allowed {
				t.Errorf("--dry-run on %s was refused (readOnly=%v danger=%v): %s",
					c.Name(), posture.readOnly, posture.danger, d.Reason)
			}
			if !d.DryRun {
				t.Errorf("%s: DryRun flag not set on the decision", c.Name())
			}
		}
	}
}

// TestDryRunDoesNotOpenTheForbiddenTier: previewing an admin command still
// exposes the surface gloop refuses to expose at all.
func TestDryRunDoesNotOpenTheForbiddenTier(t *testing.T) {
	for _, argv := range [][]string{
		{"admin", "users", "suspend", "--dry-run"},
		{"auth", "token", "--dry-run"},
		{"admin", "--dry-run"},
	} {
		if d := Classify(argv, false, true); d.Allowed {
			t.Errorf("%v was allowed by adding --dry-run", argv)
		}
	}
}

// TestRefusalsPointAtDryRun: a refusal that does not name the safe alternative
// leaves the caller guessing.
func TestRefusalsPointAtDryRun(t *testing.T) {
	d := Classify([]string{"sales", "refund", "S1"}, false, false)
	if d.Allowed {
		t.Fatal("sales refund was allowed without --allow-danger")
	}
	if !strings.Contains(d.Reason, "--dry-run") {
		t.Errorf("danger refusal does not mention --dry-run: %s", d.Reason)
	}

	d = Classify([]string{"products", "create"}, true, false)
	if !strings.Contains(d.Reason, "--dry-run") {
		t.Errorf("read-only refusal does not mention --dry-run: %s", d.Reason)
	}
}
