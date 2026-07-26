package metrics

// benchcorpus feature tests: the context_retrieval_efficiency row must
// surface the bench report's per-query-class margins when they exist —
// retrieval wins are condition-bound (RESEARCH §3.5), so the summary must say
// where the margin holds, not just its average.

import (
	"os"
	"path/filepath"
	"testing"
)

func writeBenchReport(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, "reports", "bench")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRetrievalEfficiencySurfacesClassMargins(t *testing.T) {
	root := t.TempDir()
	writeBenchReport(t, root, `{
		"suite": "s",
		"summaries": [{"tier": "tier0-grep"}],
		"margin": {"tasks": 2, "token_ratio": 0.5, "success_delta": 0},
		"class_margins": [
			{"query_class": "exact-symbol", "tasks": 1, "token_ratio": 0.3, "success_delta": 0},
			{"query_class": "conceptual", "tasks": 1, "token_ratio": 0.9, "success_delta": -1}
		],
		"pending": []
	}`)
	v := retrievalEfficiency(root)
	if v.Value == nil {
		t.Fatalf("margin present but value nil (reason %q)", v.Reason)
	}
	cms, ok := v.Detail["class_margins"].([]any)
	if !ok || len(cms) != 2 {
		t.Fatalf("detail must carry both class margins, got %+v", v.Detail)
	}
}

func TestRetrievalEfficiencyWithoutClassMarginsOmitsKey(t *testing.T) {
	root := t.TempDir()
	writeBenchReport(t, root, `{"suite": "s", "summaries": [], "margin": {"tasks": 1}, "class_margins": null, "pending": []}`)
	v := retrievalEfficiency(root)
	if v.Value == nil {
		t.Fatalf("margin present but value nil (reason %q)", v.Reason)
	}
	if _, ok := v.Detail["class_margins"]; ok {
		t.Error("empty class margins must not appear in detail")
	}
}

func TestRetrievalEfficiencyNoMarginStillReasons(t *testing.T) {
	root := t.TempDir()
	writeBenchReport(t, root, `{"suite": "s", "summaries": [], "margin": null, "pending": [1, 2]}`)
	v := retrievalEfficiency(root)
	if v.Value != nil || v.Reason == "" {
		t.Fatalf("no margin must stay null-with-reason, got %+v", v)
	}
}
