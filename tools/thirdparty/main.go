// thirdparty renders docs/THIRD_PARTY.md — the third-party notice shipped in
// every release artifact (DESIGN §11) — from licenses.lock (the audited module
// set) plus each module's license files in the local module cache. The output
// is deterministic: same lock + same module cache -> byte-identical file, so
// the generated document is committed and regenerated only when dependencies
// change (licensecheck already gates that).
//
// Usage: `go run ./tools/thirdparty` writes docs/THIRD_PARTY.md;
// `go run ./tools/thirdparty -o <path>` writes elsewhere.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Entry is one third-party module row.
type Entry struct {
	Module  string
	Version string
	License string // detected SPDX-ish name(s), " / "-joined when several files
}

func main() {
	out := flag.String("o", filepath.Join("docs", "THIRD_PARTY.md"), "output path")
	flag.Parse()

	entries, err := collect()
	if err != nil {
		fmt.Fprintln(os.Stderr, "thirdparty:", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "thirdparty:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, render(entries), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "thirdparty:", err)
		os.Exit(1)
	}
	fmt.Printf("thirdparty: wrote %s (%d modules)\n", *out, len(entries))
}

// collect resolves every module pinned in licenses.lock to its license
// file(s) via the module cache and classifies them.
func collect() ([]Entry, error) {
	raw, err := os.ReadFile("licenses.lock")
	if err != nil {
		return nil, fmt.Errorf("cannot read licenses.lock (run from the repo root): %w", err)
	}
	var locked map[string]string
	if err := json.Unmarshal(raw, &locked); err != nil {
		return nil, fmt.Errorf("corrupt licenses.lock: %w", err)
	}

	type mod struct {
		Path    string
		Version string
		Dir     string
		Main    bool
	}
	outBytes, err := exec.Command("go", "list", "-m", "-json", "all").Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	dirs := map[string]string{} // module@version -> cache dir
	dec := json.NewDecoder(bytes.NewReader(outBytes))
	for dec.More() {
		var m mod
		if err := dec.Decode(&m); err != nil {
			return nil, err
		}
		if !m.Main && m.Dir != "" {
			dirs[m.Path+"@"+m.Version] = m.Dir
		}
	}

	keys := make([]string, 0, len(locked))
	for k := range locked {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var entries []Entry
	for _, k := range keys {
		at := strings.LastIndex(k, "@")
		if at < 0 {
			return nil, fmt.Errorf("malformed lock key %q", k)
		}
		dir, ok := dirs[k]
		if !ok {
			return nil, fmt.Errorf("locked module %s is not in the module graph — licenses.lock is stale; run `go run ./tools/licensecheck -update` after a re-audit", k)
		}
		lic, err := detectLicense(dir)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		entries = append(entries, Entry{Module: k[:at], Version: k[at+1:], License: lic})
	}
	return entries, nil
}

// detectLicense classifies every license file in dir (same file heuristic as
// tools/licensecheck) and joins distinct results.
func detectLicense(dir string) (string, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var names []string
	for _, e := range files {
		n := strings.ToUpper(e.Name())
		if strings.HasPrefix(n, "LICENSE") || strings.HasPrefix(n, "COPYING") || strings.HasPrefix(n, "UNLICENSE") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "", fmt.Errorf("no license file — not integrable")
	}
	var found []string
	seen := map[string]bool{}
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return "", err
		}
		for _, c := range classify(string(data)) {
			if !seen[c] {
				seen[c] = true
				found = append(found, c)
			}
		}
	}
	return strings.Join(found, " / "), nil
}

// classify names every license found in one text by its distinctive phrases
// (a single file can carry several — gopkg.in/yaml.v3 ships MIT and
// Apache-2.0 in one LICENSE). Unrecognized texts are reported, never
// guessed — the shipped notice must not misstate a license.
func classify(text string) []string {
	t := strings.Join(strings.Fields(text), " ") // collapse whitespace
	var out []string
	if strings.Contains(t, "Permission is hereby granted, free of charge, to any person obtaining a copy") {
		out = append(out, "MIT")
	}
	if strings.Contains(t, "Apache License") && strings.Contains(t, "Version 2.0") {
		out = append(out, "Apache-2.0")
	}
	if strings.Contains(t, "Permission to use, copy, modify, and/or distribute this software for any purpose") ||
		strings.Contains(t, "Permission to use, copy, modify, and distribute this software for any purpose") {
		out = append(out, "ISC")
	}
	if strings.Contains(t, "Redistribution and use in source and binary forms") {
		lower := strings.ToLower(t)
		if strings.Contains(lower, "neither the name") ||
			strings.Contains(lower, "names of its contributors may not be used") {
			out = append(out, "BSD-3-Clause")
		} else {
			out = append(out, "BSD-2-Clause")
		}
	}
	if strings.Contains(t, "Mozilla Public License") {
		out = append(out, "MPL-2.0")
	}
	if strings.Contains(t, "This is free and unencumbered software released into the public domain") {
		out = append(out, "Unlicense")
	}
	if len(out) == 0 {
		out = append(out, "UNRECOGNIZED (audit manually)")
	}
	return out
}

// render produces the committed THIRD_PARTY.md. Deterministic: no timestamps,
// entries in lock order (sorted).
func render(entries []Entry) []byte {
	var b bytes.Buffer
	b.WriteString("# Third-party notices\n\n")
	b.WriteString("Blueprint itself is MIT-licensed (see LICENSE at the repo root). Release\n")
	b.WriteString("binaries statically link the Go modules below. Each module's full license\n")
	b.WriteString("text ships in its source distribution; the exact license-file contents are\n")
	b.WriteString("content-hashed in `licenses.lock`, and CI fails on any drift\n")
	b.WriteString("(`tools/licensecheck`).\n\n")
	b.WriteString("| Module | Version | License |\n")
	b.WriteString("|--------|---------|--------|\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", e.Module, e.Version, e.License)
	}
	b.WriteString("\nGenerated by `go run ./tools/thirdparty` from `licenses.lock`. Regenerate\n")
	b.WriteString("whenever dependencies change; the file is committed so offline artifacts\n")
	b.WriteString("can include it without a toolchain.\n")
	return b.Bytes()
}
