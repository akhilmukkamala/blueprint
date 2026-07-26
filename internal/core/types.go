// Package core holds the shared vocabulary of Blueprint: the types every other
// package speaks. It has no dependencies outside the standard library and must
// never import from sibling internal packages — dependency direction is
// core <- everything, enforced by CI.
package core

import "time"

// OwnershipTier classifies every installed file (DESIGN §2). Upgrades replace
// tool files, never touch user files, and merge mixed files only inside
// managed regions.
type OwnershipTier string

const (
	OwnerUser  OwnershipTier = "user"
	OwnerTool  OwnershipTier = "tool"
	OwnerMixed OwnershipTier = "mixed"
)

// CeremonyTier is the router's output (DESIGN §4).
type CeremonyTier string

const (
	TierExempt CeremonyTier = "exempt"
	TierLight  CeremonyTier = "light"
	TierFull   CeremonyTier = "full"
)

// ChangeStatus is the change/spec lifecycle (DESIGN §3).
type ChangeStatus string

const (
	StatusDraft    ChangeStatus = "draft"
	StatusApproved ChangeStatus = "approved"
	StatusVerified ChangeStatus = "verified"
	StatusClosed   ChangeStatus = "closed"
	StatusBackfill ChangeStatus = "backfill-due" // Sev-1 time-shifted path
)

// AutonomyLevel per (repo, scenario class); taxonomy values are ceilings and
// every class starts at L1 (ADR-0007).
type AutonomyLevel int

const (
	L1Propose   AutonomyLevel = 1
	L2Branch    AutonomyLevel = 2
	L3Automerge AutonomyLevel = 3
)

// EARSPattern is one of the canonical requirement shapes (ADR-0001).
type EARSPattern string

const (
	PatternUbiquitous  EARSPattern = "ubiquitous"
	PatternEventDriven EARSPattern = "event-driven"
	PatternStateDriven EARSPattern = "state-driven"
	PatternOptional    EARSPattern = "optional"
	PatternUnwanted    EARSPattern = "unwanted"
	PatternComplex     EARSPattern = "complex"
)

// VerifyMethod is a requirement's machine-settleable check (DESIGN §3).
// Kind is one of: test, check, bench, human. A "human" method always triggers
// a gate and counts against the lint budget.
type VerifyMethod struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"` // test ID, verifier name, threshold file, or question
}

// Requirement is one EARS requirement with a stable, never-reused ID.
type Requirement struct {
	ID      string         `json:"id"` // REQ-<area>-NNN
	Pattern EARSPattern    `json:"pattern"`
	Text    string         `json:"text"`
	Verify  []VerifyMethod `json:"verify"`
}

// DeltaOp is a delta-change operation against a living spec.
type DeltaOp string

const (
	DeltaAdded    DeltaOp = "ADDED"
	DeltaModified DeltaOp = "MODIFIED"
	DeltaRemoved  DeltaOp = "REMOVED"
)

// DeltaEntry is one requirement-level edit in a change.md.
type DeltaEntry struct {
	Op          DeltaOp     `json:"op"`
	Area        string      `json:"area"` // target living-spec area
	Requirement Requirement `json:"requirement"`
}

// Task is one checklist item in change.md. Full-tier tasks declare
// Consumes/Produces for file-handoff dispatch (DESIGN §6).
type Task struct {
	ID       string   `json:"id"`
	Text     string   `json:"text"`
	Done     bool     `json:"done"`
	Consumes []string `json:"consumes,omitempty"`
	Produces []string `json:"produces,omitempty"`
}

// Breaker holds the five no-progress patterns (OpenHands-derived, DESIGN §6).
type Breaker struct {
	RepeatActionN   int `toml:"repeat_action_n" json:"repeat_action_n"`
	RepeatErrorN    int `toml:"repeat_error_n" json:"repeat_error_n"`
	NoDiffDeltaN    int `toml:"no_diff_delta_n" json:"no_diff_delta_n"`
	OscillationN    int `toml:"oscillation_n" json:"oscillation_n"`
	MonologueTokens int `toml:"monologue_tokens" json:"monologue_tokens"`
}

// LoopContract is the TOML frontmatter of change.md (DESIGN §6).
type LoopContract struct {
	Predicate     string   `toml:"predicate" json:"predicate"`
	MaxIterations int      `toml:"max_iterations" json:"max_iterations"`
	MaxMinutes    int      `toml:"max_minutes" json:"max_minutes"`
	MaxUSD        float64  `toml:"max_usd" json:"max_usd"`
	Breaker       Breaker  `toml:"breaker" json:"breaker"`
	Writable      []string `toml:"writable" json:"writable"`
	ReadOnly      []string `toml:"readonly" json:"readonly"`
}

// Change is a parsed change.md plus its identity and routing result.
type Change struct {
	ID       string       `json:"id"`
	Title    string       `json:"title"`
	Type     string       `json:"type"` // Conventional-Commits-compatible
	Tier     CeremonyTier `json:"tier"`
	Status   ChangeStatus `json:"status"`
	Scenario string       `json:"scenario"`
	Contract LoopContract `json:"contract"`
	Delta    []DeltaEntry `json:"delta"`
	Tasks    []Task       `json:"tasks"`
	Design   string       `json:"design,omitempty"` // full tier only
	SLA      *time.Time   `json:"sla,omitempty"`    // Sev-1 backfill deadline
}

// JournalEvent is one append-only line in journal.ndjson / worklog.ndjson.
// Kind examples: route, override, iteration, verdict, tamper, breaker, approve,
// close, autonomy, adopt-stage, cost.
type JournalEvent struct {
	Time     time.Time      `json:"time"`
	Kind     string         `json:"kind"`
	ChangeID string         `json:"change_id,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

// CheckResult is one verifier's outcome (DESIGN §7 contract).
type CheckResult struct {
	Name       string `json:"name"`
	ReqID      string `json:"req_id,omitempty"`
	Pass       bool   `json:"pass"`
	ExitCode   int    `json:"exit_code"`
	ReportPath string `json:"report_path,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

// Verdict aggregates a verify run; Fingerprint hashes the failure set so the
// breaker can detect identical consecutive failures.
type Verdict struct {
	ChangeID    string        `json:"change_id"`
	Time        time.Time     `json:"time"`
	Pass        bool          `json:"pass"`
	Tamper      bool          `json:"tamper"`
	Checks      []CheckResult `json:"checks"`
	Fingerprint string        `json:"fingerprint"`
}

// LintFinding is one deterministic linter result; Remediation is written for
// agent consumption (linters-that-teach).
type LintFinding struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	Rule        string `json:"rule"`
	Message     string `json:"message"`
	Remediation string `json:"remediation"`
	Severity    string `json:"severity"` // error | warning
}

// EnforcementProfile is the forge capability probe result (DESIGN §8).
type EnforcementProfile struct {
	Forge    string `json:"forge"`    // github, gitlab, bitbucket, gitea, azure, unknown
	Enforced bool   `json:"enforced"` // required checks actually block merges
	Notes    string `json:"notes"`
}
