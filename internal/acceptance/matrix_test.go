package acceptance_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	projectenv "local-agent-workbench/internal/environment"
)

func TestAppBenchmarkMatrixSpec(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "acceptance", "app-benchmarks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		SchemaVersion int `json:"schemaVersion"`
		ShipGate      struct {
			MinimumIndependentPasses  int     `json:"minimumIndependentPasses"`
			MaximumFalseCompletions   int     `json:"maximumFalseCompletions"`
			MaximumBoundaryViolations int     `json:"maximumBoundaryViolations"`
			MinimumCompletionRate     float64 `json:"minimumCompletionRate"`
		} `json:"shipGate"`
		Cases []struct {
			ID       string   `json:"id"`
			Criteria []string `json:"criteria"`
			Faults   []string `json:"faults"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.ShipGate.MinimumIndependentPasses < 2 {
		t.Fatal("ship gate must require two independent passes")
	}
	if spec.ShipGate.MaximumFalseCompletions != 0 {
		t.Fatal("false completions must be zero")
	}
	required := []string{
		"php-composer-symfony",
		"go-cross-module",
		"node-api-db-ui",
		"python-migrate-test",
		"docx-greenfield",
		"broken-dockerfile",
		"unknown-stack-adapter",
		"parallel-merge-conflict",
		"fault-restart",
		"network-expand-denied",
	}
	byID := map[string]bool{}
	for _, item := range spec.Cases {
		byID[item.ID] = true
		if len(item.Criteria) == 0 {
			t.Fatalf("case %s has no criteria", item.ID)
		}
	}
	for _, id := range required {
		if !byID[id] {
			t.Fatalf("missing matrix case %s", id)
		}
	}
}

func TestEnvironmentMatrixStructural(t *testing.T) {
	tmp := t.TempDir()
	cases := []struct {
		name  string
		files map[string]string
		key   string
	}{
		{"go", map[string]string{"go.mod": "module demo\n\ngo 1.25\n"}, "go"},
		{"node", map[string]string{"package.json": `{"name":"demo"}`}, "node"},
		{"python", map[string]string{"requirements.txt": "pytest\n"}, "python"},
		{"php", map[string]string{"composer.json": `{"name":"demo/app"}`}, "php"},
		{"rust", map[string]string{"Cargo.toml": "[package]\nname=\"demo\"\nversion=\"0.1.0\"\nedition=\"2021\"\n"}, "rust"},
	}
	for _, tc := range cases {
		dir := filepath.Join(tmp, tc.name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for file, content := range tc.files {
			if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		plan := projectenv.Analyze(dir, "ws-"+tc.name)
		if plan.Runtime.Toolchains[tc.key] == "" {
			t.Fatalf("%s toolchain missing: %#v", tc.key, plan.Runtime.Toolchains)
		}
		if plan.Runtime.Image == "" {
			t.Fatalf("%s image missing", tc.key)
		}
	}
	emptyDir := filepath.Join(tmp, "empty")
	_ = os.MkdirAll(emptyDir, 0o700)
	empty := projectenv.Analyze(emptyDir, "ws-empty")
	if empty.Strategy != "generated" {
		t.Fatalf("empty stack strategy=%s", empty.Strategy)
	}
}
