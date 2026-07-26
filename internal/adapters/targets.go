// Package adapters generates the per-tool surfaces (DESIGN §10) from the
// canonical .blueprint/ sources: instruction shims, command stubs, steering
// rule transforms, and merged MCP configs for Claude Code, Cursor, Codex CLI,
// Windsurf, GitHub Copilot, and Gemini CLI. Generation is idempotent and pure
// repo-state -> bytes; `blueprint sync` applies it, `--check` diffs it,
// `--revert` restores the pre-sync originals.
package adapters

// GeneratorVersion stamps every provenance header. Bump when generated output
// changes shape so drift checks distinguish "user edited" from "old generator".
const GeneratorVersion = "v0"

// TargetTableVersion versions the target table itself. The table is DATA on
// purpose: tool surface paths churn roughly quarterly (RESEARCH §1.6), and a
// data table can be diffed, overridden, and staleness-probed without touching
// generation logic. v2 added windsurf, copilot, and gemini (research D7,
// verified 2026-07-20).
const TargetTableVersion = 2

// RulesTransform names how a neutral steering file becomes a target rule file.
type RulesTransform string

const (
	// TransformCopy keeps the neutral YAML frontmatter keys as-is.
	TransformCopy RulesTransform = "copy"
	// TransformMDC rewrites frontmatter to Cursor's .mdc keys
	// (description/globs/alwaysApply).
	TransformMDC RulesTransform = "mdc"
	// TransformWindsurf rewrites frontmatter to Windsurf's keys
	// (trigger: always_on|glob|manual, plus globs/description).
	TransformWindsurf RulesTransform = "windsurf"
	// TransformApplyTo rewrites frontmatter to Copilot's
	// .instructions.md keys (applyTo glob string + description).
	TransformApplyTo RulesTransform = "applyto"
)

// MCPFormat names the on-disk MCP config syntax of a target.
type MCPFormat string

const (
	MCPFormatJSON MCPFormat = "mcpjson" // {"mcpServers": {...}}
	MCPFormatTOML MCPFormat = "toml"    // [mcp_servers.<name>] tables
	// MCPFormatGeminiSettings merges mcpServers AND the context.fileName
	// AGENTS.md wiring into .gemini/settings.json. Unlike the other formats
	// the file is generated even with zero declared servers, because the
	// context key is Gemini's instruction surface (research D7 §6: AGENTS.md
	// support is "one settings key away, not native default").
	MCPFormatGeminiSettings MCPFormat = "gemini-settings"
)

// CommandsFormat names the on-disk syntax of generated command stubs.
type CommandsFormat string

const (
	CommandsMarkdown CommandsFormat = "markdown" // default when empty
	CommandsTOML     CommandsFormat = "toml"     // Gemini custom commands
)

// Target is one tool's surface map. Empty fields mean the tool has no such
// generated surface (e.g. it reads AGENTS.md natively, so no instruction shim).
type Target struct {
	Name            string
	InstructionPath string // generated shim pointing at AGENTS.md
	RulesDir        string // where transformed steering rules land
	RulesTransform  RulesTransform
	RulesExt        string // rule file extension including dot
	// RulesMirrorDirs receive byte-identical copies of every rule file —
	// used while a tool migrates between directories (Windsurf reads
	// .windsurf/rules/ but .devin/rules/ is becoming preferred post the
	// Cognition acquisition; research D7 §5 says emit BOTH).
	RulesMirrorDirs []string
	// RuleByteBudget caps each rendered rule file in bytes (0 = no cap).
	// Oversize bodies are split into numbered parts, each under the cap,
	// with a provenance note in the frontmatter. Bytes >= chars in UTF-8,
	// so a byte cap safely enforces a documented char cap.
	RuleByteBudget int
	CommandsDir    string         // where blueprint-* command stubs land
	CommandsExt    string         // stub extension including dot; "" means ".md"
	CommandsFormat CommandsFormat // "" means CommandsMarkdown
	MCPConfigPath  string         // merged MCP config file
	MCPFormat      MCPFormat
	// MCPNotePath is emitted instead of a config when the tool has no
	// generatable project-level MCP surface (unsupported, or the location
	// is undocumented). Documented honesty beats guessing a path.
	MCPNotePath string
}

// Targets returns the versioned target table. Order is stable (generation and
// gitignore output depend on it). Cursor, Codex, Windsurf, and Copilot read
// AGENTS.md natively, so only Claude Code gets an instruction shim (Gemini's
// equivalent wiring is the context.fileName settings key); Codex steering
// flows through nested AGENTS.md files (user-curated, not generated here), so
// its RulesDir is empty, and Gemini has no scoped-rules surface at all.
func Targets() []Target {
	return []Target{
		{
			Name:            "claude-code",
			InstructionPath: "CLAUDE.md",
			RulesDir:        ".claude/rules",
			RulesTransform:  TransformCopy,
			RulesExt:        ".md",
			CommandsDir:     ".claude/commands",
			MCPConfigPath:   ".mcp.json",
			MCPFormat:       MCPFormatJSON,
		},
		{
			Name:           "cursor",
			RulesDir:       ".cursor/rules",
			RulesTransform: TransformMDC,
			RulesExt:       ".mdc",
			CommandsDir:    ".cursor/commands",
			MCPConfigPath:  ".cursor/mcp.json",
			MCPFormat:      MCPFormatJSON,
		},
		{
			Name:          "codex",
			CommandsDir:   ".codex/prompts",
			MCPConfigPath: ".codex/config.toml",
			MCPFormat:     MCPFormatTOML,
		},
		{
			// Windsurf: workspace rules capped at 12,000 chars/file
			// (research D7 §5); workflows are its command surface. Its
			// project-level MCP config location was UNCONFIRMED in the
			// research pass, so a note file is emitted instead of a guess.
			Name:            "windsurf",
			RulesDir:        ".windsurf/rules",
			RulesTransform:  TransformWindsurf,
			RulesExt:        ".md",
			RulesMirrorDirs: []string{".devin/rules"},
			RuleByteBudget:  12000,
			CommandsDir:     ".windsurf/workflows",
			MCPNotePath:     ".windsurf/blueprint-mcp-note.md",
		},
		{
			// Copilot: path-scoped instructions with applyTo frontmatter,
			// prompt files as the command surface (research D7 §4). No
			// repo-level MCP config surface exists — note file.
			Name:           "copilot",
			RulesDir:       ".github/instructions",
			RulesTransform: TransformApplyTo,
			RulesExt:       ".instructions.md",
			CommandsDir:    ".github/prompts",
			CommandsExt:    ".prompt.md",
			MCPNotePath:    ".github/blueprint-mcp-note.md",
		},
		{
			// Gemini CLI: TOML custom commands; settings.json carries both
			// the AGENTS.md context wiring and mcpServers (research D7 §6).
			Name:           "gemini",
			CommandsDir:    ".gemini/commands",
			CommandsExt:    ".toml",
			CommandsFormat: CommandsTOML,
			MCPConfigPath:  ".gemini/settings.json",
			MCPFormat:      MCPFormatGeminiSettings,
		},
	}
}
