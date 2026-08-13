// Package cli builds gloop's command tree.
//
// gloop is deliberately small. Everyday Gumroad work belongs to the official
// `gumroad` CLI, which gloop drives rather than duplicates. gloop adds two
// things that CLI does not have: an MCP server with a tier policy, and an
// interactive interface.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/gshepptech/gloop/internal/gum"
	"github.com/spf13/cobra"
)

// Version is stamped at build time via -ldflags.
var Version = "dev"

var (
	styleTitle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FF90E8"))
	styleAccent = lipgloss.NewStyle().Foreground(lipgloss.Color("#7B61FF"))
	styleDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	styleErr    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5252"))
	styleOK     = lipgloss.NewStyle().Foreground(lipgloss.Color("#00C853"))
)

// Run executes the command tree and returns the process exit code.
func Run(c context.Context) int {
	if err := newRoot().ExecuteContext(c); err != nil {
		if errors.Is(err, context.Canceled) {
			return 130 // interrupted
		}
		fmt.Fprintln(os.Stderr, styleErr.Render("error: ")+err.Error())

		var gerr *gum.Error
		if errors.As(err, &gerr) && gerr.NeedsAuth() {
			fmt.Fprintln(os.Stderr, styleDim.Render("  run: gumroad auth login"))
		}
		return 1
	}
	return 0
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "gloop",
		Short: "An MCP server and terminal interface for Gumroad",
		Long: styleTitle.Render("gloop") + " wraps the official Gumroad CLI and adds two things:\n\n" +
			"  " + styleAccent.Render("gloop mcp") + "     serve Gumroad to an AI agent, with a tier policy\n" +
			"                deciding what it may invoke\n" +
			"  " + styleAccent.Render("gloop") + "         browse the account interactively\n\n" +
			"For everyday commands use " + styleAccent.Render("gumroad") + " directly — it covers more of the\n" +
			"API than gloop reasonably could, and gloop runs it under the hood.\n\n" +
			"Authenticate once with " + styleAccent.Render("gumroad auth login") + "; gloop shares that session.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       Version,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(cmd.Context())
		},
	}
	root.AddCommand(newMCPCmd(), newTUICmd(), newDoctorCmd())
	return root
}

// runner resolves the official CLI, or explains how to install it.
func runner() (*gum.Runner, error) {
	return gum.New()
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that gloop can drive the Gumroad CLI",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			r, err := runner()
			if err != nil {
				fmt.Fprintf(os.Stdout, "%s Gumroad CLI not found\n\n%v\n", styleErr.Render("✗"), err)
				return errors.New("the Gumroad CLI is required")
			}
			fmt.Fprintf(os.Stdout, "%s gumroad found at %s\n", styleOK.Render("✓"), r.Path)

			ver, err := r.Version(ctx)
			if err != nil {
				fmt.Fprintf(os.Stdout, "%s could not read its version: %v\n", styleErr.Render("✗"), err)
				return err
			}
			fmt.Fprintf(os.Stdout, "%s version %s\n", styleOK.Render("✓"), ver)

			if _, err := r.Run(ctx, "auth", "status"); err != nil {
				fmt.Fprintf(os.Stdout, "%s not logged in\n\n  run: gumroad auth login\n", styleErr.Render("✗"))
				return err
			}
			fmt.Fprintf(os.Stdout, "%s authenticated\n", styleOK.Render("✓"))

			read, write, danger, forbidden := gum.Counts()
			fmt.Fprintf(os.Stdout, "\n%s\n", styleTitle.Render("policy"))
			fmt.Fprintf(os.Stdout, "  %d read  %d write  %d irreversible  %d never proxied\n",
				read, write, danger, forbidden)
			return nil
		},
	}
}
