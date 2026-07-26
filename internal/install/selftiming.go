// Install self-timing (DESIGN §11, §15): Init and Adopt measure their own
// wall-clock duration and append a worklog event kind "install" with
// {command, duration_seconds, version}. internal/metrics folds these into the
// time_to_install row. Owned by the release feature.
package install

import (
	"time"

	"blueprint/internal/core"
	"blueprint/internal/worklog"
)

// Version is the running binary's release version, set by cmd/blueprint from
// main.version (which -ldflags stamps at release). It is recorded in install
// self-timing events so time_to_install can be correlated with a release.
var Version = "0.0.0-dev"

// appendSelfTiming records one install self-timing event. The duration comes
// from time.Since over the whole operation; the event timestamp is stamped by
// worklog.Append (an explicit journal timestamp, CONTRACTS rule 5).
func appendSelfTiming(repoRoot, command string, d time.Duration) error {
	return worklog.Append(repoRoot, core.JournalEvent{
		Kind: "install",
		Data: map[string]any{
			"command":          command,
			"duration_seconds": d.Seconds(),
			"version":          Version,
		},
	})
}
