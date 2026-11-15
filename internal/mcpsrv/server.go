// Package mcpsrv exposes the Gumroad CLI to an AI agent over MCP, with a tier
// policy deciding what the agent may invoke.
//
// gumroad-mcp does not reimplement the Gumroad API. The official `gumroad` CLI
// already covers more of it than a second client could, and its embedded
// SKILL.md documents every command. gumroad-mcp proxies that CLI and adds the one
// thing a skill file cannot provide: refusal.
//
// A skill can tell a model to be careful. A model can read that and proceed
// anyway. The official SKILL.md instructs agents to pass `--yes` on
// deletes and refunds. gumroad-mcp withholds the command instead, which is not
// advice the model can argue with.
package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gshepptech/gumroad-mcp/internal/gumroad"
	"github.com/gshepptech/gumroad-mcp/internal/gumroadcli"
	"github.com/gshepptech/gumroad-mcp/pkg/gate"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options configures the server.
type Options struct {
	// Version is reported to MCP clients during initialization.
	Version string
	// ReadOnly refuses every command that is not a pure read.
	ReadOnly bool
	// AllowDanger permits the irreversible tier. Those commands additionally
	// require confirm=true on each call.
	AllowDanger bool
}

type registry struct {
	server *mcp.Server
	run    *gate.Runner
	api    *gumroad.Client // for the few endpoints the CLI does not cover
	opts   Options
}

// New builds the MCP server. api may be nil, in which case the tools covering
// endpoints the official CLI lacks are not registered.
func New(run *gate.Runner, api *gumroad.Client, opts Options) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "gumroad-mcp",
		Title:   "Gumroad",
		Version: opts.Version,
	}, &mcp.ServerOptions{Instructions: instructions(opts)})

	r := &registry{server: srv, run: run, api: api, opts: opts}
	registerProxy(r)
	registerNative(r)
	return srv
}

func instructions(o Options) string {
	var b strings.Builder
	b.WriteString(
		"Drives a Gumroad seller account through the official `gumroad` CLI.\n\n" +
			"Start by calling gumroad_skill once. It returns Gumroad's own reference for every\n" +
			"command, including exact flags, JSON response shapes, and per-command gotchas.\n" +
			"Then call gumroad_run with the command as an argv array.\n\n" +
			"Do NOT pass --json, --no-input, or --quiet: gumroad-mcp adds them. Do not pass --yes\n" +
			"either; gumroad-mcp decides when confirmation is warranted.\n\n" +
			"DO pass --dry-run before any write you have not made before. The CLI prints the\n" +
			"request it would send and stops, nothing reaches Gumroad, and gumroad-mcp allows it for\n" +
			"any command it can run at all, including the irreversible ones it would otherwise\n" +
			"refuse. Read the preview, then decide.\n\n")

	switch {
	case o.ReadOnly:
		b.WriteString("POSTURE: read-only. Every command that could change the account is refused.\n")
	case o.AllowDanger:
		b.WriteString(
			"POSTURE: irreversible commands are enabled but each needs confirm=true, which you\n" +
				"may set only after the user has explicitly asked for that specific action.\n")
	default:
		b.WriteString(
			"POSTURE: reads and reversible writes. Irreversible commands (refunds, deletes,\n" +
				"audience sends) are refused; tell the user to restart gumroad-mcp with --allow-danger\n" +
				"if they want them.\n")
	}
	return b.String()
}

// --- the proxy ---------------------------------------------------------------

type runArgs struct {
	Command []string `json:"command" jsonschema:"The gumroad command as an argv array, e.g. [\"products\",\"list\"] or [\"sales\",\"list\",\"--after\",\"2026-01-01\"]. Omit --json, --no-input, --quiet and --yes; gumroad-mcp supplies them. Include --dry-run to preview a write instead of running it. That is allowed for every command, including ones that would otherwise be refused."`
	Confirm bool     `json:"confirm,omitempty" jsonschema:"Required only for irreversible commands. Set it solely after the user has asked for that exact action; never to clear a refusal you hit by accident."`
}

func registerProxy(r *registry) {
	mcp.AddTool(r.server, &mcp.Tool{
		Name: "gumroad_run",
		Description: "Run a Gumroad CLI command and return its JSON. This is the main tool. It " +
			"reaches products, sales, subscribers, licences, files, media, storefront pages, " +
			"upsells, offer codes, variants, audience emails, workflows, webhooks and payouts. " +
			"Call gumroad_skill first for the exact flags each command takes.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(!r.opts.ReadOnly)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in runArgs) (*mcp.CallToolResult, any, error) {
		return textResult(r.exec(ctx, in)), nil, nil
	})

	mcp.AddTool(r.server, &mcp.Tool{
		Name: "gumroad_commands",
		Description: "List the Gumroad commands this server will run, with the risk tier of each. " +
			"Call this when a command is refused, to find what is available instead.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		var b strings.Builder
		fmt.Fprintf(&b, "Commands available on this server (posture: %s)\n\n", posture(r.opts))
		for _, c := range gumroadcli.Policy.Available(r.opts.ReadOnly, r.opts.AllowDanger) {
			fmt.Fprintf(&b, "  %-30s %s", c.Name(), c.Tier)
			if c.Tier == gate.Danger {
				b.WriteString("  (needs confirm=true)")
			}
			b.WriteString("\n")
		}
		b.WriteString("\nPass the command to gumroad_run as an argv array, e.g. [\"products\",\"list\"].\n")
		return textResult(b.String(), nil), nil, nil
	})

	mcp.AddTool(r.server, &mcp.Tool{
		Name: "gumroad_skill",
		Description: "Return Gumroad's own reference documentation for the CLI: every command, its " +
			"flags, the JSON shape it returns, and its gotchas. Read this before composing a " +
			"command you have not run before.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		skill, err := gumroadcli.Skill(ctx, r.run)
		if err != nil {
			return textResult("", err), nil, nil
		}
		return textResult(skill, nil), nil, nil
	})

	mcp.AddTool(r.server, &mcp.Tool{
		Name: "gumroad_help",
		Description: "Show the CLI's own --help for one command, listing its exact flags. Use this " +
			"when gumroad_skill does not settle how to phrase a command.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Command []string `json:"command" jsonschema:"Command words to get help for, e.g. [\"products\",\"create\"]. Empty for the top-level list."`
	}) (*mcp.CallToolResult, any, error) {
		// Help is documentation, not an operation, so it is not tier-gated,
		// but it must not become a way to probe the blocked admin surface.
		if d := gumroadcli.Policy.Classify(in.Command, false, true); len(in.Command) > 0 && !d.Allowed {
			return textResult("", fmt.Errorf("%s", d.Reason)), nil, nil
		}
		out, err := r.run.Text(ctx, append(in.Command, "--help")...)
		return textResult(out, err), nil, nil
	})
}

// exec is the gate every proxied command passes through.
func (r *registry) exec(ctx context.Context, in runArgs) (string, error) {
	if len(in.Command) == 0 {
		return "", fmt.Errorf("command was empty. Pass an argv array such as [\"products\",\"list\"]")
	}

	for _, a := range in.Command {
		if !slices.Contains(gumroadcli.OwnedFlags, a) {
			continue
		}
		if a == "--yes" || a == "-y" {
			return "", fmt.Errorf(
				"do not pass --yes. gumroad-mcp decides when confirmation is warranted: reversible " +
					"commands are auto-confirmed, and irreversible ones need confirm=true on this tool")
		}
		return "", fmt.Errorf("do not pass %s; gumroad-mcp adds it to every command", a)
	}

	d := gumroadcli.Policy.Classify(in.Command, r.opts.ReadOnly, r.opts.AllowDanger)
	if !d.Allowed {
		return "", fmt.Errorf("refused: %s", d.Reason)
	}
	if d.Tier == gate.Danger && !d.Preview && !in.Confirm {
		return "", fmt.Errorf(
			"refused: `gumroad %s` cannot be undone (%s). Add --dry-run to see the request it "+
				"would send, or ask the user to confirm this exact action and call again with "+
				"confirm=true",
			d.Command.Name(), d.Command.Why)
	}

	// Writes are reversible from the dashboard, and --no-input turns any
	// prompt into a failure, so Args auto-confirms them along with confirmed
	// danger calls. A preview is never confirmed.
	args := r.run.Args(d, in.Command)

	timeout := 2 * time.Minute
	if isUpload(in.Command) {
		timeout = 30 * time.Minute
	}

	out, err := r.run.RunTimeout(ctx, timeout, args...)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func isUpload(argv []string) bool {
	joined := strings.Join(argv, " ")
	return strings.HasPrefix(joined, "files upload") ||
		strings.HasPrefix(joined, "files complete") ||
		strings.HasPrefix(joined, "sales export")
}

// --- endpoints the official CLI does not cover -------------------------------

// registerNative adds the handful of API endpoints with no `gumroad` command.
// Everything else is proxied; this is the only place gumroad-mcp talks to the API
// itself, and it authenticates with the CLI's own token.
func registerNative(r *registry) {
	if r.api == nil {
		return
	}

	mcp.AddTool(r.server, &mcp.Tool{
		Name: "gumroad_reviews",
		Description: "List the public reviews on one of the seller's products, newest first. " +
			"Star-only ratings with no written message are excluded. The official CLI has no " +
			"command for this.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		ProductID string `json:"product_id" jsonschema:"Product ID, as returned by the products list command."`
		PageKey   string `json:"page_key,omitempty" jsonschema:"next_page_key from a previous call; up to 100 reviews per page."`
	}) (*mcp.CallToolResult, any, error) {
		reviews, next, err := r.api.ListReviews(ctx, in.ProductID, in.PageKey)
		if err != nil {
			return textResult("", err), nil, nil
		}
		return jsonResult(map[string]any{"reviews": reviews, "next_page_key": next}), nil, nil
	})

	mcp.AddTool(r.server, &mcp.Tool{
		Name: "gumroad_earnings",
		Description: "Annual earnings breakdown from the Gumroad Tax Center: gross, fees, taxes, " +
			"affiliate credit and net for one year. Fully refunded sales are excluded from every " +
			"figure. US sellers with the tax centre enabled; needs the view_tax_data scope. " +
			"For per-sale totals use `sales summary` via gumroad_run instead.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Year int `json:"year" jsonschema:"Four-digit tax year, between the account-creation year and last year."`
	}) (*mcp.CallToolResult, any, error) {
		e, err := r.api.GetEarnings(ctx, in.Year)
		if err != nil {
			return textResult("", err), nil, nil
		}
		return jsonResult(e), nil, nil
	})

	mcp.AddTool(r.server, &mcp.Tool{
		Name: "gumroad_tax_forms",
		Description: "List the seller's generated 1099 forms and whether each has been filed. " +
			"US sellers with the tax centre enabled; needs the view_tax_data scope. The official " +
			"CLI has no command for this.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Year int `json:"year,omitempty" jsonschema:"Four-digit tax year. Omit for every available year."`
	}) (*mcp.CallToolResult, any, error) {
		forms, err := r.api.ListTaxForms(ctx, in.Year)
		if err != nil {
			return textResult("", err), nil, nil
		}
		return jsonResult(forms), nil, nil
	})

	mcp.AddTool(r.server, &mcp.Tool{
		Name: "gumroad_public_product",
		Description: "Read the public product-page data for ANY seller's product, with no ownership " +
			"needed. Useful for competitor research and for seeing your own page as a visitor. " +
			"Never returns seller-private fields.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Seller    string `json:"seller" jsonschema:"Seller username (sahil) or full host (sahil.gumroad.com)."`
		Permalink string `json:"permalink" jsonschema:"Last path segment of the product URL, e.g. pencil in sahil.gumroad.com/l/pencil."`
	}) (*mcp.CallToolResult, any, error) {
		p, err := r.api.GetPublicProduct(ctx, in.Seller, in.Permalink)
		if err != nil {
			return textResult("", err), nil, nil
		}
		return jsonResult(p), nil, nil
	})

	mcp.AddTool(r.server, &mcp.Tool{
		Name: "gumroad_public_profile",
		Description: "Read any creator's public profile and their visible products, with no " +
			"authentication or ownership needed.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct {
		Seller string `json:"seller" jsonschema:"Seller username (sahil) or full host (sahil.gumroad.com)."`
	}) (*mcp.CallToolResult, any, error) {
		p, err := r.api.GetPublicProfile(ctx, in.Seller)
		if err != nil {
			return textResult("", err), nil, nil
		}
		return jsonResult(p), nil, nil
	})
}

// --- result shaping ----------------------------------------------------------

// textResult returns out, or err as a tool-level error so the model reads the
// message and can correct itself rather than seeing a transport failure.
func textResult(out string, err error) *mcp.CallToolResult {
	if err != nil {
		msg := err.Error()
		var gerr *gumroadcli.Error
		if asGum(err, &gerr) && gerr.NeedsAuth() {
			msg += "\n\nThe Gumroad CLI is not logged in. A human needs to run `gumroad auth login`."
		}
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		}
	}
	if strings.TrimSpace(out) == "" {
		out = "(no output)"
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}
}

func jsonResult(v any) *mcp.CallToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return textResult("", fmt.Errorf("encode result: %w", err))
	}
	return textResult(string(b), nil)
}

func posture(o Options) string {
	switch {
	case o.ReadOnly:
		return "read-only"
	case o.AllowDanger:
		return "read + write + irreversible"
	default:
		return "read + write"
	}
}

func ptr[T any](v T) *T { return &v }

func asGum(err error, target **gumroadcli.Error) bool {
	return errors.As(err, target)
}
