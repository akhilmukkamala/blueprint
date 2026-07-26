// knowledge_fold_test.go — gardenhooks feature: the knowledge_health row now
// folds real lint.Knowledge values (pass rate, freshness buckets, orphans)
// and keeps garden-net-lines null-with-reason until garden automation ships.
package metrics

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var kNow = time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)

func TestKnowledgeHealthRealValues(t *testing.T) {
	root := t.TempDir()
	k := filepath.Join(root, ".blueprint", "knowledge")
	// Fresh, indexed, clean.
	writeLines(t, filepath.Join(k, "glossary.md"),
		"---", "reviewed: "+kNow.AddDate(0, 0, -10).Format("2006-01-02"), "---", "# Glossary")
	// Stale (error) and 200 days old -> reviewed_over_180 bucket.
	writeLines(t, filepath.Join(k, "architecture.md"),
		"---", "reviewed: "+kNow.AddDate(0, 0, -200).Format("2006-01-02"), "---", "# Arch")
	// Orphan (error): not referenced anywhere.
	writeLines(t, filepath.Join(k, "orphan.md"),
		"---", "reviewed: "+kNow.AddDate(0, 0, -40).Format("2006-01-02"), "---", "# Orphan")
	writeLines(t, filepath.Join(root, "AGENTS.md"),
		"# Index", "- .blueprint/knowledge/glossary.md", "- .blueprint/knowledge/architecture.md")

	v := knowledgeHealth(root, Options{Now: func() time.Time { return kNow }})
	if v.Value != 3 {
		t.Fatalf("value = %v, want 3 artifacts (reason=%q)", v.Value, v.Reason)
	}
	rate, ok := v.Detail["lint_pass_rate"].(float64)
	if !ok {
		t.Fatalf("lint_pass_rate missing from detail: %+v", v.Detail)
	}
	// architecture.md (stale) and orphan.md (orphan) carry errors -> 1/3.
	if rate < 0.32 || rate > 0.35 {
		t.Errorf("lint_pass_rate = %v, want ~1/3", rate)
	}
	if got := v.Detail["orphans"]; got != 1 {
		t.Errorf("orphans = %v, want 1", got)
	}
	buckets, ok := v.Detail["freshness_buckets"].(map[string]int)
	if !ok {
		t.Fatalf("freshness_buckets missing: %+v", v.Detail)
	}
	want := map[string]int{"reviewed_0_30d": 1, "reviewed_31_90d": 1, "reviewed_91_180d": 0, "reviewed_over_180": 1, "unreviewed": 0}
	for k, n := range want {
		if buckets[k] != n {
			t.Errorf("bucket %s = %d, want %d (all: %v)", k, buckets[k], n, buckets)
		}
	}
	// Garden net-lines: honest null until automation ships.
	if v.Detail["garden_net_deleted"] != nil {
		t.Errorf("garden_net_deleted must stay null, got %v", v.Detail["garden_net_deleted"])
	}
	reason, _ := v.Detail["garden_reason"].(string)
	if !strings.Contains(reason, "deferred") {
		t.Errorf("garden_reason must say automation is deferred, got %q", reason)
	}
}

func TestKnowledgeHealthAbsentStore(t *testing.T) {
	v := knowledgeHealth(t.TempDir(), Options{Now: func() time.Time { return kNow }})
	if v.Value != nil || v.Reason == "" {
		t.Fatalf("absent store must be null-with-reason, got %+v", v)
	}
}
