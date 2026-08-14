// Command gumroad-mcp serves a Gumroad store to an AI agent, refusing the
// commands that would wreck it.
//
//	gumroad-mcp mcp     MCP server on stdio, gated by risk tier
//	gumroad-mcp         interactive terminal interface
//	gumroad-mcp doctor  check that the Gumroad CLI is installed and logged in
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
