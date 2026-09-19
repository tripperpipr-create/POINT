package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/events"
	"local-agent-workbench/internal/providers"
)

func TestEngineRejectsLegacySnapshotForNewRun(t *testing.T) {
	engine := NewEngine(newMemoryRepo(), nil)
	_, err := engine.Start(StartInput{
		Workspace: domain.Workspace{ID: "ws", Path: t.TempDir()}, Task: "do not start",
		Configuration: domain.RunConfigurationSnapshot{
			SchemaVersion: 2,
			Profile:       domain.AgentProfile{ID: "legacy", Provider: domain.ProviderOllama, Model: "qwen"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "schema v3") {
		t.Fatalf("legacy snapshot created a new run: %v", err)
	}
}

type memoryRepo struct {
	*events.MemoryStore
	mu          sync.Mutex
	runs        map[string]domain.Run
	approvals   map[string]domain.Approval
	patches     map[string]domain.PatchProposal
	checkpoints map[string]domain.RunCheckpoint
}

func newMemoryRepo() *memoryRepo {
	return &memoryRepo{MemoryStore: events.NewMemoryStore(), runs: map[string]domain.Run{}, approvals: map[string]domain.Approval{}, patches: map[string]domain.PatchProposal{}, checkpoints: map[string]domain.RunCheckpoint{}}
}

func (r *memoryRepo) SaveRun(_ context.Context, run domain.Run) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs[run.ID] = run
	return nil
}

// run читает прогон под тем же замком, что и SaveRun.
//
// Большинство тестов берёт `repo.mu` вокруг чтения руками; там, где этого не
// сделали, цикл ожидания читает map, в которую прямо сейчас пишет горутина
// движка. Гонка здесь не просто портит значение: runtime роняет весь тестовый
// двоичный файл через fatal error, без единой строки `--- FAIL`, и снаружи
// такой отказ выглядит как молчаливый «exit code 1».
//
// Вызывать только без уже взятого `repo.mu`: мьютекс не рекурсивный.
func (r *memoryRepo) run(id string) domain.Run {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runs[id]
}

func (r *memoryRepo) SaveApproval(_ context.Context, a domain.Approval) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.approvals[a.ID] = a
	return nil
}

func (r *memoryRepo) SavePatch(_ context.Context, p domain.PatchProposal) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.patches[p.ID] = p
	return nil
}

func (r *memoryRepo) SaveRunCheckpoint(_ context.Context, checkpoint domain.RunCheckpoint) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checkpoints[checkpoint.RunID] = checkpoint
	return nil
}

func (r *memoryRepo) LatestRunCheckpoint(_ context.Context, runID string) (domain.RunCheckpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	checkpoint, ok := r.checkpoints[runID]
	if !ok {
		return domain.RunCheckpoint{}, fmt.Errorf("checkpoint not found")
	}
	return checkpoint, nil
}

type scriptedModel struct {
	mu    sync.Mutex
	calls int
}

type blockingModel struct{}

func (blockingModel) Stream(ctx context.Context, _ providers.ModelRequest, _ func(providers.ModelEvent) error) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestCancelStopsModel(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return blockingModel{}, nil })
	profile := domain.DefaultProfile()
	profile.MaxDurationSeconds = 5
	configuration := domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC())
	run, err := engine.Start(StartInput{Configuration: configuration, Workspace: domain.Workspace{ID: "ws", Path: t.TempDir()}, Task: "wait"})
	if err != nil {
		t.Fatal(err)
	}
	if err = engine.Cancel(run.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		repo.mu.Lock()
		status := repo.runs[run.ID].Status
		repo.mu.Unlock()
		if status == domain.RunCancelled {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("run was not cancelled")
}

func (r *failingPatchRepo) SavePatch(context.Context, domain.PatchProposal) error {
	return fmt.Errorf("simulated patch journal failure")
}

func TestSystemMessageHasCapabilityAwareExecutionContract(t *testing.T) {
	profile := domain.DefaultProfile()
	message := SystemMessage(profile)
	for _, expected := range []string{"<execution_contract>", "Prefer project_map and search_code", "truncated=true is incomplete", "include_related=true", "navigation evidence only", "inspect every oldText anchor through search_code", "After an accepted code change", "Never repeat an identical successful tool call", "<point_tool_plan_gate>", "Call tools by their exact names", "workspace-relative with forward slashes", "list_files with a subdirectory", "startLine and endLine"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("system message is missing %q:\n%s", expected, message)
		}
	}
	if strings.Contains(message, "Explain every tool call in its reason field") {
		t.Fatalf("system message still requires a field absent from read-only schemas: %s", message)
	}
	profile.AllowedTools = []string{"read_file"}
	message = SystemMessage(profile)
	if strings.Contains(message, "After an accepted code change") || strings.Contains(message, "Prefer project_map") || strings.Contains(message, "list_files with a subdirectory") {
		t.Fatalf("execution contract advertised disabled capabilities: %s", message)
	}
	if !strings.Contains(message, "startLine and endLine") || !strings.Contains(message, "Call tools by their exact names") {
		t.Fatalf("read_file contract is missing path/tool guidance: %s", message)
	}
	custom := domain.CustomTool{ID: "custom_verify", ProvidesVerification: true}
	profile.AllowedTools = []string{custom.ID}
	message = SystemMessage(profile, []domain.CustomTool{custom})
	if !strings.Contains(message, "verification-capable tool") {
		t.Fatalf("custom verification capability was absent from execution contract: %s", message)
	}
	profile.EquippedSkills = []domain.SkillRuntime{{
		ID: "skill-code-review", Name: "Code Review", Description: "Review diffs",
		Instructions: "Read the diff and report findings.", RequiredTools: []string{"read_file"},
	}}
	message = SystemMessage(profile)
	for _, expected := range []string{"<equipped_skills>", "Code Review [skill-code-review]", "Read the diff and report findings", "Follow equipped skills"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("equipped skill missing %q:\n%s", expected, message)
		}
	}
}

func TestRunEventsCarryHubCorrelation(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return &fallbackModel{}, nil })
	profile := domain.DefaultProfile()
	profile.Model = "correlation-model"
	profile.MaxDurationSeconds = 5
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		Task:          "Explain the result.",
		ExecutionID:   "execution-1",
		QuestID:       "quest-1",
		FlowRunID:     "flowrun-1",
		FlowNodeID:    "node-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if finished := waitForTerminalRun(t, repo, run.ID); finished.Status != domain.RunCompleted {
		t.Fatalf("run=%#v", finished)
	}
	eventsList, err := repo.ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(eventsList) == 0 {
		t.Fatal("no events recorded")
	}
	for _, event := range eventsList {
		if event.ExecutionID != "execution-1" || event.QuestID != "quest-1" ||
			event.FlowRunID != "flowrun-1" || event.FlowNodeID != "node-1" {
			t.Fatalf("event correlation=%#v", event)
		}
		if !strings.Contains(string(event.Data), `"executionId":"execution-1"`) ||
			!strings.Contains(string(event.Data), `"flowNodeId":"node-1"`) {
			t.Fatalf("persisted payload=%s", event.Data)
		}
	}
}

func resolveNextApproval(t *testing.T, engine *Engine, runID, excludedID string, allow bool) string {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		pending := engine.PendingApprovals(runID)
		for _, approval := range pending {
			if approval.ID == excludedID {
				continue
			}
			if err := engine.ResolveApproval(approval.ID, allow); err != nil {
				t.Fatal(err)
			}
			return approval.ID
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("approval was not requested")
	return ""
}

func waitForTerminalRun(t *testing.T, repo *memoryRepo, runID string) domain.Run {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		repo.mu.Lock()
		run := repo.runs[runID]
		repo.mu.Unlock()
		if run.Status == domain.RunCompleted || run.Status == domain.RunFailed || run.Status == domain.RunCancelled {
			return run
		}
		time.Sleep(5 * time.Millisecond)
	}
	repo.mu.Lock()
	run := repo.runs[runID]
	repo.mu.Unlock()
	t.Fatalf("run %s did not finish status=%s step=%d err=%q tools=%v", runID, run.Status, run.Step, run.Error, run.ToolsUsed)
	return domain.Run{}
}

func completionStatuses(t *testing.T, repo *memoryRepo, runID string) []string {
	t.Helper()
	eventsList, err := repo.ListByRun(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	statuses := make([]string, 0, 2)
	for _, event := range eventsList {
		if event.Type != domain.EventCompletionChecked {
			continue
		}
		var payload struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			t.Fatal(err)
		}
		statuses = append(statuses, payload.Status)
	}
	return statuses
}

func TestStartPersistsFailedRunWhenModelSetupFails(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) {
		return nil, errors.New("provider unavailable")
	})
	profile := domain.DefaultProfile()
	profile.MaxDurationSeconds = 5
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: root},
		Task:          "should fail before the model loop",
	})
	if err == nil {
		t.Fatal("expected model setup error")
	}
	if engine.IsActiveRun(run.ID) {
		t.Fatal("failed run stayed active")
	}
	saved := repo.run(run.ID)
	if saved.Status != domain.RunFailed || saved.Error == "" || saved.FinishedAt == nil {
		t.Fatalf("run not persisted as failed: %#v", saved)
	}
}

func guardrailCodes(t *testing.T, repo *memoryRepo, runID string) []string {
	t.Helper()
	eventsList, err := repo.ListByRun(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	codes := make([]string, 0)
	for _, event := range eventsList {
		if event.Type != domain.EventAgentGuardrail {
			continue
		}
		var payload struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(event.Data, &payload) != nil {
			t.Fatalf("invalid guardrail payload: %s", event.Data)
		}
		codes = append(codes, payload.Code)
	}
	return codes
}
