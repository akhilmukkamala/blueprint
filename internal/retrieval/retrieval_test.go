package retrieval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	p := filepath.Join(root, ".blueprint", "config.toml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name      string
		config    string // "" = no config file at all
		wantErr   string
		want      Config
		wantTier2 bool
		wantGraph bool
	}{
		{
			name: "absent file is tiers off",
		},
		{
			name:   "absent table is tiers off",
			config: "[router]\nescalate_loc = 400\n",
		},
		{
			name: "full config",
			config: "[retrieval]\ntier2_packing = \"repomix\"\ntier2_lsp = \"serena\"\n" +
				"[retrieval.graph]\ncommand = \"graph-backend --stdio\"\n",
			want: Config{Tier2Packing: "repomix", Tier2LSP: "serena",
				Graph: GraphConfig{Command: "graph-backend --stdio"}},
			wantTier2: true,
			wantGraph: true,
		},
		{
			name:      "packing only",
			config:    "[retrieval]\ntier2_packing = \"repomix\"\n",
			want:      Config{Tier2Packing: "repomix"},
			wantTier2: true,
		},
		{
			name:      "whitespace values are trimmed to off",
			config:    "[retrieval]\ntier2_packing = \" \"\n[retrieval.graph]\ncommand = \"  \"\n",
			want:      Config{},
			wantTier2: false,
			wantGraph: false,
		},
		{
			name:    "malformed toml names the file",
			config:  "[retrieval\n",
			wantErr: "config.toml",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var root string
			if tc.config == "" {
				root = t.TempDir()
			} else {
				root = writeConfig(t, tc.config)
			}
			got, err := Load(root)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("Load = %+v, want %+v", got, tc.want)
			}
			if got.Tier2Enabled() != tc.wantTier2 {
				t.Errorf("Tier2Enabled = %v, want %v", got.Tier2Enabled(), tc.wantTier2)
			}
			if got.GraphEnabled() != tc.wantGraph {
				t.Errorf("GraphEnabled = %v, want %v", got.GraphEnabled(), tc.wantGraph)
			}
		})
	}
}

func TestTier2Tools(t *testing.T) {
	tools, err := Tier2Tools(Config{Tier2Packing: "repomix", Tier2LSP: "serena"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 {
		t.Fatalf("want both tools, got %v", tools)
	}
	// The two shipped command lines: table churn is deliberate but must be
	// deliberate — this pins the current TableVersion snapshot.
	if r := tools["repomix"]; r.Command != "npx" || strings.Join(r.Args, " ") != "-y repomix --mcp" {
		t.Errorf("repomix launch line drifted: %+v (bump TableVersion when changing it)", r)
	}
	if s := tools["serena"]; s.Command != "uvx" ||
		strings.Join(s.Args, " ") != "--from git+https://github.com/oraios/serena serena start-mcp-server" {
		t.Errorf("serena launch line drifted: %+v (bump TableVersion when changing it)", s)
	}
	for name, tool := range tools {
		if tool.Install == "" {
			t.Errorf("%s has no install remediation", name)
		}
	}

	if _, err := Tier2Tools(Config{}); err != nil {
		t.Errorf("empty config must yield no tools, no error: %v", err)
	}

	_, err = Tier2Tools(Config{Tier2LSP: "sourcegraph"})
	if err == nil || !strings.Contains(err.Error(), "repomix, serena") {
		t.Errorf("unknown tool must error naming the accepted set, got %v", err)
	}
}

func TestSplitCommand(t *testing.T) {
	got := SplitCommand("  graph-backend  --stdio --db x  ")
	if strings.Join(got, ",") != "graph-backend,--stdio,--db,x" {
		t.Errorf("SplitCommand = %v", got)
	}
	if len(SplitCommand("")) != 0 {
		t.Error("empty command must split to nothing")
	}
}
