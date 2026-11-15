// Package gumroadcli is the Gumroad adapter: everything about driving the
// official `gumroad` CLI that the generic gate package deliberately does not
// know.
//
// Two things live here. The command table, classifying all 99 subcommands into
// risk tiers with a written reason for every irreversible one. And the runner
// configuration: which flags to force, which flag confirms, and how to read the
// CLI's error envelope.
//
// Adding support for a different CLI means writing a sibling of this file. The
// gate package should not need to change.
package gumroadcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gshepptech/gumroad-mcp/pkg/gate"
)

// Binary is the executable this adapter drives.
const Binary = "gumroad"

// Commands classifies the Gumroad CLI surface.
//
// The CLI's own SKILL.md tells agents to "add --yes to skip confirmation" on
// deletes and refunds. That is advice a model can simply follow. This table is
// the alternative: withholding a command is structural, and a model cannot
// instruct its way past it.
var Commands = []gate.Command{
	// --- auth -------------------------------------------------------------
	{Path: []string{"auth", "status"}, Tier: gate.Read},
	{Path: []string{"auth", "login"}, Tier: gate.Forbidden, Why: "credential flow; a human runs this"},
	{Path: []string{"auth", "logout"}, Tier: gate.Forbidden, Why: "would destroy the operator's stored credential"},
	{Path: []string{"auth", "token"}, Tier: gate.Forbidden, Why: "prints the raw access token"},

	// --- account ----------------------------------------------------------
	{Path: []string{"user"}, Tier: gate.Read},
	{Path: []string{"user", "update"}, Tier: gate.Write},
	{Path: []string{"user", "page", "preview"}, Tier: gate.Read},
	{Path: []string{"user", "page", "url"}, Tier: gate.Read},
	{
		Path: []string{"user", "page", "publish"}, Tier: gate.Danger,
		Why: "replaces the live profile landing page; the prior HTML comes back only in the response, so it is gone unless the caller saved it",
	},
	{Path: []string{"user", "page", "clear"}, Tier: gate.Danger, Why: "removes the live profile landing page; there is no version history"},

	// --- products ---------------------------------------------------------
	{Path: []string{"products", "list"}, Tier: gate.Read},
	{Path: []string{"products", "view"}, Tier: gate.Read},
	{Path: []string{"products", "url"}, Tier: gate.Read},
	{Path: []string{"products", "categories"}, Tier: gate.Read},
	{Path: []string{"products", "comps"}, Tier: gate.Read},
	{Path: []string{"products", "skus"}, Tier: gate.Read},
	{Path: []string{"products", "create"}, Tier: gate.Write},
	{
		Path: []string{"products", "update"}, Tier: gate.Write,
		// Most of this command is ordinary editing. Two flags are not.
		DangerFlags: []string{"--file", "--custom-html"},
		DangerFlagWhy: "--file rewrites the content page to embed ONLY the new upload, orphaning " +
			"files buyers already download (antiwork/gumroad-cli#205), and --custom-html replaces " +
			"the live landing page. Neither has version history",
	},
	{
		Path: []string{"products", "publish"}, Tier: gate.Write,
		Why: "makes the product publicly buyable; reversible with products unpublish",
	},
	{Path: []string{"products", "unpublish"}, Tier: gate.Write},
	{Path: []string{"products", "delete"}, Tier: gate.Danger, Why: "permanently deletes a product"},

	{Path: []string{"products", "content", "list"}, Tier: gate.Read},
	{Path: []string{"products", "content", "get"}, Tier: gate.Read},
	{Path: []string{"products", "content", "set"}, Tier: gate.Danger, Why: "a whole-document set deletes any page omitted from the JSON"},

	{Path: []string{"products", "covers", "add"}, Tier: gate.Write},
	{Path: []string{"products", "covers", "reorder"}, Tier: gate.Write},
	{Path: []string{"products", "covers", "remove"}, Tier: gate.Danger, Why: "removes a cover from a live product"},
	{Path: []string{"products", "thumbnail", "set"}, Tier: gate.Write},
	{Path: []string{"products", "thumbnail", "remove"}, Tier: gate.Danger, Why: "removes the thumbnail from a live product"},

	{Path: []string{"products", "page", "preview"}, Tier: gate.Read},
	{Path: []string{"products", "page", "url"}, Tier: gate.Read},
	{
		Path: []string{"products", "page", "publish"}, Tier: gate.Danger,
		Why: "replaces the live product landing page; the prior HTML comes back only in the response, so it is gone unless the caller saved it",
	},
	{Path: []string{"products", "page", "clear"}, Tier: gate.Danger, Why: "removes the live landing page; only the latest version is stored"},

	// --- catalogue --------------------------------------------------------
	{Path: []string{"variant-categories", "list"}, Tier: gate.Read},
	{Path: []string{"variant-categories", "view"}, Tier: gate.Read},
	{Path: []string{"variant-categories", "create"}, Tier: gate.Write},
	{Path: []string{"variant-categories", "update"}, Tier: gate.Write},
	{Path: []string{"variant-categories", "delete"}, Tier: gate.Danger, Why: "deletes the category and every variant inside it"},

	{Path: []string{"variants", "list"}, Tier: gate.Read},
	{Path: []string{"variants", "view"}, Tier: gate.Read},
	{Path: []string{"variants", "create"}, Tier: gate.Write},
	{Path: []string{"variants", "update"}, Tier: gate.Write},
	{Path: []string{"variants", "delete"}, Tier: gate.Danger, Why: "permanently deletes a variant"},

	{Path: []string{"offer-codes", "list"}, Tier: gate.Read},
	{Path: []string{"offer-codes", "view"}, Tier: gate.Read},
	{Path: []string{"offer-codes", "create"}, Tier: gate.Write},
	{Path: []string{"offer-codes", "update"}, Tier: gate.Write},
	{Path: []string{"offer-codes", "delete"}, Tier: gate.Danger, Why: "buyers who try the code afterwards get an error"},

	{Path: []string{"custom-fields", "list"}, Tier: gate.Read},
	{Path: []string{"custom-fields", "create"}, Tier: gate.Write},
	{Path: []string{"custom-fields", "update"}, Tier: gate.Write},
	{Path: []string{"custom-fields", "delete"}, Tier: gate.Danger, Why: "permanently removes a checkout field"},

	{Path: []string{"upsells", "list"}, Tier: gate.Read},
	{Path: []string{"upsells", "view"}, Tier: gate.Read},
	{Path: []string{"upsells", "create"}, Tier: gate.Write},
	{Path: []string{"upsells", "update"}, Tier: gate.Write},
	{Path: []string{"upsells", "delete"}, Tier: gate.Danger, Why: "permanently deletes an upsell"},

	// --- files and media --------------------------------------------------
	{Path: []string{"files", "upload"}, Tier: gate.Write},
	{Path: []string{"files", "complete"}, Tier: gate.Write},
	{Path: []string{"files", "abort"}, Tier: gate.Danger, Why: "cancels an in-flight upload; the parts already sent are discarded"},

	{Path: []string{"media", "list"}, Tier: gate.Read},
	{Path: []string{"media", "upload"}, Tier: gate.Write},
	{Path: []string{"media", "delete"}, Tier: gate.Danger, Why: "any page still embedding the URL shows a broken image"},

	// --- storefront pages -------------------------------------------------
	{Path: []string{"pages", "list"}, Tier: gate.Read},
	{Path: []string{"pages", "pull"}, Tier: gate.Read},
	{Path: []string{"pages", "scaffold"}, Tier: gate.Read},
	{Path: []string{"pages", "preview"}, Tier: gate.Read},
	{Path: []string{"pages", "create"}, Tier: gate.Write},
	{Path: []string{"pages", "push"}, Tier: gate.Danger, Why: "replaces a live storefront page and there is no version history"},

	// --- sales ------------------------------------------------------------
	{Path: []string{"sales", "list"}, Tier: gate.Read},
	{Path: []string{"sales", "view"}, Tier: gate.Read},
	{Path: []string{"sales", "summary"}, Tier: gate.Read},
	{Path: []string{"sales", "buyers"}, Tier: gate.Read},
	{Path: []string{"sales", "ship"}, Tier: gate.Write},
	{
		Path: []string{"sales", "export"}, Tier: gate.Danger,
		Why: "emails a CSV of customer names, addresses and UTM data off the account; once sent it cannot be recalled",
	},
	{Path: []string{"sales", "refund"}, Tier: gate.Danger, Why: "moves real money back to the buyer"},
	{Path: []string{"sales", "resend-receipt"}, Tier: gate.Danger, Why: "sends mail to a customer's inbox"},

	{Path: []string{"subscribers", "list"}, Tier: gate.Read},
	{Path: []string{"subscribers", "view"}, Tier: gate.Read},

	// --- money out --------------------------------------------------------
	{Path: []string{"payouts", "list"}, Tier: gate.Read},
	{Path: []string{"payouts", "view"}, Tier: gate.Read},
	{Path: []string{"payouts", "upcoming"}, Tier: gate.Read},

	// --- licences ---------------------------------------------------------
	// verify is a write: the API increments the seat count unless the caller
	// passes --no-increment, so a bare verify consumes one of the buyer's uses.
	{Path: []string{"licenses", "verify"}, Tier: gate.Write},
	{Path: []string{"licenses", "enable"}, Tier: gate.Write},
	{Path: []string{"licenses", "decrement"}, Tier: gate.Write},
	{Path: []string{"licenses", "disable"}, Tier: gate.Danger, Why: "cuts off a paying customer's access"},
	{Path: []string{"licenses", "rotate"}, Tier: gate.Danger, Why: "anything using the old key stops working immediately"},

	// --- audience ---------------------------------------------------------
	{Path: []string{"emails", "list"}, Tier: gate.Read},
	{Path: []string{"emails", "view"}, Tier: gate.Read},
	{Path: []string{"emails", "create"}, Tier: gate.Write, Why: "drafts only; sending is a separate, gated step"},
	{Path: []string{"emails", "send-preview"}, Tier: gate.Write, Why: "reaches the seller's own inbox only"},
	{Path: []string{"emails", "send"}, Tier: gate.Danger, Why: "blasts the seller's whole audience and cannot be recalled"},
	{Path: []string{"emails", "delete"}, Tier: gate.Danger, Why: "permanently deletes an audience email"},

	{Path: []string{"workflows", "list"}, Tier: gate.Read},
	{Path: []string{"workflows", "view"}, Tier: gate.Read},
	{Path: []string{"workflows", "add-email"}, Tier: gate.Danger, Why: "on a live workflow that backfills, this schedules mail to past customers immediately"},
	{Path: []string{"workflows", "update-email"}, Tier: gate.Danger, Why: "changing a delay reschedules recipients who have not yet received the step"},

	// --- plumbing ---------------------------------------------------------
	{Path: []string{"refund-policy", "view"}, Tier: gate.Read},
	{Path: []string{"refund-policy", "set"}, Tier: gate.Write},

	{Path: []string{"webhooks", "list"}, Tier: gate.Read},
	{Path: []string{"webhooks", "create"}, Tier: gate.Write},
	{Path: []string{"webhooks", "delete"}, Tier: gate.Write, Why: "recreating a subscription is trivial"},

	// --- never proxied ----------------------------------------------------
	{Path: []string{"admin"}, Tier: gate.Forbidden, Why: "internal admin API: suspends users, refunds balances, issues credits"},
	{Path: []string{"skill"}, Tier: gate.Forbidden, Why: "writes files outside the Gumroad account; read it with gumroad_skill instead"},
	{Path: []string{"completion"}, Tier: gate.Forbidden, Why: "shell plumbing, not a Gumroad operation"},
}

// Policy is the compiled command table.
//
// --dry-run is Gumroad's preview flag: mutating commands print the request they
// would send and stop, and read commands ignore it. That makes a preview safe
// at any posture.
var Policy = gate.MustPolicy(Commands, gate.WithPreviewFlags("--dry-run"))

// NewRunner returns a Runner configured for the Gumroad CLI.
//
//   - --json      structured output instead of human tables
//   - --no-input  never block on a prompt; there is no terminal here
//   - --quiet     no spinners or status chatter polluting stdout
//
// --yes is appended only once the gate has passed, never on a preview.
func NewRunner() *gate.Runner {
	return &gate.Runner{
		Bin:         Binary,
		BaseFlags:   []string{"--json", "--no-input", "--quiet"},
		ConfirmFlag: "--yes",
		Timeout:     2 * time.Minute,
		ParseError:  parseError,
	}
}

// OwnedFlags are the flags this adapter supplies itself. A caller passing one
// is refused rather than silently duplicated. --yes in particular is the
// whole point of the gate, so accepting it from a model would hand over the
// bypass.
var OwnedFlags = []string{"--json", "--no-input", "--quiet", "--yes", "-y"}

// Error is a failed Gumroad CLI invocation. The CLI reports failures as a JSON
// envelope on stdout with a non-zero exit, so the structured detail survives.
type Error struct {
	ExitCode int
	Type     string // e.g. auth_error, api_error
	Code     string // e.g. not_authenticated
	Message  string
	Status   int // upstream HTTP status, when reported
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("gumroad: exit %d", e.ExitCode)
	}
	if e.Code != "" {
		return fmt.Sprintf("gumroad: %s (%s)", e.Message, e.Code)
	}
	return "gumroad: " + e.Message
}

// NeedsAuth reports a missing or rejected credential, which a caller should
// surface as "log in" rather than "try again".
func (e *Error) NeedsAuth() bool {
	return e.Type == "auth_error" || e.Code == "not_authenticated" || e.Status == 401
}

// parseError lifts the CLI's envelope into an *Error. Returning nil lets the
// runner fall back to its generic exit-code error.
func parseError(exitCode int, stdout, stderr []byte) error {
	var env struct {
		Error struct {
			Type       string `json:"type"`
			Code       string `json:"code"`
			Message    string `json:"message"`
			StatusCode int    `json:"status_code"`
		} `json:"error"`
	}
	if json.Unmarshal(stdout, &env) == nil && env.Error.Message != "" {
		return &Error{
			ExitCode: exitCode,
			Type:     env.Error.Type,
			Code:     env.Error.Code,
			Message:  env.Error.Message,
			Status:   env.Error.StatusCode,
		}
	}
	if s := strings.TrimSpace(string(stderr)); s != "" {
		return &Error{ExitCode: exitCode, Message: s}
	}
	return nil
}

// --- adapter-specific helpers -----------------------------------------------

// Skill returns the SKILL.md the Gumroad CLI embeds. It documents every command
// this adapter proxies, so an agent reads Gumroad's own reference rather than a
// restatement of it.
func Skill(ctx context.Context, r *gate.Runner) (string, error) {
	return r.Text(ctx, "skill")
}

// Token returns the seller token the CLI already resolved, so a caller can
// reach the few endpoints the CLI does not cover without storing a second
// credential.
func Token(ctx context.Context, r *gate.Runner) (string, error) {
	tok, err := r.Text(ctx, "auth", "token", "--no-input")
	if err != nil {
		return "", fmt.Errorf("could not read the Gumroad CLI's token (run `gumroad auth login`): %w", err)
	}
	if tok == "" {
		return "", errors.New("the Gumroad CLI has no token. Run `gumroad auth login`")
	}
	return tok, nil
}

// Version reports the CLI version being driven.
func Version(ctx context.Context, r *gate.Runner) (string, error) {
	out, err := r.Text(ctx, "--version")
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(out, "gumroad version "), nil
}

// ErrNotInstalled explains how to get the CLI this adapter requires.
var ErrNotInstalled = errors.New(
	"the Gumroad CLI is not installed. gumroad-mcp drives it.\n" +
		"  install: brew install antiwork/cli/gumroad\n" +
		"       or: curl -fsSL https://gumroad.com/install-cli.sh | bash")
