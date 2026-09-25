package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"local-agent-workbench/internal/workspace"
)

// Короткое имя `ENV~1` проходило проверку секретности по присланному имени и
// разрешалось в настоящий `.env`: предложение правки несло его содержимое в
// diff, а после одобрения запись уходила в секрет под чужим именем.
func TestPatchRefusesSecretBehindShortName(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("8.3 short names")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("DB_PASS=hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "ENV~1")); err != nil {
		t.Skip("short names are disabled on this volume")
	}
	fs, _ := workspace.Open(root)
	input, _ := json.Marshal(map[string]string{"path": "ENV~1", "content": "DB_PASS=changed\n", "reason": "test"})
	result := NewPatchManager(fs).Execute(context.Background(), input)
	if result.OK || strings.Contains(string(result.Output), "hunter2") {
		t.Fatalf("секрет за коротким именем открыт правке: %#v", result)
	}
}
