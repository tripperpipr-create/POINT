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
	workbenchtools "local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
)

func TestExecutableChangeJournalReportsRecordingLimit(t *testing.T) {
	fs, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	active := &activeRun{run: domain.Run{ID: "run-limit", AgentID: "agent-limit", ChangedFiles: []string{}}}
	after := workspace.TextSnapshot{Files: make(map[string]workspace.SnapshotEntry), Complete: true}
	for index := 0; index < maxRecordedExecutableChanges+1; index++ {
		after.Files[fmt.Sprintf("generated-%03d.txt", index)] = workspace.SnapshotEntry{Fingerprint: fmt.Sprintf("hash-%d", index), Content: "generated", Revertible: true}
	}
	summary, err := engine.recordExecutableChanges(active, workbenchtools.NewPatchManager(fs), "run_command", "approval-limit", workspace.TextSnapshot{Files: map[string]workspace.SnapshotEntry{}, Complete: true}, after)
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalChanges != maxRecordedExecutableChanges+1 || summary.RecordedChanges != maxRecordedExecutableChanges || summary.OmittedRevertibleChanges != 1 || len(active.run.ChangedFiles) != maxRecordedExecutableChanges+1 || active.workspaceRevision != 1 {
		t.Fatalf("summary=%#v run=%#v revision=%d", summary, active.run, active.workspaceRevision)
	}
	if len(repo.patches) != maxRecordedExecutableChanges {
		t.Fatalf("persisted patches=%d", len(repo.patches))
	}
}

type invalidProcessModel struct {
	mu              sync.Mutex
	calls           int
	sawInvalidInput bool
}

type failingPatchRepo struct{ *memoryRepo }

type mutatingToolModel struct{ calls int }

func (m *mutatingToolModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.calls++
	if m.calls == 1 {
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{
			ID: "mutate", Name: "customtool_0123456789abcdef01234567", Arguments: json.RawMessage(`{"reason":"create fixture"}`),
		}})
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "This final must not be reached."})
}

func TestRunFailsWhenPostExecutionMutationJournalCannotPersist(t *testing.T) {
	root := t.TempDir()
	helper := `package main
import "os"
func main() { _ = os.WriteFile("generated.txt", []byte("changed"), 0600) }
`
	if err := os.WriteFile(filepath.Join(root, "mutate.go"), []byte(helper), 0600); err != nil {
		t.Fatal(err)
	}
	repo := &failingPatchRepo{memoryRepo: newMemoryRepo()}
	engine := NewEngine(repo, nil)
	model := &mutatingToolModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	custom := domain.CustomTool{
		ID: "customtool_0123456789abcdef01234567", Kind: domain.CustomToolProcess,
		DisplayName: "Mutator", Description: "Creates a fixture", Program: "go", Arguments: []string{"run", "mutate.go"}, CWD: ".", TimeoutSeconds: 30,
	}
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{custom.ID}
	configuration := domain.NewRunConfigurationSnapshot("test", profile, []domain.CustomTool{custom}, time.Now().UTC())
	run, err := engine.Start(StartInput{Configuration: configuration, Workspace: domain.Workspace{ID: "ws", Path: root}, Task: "create fixture"})
	if err != nil {
		t.Fatal(err)
	}
	resolveNextApproval(t, engine, run.ID, "", true)
	finished := waitForTerminalRun(t, repo.memoryRepo, run.ID)
	if finished.Status != domain.RunFailed || !strings.Contains(finished.Error, "workspace mutation audit failed") || model.calls != 1 {
		t.Fatalf("run=%#v model calls=%d", finished, model.calls)
	}
	eventsList, err := repo.ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundAuditFailure := false
	for _, item := range eventsList {
		if item.Type == domain.EventToolFinished && strings.Contains(string(item.Data), "workspace_audit_failed") {
			foundAuditFailure = true
		}
	}
	if !foundAuditFailure {
		t.Fatalf("audit failure was not persisted: %#v", eventsList)
	}
}
