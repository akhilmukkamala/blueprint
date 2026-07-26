// model.go — optional model consolidation (DESIGN §12 stage 2). Like the
// verify model checker (internal/verify/checker.go), this is a HOOK, not an
// API client: blueprint execs the [dream] command from .blueprint/config.toml
// and feeds it a JSON packet on stdin. The binary itself stays
// network-incapable (AC-12) — any batch-API call happens inside the
// user-configured external command. Unconfigured means deterministic-only
// proposals; a failing command degrades to deterministic with a warning,
// never a hard failure, because dream output is advisory.
package dream

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const defaultMaxUSD = 2.0

// modelRubric is the fixed instruction set every [dream] command receives.
const modelRubric = "Consolidate the signals into at most 5 proposal items. Each item: cite the signal ids it derives from (signal_ids), change at most 40 lines if it carries a patch, and propose only registry candidates, tightened caps/breakers, banned-word additions, steering corrections, or spec clarifications. [user]-tier files (steering, registry.toml, config.toml, specs) must be proposed as unified-diff patches, never as instructions to edit in place. Never propose applying anything derived from a quarantined signal. Output a JSON array of {title, body, signal_ids, patch} objects on stdout and stay within max_usd."

type dreamConfig struct {
	Command string  `toml:"command"`
	MaxUSD  float64 `toml:"max_usd"`
}

type dreamConfigFile struct {
	Dream dreamConfig `toml:"dream"`
}

func loadDreamConfig(repoRoot string) (dreamConfig, error) {
	cfg := dreamConfig{MaxUSD: defaultMaxUSD}
	b, err := os.ReadFile(filepath.Join(repoRoot, ".blueprint", "config.toml"))
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	var f dreamConfigFile
	if err := toml.Unmarshal(b, &f); err != nil {
		return cfg, fmt.Errorf(".blueprint/config.toml is not valid TOML (%v); the dream hook is a [dream] table with command and optional max_usd", err)
	}
	cfg.Command = f.Dream.Command
	if f.Dream.MaxUSD > 0 {
		cfg.MaxUSD = f.Dream.MaxUSD
	}
	return cfg, nil
}

// modelPacket is the stdin contract for external [dream] commands.
type modelPacket struct {
	Date     string            `json:"date"`
	Signals  []Signal          `json:"signals"`
	Steering map[string]string `json:"steering"` // filename -> content (excerpted)
	Registry string            `json:"registry"` // .blueprint/registry.toml (excerpted)
	Config   string            `json:"config"`   // .blueprint/config.toml (excerpted)
	MaxUSD   float64           `json:"max_usd"`  // cost cap the command must honor
	Rubric   string            `json:"rubric"`
}

// modelItem is one entry of the JSON array a [dream] command prints on stdout.
type modelItem struct {
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	SignalIDs []string `json:"signal_ids"`
	Patch     string   `json:"patch,omitempty"`
}

// excerptLimit caps file excerpts in the packet; steering/config files are
// short by design, so 4 KiB keeps packets bounded without losing substance.
const excerptLimit = 4 * 1024

func excerpt(b []byte) string {
	if len(b) > excerptLimit {
		return string(b[:excerptLimit]) + "\n… [truncated]"
	}
	return string(b)
}

// buildModelPacket assembles the stdin packet: signals plus current
// steering/registry/config excerpts, all read-only.
func buildModelPacket(repoRoot, date string, signals []Signal, maxUSD float64) modelPacket {
	p := modelPacket{
		Date:     date,
		Signals:  signals,
		Steering: map[string]string{},
		MaxUSD:   maxUSD,
		Rubric:   modelRubric,
	}
	if b, err := os.ReadFile(filepath.Join(repoRoot, ".blueprint", "registry.toml")); err == nil {
		p.Registry = excerpt(b)
	}
	if b, err := os.ReadFile(filepath.Join(repoRoot, ".blueprint", "config.toml")); err == nil {
		p.Config = excerpt(b)
	}
	dir := filepath.Join(repoRoot, ".blueprint", "steering")
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			if b, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
				p.Steering[e.Name()] = excerpt(b)
			}
		}
	}
	return p
}

// runModel execs the configured command with the packet on stdin and parses
// its stdout as a JSON item array.
func runModel(repoRoot, command string, packet modelPacket) ([]modelItem, error) {
	args := splitCommand(command)
	if len(args) == 0 {
		return nil, fmt.Errorf("[dream] command in .blueprint/config.toml is empty; set it to an executable that reads the JSON packet on stdin")
	}
	in, err := json.Marshal(packet)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = repoRoot
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("[dream] command failed (%v): %s — fix the command in .blueprint/config.toml or unset it for deterministic-only proposals", err, strings.TrimSpace(stderr.String()))
	}
	var items []modelItem
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &items); err != nil {
		return nil, fmt.Errorf("[dream] command output is not a JSON array of {title, body, signal_ids, patch} items: %v", err)
	}
	return items, nil
}

// consolidate validates model items against the evidence rubric and converts
// them to proposal items: unknown signal citations drop the item, items citing
// any quarantined signal are quarantined themselves, and the ≤5-item cap
// applies. The per-item patch cap is enforced later in enforceItems, same as
// the deterministic path.
func consolidate(items []modelItem, signals []Signal) ([]Item, []string) {
	known := map[string]Signal{}
	for _, s := range signals {
		known[s.ID] = s
	}
	var out []Item
	var warnings []string
	for _, mi := range items {
		if strings.TrimSpace(mi.Title) == "" || len(mi.SignalIDs) == 0 {
			warnings = append(warnings, "dream: dropped a model item without a title or signal citations — every item must cite signal evidence ids")
			continue
		}
		valid := true
		quarantined := false
		for _, sid := range mi.SignalIDs {
			s, ok := known[sid]
			if !ok {
				warnings = append(warnings, fmt.Sprintf("dream: dropped model item %q — it cites unknown signal id %q", mi.Title, sid))
				valid = false
				break
			}
			quarantined = quarantined || s.Quarantined
		}
		if !valid {
			continue
		}
		out = append(out, Item{
			Title:       mi.Title,
			Body:        mi.Body,
			SignalIDs:   mi.SignalIDs,
			Patch:       mi.Patch,
			Quarantined: quarantined,
		})
		if len(out) == maxItems {
			if len(items) > maxItems {
				warnings = append(warnings, fmt.Sprintf("dream: model proposed more than %d items; extras were dropped (AC-10 cap)", maxItems))
			}
			break
		}
	}
	return out, warnings
}

// splitCommand tokenizes the hook command honoring double/single quotes —
// same contract as internal/verify's checker hook: deliberately not a shell
// (no expansion, pipes, or redirects; Windows-clean rule). Commands needing
// shell features wrap themselves in a script.
func splitCommand(s string) []string {
	var (
		args  []string
		cur   strings.Builder
		quote rune
		has   bool
	)
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
			has = true
		case r == ' ' || r == '\t':
			if has || cur.Len() > 0 {
				args = append(args, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if has || cur.Len() > 0 {
		args = append(args, cur.String())
	}
	return args
}
