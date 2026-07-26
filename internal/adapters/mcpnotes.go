// MCP note files: emitted for targets where declared servers exist but no
// project-level MCP config can be generated honestly — either the surface is
// unsupported (Copilot) or its documented location could not be confirmed
// (Windsurf, research D7 "Could not verify"). Documented-unknown beats a
// guessed path that silently does nothing.
package adapters

import (
	"fmt"
	"sort"
	"strings"
)

// mcpServerList renders the declared servers as launch lines the user can
// copy into whatever config their tool actually reads.
func mcpServerList(servers map[string]MCPServer) string {
	names := make([]string, 0, len(servers))
	for n := range servers {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		s := servers[n]
		line := s.Command
		if len(s.Args) > 0 {
			line += " " + strings.Join(s.Args, " ")
		}
		fmt.Fprintf(&b, "- `%s`: `%s`\n", n, line)
	}
	return b.String()
}

// RenderMCPNote renders the per-target note file named by t.MCPNotePath.
func RenderMCPNote(t Target, servers map[string]MCPServer) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- %s %s from .blueprint/config.toml [mcp.servers]; edit the source, then run `blueprint sync` -->\n", provenanceMarker, GeneratorVersion)
	switch t.Name {
	case "windsurf":
		b.WriteString("# Windsurf MCP note (blueprint)\n\n")
		b.WriteString("Windsurf's project-level MCP config location is NOT confirmed in current\n")
		b.WriteString("official docs (the widely reported `~/.codeium/windsurf/mcp_config.json`\n")
		b.WriteString("is user-level and unverified), so blueprint generates no Windsurf MCP\n")
		b.WriteString("config rather than guessing a path the tool may silently ignore.\n\n")
	case "copilot":
		b.WriteString("# GitHub Copilot MCP note (blueprint)\n\n")
		b.WriteString("Copilot has no repo-level MCP config surface blueprint can generate\n")
		b.WriteString("(VS Code reads editor-level `.vscode/mcp.json`; other Copilot surfaces\n")
		b.WriteString("configure MCP outside the repository), so no config file is emitted.\n\n")
	default:
		fmt.Fprintf(&b, "# %s MCP note (blueprint)\n\n", t.Name)
		b.WriteString("This tool has no generatable project-level MCP config surface.\n\n")
	}
	b.WriteString("Servers declared in .blueprint/config.toml, for manual wiring wherever\n")
	b.WriteString("this tool actually reads MCP config:\n\n")
	b.WriteString(mcpServerList(servers))
	return []byte(b.String())
}
