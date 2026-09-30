package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

// Форма квеста cba8: модель читает большой файл диапазоном и правит строку
// внутри прочитанного. Прежде такой патч получал inspection_required и
// модель перечитывала файл целиком.
type rangedReadPatchModel struct {
	mu      sync.Mutex
	calls   int
	refused bool
}

func (m *rangedReadPatchModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	for _, message := range request.Messages {
		if message.Role == "tool" && strings.Contains(message.Content, "inspection_") {
			m.refused = true
		}
	}
	switch m.calls {
	case 1:
		args, _ := json.Marshal(map[string]any{"path": "lock.json", "startLine": 1500, "endLine": 1502})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "read-range", Name: "read_file", Arguments: args}})
	case 2:
		args, _ := json.Marshal(map[string]any{
			"path": "lock.json", "reason": "bump one entry",
			"edits": []map[string]string{{"oldText": "  \"pkg-1500\": \"1.0.0\",\n", "newText": "  \"pkg-1500\": \"2.0.0\",\n"}},
		})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "patch", Name: "propose_patch", Arguments: args}})
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Done."})
}

func TestRangedReadAllowsExactEditInsideTheRange(t *testing.T) {
	root := t.TempDir()
	var body strings.Builder
	body.WriteString("{\n")
	for i := 1; i <= 3000; i++ {
		fmt.Fprintf(&body, "  \"pkg-%d\": \"1.0.0\",\n", i)
	}
	body.WriteString("  \"end\": true\n}\n")
	if err := os.WriteFile(filepath.Join(root, "lock.json"), []byte(body.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &rangedReadPatchModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"read_file", "propose_patch"}
	profile.MaxDurationSeconds = 10
	run, err := engine.Start(StartInput{Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()), Workspace: domain.Workspace{ID: "ws", Path: root}, Task: "bump pkg-1500"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if pending := engine.PendingApprovals(run.ID); len(pending) > 0 {
			if err = engine.ResolveApproval(pending[0].ID, true); err != nil {
				t.Fatal(err)
			}
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted || model.refused {
		t.Fatalf("status=%s refused=%v", finished.Status, model.refused)
	}
	data, _ := os.ReadFile(filepath.Join(root, "lock.json"))
	if !strings.Contains(string(data), "\"pkg-1500\": \"2.0.0\"") {
		t.Fatal("patch inside the read range was not applied")
	}
}

func TestModelSeesHeadAndTailOfLongCommandOutput(t *testing.T) {
	long := strings.Repeat("progress line\n", 5000) + "ERROR: build failed at step 7\n"
	raw, _ := json.Marshal(map[string]any{"stdout": long, "stderr": "", "exitCode": 1})
	result := modelFacingResult("run_command", domain.ToolResult{OK: true, Output: raw})
	var output map[string]any
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	stdout := output["stdout"].(string)
	if !result.Truncated || len(stdout) > modelCommandHeadBytes+modelCommandTailBytes+200 || !strings.HasSuffix(stdout, "ERROR: build failed at step 7\n") || output["exitCode"].(float64) != 1 {
		t.Fatalf("len=%d truncated=%v", len(stdout), result.Truncated)
	}
	other := domain.ToolResult{OK: true, Output: raw}
	if got := modelFacingResult("read_file", other); string(got.Output) != string(raw) {
		t.Fatal("non-command output must not change")
	}
}
