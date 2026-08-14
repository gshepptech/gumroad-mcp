package gum

import (
	"fmt"
	"sort"
	"strings"
)

// Tier is how much damage a command can do.
type Tier int

const (
	// Read commands cannot change anything.
	Read Tier = iota
	// Write commands change something the seller can undo from the Gumroad
	// dashboard.
	Write
	// Danger commands are irreversible, move money, or reach a customer's
	// inbox. They are withheld unless explicitly enabled, and even then require
	// the caller to confirm.
	Danger
	// Forbidden commands are never proxied, whatever the settings.
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
	default:
		return "forbidden"
	}
}

// Command is one entry in the policy.
type Command struct {
	// Path is the subcommand words, e.g. {"sales", "refund"}.
	Path []string
	Tier Tier
	// Why explains a Danger or Forbidden classification to whoever reads the
	// policy — including the model, which is shown it on refusal.
	Why string

	// DangerFlags promote the command to Danger when any of them appears in the
	// argv. Some commands are ordinary edits until one flag turns them
	// destructive: `products update --name` is reversible, `products update
	// --file` silently orphans content that has no version history.
	DangerFlags []string
	// DangerFlagWhy explains the promotion. Required when DangerFlags is set.
	DangerFlagWhy string
}

func (c Command) Name() string { return strings.Join(c.Path, " ") }

// policy is the complete classification of the Gumroad CLI surface.
//
// It is a strict allowlist: a command absent from this table is refused, so a
// new upstream subcommand cannot reach an agent until someone classifies it.
// That is the intended failure mode.
//
// The official CLI's own SKILL.md tells agents to "add --yes to skip
// confirmation" on deletes and refunds. That is advice a model can simply
// follow. This table is the reason gloop exists: withholding a tool is
// structural, and a model cannot instruct its way past it.
var policy = []Command{
	// --- auth -------------------------------------------------------------
	{Path: []string{"auth", "status"}, Tier: Read},
	{Path: []string{"auth", "login"}, Tier: Forbidden, Why: "credential flow; a human runs this"},
	{Path: []string{"auth", "logout"}, Tier: Forbidden, Why: "would destroy the operator's stored credential"},
	{Path: []string{"auth", "token"}, Tier: Forbidden, Why: "prints the raw access token"},

	// --- account ----------------------------------------------------------
	{Path: []string{"user"}, Tier: Read},
	{Path: []string{"user", "update"}, Tier: Write},
	{Path: []string{"user", "page", "preview"}, Tier: Read},
	{Path: []string{"user", "page", "url"}, Tier: Read},
	{
		Path: []string{"user", "page", "publish"}, Tier: Danger,
		Why: "replaces the live profile landing page; the prior HTML comes back only in the response, so it is gone unless the caller saved it",
	},
	{Path: []string{"user", "page", "clear"}, Tier: Danger, Why: "removes the live profile landing page; there is no version history"},

	// --- products ---------------------------------------------------------
	{Path: []string{"products", "list"}, Tier: Read},
	{Path: []string{"products", "view"}, Tier: Read},
	{Path: []string{"products", "url"}, Tier: Read},
	{Path: []string{"products", "categories"}, Tier: Read},
	{Path: []string{"products", "comps"}, Tier: Read},
	{Path: []string{"products", "skus"}, Tier: Read},
	{Path: []string{"products", "create"}, Tier: Write},
	{
		Path: []string{"products", "update"}, Tier: Write,
		// Most of this command is ordinary editing. Two flags are not.
		DangerFlags: []string{"--file", "--custom-html"},
		DangerFlagWhy: "--file rewrites the content page to embed ONLY the new upload, orphaning " +
			"files buyers already download (antiwork/gumroad-cli#205), and --custom-html replaces " +
			"the live landing page. Neither has version history",
	},
	{
		Path: []string{"products", "publish"}, Tier: Write,
		Why: "makes the product publicly buyable; reversible with products unpublish",
	},
	{Path: []string{"products", "unpublish"}, Tier: Write},
	{Path: []string{"products", "delete"}, Tier: Danger, Why: "permanently deletes a product"},

	{Path: []string{"products", "content", "list"}, Tier: Read},
	{Path: []string{"products", "content", "get"}, Tier: Read},
	{Path: []string{"products", "content", "set"}, Tier: Danger, Why: "a whole-document set deletes any page omitted from the JSON"},

	{Path: []string{"products", "covers", "add"}, Tier: Write},
	{Path: []string{"products", "covers", "reorder"}, Tier: Write},
	{Path: []string{"products", "covers", "remove"}, Tier: Danger, Why: "removes a cover from a live product"},
	{Path: []string{"products", "thumbnail", "set"}, Tier: Write},
	{Path: []string{"products", "thumbnail", "remove"}, Tier: Danger, Why: "removes the thumbnail from a live product"},

	{Path: []string{"products", "page", "preview"}, Tier: Read},
	{Path: []string{"products", "page", "url"}, Tier: Read},
	{
		Path: []string{"products", "page", "publish"}, Tier: Danger,
		Why: "replaces the live product landing page; the prior HTML comes back only in the response, so it is gone unless the caller saved it",
	},
	{Path: []string{"products", "page", "clear"}, Tier: Danger, Why: "removes the live landing page; only the latest version is stored"},

	// --- catalogue --------------------------------------------------------
	{Path: []string{"variant-categories", "list"}, Tier: Read},
	{Path: []string{"variant-categories", "view"}, Tier: Read},
	{Path: []string{"variant-categories", "create"}, Tier: Write},
	{Path: []string{"variant-categories", "update"}, Tier: Write},
	{Path: []string{"variant-categories", "delete"}, Tier: Danger, Why: "deletes the category and every variant inside it"},

	{Path: []string{"variants", "list"}, Tier: Read},
	{Path: []string{"variants", "view"}, Tier: Read},
	{Path: []string{"variants", "create"}, Tier: Write},
	{Path: []string{"variants", "update"}, Tier: Write},
	{Path: []string{"variants", "delete"}, Tier: Danger, Why: "permanently deletes a variant"},

	{Path: []string{"offer-codes", "list"}, Tier: Read},
	{Path: []string{"offer-codes", "view"}, Tier: Read},
	{Path: []string{"offer-codes", "create"}, Tier: Write},
	{Path: []string{"offer-codes", "update"}, Tier: Write},
	{Path: []string{"offer-codes", "delete"}, Tier: Danger, Why: "buyers who try the code afterwards get an error"},

	{Path: []string{"custom-fields", "list"}, Tier: Read},
	{Path: []string{"custom-fields", "create"}, Tier: Write},
	{Path: []string{"custom-fields", "update"}, Tier: Write},
	{Path: []string{"custom-fields", "delete"}, Tier: Danger, Why: "permanently removes a checkout field"},

	{Path: []string{"upsells", "list"}, Tier: Read},
	{Path: []string{"upsells", "view"}, Tier: Read},
	{Path: []string{"upsells", "create"}, Tier: Write},
	{Path: []string{"upsells", "update"}, Tier: Write},
	{Path: []string{"upsells", "delete"}, Tier: Danger, Why: "permanently deletes an upsell"},

	// --- files and media --------------------------------------------------
	{Path: []string{"files", "upload"}, Tier: Write},
	{Path: []string{"files", "complete"}, Tier: Write},
	{Path: []string{"files", "abort"}, Tier: Danger, Why: "cancels an in-flight upload; the parts already sent are discarded"},

	{Path: []string{"media", "list"}, Tier: Read},
	{Path: []string{"media", "upload"}, Tier: Write},
	{Path: []string{"media", "delete"}, Tier: Danger, Why: "any page still embedding the URL shows a broken image"},

	// --- storefront pages -------------------------------------------------
	{Path: []string{"pages", "list"}, Tier: Read},
	{Path: []string{"pages", "pull"}, Tier: Read},
	{Path: []string{"pages", "scaffold"}, Tier: Read},
	{Path: []string{"pages", "preview"}, Tier: Read},
	{Path: []string{"pages", "create"}, Tier: Write},
	{Path: []string{"pages", "push"}, Tier: Danger, Why: "replaces a live storefront page and there is no version history"},

	// --- sales ------------------------------------------------------------
	{Path: []string{"sales", "list"}, Tier: Read},
	{Path: []string{"sales", "view"}, Tier: Read},
	{Path: []string{"sales", "summary"}, Tier: Read},
	{Path: []string{"sales", "buyers"}, Tier: Read},
	{Path: []string{"sales", "ship"}, Tier: Write},
	{
		Path: []string{"sales", "export"}, Tier: Danger,
		Why: "emails a CSV of customer names, addresses and UTM data off the account; once sent it cannot be recalled",
	},
	{Path: []string{"sales", "refund"}, Tier: Danger, Why: "moves real money back to the buyer"},
	{Path: []string{"sales", "resend-receipt"}, Tier: Danger, Why: "sends mail to a customer's inbox"},

	{Path: []string{"subscribers", "list"}, Tier: Read},
	{Path: []string{"subscribers", "view"}, Tier: Read},

	// --- money out --------------------------------------------------------
	{Path: []string{"payouts", "list"}, Tier: Read},
	{Path: []string{"payouts", "view"}, Tier: Read},
	{Path: []string{"payouts", "upcoming"}, Tier: Read},

	// --- licences ---------------------------------------------------------
	// verify is a write: the API increments the seat count unless the caller
	// passes --no-increment, so a bare verify consumes one of the buyer's uses.
	{Path: []string{"licenses", "verify"}, Tier: Write},
	{Path: []string{"licenses", "enable"}, Tier: Write},
	{Path: []string{"licenses", "decrement"}, Tier: Write},
	{Path: []string{"licenses", "disable"}, Tier: Danger, Why: "cuts off a paying customer's access"},
	{Path: []string{"licenses", "rotate"}, Tier: Danger, Why: "anything using the old key stops working immediately"},

	// --- audience ---------------------------------------------------------
	{Path: []string{"emails", "list"}, Tier: Read},
	{Path: []string{"emails", "view"}, Tier: Read},
	{Path: []string{"emails", "create"}, Tier: Write, Why: "drafts only — sending is a separate, gated step"},
	{Path: []string{"emails", "send-preview"}, Tier: Write, Why: "reaches the seller's own inbox only"},
	{Path: []string{"emails", "send"}, Tier: Danger, Why: "blasts the seller's whole audience and cannot be recalled"},
	{Path: []string{"emails", "delete"}, Tier: Danger, Why: "permanently deletes an audience email"},

	{Path: []string{"workflows", "list"}, Tier: Read},
	{Path: []string{"workflows", "view"}, Tier: Read},
	{Path: []string{"workflows", "add-email"}, Tier: Danger, Why: "on a live workflow that backfills, this schedules mail to past customers immediately"},
	{Path: []string{"workflows", "update-email"}, Tier: Danger, Why: "changing a delay reschedules recipients who have not yet received the step"},

	// --- plumbing ---------------------------------------------------------
	{Path: []string{"refund-policy", "view"}, Tier: Read},
	{Path: []string{"refund-policy", "set"}, Tier: Write},

	{Path: []string{"webhooks", "list"}, Tier: Read},
	{Path: []string{"webhooks", "create"}, Tier: Write},
	{Path: []string{"webhooks", "delete"}, Tier: Write, Why: "recreating a subscription is trivial"},

	// --- never proxied ----------------------------------------------------
	{Path: []string{"admin"}, Tier: Forbidden, Why: "internal admin API — suspends users, refunds balances, issues credits"},
	{Path: []string{"skill"}, Tier: Forbidden, Why: "writes files outside the Gumroad account; read it with gumroad_skill instead"},
	{Path: []string{"completion"}, Tier: Forbidden, Why: "shell plumbing, not a Gumroad operation"},
}

// index maps a command name to its policy entry, plus every prefix that has to
// be blocked wholesale (`admin` covers `admin users suspend`).
var (
	byName   = map[string]Command{}
	blockedP [][]string
)

func init() {
	for _, c := range policy {
		byName[c.Name()] = c
		if c.Tier == Forbidden {
			blockedP = append(blockedP, c.Path)
		}
	}
}

// Decision is the outcome of checking an argv against the policy.
type Decision struct {
	Command Command
	// Matched is how many leading argv words formed the command name.
	Matched int
	Allowed bool
	// Reason explains a refusal, in words meant for the model to act on.
	Reason string
	// DryRun means the argv asked the CLI to print the request it would send
	// instead of sending it, which is why the tier gate let it through.
	DryRun bool
	// Tier is the EFFECTIVE tier, which may be higher than the command's own
	// when a flag promoted it. Callers gate on this, not Command.Tier.
	Tier Tier
	// PromotedBy names the flag that raised the tier, empty when none did.
	PromotedBy string
}

// dangerFlag reports the first argv flag that promotes c to Danger.
func dangerFlag(c Command, argv []string) (string, bool) {
	for _, want := range c.DangerFlags {
		for _, got := range argv {
			// Matches both `--file x` and `--file=x`.
			if got == want || strings.HasPrefix(got, want+"=") {
				return want, true
			}
		}
	}
	return "", false
}

// hasDryRun reports whether the caller asked for a preview. The Gumroad CLI
// treats --dry-run as a global flag: mutating commands print the request they
// would send and stop, and read commands ignore it.
func hasDryRun(argv []string) bool {
	for _, a := range argv {
		if a == "--dry-run" {
			return true
		}
	}
	return false
}

// Classify resolves an argv against the policy. It matches the longest command
// path, so `products page clear` beats `products page`.
//
// enableDanger and readOnly describe the server's configured posture.
func Classify(argv []string, readOnly, enableDanger bool) Decision {
	if len(argv) == 0 {
		return Decision{Reason: "no command given"}
	}

	// Classify on the leading words only. Everything from the first flag on is
	// an argument, not part of the command name.
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

	// Refuse anything under a wholesale-blocked prefix first, so `admin users
	// suspend` is caught even though only `admin` is listed.
	for _, p := range blockedP {
		if hasPrefixWords(words, p) {
			c := byName[strings.Join(p, " ")]
			return Decision{
				Command: c, Matched: len(p),
				Reason: fmt.Sprintf("`gumroad %s` is never available through gloop: %s", c.Name(), c.Why),
			}
		}
	}

	// Longest-prefix match against the allowlist, so `products page clear`
	// resolves ahead of any shorter `products` entry.
	var best Command
	var bestN int
	for n := min(len(words), 3); n >= 1; n-- {
		if c, ok := byName[strings.Join(words[:n], " ")]; ok {
			best, bestN = c, n
			break
		}
	}
	if bestN == 0 {
		return Decision{Reason: fmt.Sprintf(
			"`gumroad %s` is not in gloop's allowlist. Call gumroad_commands to see what is available.",
			strings.Join(words, " "))}
	}

	d := Decision{Command: best, Matched: bestN, DryRun: hasDryRun(argv), Tier: best.Tier}

	// A flag can make an otherwise ordinary command destructive. Promote before
	// the gate runs, and carry the flag's own explanation into the refusal.
	if flag, ok := dangerFlag(best, argv); ok {
		d.Tier = Danger
		d.PromotedBy = flag
		d.Command.Why = best.DangerFlagWhy
	}

	switch {
	case best.Tier == Forbidden:
		// A preview of a forbidden command still reveals the surface gloop
		// refuses to expose, so --dry-run does not open it.
		d.Reason = fmt.Sprintf("`gumroad %s` is never available through gloop: %s", best.Name(), best.Why)

	case d.DryRun:
		// A dry run prints the request the CLI would send and stops. Nothing
		// reaches Gumroad, so it is a read whatever the command's own tier is.
		// Refusing it would push a caller towards running the real thing blind,
		// which is the opposite of what the gate is for.
		d.Allowed = true

	case readOnly && d.Tier != Read:
		d.Reason = fmt.Sprintf(
			"this gloop server is read-only; `gumroad %s` is a %s command. Add --dry-run to preview it.",
			best.Name(), d.Tier)

	case d.Tier == Danger && !enableDanger:
		what := fmt.Sprintf("`gumroad %s`", best.Name())
		if d.PromotedBy != "" {
			what = fmt.Sprintf("`gumroad %s` with %s", best.Name(), d.PromotedBy)
		}
		d.Reason = fmt.Sprintf(
			"%s is irreversible (%s) and this gloop server was not started with "+
				"--allow-danger. Add --dry-run to see the request it would send.",
			what, d.Command.Why)

	default:
		d.Allowed = true
	}
	return d
}

func hasPrefixWords(argv, prefix []string) bool {
	if len(argv) < len(prefix) {
		return false
	}
	for i, w := range prefix {
		if argv[i] != w {
			return false
		}
	}
	return true
}

// Available lists the commands a server with this posture will run, sorted.
func Available(readOnly, enableDanger bool) []Command {
	var out []Command
	for _, c := range policy {
		if c.Tier == Forbidden {
			continue
		}
		if readOnly && c.Tier != Read {
			continue
		}
		if c.Tier == Danger && !enableDanger {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Counts summarises the policy for a startup banner.
func Counts() (read, write, danger, forbidden int) {
	for _, c := range policy {
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
