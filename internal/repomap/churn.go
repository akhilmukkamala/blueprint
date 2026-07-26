package repomap

import (
	"bufio"
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
)

// applyChurn counts per-file commit touches over the last 500 commits
// (`git log --name-only -n 500`). No git, no repo, or any git failure means
// churn 0 everywhere — ranking degrades to pure graph centrality.
func applyChurn(repoRoot string, files []*File) {
	counts := gitChurn(repoRoot)
	if counts == nil {
		return
	}
	for _, f := range files {
		f.Churn = counts[f.Path]
	}
}

func gitChurn(repoRoot string) map[string]int {
	git, err := exec.LookPath("git")
	if err != nil {
		return nil
	}
	cmd := exec.Command(git, "-C", repoRoot, "log", "--name-only", "-n", "500", "--pretty=format:")
	var out bytes.Buffer
	cmd.Stdout = &out
	if cmd.Run() != nil {
		return nil
	}
	counts := map[string]int{}
	sc := bufio.NewScanner(&out)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		counts[filepath.ToSlash(line)]++
	}
	return counts
}
