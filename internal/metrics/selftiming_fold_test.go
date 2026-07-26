package metrics

import (
	"testing"

	"blueprint/internal/core"
)

// The release feature's install self-timing events (kind "install", command in
// data) must fold into time_to_install exactly like the direct init/adopt
// kinds, with the latest run winning.
func TestTimeToInstallReadsInstallKind(t *testing.T) {
	src := &sources{Worklog: []core.JournalEvent{
		{Time: t0, Kind: "install", Data: map[string]any{
			"command": "init", "duration_seconds": 3.5, "version": "1.0.0"}},
		{Time: t0.Add(1000000000), Kind: "install", Data: map[string]any{
			"command": "adopt", "duration_seconds": 7.25, "version": "1.0.0"}},
		// Unknown command and missing duration are both skipped, not fatal.
		{Time: t0, Kind: "install", Data: map[string]any{"command": "upgrade", "duration_seconds": 1.0}},
		{Time: t0, Kind: "install", Data: map[string]any{"command": "init"}},
	}}
	v := timeToInstall(src)
	got, ok := v.Value.(float64)
	if !ok {
		t.Fatalf("time_to_install value = %v (reason %q), want float", v.Value, v.Reason)
	}
	if got != 7.25 {
		t.Errorf("time_to_install = %g, want 7.25 (latest run)", got)
	}
	if m := v.Detail["measured"]; m != "adopt" {
		t.Errorf("measured = %v, want adopt", m)
	}
}
