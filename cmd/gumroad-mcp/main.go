// Command gloop is a Gumroad client for humans and AI agents.
//
//	gloop              interactive terminal interface
//	gloop mcp          MCP server for AI agents
//	gloop <command>    one operation, printed
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/gshepptech/gumroad-mcp/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(cli.Run(ctx))
}
