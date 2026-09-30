package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/workspace"
)

func validateFixture(t *testing.T, name, content string) map[string]any {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	result := ValidateSyntax{FS: fs}.Execute(context.Background(), json.RawMessage(`{"path":"`+name+`"}`))
	if !result.OK {
		t.Fatalf("validate failed: %#v", result.Error)
	}
	var output map[string]any
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	return output
}

// Форма E6/cba8: неэкранированная `*` в YAML — ссылка на несуществующий якорь.
func TestValidateSyntaxFindsUnknownYAMLAnchor(t *testing.T) {
	output := validateFixture(t, ".gitlab-ci.yml", "build:\n  script:\n    - npm pack\n  artifacts:\n    paths:\n      - *.tgz\n")
	if output["valid"] != false || output["line"] != float64(6) || !strings.Contains(output["hint"].(string), "YAML alias") {
		t.Fatalf("output=%v", output)
	}
	fixed := validateFixture(t, "ci.yaml", "a: 1\n---\nb:\n  - \"*.tgz\"\n")
	if fixed["valid"] != true {
		t.Fatalf("valid multi-document YAML rejected: %v", fixed)
	}
}

func TestValidateSyntaxReportsJSONPosition(t *testing.T) {
	output := validateFixture(t, "package.json", "{\n  \"name\": \"x\",\n  \"version\": 1,,\n}\n")
	if output["valid"] != false || output["line"] != float64(3) {
		t.Fatalf("output=%v", output)
	}
	if ok := validateFixture(t, "ok.json", `{"a":[1,2]}`); ok["valid"] != true {
		t.Fatalf("valid JSON rejected: %v", ok)
	}
}
