// hooks.go — hooks-liveness check. The superpowers scar: .claude/settings.json
// can declare hooks whose command files were moved or never committed, and the
// host runs NOTHING while everyone believes the guardrail is active. Doctor
// walks the declared hook commands and proves each referenced executable
// still resolves.
package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"blueprint/internal/adapters"
)

// checkHooksLiveness inspects .claude/settings.json (if present). No settings
// file, or a settings file without hooks, passes vacuously — the check guards
// against SILENTLY inert hooks, not against not using hooks.
func checkHooksLiveness(repoRoot string, opts Options) Check {
	path := filepath.Join(repoRoot, ".claude", "settings.json")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Check{Name: "hooks-liveness", Pass: true, Detail: "no .claude/settings.json — nothing declared, nothing inert"}
	}
	if err != nil {
		return Check{Name: "hooks-liveness", Pass: false,
			Detail:      fmt.Sprintf("cannot read %s: %v", path, err),
			Remediation: "fix permissions on .claude/settings.json so declared hooks can be verified"}
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Check{Name: "hooks-liveness", Pass: false,
			Detail:      fmt.Sprintf(".claude/settings.json is not valid JSON: %v", err),
			Remediation: "repair the JSON — a malformed settings file means the host ignores every hook in it"}
	}
	hooks, ok := doc["hooks"]
	if !ok {
		return Check{Name: "hooks-liveness", Pass: true, Detail: ".claude/settings.json declares no hooks"}
	}

	var commands []string
	collectCommands(hooks, &commands)
	if len(commands) == 0 {
		return Check{Name: "hooks-liveness", Pass: true, Detail: "hooks key present but no command entries"}
	}

	var dead []string
	for _, cmd := range commands {
		if target, ok := commandTarget(repoRoot, cmd); ok {
			if !resolves(repoRoot, target, opts) {
				dead = append(dead, fmt.Sprintf("%q -> %s", firstLine(cmd), target))
			}
		}
	}
	if len(dead) > 0 {
		return Check{Name: "hooks-liveness", Pass: false,
			Detail:      fmt.Sprintf("%d hook command(s) reference missing files: %s", len(dead), strings.Join(dead, "; ")),
			Remediation: "restore the referenced hook scripts (or delete the dead entries from .claude/settings.json) — declared-but-missing hooks run nothing while looking active"}
	}
	return Check{Name: "hooks-liveness", Pass: true,
		Detail: fmt.Sprintf("all %d declared hook command(s) resolve", len(commands))}
}

// checkSafetyDenyRules is the liveness follow-through for the safety compile:
// `blueprint sync` compiles .blueprint/safety.toml [deny].write into Claude
// Code permission deny rules in .claude/settings.json, and — same scar as
// silently inert hooks — a denylist that never reached settings.json protects
// nothing while everyone believes it does. When [deny].write is non-empty,
// every compiled rule must actually be present.
func checkSafetyDenyRules(repoRoot string) Check {
	const name = "safety-deny-rules"
	denyGlobs, err := adapters.LoadDenyGlobs(repoRoot)
	if err != nil {
		return Check{Name: name, Pass: false,
			Detail:      err.Error(),
			Remediation: "fix .blueprint/safety.toml so its [deny].write list parses — until it does, no deny rules reach any agent surface"}
	}
	if len(denyGlobs) == 0 {
		return Check{Name: name, Pass: true,
			Detail: "safety.toml declares no [deny].write globs — nothing to compile, nothing inert"}
	}

	path := filepath.Join(repoRoot, ".claude", "settings.json")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Check{Name: name, Pass: false,
			Detail:      fmt.Sprintf("safety.toml [deny].write has %d glob(s) but .claude/settings.json does not exist — the denylist is not enforced anywhere on this host", len(denyGlobs)),
			Remediation: "run `blueprint sync` — it compiles [deny].write into Claude Code permission deny rules in .claude/settings.json"}
	}
	if err != nil {
		return Check{Name: name, Pass: false,
			Detail:      fmt.Sprintf("cannot read %s: %v", path, err),
			Remediation: "fix permissions on .claude/settings.json so the compiled deny rules can be verified"}
	}
	var doc struct {
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Check{Name: name, Pass: false,
			Detail:      fmt.Sprintf(".claude/settings.json is not valid JSON: %v", err),
			Remediation: "repair the JSON, then run `blueprint sync` — a malformed settings file means the host enforces none of the deny rules in it"}
	}
	present := map[string]bool{}
	for _, r := range doc.Permissions.Deny {
		present[r] = true
	}
	var missing []string
	for _, g := range denyGlobs {
		for _, rule := range []string{"Write(" + g + ")", "Edit(" + g + ")"} {
			if !present[rule] {
				missing = append(missing, rule)
			}
		}
	}
	if len(missing) > 0 {
		return Check{Name: name, Pass: false,
			Detail:      fmt.Sprintf("%d compiled deny rule(s) missing from .claude/settings.json permissions.deny: %s", len(missing), strings.Join(missing, ", ")),
			Remediation: "run `blueprint sync` to recompile safety.toml [deny].write into settings.json — a deny glob that never reached the host blocks nothing"}
	}
	return Check{Name: name, Pass: true,
		Detail: fmt.Sprintf("all %d deny glob(s) from safety.toml are compiled into .claude/settings.json", len(denyGlobs))}
}

// collectCommands walks arbitrary JSON gathering every string value keyed
// "command" under the hooks tree — shape-agnostic on purpose, because the
// settings schema has grown fields before and a stricter parse would go
// silently blind (the exact failure this check exists to catch).
func collectCommands(v any, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if k == "command" {
				if s, ok := child.(string); ok && s != "" {
					*out = append(*out, s)
				}
				continue
			}
			collectCommands(child, out)
		}
	case []any:
		for _, child := range t {
			collectCommands(child, out)
		}
	}
}

// commandTarget extracts the checkable target from a hook command line: the
// first shell token, with $CLAUDE_PROJECT_DIR expanded. Returns ok=false when
// the token carries other unexpandable shell syntax (we cannot verify what we
// cannot resolve — better silent-skip than false alarm).
func commandTarget(repoRoot, cmd string) (string, bool) {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return "", false
	}
	tok := fields[0]
	tok = strings.ReplaceAll(tok, "${CLAUDE_PROJECT_DIR}", repoRoot)
	tok = strings.ReplaceAll(tok, "$CLAUDE_PROJECT_DIR", repoRoot)
	tok = strings.Trim(tok, `"'`)
	if strings.ContainsAny(tok, "$`(){}|&;<>") {
		return "", false
	}
	return tok, true
}

// resolves reports whether a hook target exists: path-like tokens must stat
// (relative ones against repoRoot), bare names must be on PATH.
func resolves(repoRoot, target string, opts Options) bool {
	if strings.ContainsAny(target, `/\`) || strings.HasPrefix(target, ".") {
		p := target
		if !filepath.IsAbs(p) {
			p = filepath.Join(repoRoot, filepath.FromSlash(target))
		}
		_, err := os.Stat(p)
		return err == nil
	}
	_, err := opts.lookPath(target)
	return err == nil
}
