// Neutral MCP interface command (DESIGN §9): `blueprint mcp serve` speaks
// MCP over stdio. This file is owned by the repomap+mcp feature — other
// features register commands in their own files.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"blueprint/internal/mcpserve"
)

func init() {
	Register(mcpCommand())
}

func mcpCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Model Context Protocol integration",
	}
	serve := &cobra.Command{
		Use:   "serve",
		Short: "Serve the five Blueprint retrieval tools over MCP stdio",
		Long: "Runs a local-first, zero-egress MCP server on stdin/stdout exposing\n" +
			"repo_map, search, symbols, blast_radius, and spec_lookup. Point your\n" +
			"agent's MCP config at `blueprint mcp serve`. Protocol output is JSON-RPC\n" +
			"on stdout, so --json is implicit for this command.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, err := findRepoRoot()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "blueprint mcp: serving stdio for %s (ctrl-d to stop)\n", repoRoot)
			return mcpserve.New(repoRoot).Serve(cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
	cmd.AddCommand(serve)
	return cmd
}
