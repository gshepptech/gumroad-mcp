package cli

import (
	"context"

	"github.com/gshepptech/gumroad-mcp/internal/tui"
	"github.com/spf13/cobra"
)

func newTUICmd() *cobra.Command {
	return &cobra.Command{
		Use:     "tui",
		Aliases: []string{"ui", "browse"},
		Short:   "Open the interactive interface (same as running `gumroad-mcp` with no arguments)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(cmd.Context())
		},
	}
}

func runTUI(c context.Context) error {
	r, err := runner()
	if err != nil {
		return err
	}
	return tui.Run(c, r)
}
