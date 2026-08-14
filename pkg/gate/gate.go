// Package gate decides what an AI agent may run through a command-line tool.
//
// An agent skill can tell a model to be careful. A model can read that and
// proceed anyway. Gumroad's own CLI skill instructs agents to pass `--yes` on
// deletes and refunds. gate takes the other approach: classify every command a
// CLI exposes, and withhold the dangerous ones so there is no decision to make.
// Refusal is structural, and a model cannot instruct its way past it.
//
// A Policy is the classification. A Runner invokes the binary. Neither knows
// anything about a particular CLI: the caller supplies the command table and
// describes how to drive the tool.
//
//	policy, err := gate.NewPolicy(commands, gate.WithPreviewFlags("--dry-run"))
//	d := policy.Classify(argv, readOnly, allowDanger)
//	if !d.Allowed {
//	    return errors.New(d.Reason)
//	}
package gate

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Tier is how much damage a command can do.
type Tier int

const (
	// Read commands cannot change anything.
	Read Tier = iota
	// Write commands change something the operator can undo through the
	// tool's normal interface.
	Write
	// Danger commands are irreversible, move money, or reach someone outside
	// the account. They are withheld unless explicitly enabled, and even then
	// the caller must confirm each one.
	Danger
	// Forbidden commands are never exposed, whatever the settings.
	Forbidden
)

func (t Tier) String() string {
	switch t {
	case Read:
		return "read"
	case Write:
		return "write"
	case Danger:
		return "danger"
	case Forbidden:
		return "forbidden"
	default:
		return fmt.Sprintf("tier(%d)", int(t))
	}
}

// Command is one entry in a Policy.
type Command struct {
	// Path is the subcommand words, e.g. {"sales", "refund"}. A Forbidden
	// entry blocks everything beneath it, so {"admin"} also blocks
	// {"admin", "users", "suspend"}.
	Path []string
	Tier Tier
	// Why explains a Danger or Forbidden classification. It is shown to the
	// model on refusal, so it should say what the damage would be rather than
	// restate the rule.
	Why string

	// DangerFlags promote this command to Danger when any of them appears in
	// the argv. Risk is often a property of the invocation rather than the
	// command: an edit that renames something is reversible, while the same
	// edit replacing its contents may not be.
	DangerFlags []string
	// DangerFlagWhy explains the promotion. Required when DangerFlags is set.
	DangerFlagWhy string
}

// Name is the command's space-joined path.
func (c Command) Name() string { return strings.Join(c.Path, " ") }

// Policy is a complete classification of one CLI's surface.
//
// It is a strict allowlist. A command absent from the table is refused, so a
// new upstream subcommand stays unreachable until someone classifies it. That
// is the intended failure mode: unclassified means unknown, and unknown means
// no.
type Policy struct {
	commands     []Command
	byName       map[string]Command
	blocked      [][]string
	previewFlags []string
	maxDepth     int
}

// Option configures a Policy.
type Option func(*Policy)

// WithPreviewFlags names the flags that make an invocation a preview: the tool
// prints what it would do and stops. A preview changes nothing, so it is
// treated as a Read whatever the command's own tier. Refusing it would only
// push a caller towards running the real thing unseen.
//
// Pass none if the tool has no preview mechanism.
func WithPreviewFlags(flags ...string) Option {
	return func(p *Policy) { p.previewFlags = append(p.previewFlags, flags...) }
}

// NewPolicy validates and compiles a command table.
//
// It rejects duplicates, empty paths, unexplained Danger or Forbidden entries,
// and DangerFlags without an explanation, all of which produce refusals a
// caller cannot act on.
func NewPolicy(commands []Command, opts ...Option) (*Policy, error) {
	p := &Policy{
		commands: slices.Clone(commands),
		byName:   make(map[string]Command, len(commands)),
	}
	for _, o := range opts {
		o(p)
	}

	for _, c := range commands {
		if len(c.Path) == 0 {
			return nil, fmt.Errorf("gate: a command has an empty path")
		}
		name := c.Name()
		if _, dup := p.byName[name]; dup {
			return nil, fmt.Errorf("gate: %q is listed twice", name)
		}
		if (c.Tier == Danger || c.Tier == Forbidden) && strings.TrimSpace(c.Why) == "" {
			return nil, fmt.Errorf("gate: %q is %s but has no Why; the refusal would not say what the damage is", name, c.Tier)
		}
		if len(c.DangerFlags) > 0 && strings.TrimSpace(c.DangerFlagWhy) == "" {
			return nil, fmt.Errorf("gate: %q promotes on %v but has no DangerFlagWhy", name, c.DangerFlags)
		}
		p.byName[name] = c
		if c.Tier == Forbidden {
			p.blocked = append(p.blocked, c.Path)
		}
		p.maxDepth = max(p.maxDepth, len(c.Path))
	}
	return p, nil
}

// MustPolicy is NewPolicy for a table known at build time. It panics on an
// invalid table, which is a programming error rather than a runtime condition.
func MustPolicy(commands []Command, opts ...Option) *Policy {
	p, err := NewPolicy(commands, opts...)
	if err != nil {
		panic(err)
	}
	return p
}

// Decision is the outcome of checking one argv.
type Decision struct {
	// Command is the matched entry. Its Why carries the flag's explanation
	// when a flag caused the promotion.
	Command Command
	// Matched is how many leading argv words formed the command name; zero
	// when nothing matched.
	Matched int
	// Tier is the EFFECTIVE tier, which may exceed Command.Tier when a flag
	// promoted it. Gate on this, not on Command.Tier.
	Tier Tier
	// PromotedBy names the flag that raised the tier, empty when none did.
	PromotedBy string
	// Preview means the argv asked the tool to print rather than act.
	Preview bool
	Allowed bool
	// Reason explains a refusal, in words meant for the model to act on.
	Reason string
}

// Classify resolves an argv against the policy under a given posture.
//
// readOnly refuses everything that is not a Read. allowDanger admits the
// Danger tier; the caller is still expected to require an explicit
// confirmation per call, which is outside gate's remit.
func (p *Policy) Classify(argv []string, readOnly, allowDanger bool) Decision {
	if len(argv) == 0 {
		return Decision{Reason: "no command given"}
	}

	// The command is the leading words. Everything from the first flag on is
	// an argument, and arguments must not change which command you are running.
	words := argv
	for i, w := range argv {
		if strings.HasPrefix(w, "-") {
			words = argv[:i]
			break
		}
	}
	if len(words) == 0 {
		return Decision{Reason: "no command given, only flags"}
	}

	// Wholesale-blocked prefixes first, so a single Forbidden entry covers
	// every subcommand beneath it.
	for _, prefix := range p.blocked {
		if hasPrefix(words, prefix) {
			c := p.byName[strings.Join(prefix, " ")]
			return Decision{
				Command: c, Matched: len(prefix), Tier: Forbidden,
				Reason: fmt.Sprintf("`%s` is never available: %s", c.Name(), c.Why),
			}
		}
	}

	// Longest-prefix match, so a three-word command resolves ahead of any
	// shorter entry that shares its opening words.
	var best Command
	var bestN int
	for n := min(len(words), p.maxDepth); n >= 1; n-- {
		if c, ok := p.byName[strings.Join(words[:n], " ")]; ok {
			best, bestN = c, n
			break
		}
	}
	if bestN == 0 {
		return Decision{Reason: fmt.Sprintf(
			"`%s` is not in the allowlist", strings.Join(words, " "))}
	}

	d := Decision{
		Command: best,
		Matched: bestN,
		Tier:    best.Tier,
		Preview: p.isPreview(argv),
	}
	if flag, ok := dangerFlag(best, argv); ok {
		d.Tier = Danger
		d.PromotedBy = flag
		d.Command.Why = best.DangerFlagWhy
	}

	switch {
	case best.Tier == Forbidden:
		// A preview of a forbidden command still exposes the surface the
		// policy refuses to expose, so it stays blocked.
		d.Reason = fmt.Sprintf("`%s` is never available: %s", best.Name(), best.Why)

	case d.Preview:
		d.Allowed = true

	case readOnly && d.Tier != Read:
		d.Reason = fmt.Sprintf("this server is read-only; `%s` is a %s command%s",
			best.Name(), d.Tier, p.previewHint())

	case d.Tier == Danger && !allowDanger:
		what := "`" + best.Name() + "`"
		if d.PromotedBy != "" {
			what += " with " + d.PromotedBy
		}
		d.Reason = fmt.Sprintf("%s is irreversible (%s) and this server does not allow that tier%s",
			what, d.Command.Why, p.previewHint())

	default:
		d.Allowed = true
	}
	return d
}

func (p *Policy) previewHint() string {
	if len(p.previewFlags) == 0 {
		return ""
	}
	return fmt.Sprintf(". Add %s to preview it", p.previewFlags[0])
}

func (p *Policy) isPreview(argv []string) bool {
	for _, f := range p.previewFlags {
		for _, a := range argv {
			if a == f || strings.HasPrefix(a, f+"=") {
				return true
			}
		}
	}
	return false
}

// dangerFlag reports the first argv flag that promotes c to Danger.
func dangerFlag(c Command, argv []string) (string, bool) {
	for _, want := range c.DangerFlags {
		for _, got := range argv {
			// Matches both `--flag value` and `--flag=value`.
			if got == want || strings.HasPrefix(got, want+"=") {
				return want, true
			}
		}
	}
	return "", false
}

func hasPrefix(argv, prefix []string) bool {
	return len(argv) >= len(prefix) && slices.Equal(argv[:len(prefix)], prefix)
}

// Available lists the commands a server with this posture will run, sorted by
// name. Forbidden entries never appear.
func (p *Policy) Available(readOnly, allowDanger bool) []Command {
	var out []Command
	for _, c := range p.commands {
		switch {
		case c.Tier == Forbidden,
			readOnly && c.Tier != Read,
			c.Tier == Danger && !allowDanger:
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Commands returns every entry, including Forbidden ones.
func (p *Policy) Commands() []Command { return slices.Clone(p.commands) }

// PreviewFlags returns the flags that mark an invocation as a preview.
func (p *Policy) PreviewFlags() []string { return slices.Clone(p.previewFlags) }

// Counts summarises the policy, for a startup banner or a doctor command.
func (p *Policy) Counts() (read, write, danger, forbidden int) {
	for _, c := range p.commands {
		switch c.Tier {
		case Read:
			read++
		case Write:
			write++
		case Danger:
			danger++
		case Forbidden:
			forbidden++
		}
	}
	return
}
