// Per-metric folds over the collected sources. Each fold returns a complete
// Value: measured (Value set) or honestly unmeasured (Reason set) — the two
// are mutually exclusive by construction and callers never omit a row.
package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"blueprint/internal/core"
	"blueprint/internal/lint"
	"blueprint/internal/loop"
)

// greenVerdictTime returns the earliest passing verdict event, if any.
func greenVerdictTime(ci changeInfo) (time.Time, bool) {
	for _, ev := range ci.Events {
		if ev.Kind == "verdict" {
			if pass, ok := ev.Data["pass"].(bool); ok && pass {
				return ev.Time, true
			}
		}
	}
	return time.Time{}, false
}

func timeToFirstVerified(src *sources) Value {
	var start time.Time
	for _, ev := range src.Worklog {
		stage, hasStage := dataFloat(ev, "stage")
		isStart := ev.Kind == "init" || (ev.Kind == "adopt-stage" && hasStage && stage == 0)
		if isStart && (start.IsZero() || ev.Time.Before(start)) {
			start = ev.Time
		}
	}
	if start.IsZero() {
		return Value{Reason: "no adoption stage-0 or init event in worklog — the clock starts when `blueprint init` or `blueprint adopt` logs it (ships with the packaging/adoption features)"}
	}
	var (
		first   time.Time
		firstID string
	)
	for _, ci := range src.Changes {
		if t, ok := greenVerdictTime(ci); ok && (first.IsZero() || t.Before(first)) {
			first, firstID = t, ci.ID
		}
	}
	if first.IsZero() {
		return Value{Reason: "no green verdict recorded in any change journal yet — run `blueprint verify <id>` until it passes"}
	}
	return Value{
		Value: first.Sub(start).Seconds(),
		Unit:  "seconds",
		Detail: map[string]any{
			"start":        start.UTC().Format(time.RFC3339),
			"first_green":  first.UTC().Format(time.RFC3339),
			"first_change": firstID,
		},
	}
}

func timeToInstall(src *sources) Value {
	type run struct {
		Kind     string  `json:"kind"`
		Seconds  float64 `json:"seconds"`
		Occurred string  `json:"occurred"`
	}
	var runs []run
	for _, ev := range src.Worklog {
		kind := ev.Kind
		// The release feature's self-timing events use kind "install" with the
		// command ("init"/"adopt") in data (DESIGN §11); older direct kinds
		// stay accepted.
		if kind == "install" {
			if c := dataString(ev, "command"); c != "" {
				kind = c
			}
		}
		if kind != "init" && kind != "adopt" {
			continue
		}
		if d, ok := dataFloat(ev, "duration_seconds"); ok {
			runs = append(runs, run{Kind: kind, Seconds: d, Occurred: ev.Time.UTC().Format(time.RFC3339)})
		}
	}
	if len(runs) == 0 {
		return Value{Reason: "no init/adopt self-timing events in worklog — `blueprint init` and `blueprint adopt` log duration_seconds when the packaging feature ships; the install CI matrix covers cold installs"}
	}
	latest := runs[len(runs)-1]
	return Value{
		Value:  latest.Seconds,
		Unit:   "seconds",
		Detail: map[string]any{"runs": runs, "measured": latest.Kind},
	}
}

func timeToOnboard(src *sources) Value {
	stages := map[int]time.Time{} // stage -> first exit timestamp
	for _, ev := range src.Worklog {
		if ev.Kind != "adopt-stage" {
			continue
		}
		s, ok := dataFloat(ev, "stage")
		if !ok {
			continue
		}
		n := int(s)
		if t, seen := stages[n]; !seen || ev.Time.Before(t) {
			stages[n] = ev.Time
		}
	}
	if len(stages) == 0 {
		return Value{Reason: "no adopt-stage events in worklog — `blueprint doctor --adopt-stage <n>` logs stage exits when the adoption feature ships"}
	}
	detail := map[string]any{}
	nums := make([]int, 0, len(stages))
	for n, t := range stages {
		detail["stage_"+strconv.Itoa(n)] = t.UTC().Format(time.RFC3339)
		nums = append(nums, n)
	}
	sort.Ints(nums)
	lo, hi := nums[0], nums[len(nums)-1]
	if lo == hi {
		return Value{
			Reason: fmt.Sprintf("only adoption stage %d recorded — the metric is the span across stage exits; keep following ADOPT.md", lo),
			Detail: detail,
		}
	}
	detail["from_stage"] = lo
	detail["to_stage"] = hi
	return Value{
		Value:  stages[hi].Sub(stages[lo]).Seconds(),
		Unit:   "seconds",
		Detail: detail,
	}
}

func retrievalEfficiency(repoRoot string) Value {
	path := filepath.Join(repoRoot, "reports", "bench", "report.json")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Value{Reason: "no bench report at reports/bench/report.json — run `blueprint bench retrieval` and record results (AC-8); tokens are only reported jointly with verified success"}
	}
	if err != nil {
		return Value{Reason: fmt.Sprintf("cannot read %s: %v — fix permissions or delete the file and re-run `blueprint bench retrieval`", path, err)}
	}
	var rep struct {
		Suite        string `json:"suite"`
		Summaries    []any  `json:"summaries"`
		Margin       any    `json:"margin"`
		ClassMargins []any  `json:"class_margins"`
		Pending      []any  `json:"pending"`
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		return Value{Reason: fmt.Sprintf("%s is not valid JSON: %v — regenerate it with `blueprint bench retrieval`", path, err)}
	}
	if rep.Margin == nil {
		return Value{
			Reason: fmt.Sprintf("bench report has no cross-tier margin yet (%d runs pending) — record both tiers for at least one task via `blueprint bench retrieval`", len(rep.Pending)),
			Detail: map[string]any{"suite": rep.Suite, "summaries": rep.Summaries},
		}
	}
	detail := map[string]any{"suite": rep.Suite, "summaries": rep.Summaries, "report": "reports/bench/report.json"}
	if len(rep.ClassMargins) > 0 {
		// Per-query-class margins (retrieval wins are condition-bound —
		// RESEARCH §3.5); each entry already carries its own success delta.
		detail["class_margins"] = rep.ClassMargins
	}
	return Value{
		Value:  rep.Margin,
		Detail: detail,
	}
}

func indexFreshness(repoRoot string, opts Options) Value {
	candidates := []string{
		filepath.Join(repoRoot, ".blueprint", "map.json"),
		filepath.Join(repoRoot, ".blueprint", "index", "map.json"),
	}
	var (
		mapPath string
		st      os.FileInfo
	)
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil {
			mapPath, st = p, info
			break
		}
	}
	if mapPath == "" {
		return Value{Reason: "no repo map found (.blueprint/map.json) — run `blueprint map` once the map feature is installed; freshness is last-commit time minus map regeneration time"}
	}
	out, err := opts.git()(repoRoot, "log", "-1", "--format=%ct")
	if err != nil {
		return Value{Reason: fmt.Sprintf("cannot read last commit time: %v — ensure git is installed and the repo has at least one commit", err)}
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return Value{Reason: fmt.Sprintf("unparseable git commit timestamp %q — ensure `git log -1 --format=%%ct` works in this repo", strings.TrimSpace(out))}
	}
	lag := time.Unix(sec, 0).UTC().Sub(st.ModTime().UTC()).Seconds()
	return Value{
		Value: lag,
		Unit:  "seconds",
		Detail: map[string]any{
			"map":          mapPath,
			"map_mtime":    st.ModTime().UTC().Format(time.RFC3339),
			"last_commit":  time.Unix(sec, 0).UTC().Format(time.RFC3339),
			"lag_positive": "map older than last commit (stale)",
		},
	}
}

// reworkPattern marks a commit subject as rework: a revert or a fix landing on
// files a closed change touched.
var reworkPattern = regexp.MustCompile(`(?i)^(revert|fix|hotfix)([:!(\s]|$)|this reverts commit`)

func reworkRate(repoRoot string, src *sources, opts Options) Value {
	// closeTime per archived change: prefer the worklog close event.
	closeAt := map[string]time.Time{}
	for _, ev := range src.Worklog {
		if ev.Kind == "close" && ev.ChangeID != "" {
			closeAt[ev.ChangeID] = ev.Time
		}
	}
	type archived struct {
		id     string
		closed time.Time
		files  map[string]bool
	}
	var arch []archived
	for _, ci := range src.Changes {
		if !ci.Archived {
			continue
		}
		a := archived{id: ci.ID, files: map[string]bool{}}
		for _, ev := range ci.Events {
			if ev.Kind == loop.KindIteration {
				for _, f := range dataStrings(ev, "files") {
					a.files[filepath.ToSlash(f)] = true
				}
			}
			if ev.Time.After(a.closed) {
				a.closed = ev.Time
			}
		}
		if t, ok := closeAt[ci.ID]; ok {
			a.closed = t
		}
		arch = append(arch, a)
	}
	if len(arch) == 0 {
		return Value{Reason: "no archived changes yet — rework is measured as reverts/fixes touching a closed change's files within 30 days of `blueprint close`"}
	}

	oldest := arch[0].closed
	for _, a := range arch {
		if a.closed.Before(oldest) {
			oldest = a.closed
		}
	}
	out, err := opts.git()(repoRoot, "log", "--since="+oldest.UTC().Format(time.RFC3339),
		"--date-order", "--pretty=format:%x01%H%x09%ct%x09%s", "--name-only")
	if err != nil {
		return Value{Reason: fmt.Sprintf("git log failed: %v — ensure git is installed and this is a git repository", err)}
	}
	commits := parseGitLog(out)

	reworked := map[string][]string{} // change id -> rework commit hashes
	noFileData := 0
	for _, a := range arch {
		if len(a.files) == 0 {
			noFileData++
			continue
		}
		deadline := a.closed.Add(30 * 24 * time.Hour)
		for _, c := range commits {
			if !c.when.After(a.closed) || c.when.After(deadline) || !reworkPattern.MatchString(c.subject) {
				continue
			}
			for _, f := range c.files {
				if a.files[f] {
					reworked[a.id] = append(reworked[a.id], c.hash)
					break
				}
			}
		}
	}
	detail := map[string]any{
		"archived_changes": len(arch),
		"reworked_changes": len(reworked),
		"window_days":      30,
	}
	if len(reworked) > 0 {
		detail["rework"] = reworked
	}
	if noFileData > 0 {
		detail["changes_without_file_data"] = noFileData
		detail["note"] = "changes whose journals carry no iteration file lists cannot be matched to later commits"
	}
	return Value{
		Value:  float64(len(reworked)) / float64(len(arch)),
		Unit:   "ratio",
		Detail: detail,
	}
}

type gitCommit struct {
	hash    string
	when    time.Time
	subject string
	files   []string
}

// parseGitLog parses `--pretty=format:%x01%H%x09%ct%x09%s --name-only` output;
// \x01 delimits commits so subjects containing newlines-free text stay safe.
func parseGitLog(out string) []gitCommit {
	var commits []gitCommit
	for _, block := range strings.Split(out, "\x01") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		lines := strings.Split(block, "\n")
		head := strings.SplitN(lines[0], "\t", 3)
		if len(head) != 3 {
			continue
		}
		sec, err := strconv.ParseInt(head[1], 10, 64)
		if err != nil {
			continue
		}
		c := gitCommit{hash: head[0], when: time.Unix(sec, 0).UTC(), subject: head[2]}
		for _, f := range lines[1:] {
			if f = strings.TrimSpace(f); f != "" {
				c.files = append(c.files, filepath.ToSlash(f))
			}
		}
		commits = append(commits, c)
	}
	return commits
}

func ceremonyFit(src *sources) Value {
	routes, overrides := 0, 0
	for _, ev := range src.Worklog {
		switch ev.Kind {
		case "route":
			routes++
		case "override":
			overrides++
		}
	}
	if routes == 0 {
		return Value{Reason: "no route events in worklog — `blueprint new` logs every routing decision; ceremony fit is 1 minus the human-override rate"}
	}
	return Value{
		Value: 1 - float64(overrides)/float64(routes),
		Unit:  "ratio",
		Detail: map[string]any{
			"routes":    routes,
			"overrides": overrides,
			"note":      "complement with a periodic sampled human audit (DESIGN §15)",
		},
	}
}

// costBucket is one ceremony tier's folded spend over verified changes.
type costBucket struct {
	Changes   int     `json:"changes"`
	USD       float64 `json:"usd"`
	Tokens    float64 `json:"tokens"`
	USDPer    float64 `json:"usd_per_change"`
	TokensPer float64 `json:"tokens_per_change"`
}

func costPerVerified(src *sources) Value {
	byTier := map[string]*costBucket{}
	verified, costEvents := 0, 0
	for _, ci := range src.Changes {
		if _, green := greenVerdictTime(ci); !green {
			continue
		}
		verified++
		tier := string(ci.Tier)
		if tier == "" {
			tier = "unknown"
		}
		b := byTier[tier]
		if b == nil {
			b = &costBucket{}
			byTier[tier] = b
		}
		b.Changes++
		for _, ev := range ci.Events {
			if ev.Kind != loop.KindCost && ev.Kind != loop.KindIteration {
				continue
			}
			if usd, ok := dataFloat(ev, "usd"); ok {
				b.USD += usd
				costEvents++
			}
			if tok, ok := dataFloat(ev, "tokens"); ok {
				b.Tokens += tok
				costEvents++
			}
		}
	}
	if verified == 0 {
		return Value{Reason: "no verified changes yet — cost is folded from journal cost/iteration events of changes with a green verdict, grouped by ceremony tier"}
	}
	if costEvents == 0 {
		return Value{Reason: fmt.Sprintf("%d verified change(s) but no cost events in their journals — agent adapters log `cost` events with usd/tokens; wire the adapter hooks to enable this row", verified)}
	}
	out := map[string]costBucket{}
	for tier, b := range byTier {
		if b.Changes > 0 {
			b.USDPer = b.USD / float64(b.Changes)
			b.TokensPer = b.Tokens / float64(b.Changes)
		}
		out[tier] = *b
	}
	return Value{Value: out, Unit: "per tier"}
}

func escapedDefects(src *sources) Value {
	verified, incidents := 0, 0
	for _, ci := range src.Changes {
		if _, green := greenVerdictTime(ci); green {
			verified++
		}
		for _, ev := range ci.Events {
			if ev.Kind == "incident" {
				incidents++
			}
		}
	}
	for _, ev := range src.Worklog {
		if ev.Kind == "incident" {
			incidents++
		}
	}
	if verified == 0 {
		return Value{Reason: "no verified changes yet — escaped defects are incident events tracing back to a verifier-green merged change"}
	}
	return Value{
		Value: float64(incidents) / float64(verified),
		Unit:  "incidents per verified change",
		Detail: map[string]any{
			"incidents":        incidents,
			"verified_changes": verified,
			"note":             "MVP uses manual incident annotation (append an `incident` journal event); `blueprint incident link` is post-MVP — zero may mean unannotated",
		},
	}
}

// knowledgeHealth is the DESIGN §15 row: knowledge-lint pass rate +
// freshness-age distribution + garden net-lines-deleted. The first two are
// real (folded from lint.Knowledge); garden-net-lines stays null-with-reason
// until garden automation ships (MVP has only the manual report).
func knowledgeHealth(repoRoot string, opts Options) Value {
	dir := filepath.Join(repoRoot, ".blueprint", "knowledge")
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return Value{Reason: "no .blueprint/knowledge directory — the knowledge store ships with the knowledge feature (ADOPT stage 3 creates glossary/ADRs/runbooks)"}
	}
	infos, err := lint.KnowledgeFiles(repoRoot)
	if err != nil || len(infos) == 0 {
		return Value{Reason: ".blueprint/knowledge exists but holds no .md artifacts — capture at least one glossary, ADR, or runbook (AC-9 stage 4)"}
	}
	now := opts.now().UTC()
	findings, err := lint.Knowledge(repoRoot, now, lint.Config{})
	if err != nil {
		return Value{Reason: fmt.Sprintf("knowledge lint failed: %v — fix the store so the health row can be measured", err)}
	}

	// Pass rate: fraction of knowledge files with zero error-severity
	// findings attributed to them. Orphan count is its own sub-metric.
	errFiles := map[string]bool{}
	warnings, errors, orphans := 0, 0, 0
	for _, f := range findings {
		switch f.Severity {
		case lint.SevError:
			errors++
			errFiles[f.File] = true
		case lint.SevWarning:
			warnings++
		}
		if f.Rule == "knowledge-orphan" {
			orphans++
		}
	}
	passing := 0
	for _, info := range infos {
		if !errFiles[info.RelPath] {
			passing++
		}
	}

	// Freshness distribution: days since `reviewed:` frontmatter.
	buckets := map[string]int{
		"reviewed_0_30d":    0,
		"reviewed_31_90d":   0,
		"reviewed_91_180d":  0,
		"reviewed_over_180": 0,
		"unreviewed":        0,
	}
	for _, info := range infos {
		switch days := int(now.Sub(info.Reviewed).Hours() / 24); {
		case info.Reviewed.IsZero():
			buckets["unreviewed"]++
		case days <= 30:
			buckets["reviewed_0_30d"]++
		case days <= 90:
			buckets["reviewed_31_90d"]++
		case days <= 180:
			buckets["reviewed_91_180d"]++
		default:
			buckets["reviewed_over_180"]++
		}
	}

	detail := map[string]any{
		"files":              len(infos),
		"lint_pass_rate":     float64(passing) / float64(len(infos)),
		"lint_errors":        errors,
		"lint_warnings":      warnings,
		"orphans":            orphans,
		"freshness_buckets":  buckets,
		"garden_net_deleted": nil,
		"garden_reason":      "garden automation is deferred (MVP ships the manual `blueprint garden` report) — net-lines-deleted joins this row when automated garden cycles are recorded",
	}
	return Value{Value: len(infos), Unit: "knowledge artifacts", Detail: detail}
}

func supervisionRatio(src *sources) Value {
	active, parked := 0, 0
	for _, ci := range src.Changes {
		if ci.Archived || ci.Status == core.StatusClosed {
			continue
		}
		if ci.Phase == loop.PhaseParked {
			parked++
			continue
		}
		active++
	}
	return Value{
		Value: active,
		Unit:  "concurrent active loops per approver",
		Detail: map[string]any{
			"active": active,
			"parked": parked,
			"note":   "worklog carries no approver identity yet — assuming a single human approver; per-approver attribution joins when approve events log identity",
		},
	}
}
