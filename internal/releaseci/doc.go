// Package releaseci holds lint-level sanity tests for the GitHub release
// workflow (.github/workflows/release.yml): YAML well-formedness, SHA-pinned
// actions from the CONTRACTS allowlist, the v* tag trigger, and the native
// runner matrix (cgo forbids GOOS-cross builds). Owned by the release feature.
package releaseci
