package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Golden test: render is a pure function of its entries; the shipped
// THIRD_PARTY.md format must not drift silently.
func TestRenderGolden(t *testing.T) {
	entries := []Entry{
		{Module: "example.com/alpha", Version: "v1.2.3", License: "MIT"},
		{Module: "example.com/beta", Version: "v0.9.0", License: "Apache-2.0"},
		{Module: "example.com/gamma", Version: "v2.0.0-rc.1", License: "Apache-2.0 / MIT"},
	}
	got := render(entries)

	golden := filepath.Join("testdata", "golden.md")
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v (regenerate by writing render output to %s)", err, golden)
	}
	if string(got) != string(want) {
		t.Errorf("render drifted from golden.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"mit", "MIT License\n\nPermission is hereby granted, free of charge, to any person obtaining a copy of this software...", "MIT"},
		{"apache", "Apache License\n   Version 2.0, January 2004\n", "Apache-2.0"},
		{"isc", "ISC License\nPermission to use, copy, modify, and/or distribute this software for any purpose with or without fee...", "ISC"},
		{"bsd3", "Redistribution and use in source and binary forms...\n3. Neither the name of the copyright holder...", "BSD-3-Clause"},
		{"bsd3-contributors", "Redistribution and use in source and binary forms...\nThe names of its contributors may not be used to endorse...", "BSD-3-Clause"},
		{"bsd2", "Redistribution and use in source and binary forms, with or without modification, are permitted...", "BSD-2-Clause"},
		{"dual mit+apache", "covered by two licenses.\nPermission is hereby granted, free of charge, to any person obtaining a copy...\nApache License Version 2.0", "MIT,Apache-2.0"},
		{"unknown", "You may do whatever you like.", "UNRECOGNIZED (audit manually)"},
	}
	for _, tc := range cases {
		if got := strings.Join(classify(tc.text), ","); got != tc.want {
			t.Errorf("classify(%s) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The committed docs/THIRD_PARTY.md must match a fresh render of the current
// module graph — a stale notice in release artifacts is a licensing defect.
func TestCommittedThirdPartyIsCurrent(t *testing.T) {
	t.Chdir("../..") // collect() reads licenses.lock at the repo root
	entries, err := collect()
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	want := render(entries)
	got, err := os.ReadFile(filepath.Join("docs", "THIRD_PARTY.md"))
	if err != nil {
		t.Fatalf("read committed docs/THIRD_PARTY.md: %v — run `go run ./tools/thirdparty`", err)
	}
	if string(got) != string(want) {
		t.Error("docs/THIRD_PARTY.md is stale — run `go run ./tools/thirdparty` and commit the result")
	}
	for _, e := range entries {
		if e.License == "UNRECOGNIZED (audit manually)" {
			t.Errorf("module %s@%s has an unrecognized license — audit it and extend classify", e.Module, e.Version)
		}
	}
}
