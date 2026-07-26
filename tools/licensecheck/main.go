// licensecheck enforces AC-11: every module dependency's license file is
// content-hashed into licenses.lock; CI fails if a hash drifts without a
// deliberate -update (which must be accompanied by a LICENSES.md re-audit in
// the commission workspace). Rationale: license drift is a live supply-chain
// risk — Soda Core silently relicensed Apache-2.0 -> ELv2.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type mod struct {
	Path    string
	Version string
	Dir     string
	Main    bool
}

const lockPath = "licenses.lock"

func main() {
	update := flag.Bool("update", false, "rewrite licenses.lock from the current module graph")
	verify := flag.Bool("verify", false, "fail if licenses.lock does not match the current module graph")
	flag.Parse()
	if *update == *verify {
		fmt.Fprintln(os.Stderr, "licensecheck: pass exactly one of -update or -verify")
		os.Exit(2)
	}

	current, err := collect()
	if err != nil {
		fmt.Fprintln(os.Stderr, "licensecheck:", err)
		os.Exit(1)
	}

	if *update {
		b, _ := json.MarshalIndent(current, "", "  ")
		if err := os.WriteFile(lockPath, append(b, '\n'), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "licensecheck:", err)
			os.Exit(1)
		}
		fmt.Printf("licensecheck: wrote %s (%d modules)\n", lockPath, len(current))
		return
	}

	b, err := os.ReadFile(lockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "licensecheck: cannot read %s: %v\nRemediation: run `go run ./tools/licensecheck -update` and re-audit LICENSES.md.\n", lockPath, err)
		os.Exit(1)
	}
	var locked map[string]string
	if err := json.Unmarshal(b, &locked); err != nil {
		fmt.Fprintln(os.Stderr, "licensecheck: corrupt lock:", err)
		os.Exit(1)
	}

	bad := false
	for k, v := range current {
		if lv, ok := locked[k]; !ok {
			fmt.Fprintf(os.Stderr, "NEW DEPENDENCY %s — audit its LICENSE in LICENSES.md, then run -update\n", k)
			bad = true
		} else if lv != v {
			fmt.Fprintf(os.Stderr, "LICENSE DRIFT %s — license file hash changed; RE-AUDIT before -update\n", k)
			bad = true
		}
	}
	for k := range locked {
		if _, ok := current[k]; !ok {
			fmt.Fprintf(os.Stderr, "REMOVED DEPENDENCY %s — run -update to prune\n", k)
			bad = true
		}
	}
	if bad {
		os.Exit(1)
	}
	fmt.Printf("licensecheck: %d modules match %s\n", len(current), lockPath)
}

// collect maps "module@version" -> sha256 of its concatenated license files.
func collect() (map[string]string, error) {
	out, err := exec.Command("go", "list", "-m", "-json", "all").Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	res := map[string]string{}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var m mod
		if err := dec.Decode(&m); err != nil {
			return nil, err
		}
		if m.Main || m.Dir == "" {
			continue
		}
		entries, err := os.ReadDir(m.Dir)
		if err != nil {
			return nil, err
		}
		var names []string
		for _, e := range entries {
			n := strings.ToUpper(e.Name())
			if strings.HasPrefix(n, "LICENSE") || strings.HasPrefix(n, "COPYING") || strings.HasPrefix(n, "UNLICENSE") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		h := sha256.New()
		if len(names) == 0 {
			// A dependency with no license file is an audit failure by itself.
			return nil, fmt.Errorf("dependency %s@%s has no license file — not integrable (BUILD_BRIEF 3A)", m.Path, m.Version)
		}
		for _, n := range names {
			data, err := os.ReadFile(filepath.Join(m.Dir, n))
			if err != nil {
				return nil, err
			}
			h.Write([]byte(n))
			h.Write(data)
		}
		res[m.Path+"@"+m.Version] = fmt.Sprintf("%x", h.Sum(nil))
	}
	return res, nil
}
