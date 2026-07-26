package install

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Managed-region markers. Mixed-tier files carry exactly one region; upgrade
// replaces its content and never touches the user body around it. Marker
// syntax follows the host file's comment style.
const (
	mdStartMarker   = "<!-- blueprint:managed -->"
	mdEndMarker     = "<!-- /blueprint:managed -->"
	hashStartMarker = "# blueprint:managed"
	hashEndMarker   = "# /blueprint:managed"
)

// markersFor picks the marker pair for a repo-relative path: HTML comments for
// markdown, hash comments for everything else (TOML, YAML, gitignore).
func markersFor(relPath string) (start, end string) {
	if strings.HasSuffix(relPath, ".md") {
		return mdStartMarker, mdEndMarker
	}
	return hashStartMarker, hashEndMarker
}

// managedSplit is a file cut at its managed region. Reassembly invariant:
// before + startLine + inner + endLine + after == original.
type managedSplit struct {
	before string // everything up to and excluding the start-marker line
	inner  string // region content between the marker lines
	after  string // everything after the end-marker line
	found  bool
}

// splitManaged locates the managed region. Markers must each sit on their own
// line; the first start marker and the first end marker after it delimit the
// region. found is false when either marker is absent.
func splitManaged(content, relPath string) managedSplit {
	start, end := markersFor(relPath)
	lines := strings.SplitAfter(content, "\n")
	startIdx, endIdx := -1, -1
	for i, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if startIdx == -1 && trimmed == start {
			startIdx = i
			continue
		}
		if startIdx != -1 && trimmed == end {
			endIdx = i
			break
		}
	}
	if startIdx == -1 || endIdx == -1 {
		return managedSplit{before: content}
	}
	return managedSplit{
		before: strings.Join(lines[:startIdx], ""),
		inner:  strings.Join(lines[startIdx+1:endIdx], ""),
		after:  strings.Join(lines[endIdx+1:], ""),
		found:  true,
	}
}

// renderManaged reassembles a file from a split with new region content.
func renderManaged(sp managedSplit, relPath, newInner string) string {
	start, end := markersFor(relPath)
	if newInner != "" && !strings.HasSuffix(newInner, "\n") {
		newInner += "\n"
	}
	return sp.before + start + "\n" + newInner + end + "\n" + sp.after
}

// replaceManaged swaps the managed-region content, preserving the user body.
// ok is false when the file has no (complete) managed region.
func replaceManaged(content, relPath, newInner string) (string, bool) {
	sp := splitManaged(content, relPath)
	if !sp.found {
		return content, false
	}
	return renderManaged(sp, relPath, newInner), true
}

// stripManaged removes the managed region including its markers (uninstall of
// a mixed file keeps only the user body).
func stripManaged(content, relPath string) (string, bool) {
	sp := splitManaged(content, relPath)
	if !sp.found {
		return content, false
	}
	return sp.before + sp.after, true
}

// managedHash returns the SHA-256 of the managed-region content; upgrade uses
// it to detect hand-edits inside the region (stored hash vs current).
func managedHash(content, relPath string) (string, bool) {
	sp := splitManaged(content, relPath)
	if !sp.found {
		return "", false
	}
	return hashBytes([]byte(sp.inner)), true
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// conflictInner renders the inline conflict body written into a managed region
// whose installed content was hand-edited (three-way merge cannot pick a side;
// the human resolves the markers and commits).
func conflictInner(current, incoming, toVersion string) string {
	if current != "" && !strings.HasSuffix(current, "\n") {
		current += "\n"
	}
	if incoming != "" && !strings.HasSuffix(incoming, "\n") {
		incoming += "\n"
	}
	return fmt.Sprintf("<<<<<<< local (hand-edited managed region)\n%s=======\n%s>>>>>>> blueprint upgrade v%s\n",
		current, incoming, toVersion)
}
