package gate_test

import (
	"strings"
	"testing"

	"github.com/gshepptech/gumroad-mcp/pkg/gate"
)

// A synthetic CLI, deliberately not Gumroad: the package under test must not
// know anything about a particular tool.
func fixture(t *testing.T, opts ...gate.Option) *gate.Policy {
	t.Helper()
	p, err := gate.NewPolicy([]gate.Command{
		{Path: []string{"thing", "list"}, Tier: gate.Read},
		{Path: []string{"thing", "edit"}, Tier: gate.Write,
			DangerFlags:   []string{"--replace"},
			DangerFlagWhy: "--replace overwrites the body with no history"},
		{Path: []string{"thing", "remove"}, Tier: gate.Danger, Why: "permanently deletes it"},
		{Path: []string{"thing", "page", "show"}, Tier: gate.Read},
		{Path: []string{"thing", "page", "wipe"}, Tier: gate.Danger, Why: "clears a live page"},
		{Path: []string{"secrets"}, Tier: gate.Forbidden, Why: "prints credentials"},
	}, opts...)
	if err != nil {
		t.Fatalf("fixture policy rejected: %v", err)
	}
	return p
}

func TestClassifyPostures(t *testing.T) {
	p := fixture(t)
	cases := []struct {
		name             string
		argv             []string
		readOnly, danger bool
		wantAllowed      bool
		wantTier         gate.Tier
	}{
		{"read on read-only", []string{"thing", "list"}, true, false, true, gate.Read},
		{"write on read-only", []string{"thing", "edit"}, true, false, false, gate.Write},
		{"write by default", []string{"thing", "edit"}, false, false, true, gate.Write},
		{"danger by default", []string{"thing", "remove"}, false, false, false, gate.Danger},
		{"danger when allowed", []string{"thing", "remove"}, false, true, true, gate.Danger},
		{"forbidden never", []string{"secrets"}, false, true, false, gate.Forbidden},
		{"forbidden by prefix", []string{"secrets", "show", "all"}, false, true, false, gate.Forbidden},
	}
	for _, tc := range cases {
		d := p.Classify(tc.argv, tc.readOnly, tc.danger)
		if d.Allowed != tc.wantAllowed {
			t.Errorf("%s: allowed=%v want %v (%s)", tc.name, d.Allowed, tc.wantAllowed, d.Reason)
		}
		if d.Tier != tc.wantTier {
			t.Errorf("%s: tier=%s want %s", tc.name, d.Tier, tc.wantTier)
		}
	}
}

func TestLongestPrefixWins(t *testing.T) {
	p := fixture(t)
	d := p.Classify([]string{"thing", "page", "wipe"}, false, true)
	if d.Command.Name() != "thing page wipe" {
		t.Errorf("matched %q, want the three-word command", d.Command.Name())
	}
	if d.Matched != 3 {
		t.Errorf("Matched=%d, want 3", d.Matched)
	}
}

func TestArgumentsCannotChangeTheCommand(t *testing.T) {
	p := fixture(t)
	d := p.Classify([]string{"thing", "list", "--filter", "remove"}, false, false)
	if !d.Allowed || d.Command.Name() != "thing list" {
		t.Errorf("an argument that looks like a subcommand shifted classification: %q allowed=%v",
			d.Command.Name(), d.Allowed)
	}
}

func TestFlagPromotesTier(t *testing.T) {
	p := fixture(t)

	if d := p.Classify([]string{"thing", "edit", "--title", "x"}, false, false); !d.Allowed {
		t.Errorf("an ordinary edit was refused: %s", d.Reason)
	}
	for _, argv := range [][]string{
		{"thing", "edit", "--replace", "f"},
		{"thing", "edit", "--replace=f"},
	} {
		d := p.Classify(argv, false, false)
		if d.Allowed {
			t.Errorf("%v was allowed", argv)
		}
		if d.Tier != gate.Danger || d.PromotedBy != "--replace" {
			t.Errorf("%v: tier=%s promotedBy=%q", argv, d.Tier, d.PromotedBy)
		}
		if !strings.Contains(d.Reason, "no history") {
			t.Errorf("%v: refusal lost the flag's explanation: %s", argv, d.Reason)
		}
	}
}

// TestPreviewIsAlwaysARead: refusing a preview pushes a caller towards running
// the real thing unseen, which is the opposite of the point.
func TestPreviewIsAlwaysARead(t *testing.T) {
	p := fixture(t, gate.WithPreviewFlags("--dry-run"))

	for _, argv := range [][]string{
		{"thing", "remove", "--dry-run"},
		{"thing", "edit", "--replace", "f", "--dry-run"},
		{"thing", "page", "wipe", "--dry-run"},
	} {
		for _, posture := range []struct{ ro, danger bool }{{true, false}, {false, false}, {false, true}} {
			d := p.Classify(argv, posture.ro, posture.danger)
			if !d.Allowed {
				t.Errorf("%v refused at readOnly=%v danger=%v: %s", argv, posture.ro, posture.danger, d.Reason)
			}
			if !d.Preview {
				t.Errorf("%v: Preview not set", argv)
			}
		}
	}

	// But it must not open the forbidden tier.
	if d := p.Classify([]string{"secrets", "--dry-run"}, false, true); d.Allowed {
		t.Error("a preview reached a forbidden command")
	}
}

// TestNoPreviewFlagsMeansNoPreview: a CLI without a preview mechanism must not
// be given one accidentally.
func TestNoPreviewFlagsMeansNoPreview(t *testing.T) {
	p := fixture(t) // no WithPreviewFlags
	d := p.Classify([]string{"thing", "remove", "--dry-run"}, false, false)
	if d.Allowed {
		t.Error("--dry-run was honoured on a policy that never declared it")
	}
	if strings.Contains(d.Reason, "--dry-run") {
		t.Errorf("refusal suggested a preview flag the tool does not have: %s", d.Reason)
	}
}

// TestNewPolicyRejectsUnusableTables: each of these produces a refusal a caller
// cannot act on, or a silently ambiguous match.
func TestNewPolicyRejectsUnusableTables(t *testing.T) {
	cases := map[string][]gate.Command{
		"empty path": {{Path: nil, Tier: gate.Read}},
		"duplicate": {
			{Path: []string{"a"}, Tier: gate.Read},
			{Path: []string{"a"}, Tier: gate.Write},
		},
		"danger without why":    {{Path: []string{"a"}, Tier: gate.Danger}},
		"forbidden without why": {{Path: []string{"a"}, Tier: gate.Forbidden}},
		"promotion without why": {{Path: []string{"a"}, Tier: gate.Write, DangerFlags: []string{"-x"}}},
	}
	for name, cmds := range cases {
		if _, err := gate.NewPolicy(cmds); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestAvailableAndCounts(t *testing.T) {
	p := fixture(t)

	read, write, danger, forbidden := p.Counts()
	if read != 2 || write != 1 || danger != 2 || forbidden != 1 {
		t.Errorf("counts = %d/%d/%d/%d, want 2/1/2/1", read, write, danger, forbidden)
	}

	for _, c := range p.Available(true, false) {
		if c.Tier != gate.Read {
			t.Errorf("read-only listing included %s (%s)", c.Name(), c.Tier)
		}
	}
	if got := len(p.Available(false, true)); got != 5 {
		t.Errorf("full listing has %d commands, want 5 (forbidden excluded)", got)
	}
}

func TestUnknownAndEmptyAreRefused(t *testing.T) {
	p := fixture(t)
	for _, argv := range [][]string{nil, {}, {"--flag"}, {"nope"}, {"thing", "nope"}} {
		if d := p.Classify(argv, false, true); d.Allowed {
			t.Errorf("%v was allowed", argv)
		}
	}
}

// TestRunnerArgsConfirmsOnlyWhatItShould: the confirm flag is the gate's teeth,
// so it must appear exactly when the tier earned it and never on a preview.
func TestRunnerArgsConfirmsOnlyWhatItShould(t *testing.T) {
	r := &gate.Runner{Bin: "x", ConfirmFlag: "--yes"}
	argv := []string{"thing", "remove"}

	cases := []struct {
		name string
		d    gate.Decision
		want bool
	}{
		{"read", gate.Decision{Tier: gate.Read}, false},
		{"write", gate.Decision{Tier: gate.Write}, true},
		{"danger", gate.Decision{Tier: gate.Danger}, true},
		{"preview", gate.Decision{Tier: gate.Danger, Preview: true}, false},
	}
	for _, tc := range cases {
		got := slicesContains(r.Args(tc.d, argv), "--yes")
		if got != tc.want {
			t.Errorf("%s: --yes present=%v want %v", tc.name, got, tc.want)
		}
	}

	// A tool that never prompts gets no confirm flag at all.
	plain := &gate.Runner{Bin: "x"}
	if len(plain.Args(gate.Decision{Tier: gate.Danger}, argv)) != len(argv) {
		t.Error("a runner with no ConfirmFlag appended something")
	}
}

func slicesContains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
