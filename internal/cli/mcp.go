package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/gshepptech/gloop/internal/gum"
	"github.com/gshepptech/gloop/internal/gumroad"
	"github.com/gshepptech/gloop/internal/mcpsrv"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func newMCPCmd() *cobra.Command {
	var (
		readOnly    bool
		allowDanger bool
	)
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve Gumroad to an AI agent over MCP (stdio)",
		Long: "Run an MCP server on stdin/stdout so an AI agent can work a Gumroad account.\n\n" +
			"gloop proxies the official `gumroad` CLI, so the agent reaches its whole surface —\n" +
			"products, sales, subscribers, licences, files, media, storefront pages, upsells,\n" +
			"audience emails, workflows, webhooks and payouts — plus the few endpoints that CLI\n" +
			"does not cover (reviews, tax forms, earnings, public product and profile data).\n\n" +
			"Every command is classified into a tier:\n" +
			"  read    cannot change anything\n" +
			"  write   changes something undoable from the Gumroad dashboard\n" +
			"  danger  irreversible or customer-visible: refunds, deletes, audience sends\n\n" +
			"Danger commands are refused outright unless --allow-danger, and even then need\n" +
			"confirm=true on each call. Gumroad's own agent skill tells models to pass --yes on\n" +
			"deletes and refunds; withholding the command is the part a model cannot talk past.\n\n" +
			"Register it with Claude Code:\n" +
			"  claude mcp add gumroad -- gloop mcp",
		Example: "  gloop mcp                 # read + write\n" +
			"  gloop mcp --read-only     # nothing can change the account\n" +
			"  gloop mcp --allow-danger  # adds refunds, deletes, audience sends",
		RunE: func(cmd *cobra.Command, args []string) error {
			if readOnly && allowDanger {
				return fmt.Errorf("--read-only and --allow-danger contradict each other")
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			r, err := runner()
			if err != nil {
				return err
			}

			// Fail here rather than on the agent's first tool call, where a
			// login prompt would surface as a confusing error mid-conversation.
			if _, err := r.Run(ctx, "auth", "status"); err != nil {
				return fmt.Errorf("the Gumroad CLI is not usable: %w", err)
			}

			// The API client covers only what the CLI cannot, and borrows the
			// CLI's token so gloop stores no credential of its own. Losing it
			// is not fatal: the proxied commands are the important ones.
			var api *gumroad.Client
			if tok, err := r.Token(ctx); err == nil {
				api = gumroad.New(tok)
			} else {
				fmt.Fprintf(os.Stderr,
					"gloop: could not read the CLI token, so reviews/tax/earnings tools are off (%v)\n", err)
			}

			srv := mcpsrv.New(r, api, mcpsrv.Options{
				ReadOnly:    readOnly,
				AllowDanger: allowDanger,
			})

			read, write, danger, _ := gum.Counts()
			mode := fmt.Sprintf("%d read + %d write", read, write)
			switch {
			case readOnly:
				mode = fmt.Sprintf("%d read only", read)
			case allowDanger:
				mode = fmt.Sprintf("%d read + %d write + %d irreversible", read, write, danger)
			}
			fmt.Fprintf(os.Stderr, "gloop mcp on stdio — %s\n", mode)

			return srv.Run(ctx, &mcp.StdioTransport{})
		},
	}
	cmd.Flags().BoolVar(&readOnly, "read-only", false, "refuse every command that could change the account")
	cmd.Flags().BoolVar(&allowDanger, "allow-danger", false, "also allow irreversible commands (refunds, deletes, audience sends)")
	return cmd
}
