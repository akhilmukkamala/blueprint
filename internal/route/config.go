// Loading of the three router input files (DESIGN §4): config.toml [router]
// thresholds, registry.toml standard-change classes, safety.toml sensitive
// paths + reversibility classes. Missing files yield safe defaults — a repo
// with no config still routes (start light, escalate on evidence).
package route

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is the [router] table of .blueprint/config.toml.
type Config struct {
	// EscalateLOC is the light→full changed-LOC line (the 200–400 LOC
	// review-collapse threshold; DESIGN §4 default 400).
	EscalateLOC int `toml:"escalate_loc" json:"escalate_loc"`
	// EscalateOnSensitiveTouch keeps sensitive-path escalation absolute.
	EscalateOnSensitiveTouch bool `toml:"escalate_on_sensitive_touch" json:"escalate_on_sensitive_touch"`
	// EscalateRadius is the blast-radius (touched-file count under the
	// fallback heuristic) light→full line. 0 means use the default.
	EscalateRadius int `toml:"escalate_radius" json:"escalate_radius"`
}

// DefaultRadiusEscalate is the fallback blast-radius threshold when
// escalate_radius is unset: past ~20 touched files a light review collapses
// for the same reason the LOC threshold exists.
const DefaultRadiusEscalate = 20

// DefaultConfig returns the documented DESIGN §4 defaults.
func DefaultConfig() Config {
	return Config{
		EscalateLOC:              400,
		EscalateOnSensitiveTouch: true,
		EscalateRadius:           DefaultRadiusEscalate,
	}
}

type configFile struct {
	Router Config `toml:"router"`
}

// LoadConfig reads .blueprint/config.toml, filling defaults for absent keys.
// A missing file is not an error; a malformed one is, with the path named.
func LoadConfig(repoRoot string) (Config, error) {
	cfg := DefaultConfig()
	p := filepath.Join(repoRoot, ".blueprint", "config.toml")
	raw, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("router: cannot read %s: %w", p, err)
	}
	var f configFile
	if _, err := toml.Decode(string(raw), &f); err != nil {
		return cfg, fmt.Errorf("router: %s is not valid TOML — fix the syntax error and re-run: %w", p, err)
	}
	if f.Router.EscalateLOC > 0 {
		cfg.EscalateLOC = f.Router.EscalateLOC
	}
	if f.Router.EscalateRadius > 0 {
		cfg.EscalateRadius = f.Router.EscalateRadius
	}
	// escalate_on_sensitive_touch defaults true; only an explicit false in the
	// file may disable it. Re-decode into a pointer shape to detect presence.
	var probe struct {
		Router struct {
			Sensitive *bool `toml:"escalate_on_sensitive_touch"`
		} `toml:"router"`
	}
	if _, err := toml.Decode(string(raw), &probe); err == nil && probe.Router.Sensitive != nil {
		cfg.EscalateOnSensitiveTouch = *probe.Router.Sensitive
	} else {
		cfg.EscalateOnSensitiveTouch = true
	}
	return cfg, nil
}

// RegistryClass is one pre-approved standard-change class (registry.toml).
// A change is exempt when its type matches, every touched path matches at
// least one glob, and the changed LOC stays within MaxLOC.
type RegistryClass struct {
	Name   string   `toml:"name" json:"name"`
	Type   string   `toml:"type" json:"type"`       // conventional-commits type; "" = any
	Globs  []string `toml:"globs" json:"globs"`     // every touched path must match one
	MaxLOC int      `toml:"max_loc" json:"max_loc"` // 0 = unlimited
	Checks []string `toml:"checks" json:"checks"`   // required checks that still run
}

// Registry is the parsed .blueprint/registry.toml.
type Registry struct {
	Classes []RegistryClass `toml:"class" json:"classes"`
}

// LoadRegistry reads .blueprint/registry.toml; a missing file is an empty
// registry (nothing is exempt).
func LoadRegistry(repoRoot string) (*Registry, error) {
	p := filepath.Join(repoRoot, ".blueprint", "registry.toml")
	var r Registry
	raw, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return &r, nil
	}
	if err != nil {
		return nil, fmt.Errorf("router: cannot read %s: %w", p, err)
	}
	if _, err := toml.Decode(string(raw), &r); err != nil {
		return nil, fmt.Errorf("router: %s is not valid TOML — each class is a [[class]] table with name, type, globs, max_loc, checks: %w", p, err)
	}
	return &r, nil
}

// Match returns the first class covering (changeType, paths, changedLOC), or
// nil. An empty path set never matches: exemption requires knowing what is
// touched.
func (r *Registry) Match(changeType string, paths []string, changedLOC int) *RegistryClass {
	if len(paths) == 0 {
		return nil
	}
	for i := range r.Classes {
		c := &r.Classes[i]
		if c.Type != "" && c.Type != changeType {
			continue
		}
		if c.MaxLOC > 0 && changedLOC > c.MaxLOC {
			continue
		}
		all := true
		for _, p := range paths {
			if _, ok := matchAny(c.Globs, p); !ok {
				all = false
				break
			}
		}
		if all {
			return c
		}
	}
	return nil
}

// ReversibilityClass groups one-way paths (schema/data/API/money — DESIGN §4
// input 4). Touching a one-way class bumps the tier one step.
type ReversibilityClass struct {
	Class  string   `toml:"class" json:"class"`
	Globs  []string `toml:"globs" json:"globs"`
	OneWay bool     `toml:"one_way" json:"one_way"`
}

// Safety is the router-relevant subset of .blueprint/safety.toml.
type Safety struct {
	Sensitive     []string             `toml:"sensitive" json:"sensitive"` // globs: auth, payments, migrations, infra, crypto
	Reversibility []ReversibilityClass `toml:"reversibility" json:"reversibility"`
}

type safetyFile struct {
	Safety struct {
		Sensitive []string `toml:"sensitive"`
	} `toml:"safety"`
	Reversibility []ReversibilityClass `toml:"reversibility"`
}

// LoadSafety reads .blueprint/safety.toml; missing file = no sensitive paths
// declared (the adoption floor — safety.toml is user-owned and appears when
// the user writes it).
func LoadSafety(repoRoot string) (*Safety, error) {
	p := filepath.Join(repoRoot, ".blueprint", "safety.toml")
	var s Safety
	raw, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return &s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("router: cannot read %s: %w", p, err)
	}
	var f safetyFile
	if _, err := toml.Decode(string(raw), &f); err != nil {
		return nil, fmt.Errorf("router: %s is not valid TOML — sensitive globs live under [safety] sensitive = [...], reversibility classes under [[reversibility]]: %w", p, err)
	}
	s.Sensitive = f.Safety.Sensitive
	s.Reversibility = f.Reversibility
	return &s, nil
}

// SensitiveHits returns each touched path that matches a sensitive glob,
// as "path (glob)" strings for reason rendering.
func (s *Safety) SensitiveHits(paths []string) []string {
	var hits []string
	for _, p := range paths {
		if g, ok := matchAny(s.Sensitive, p); ok {
			hits = append(hits, fmt.Sprintf("%s (matches %s)", filepath.ToSlash(p), g))
		}
	}
	return hits
}

// OneWayHits returns "path (class)" for each touched path in a one-way
// reversibility class.
func (s *Safety) OneWayHits(paths []string) []string {
	var hits []string
	for _, rc := range s.Reversibility {
		if !rc.OneWay {
			continue
		}
		for _, p := range paths {
			if _, ok := matchAny(rc.Globs, p); ok {
				hits = append(hits, fmt.Sprintf("%s (%s is one-way)", filepath.ToSlash(p), rc.Class))
			}
		}
	}
	return hits
}
