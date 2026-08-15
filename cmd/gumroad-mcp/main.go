// Command gumroad-mcp gives an AI agent safe access to a Gumroad store.
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
