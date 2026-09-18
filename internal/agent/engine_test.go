package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/events"
	"local-agent-workbench/internal/providers"
	"local-agent-workbench/internal/sandbox"
	workbenchtools "local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
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

func (m *scriptedModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.calls == 1 {
		args, _ := json.Marshal(map[string]any{"path": "main.go"})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "call-read", Name: "read_file", Arguments: args}})
	}
	if m.calls == 2 {
		args, _ := json.Marshal(map[string]any{
			"path": "main.go", "reason": "add health with a localized edit",
			"edits": []map[string]string{{"oldText": "package main\n", "newText": "package main\n\nfunc Health() string { return \"ok\" }\n"}},
		})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "call-patch", Name: "propose_patch", Arguments: args}})
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Implemented and verified."})
}

func TestPatchApprovalEventOrder(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0600)
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &scriptedModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"read_file", "propose_patch"}
	profile.MaxDurationSeconds = 5
	configuration := domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC())
	run, err := engine.Start(StartInput{Configuration: configuration, Workspace: domain.Workspace{ID: "ws", Path: root}, Task: "add health"})
	if err != nil {
		t.Fatal(err)
	}
	var approval domain.Approval
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pending := engine.PendingApprovals(run.ID)
		if len(pending) > 0 {
			approval = pending[0]
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if approval.ID == "" {
		t.Fatal("approval not requested")
	}
	if err = engine.ResolveApproval(approval.ID, true); err != nil {
		t.Fatal(err)
	}
	completionDeadline := time.Now().Add(5 * time.Second)
	var eventsList []domain.Event
	for time.Now().Before(completionDeadline) {
		eventsList, err = repo.ListByRun(context.Background(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(eventsList) > 0 && eventsList[len(eventsList)-1].Type == domain.EventRunCompleted {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(eventsList) == 0 || eventsList[len(eventsList)-1].Type != domain.EventRunCompleted {
		t.Fatalf("run.completed event not observed: %#v", eventsList)
	}
	wanted := []domain.EventType{
		domain.EventRunStarted,
		domain.EventModelRequested, domain.EventModelResponded, domain.EventToolRequested, domain.EventToolStarted, domain.EventToolFinished,
		domain.EventModelRequested, domain.EventModelResponded, domain.EventToolRequested, domain.EventToolStarted, domain.EventToolFinished,
		domain.EventPatchProposed, domain.EventApprovalRequested, domain.EventApprovalResolved, domain.EventPatchApplied,
		domain.EventModelRequested, domain.EventModelStreamed, domain.EventModelResponded, domain.EventRunCompleted,
	}
	if len(eventsList) != len(wanted) {
		t.Fatalf("got %d events: %#v", len(eventsList), eventsList)
	}
	for i, event := range eventsList {
		if event.Type != wanted[i] {
			t.Fatalf("event %d=%s, want %s", i, event.Type, wanted[i])
		}
	}
	data, _ := os.ReadFile(filepath.Join(root, "main.go"))
	if string(data) != "package main\n\nfunc Health() string { return \"ok\" }\n" {
		t.Fatalf("patch not applied: %q", data)
	}
}

type blindPatchRepairModel struct {
	calls              int
	sawInspectionError bool
}

func (m *blindPatchRepairModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.calls++
	for _, message := range request.Messages {
		if message.Role == "tool" && strings.Contains(message.Content, `"code":"inspection_required"`) {
			m.sawInspectionError = true
		}
	}
	switch m.calls {
	case 1:
		arguments, _ := json.Marshal(map[string]any{"path": "main.go", "content": "package main\n\nfunc Ready() bool { return true }\n", "reason": "implement readiness"})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "blind-patch", Name: "propose_patch", Arguments: arguments}})
	case 2:
		arguments, _ := json.Marshal(map[string]any{"path": "main.go"})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "repair-read", Name: "read_file", Arguments: arguments}})
	case 3:
		arguments, _ := json.Marshal(map[string]any{"path": "main.go", "content": "package main\n\nfunc Ready() bool { return true }\n", "reason": "implement readiness after inspection"})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "repair-patch", Name: "propose_patch", Arguments: arguments}})
	default:
		return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Implemented after inspecting the current file."})
	}
}

func TestEngineBlocksBlindPatchAndLetsModelSelfCorrect(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &blindPatchRepairModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"read_file", "propose_patch"}
	profile.MaxDurationSeconds = 5
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: root},
		Task:          "implement readiness",
	})
	if err != nil {
		t.Fatal(err)
	}
	resolveNextApproval(t, engine, run.ID, "", true)
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted || !model.sawInspectionError {
		t.Fatalf("run=%#v saw inspection error=%v", finished, model.sawInspectionError)
	}
	repo.mu.Lock()
	approvalCount := len(repo.approvals)
	repo.mu.Unlock()
	if approvalCount != 1 {
		t.Fatalf("approvals=%d, blind patch must not request one", approvalCount)
	}
	if codes := guardrailCodes(t, repo, run.ID); fmt.Sprint(codes) != "[inspection_required]" {
		t.Fatalf("guardrails=%v", codes)
	}
}

type sameTurnReadPatchModel struct{ calls int }

func (m *sameTurnReadPatchModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.calls++
	patchArguments, _ := json.Marshal(map[string]any{"path": "main.go", "content": "package main\n\nfunc Ready() bool { return true }\n", "reason": "implement readiness"})
	if m.calls == 1 {
		readArguments, _ := json.Marshal(map[string]any{"path": "main.go"})
		if err := emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "same-turn-read", Name: "read_file", Arguments: readArguments}}); err != nil {
			return err
		}
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "same-turn-patch", Name: "propose_patch", Arguments: patchArguments}})
	}
	if m.calls == 2 {
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "visible-patch", Name: "propose_patch", Arguments: patchArguments}})
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Implemented from visible evidence."})
}

func TestEngineRejectsReadAndPatchFromSameModelTurn(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return &sameTurnReadPatchModel{}, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"read_file", "propose_patch"}
	profile.MaxDurationSeconds = 5
	run, err := engine.Start(StartInput{Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()), Workspace: domain.Workspace{ID: "ws", Path: root}, Task: "implement readiness"})
	if err != nil {
		t.Fatal(err)
	}
	resolveNextApproval(t, engine, run.ID, "", true)
	if finished := waitForTerminalRun(t, repo, run.ID); finished.Status != domain.RunCompleted {
		t.Fatalf("run=%#v", finished)
	}
	if codes := guardrailCodes(t, repo, run.ID); fmt.Sprint(codes) != "[inspection_required]" {
		t.Fatalf("guardrails=%v", codes)
	}
}

type staleReadRepairModel struct {
	calls int
	root  string
}

func (m *staleReadRepairModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.calls++
	read := func(id string) error {
		arguments, _ := json.Marshal(map[string]any{"path": "main.go"})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: id, Name: "read_file", Arguments: arguments}})
	}
	patch := func(id string) error {
		arguments, _ := json.Marshal(map[string]any{"path": "main.go", "content": "package main\n\n// user edit\nfunc Ready() bool { return true }\n", "reason": "preserve the latest user edit"})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: id, Name: "propose_patch", Arguments: arguments}})
	}
	switch m.calls {
	case 1:
		return read("stale-read")
	case 2:
		if err := os.WriteFile(filepath.Join(m.root, "main.go"), []byte("package main\n\n// user edit\n"), 0600); err != nil {
			return err
		}
		return patch("stale-patch")
	case 3:
		return read("fresh-read")
	case 4:
		return patch("fresh-patch")
	default:
		return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Preserved the concurrent user edit."})
	}
}

func TestEngineRejectsPatchAfterExternalFileChange(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return &staleReadRepairModel{root: root}, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"read_file", "propose_patch"}
	profile.MaxDurationSeconds = 5
	run, err := engine.Start(StartInput{Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()), Workspace: domain.Workspace{ID: "ws", Path: root}, Task: "implement readiness without losing user edits"})
	if err != nil {
		t.Fatal(err)
	}
	resolveNextApproval(t, engine, run.ID, "", true)
	if finished := waitForTerminalRun(t, repo, run.ID); finished.Status != domain.RunCompleted {
		t.Fatalf("run=%#v", finished)
	}
	if codes := guardrailCodes(t, repo, run.ID); fmt.Sprint(codes) != "[inspection_stale]" {
		t.Fatalf("guardrails=%v", codes)
	}
	data, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil || !strings.Contains(string(data), "// user edit") || !strings.Contains(string(data), "func Ready") {
		t.Fatalf("concurrent edit was not preserved: %q err=%v", data, err)
	}
}

func TestPatchInspectionStateDetectsPreparationRace(t *testing.T) {
	if patchInspectionMatches(patchInspectionState{Known: true, OriginalExisted: true, SHA256: strings.Repeat("a", 64)}, domain.PatchProposal{OriginalExisted: true, OriginalHash: strings.Repeat("b", 64)}) {
		t.Fatal("changed original hash was accepted")
	}
	if patchInspectionMatches(patchInspectionState{Known: true, OriginalExisted: false}, domain.PatchProposal{OriginalExisted: true, OriginalHash: strings.Repeat("a", 64)}) {
		t.Fatal("file created during patch preparation was accepted")
	}
}

func TestPatchSchemaValidationRunsBeforeInspectionGuard(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	registry, patches := BuildToolRegistry(fs, nil)
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"propose_patch"}
	active := &activeRun{run: domain.Run{ID: "validation-before-inspection", AgentID: "agent", Step: 1}}
	arguments, _ := json.Marshal(map[string]any{
		"path": "main.go", "reason": "invalid dual mode", "content": "package main\n",
		"edits": []map[string]string{{"oldText": "package main", "newText": "package next"}},
	})
	result, err := engine.executeTool(context.Background(), active, profile, registry, patches, newObservationTracker(nil), providers.ToolCall{ID: "invalid-patch", Name: "propose_patch", Arguments: arguments})
	if err != nil || result.OK || result.Error == nil || result.Error.Code != "invalid_input" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if codes := guardrailCodes(t, repo, active.run.ID); len(codes) != 0 {
		t.Fatalf("schema error was misclassified as inspection guard: %v", codes)
	}
}

func TestExecuteToolRemapsCursorStyleNamesAndAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\n\nfunc Ready() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	registry, patches := BuildToolRegistry(fs, nil)
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"read_file", "search_code", "list_files"}
	active := &activeRun{run: domain.Run{ID: "remap-tools", AgentID: "agent", Step: 1}}

	readArgs, _ := json.Marshal(map[string]any{"file": filepath.Join(root, "src", "main.go")})
	result, err := engine.executeTool(context.Background(), active, profile, registry, patches, newObservationTracker(nil), providers.ToolCall{ID: "alias-read", Name: "Read", Arguments: readArgs})
	if err != nil || !result.OK {
		t.Fatalf("Read alias failed: %#v err=%v", result, err)
	}
	if !slices.Contains(active.run.ToolsUsed, "read_file") {
		t.Fatalf("tools used=%v", active.run.ToolsUsed)
	}
	var readOut struct {
		Path string `json:"path"`
	}
	if err = json.Unmarshal(result.Output, &readOut); err != nil || readOut.Path != "src/main.go" {
		t.Fatalf("read output=%s err=%v", result.Output, err)
	}

	searchArgs, _ := json.Marshal(map[string]any{"pattern": "Ready"})
	result, err = engine.executeTool(context.Background(), active, profile, registry, patches, newObservationTracker(nil), providers.ToolCall{ID: "alias-grep", Name: "Grep", Arguments: searchArgs})
	if err != nil || !result.OK {
		t.Fatalf("Grep alias failed: %#v err=%v", result, err)
	}

	unknown, err := engine.executeTool(context.Background(), active, profile, registry, patches, newObservationTracker(nil), providers.ToolCall{ID: "alias-unknown", Name: "Delete", Arguments: json.RawMessage(`{}`)})
	if err != nil || unknown.OK || unknown.Error == nil || unknown.Error.Code != "tool_not_allowed" || unknown.Error.Hint == "" {
		t.Fatalf("unknown tool=%#v err=%v", unknown, err)
	}
	if !strings.Contains(unknown.Error.Hint, "read_file") {
		t.Fatalf("hint=%q", unknown.Error.Hint)
	}
}

type ambiguousEditRepairModel struct {
	calls             int
	sawAmbiguousError bool
}

func (m *ambiguousEditRepairModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.calls++
	for _, message := range request.Messages {
		if message.Role == "tool" && strings.Contains(message.Content, `"code":"edit_anchor_ambiguous"`) {
			m.sawAmbiguousError = true
		}
	}
	switch m.calls {
	case 1:
		arguments, _ := json.Marshal(map[string]any{"path": "main.go"})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "edit-read", Name: "read_file", Arguments: arguments}})
	case 2:
		arguments, _ := json.Marshal(map[string]any{
			"path": "main.go", "reason": "initial localized edit",
			"edits": []map[string]string{{"oldText": `return "old"`, "newText": `return "ok"`}},
		})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "ambiguous-edit", Name: "propose_patch", Arguments: arguments}})
	case 3:
		arguments, _ := json.Marshal(map[string]any{
			"path": "main.go", "reason": "use a unique function anchor",
			"edits": []map[string]string{{"oldText": `func Second() string { return "old" }`, "newText": `func Second() string { return "ok" }`}},
		})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "unique-edit", Name: "propose_patch", Arguments: arguments}})
	default:
		return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Updated only the requested function."})
	}
}

func TestEngineLetsAgentRepairAmbiguousExactEdit(t *testing.T) {
	root := t.TempDir()
	original := "package main\n\nfunc First() string { return \"old\" }\nfunc Second() string { return \"old\" }\n"
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &ambiguousEditRepairModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"read_file", "propose_patch"}
	profile.MaxDurationSeconds = 5
	run, err := engine.Start(StartInput{Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()), Workspace: domain.Workspace{ID: "ws", Path: root}, Task: "update only Second"})
	if err != nil {
		t.Fatal(err)
	}
	resolveNextApproval(t, engine, run.ID, "", true)
	if finished := waitForTerminalRun(t, repo, run.ID); finished.Status != domain.RunCompleted || !model.sawAmbiguousError {
		t.Fatalf("run=%#v saw ambiguous error=%v", finished, model.sawAmbiguousError)
	}
	data, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil || !strings.Contains(string(data), `func First() string { return "old" }`) || !strings.Contains(string(data), `func Second() string { return "ok" }`) {
		t.Fatalf("localized repair changed the wrong content: %q err=%v", data, err)
	}
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

type invalidProcessModel struct {
	mu              sync.Mutex
	calls           int
	sawInvalidInput bool
}

type failingPatchRepo struct{ *memoryRepo }

func (r *failingPatchRepo) SavePatch(context.Context, domain.PatchProposal) error {
	return fmt.Errorf("simulated patch journal failure")
}

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

func (m *invalidProcessModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.calls == 1 {
		args := json.RawMessage(`{"reason":"try unsafe value","mode":"delete"}`)
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "call-process", Name: "customtool_0123456789abcdef01234567", Arguments: args}})
	}
	for _, message := range request.Messages {
		if message.Role == "tool" && strings.Contains(message.Content, `"code":"invalid_input"`) {
			m.sawInvalidInput = true
		}
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Invalid input was rejected."})
}

func TestInvalidProcessArgumentsAreRejectedBeforeApproval(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &invalidProcessModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	custom := domain.CustomTool{
		ID: "customtool_0123456789abcdef01234567", Kind: domain.CustomToolProcess,
		DisplayName: "Typed process", Description: "Requires a safe enum", Program: "go", Arguments: []string{"version", "{{mode}}"},
		Parameters: []domain.CustomToolParameter{{Name: "mode", DisplayName: "Mode", Description: "Safe mode", Type: domain.CustomToolParameterEnum, Required: true, EnumValues: []string{"read"}, MaxLength: 16}},
		CWD:        ".", TimeoutSeconds: 30,
	}
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{custom.ID}
	profile.MaxDurationSeconds = 5
	configuration := domain.NewRunConfigurationSnapshot("test", profile, []domain.CustomTool{custom}, time.Now().UTC())
	run, err := engine.Start(StartInput{Configuration: configuration, Workspace: domain.Workspace{ID: "ws", Path: t.TempDir()}, Task: "reject invalid process input"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		repo.mu.Lock()
		status := repo.runs[run.ID].Status
		approvalCount := len(repo.approvals)
		repo.mu.Unlock()
		if approvalCount != 0 {
			t.Fatal("invalid process input created an approval")
		}
		if status == domain.RunCompleted {
			model.mu.Lock()
			sawInvalid := model.sawInvalidInput
			model.mu.Unlock()
			if !sawInvalid {
				t.Fatal("model did not receive structured invalid_input result")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("run did not complete")
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

type repeatingToolModel struct {
	mu           sync.Mutex
	calls        int
	sawGuardrail bool
}

func (m *repeatingToolModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	for _, message := range request.Messages {
		if message.Role == "tool" && strings.Contains(message.Content, `"code":"duplicate_tool_call"`) {
			m.sawGuardrail = true
		}
	}
	return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: fmt.Sprintf("call-%d", m.calls), Name: "list_files", Arguments: json.RawMessage(`{"maxDepth":1}`)}})
}

func TestIdenticalSuccessfulToolPlanIsNotExecutedRepeatedly(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &repeatingToolModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"list_files"}
	profile.MaxSteps = 10
	profile.MaxDurationSeconds = 5
	configuration := domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC())
	run, err := engine.Start(StartInput{Configuration: configuration, Workspace: domain.Workspace{ID: "ws", Path: t.TempDir()}, Task: "inspect once"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var final domain.Run
	for time.Now().Before(deadline) {
		repo.mu.Lock()
		final = repo.runs[run.ID]
		repo.mu.Unlock()
		if final.Status == domain.RunFailed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if final.Status != domain.RunFailed || !strings.Contains(final.Error, "agent stalled") || final.RequestCount != 6 {
		t.Fatalf("final run=%#v", final)
	}
	model.mu.Lock()
	modelCalls, sawGuardrail := model.calls, model.sawGuardrail
	model.mu.Unlock()
	if modelCalls != 6 || !sawGuardrail {
		t.Fatalf("model calls=%d saw duplicate feedback=%v", modelCalls, sawGuardrail)
	}
	eventsList, _ := repo.ListByRun(context.Background(), run.ID)
	started, guarded, recovered := 0, 0, 0
	for _, event := range eventsList {
		if event.Type == domain.EventToolStarted {
			started++
		}
		if event.Type == domain.EventAgentGuardrail {
			guarded++
			if strings.Contains(string(event.Data), `"code":"tool_plan_recovery"`) {
				recovered++
			}
		}
	}
	if started != 1 || recovered != 1 || guarded < 3 {
		t.Fatalf("tool execution was not bounded: started=%d recovered=%d guardrails=%d events=%#v", started, recovered, guarded, eventsList)
	}
}

type mixedRepeatingModel struct{ calls int }

func (m *mixedRepeatingModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.calls++
	if err := emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: fmt.Sprintf("list-%d", m.calls), Name: "list_files", Arguments: json.RawMessage(`{"maxDepth":1}`)}}); err != nil {
		return err
	}
	return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: fmt.Sprintf("read-%d", m.calls), Name: "read_file", Arguments: json.RawMessage(`{"path":"missing.go"}`)}})
}

func TestSuccessfulCallInMixedFailingPlanIsNotRepeated(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &mixedRepeatingModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"list_files", "read_file"}
	profile.MaxSteps = 10
	profile.MaxDurationSeconds = 5
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		Task:          "inspect mixed plan",
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		repo.mu.Lock()
		status := repo.runs[run.ID].Status
		repo.mu.Unlock()
		if status == domain.RunFailed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	eventsList, _ := repo.ListByRun(context.Background(), run.ID)
	started := map[string]int{}
	for _, event := range eventsList {
		if event.Type != domain.EventToolStarted {
			continue
		}
		var payload struct {
			Tool string `json:"tool"`
		}
		_ = json.Unmarshal(event.Data, &payload)
		started[payload.Tool]++
	}
	if started["list_files"] != 1 || started["read_file"] != 2 {
		t.Fatalf("successful calls were repeated or failures were not retryable: %#v", started)
	}
}

type retryEventModel struct{}

func (retryEventModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	if err := emit(providers.ModelEvent{Kind: providers.EventRetry, Attempt: 2, DelayMs: 250, Message: "temporary provider error"}); err != nil {
		return err
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Recovered."})
}

func TestProviderRetryIsPersistedInRunChronicle(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return retryEventModel{}, nil })
	profile := domain.DefaultProfile()
	profile.MaxDurationSeconds = 5
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		Task:          "recover provider",
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		repo.mu.Lock()
		status := repo.runs[run.ID].Status
		repo.mu.Unlock()
		if status == domain.RunCompleted {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	eventsList, _ := repo.ListByRun(context.Background(), run.ID)
	for _, event := range eventsList {
		if event.Type == domain.EventModelRetrying && strings.Contains(string(event.Data), `"attempt":2`) {
			return
		}
	}
	t.Fatalf("provider retry event was not persisted: %#v", eventsList)
}

type fallbackModel struct {
	mu     sync.Mutex
	models []string
}

func (m *fallbackModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	m.models = append(m.models, request.Model)
	m.mu.Unlock()
	if request.Model == "primary-model" {
		return errors.New("provider rate limit: 429")
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Fallback completed the task."})
}

func TestProjectTaskFreezesModelFallback(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &fallbackModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	engine.SetProcessExecutor(strongBoundaryExecutor{})
	profile := domain.DefaultProfile()
	profile.Model = "primary-model"
	profile.FallbackModels = []string{"fallback-model"}
	profile.MaxDurationSeconds = 5
	profile.MaxSteps = 2
	brief := domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, State: "ready", Goal: "Build", ResultKind: "report",
		Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Report", Kind: "manual"}},
	})
	approved, err := domain.ApproveTaskBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		SandboxPath:   t.TempDir(),
		Task:          "Build the report",
		TaskBrief:     &approved,
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunFailed {
		t.Fatalf("project fallback must fail closed, got %#v", finished)
	}
	if !strings.Contains(finished.Error, "freezes model") {
		t.Fatalf("error=%q", finished.Error)
	}
}

type reasoningBudgetModel struct {
	mu     sync.Mutex
	models []string
}

func (m *reasoningBudgetModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	m.models = append(m.models, request.Model)
	m.mu.Unlock()
	if request.Model == "primary-model" {
		return errors.New("model returned no answer: the entire output budget of 65536 tokens went to reasoning (finish_reason=length)")
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Fallback completed the task."})
}

type reasoningDisableThinkingModel struct {
	mu             sync.Mutex
	disableRetries int
	requests       int
}

func (m *reasoningDisableThinkingModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	m.requests++
	disable := request.DisableThinking
	if disable {
		m.disableRetries++
	}
	m.mu.Unlock()
	if !disable {
		return errors.New("model returned no answer: the entire output budget of 16384 tokens went to reasoning (finish_reason=length)")
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Completed after thinking was disabled."})
}

func TestProjectTaskFallsBackWhenReasoningConsumesOutputBudget(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &reasoningBudgetModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	engine.SetProcessExecutor(strongBoundaryExecutor{})
	profile := domain.DefaultProfile()
	profile.Model = "primary-model"
	profile.FallbackModels = []string{"fallback-model"}
	profile.MaxDurationSeconds = 5
	profile.MaxSteps = 2
	brief := domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, State: "ready", Goal: "Build", ResultKind: "report",
		Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Report", Kind: "manual"}},
	})
	approved, err := domain.ApproveTaskBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		SandboxPath:   t.TempDir(),
		Task:          "Build the report",
		TaskBrief:     &approved,
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted || finished.Model != "fallback-model" {
		t.Fatalf("run=%#v", finished)
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	if len(model.models) < 2 || model.models[len(model.models)-1] != "fallback-model" {
		t.Fatalf("models=%v", model.models)
	}
}

func TestPaidRuntimeRetriesWithDisableThinkingAfterReasoningBudget(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &reasoningDisableThinkingModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	engine.SetProcessExecutor(strongBoundaryExecutor{})
	profile := domain.DefaultProfile()
	// Глушение осталось только там, где токены размышления оплачены: свой
	// endpoint принимает переключатель и может стоять перед платным API.
	profile.Provider = domain.ProviderOpenAI
	profile.ProviderPreset = "custom"
	profile.BaseURL = "https://gateway.invalid/v1"
	profile.Model = "primary-model"
	profile.FallbackModels = nil
	profile.MaxDurationSeconds = 5
	profile.MaxSteps = 2
	profile.ReasoningEffort = "low"
	brief := domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, State: "ready", Goal: "Build", ResultKind: "report",
		Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Report", Kind: "manual"}},
	})
	approved, err := domain.ApproveTaskBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		SandboxPath:   t.TempDir(),
		Task:          "Build the report",
		TaskBrief:     &approved,
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted {
		t.Fatalf("run=%#v", finished)
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	if model.disableRetries < 1 || model.requests < 2 {
		t.Fatalf("disableRetries=%d requests=%d", model.disableRetries, model.requests)
	}
}

// reasoningKeepsThinkingModel отвечает со второго захода, не требуя тишины.
type reasoningKeepsThinkingModel struct {
	mu        sync.Mutex
	requests  int
	suppressd int
}

func (m *reasoningKeepsThinkingModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	m.requests++
	first := m.requests == 1
	if request.DisableThinking {
		m.suppressd++
	}
	m.mu.Unlock()
	if first {
		return errors.New("model returned no answer: the entire output budget of 16384 tokens went to reasoning (finish_reason=length)")
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Completed while still thinking."})
}

// Бесплатный рантайм не расплачивается за размышление, поэтому обрезанный ход
// не отнимает размышление у всего остатка прогона: движок подсказывает про
// бюджет и идёт дальше думающим.
func TestFreeRuntimeKeepsThinkingAfterReasoningBudget(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &reasoningKeepsThinkingModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	engine.SetProcessExecutor(strongBoundaryExecutor{})
	profile := domain.DefaultProfile()
	profile.Model = "primary-model"
	profile.FallbackModels = nil
	profile.MaxDurationSeconds = 5
	profile.MaxSteps = 3
	profile.ReasoningEffort = "low"
	brief := domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, State: "ready", Goal: "Build", ResultKind: "report",
		Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Report", Kind: "manual"}},
	})
	approved, err := domain.ApproveTaskBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		SandboxPath:   t.TempDir(),
		Task:          "Build the report",
		TaskBrief:     &approved,
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted {
		t.Fatalf("status=%s error=%s", finished.Status, finished.Error)
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	if model.suppressd != 0 {
		t.Fatalf("thinking was suppressed %d times on a free runtime", model.suppressd)
	}
	if model.requests < 2 {
		t.Fatalf("requests=%d", model.requests)
	}
}

func TestRunSwitchesToFallbackModelOnClassifiedProviderError(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &fallbackModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.Model = "primary-model"
	profile.FallbackModels = []string{"fallback-model"}
	profile.MaxDurationSeconds = 5
	profile.MaxSteps = 2
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		Task:          "Explain the result.",
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted || finished.Model != "fallback-model" {
		t.Fatalf("run=%#v", finished)
	}
	if finished.ConfigurationSnapshot.Profile.Model != "fallback-model" {
		t.Fatalf("snapshot model=%q", finished.ConfigurationSnapshot.Profile.Model)
	}
	startedDigest := domain.NewRunConfigurationSnapshot("test", profile, nil, finished.ConfigurationSnapshot.CapturedAt).ConfigurationDigest
	if finished.ConfigurationSnapshot.ConfigurationDigest == "" || finished.ConfigurationSnapshot.ConfigurationDigest == startedDigest {
		t.Fatalf("fallback must change configuration digest; got %q started %q", finished.ConfigurationSnapshot.ConfigurationDigest, startedDigest)
	}
	model.mu.Lock()
	models := append([]string(nil), model.models...)
	model.mu.Unlock()
	if strings.Join(models, ",") != "primary-model,fallback-model" {
		t.Fatalf("models=%v", models)
	}
	eventsList, err := repo.ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	sawFallback := false
	for _, event := range eventsList {
		if event.Type == domain.EventModelRetrying && strings.Contains(string(event.Data), `"fallback":true`) {
			if !strings.Contains(string(event.Data), `"configurationDigest"`) {
				t.Fatalf("fallback event missing configuration digest: %s", event.Data)
			}
			sawFallback = true
			break
		}
	}
	if !sawFallback {
		t.Fatal("fallback transition was not recorded")
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

type indexedScopeRepairModel struct {
	mu                    sync.Mutex
	calls                 int
	sawDigest             bool
	sawScopeGuardrail     bool
	maxSearchMessageBytes int
}

func (m *indexedScopeRepairModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	for _, message := range request.Messages {
		if message.Role != "tool" {
			continue
		}
		if strings.Contains(message.Content, `"fileSha256"`) {
			m.sawDigest = true
			if len(message.Content) > m.maxSearchMessageBytes {
				m.maxSearchMessageBytes = len(message.Content)
			}
		}
		if strings.Contains(message.Content, `"code":"inspection_scope_required"`) {
			m.sawScopeGuardrail = true
		}
	}
	firstEdit := map[string]string{
		"oldText": `func AlphaQuestMarker() string { return "sealed" }`,
		"newText": `func AlphaQuestMarker() string { return "opened" }`,
	}
	secondEdit := map[string]string{
		"oldText": `func OmegaQuestMarker() string { return "sealed" }`,
		"newText": `func OmegaQuestMarker() string { return "opened" }`,
	}
	switch m.calls {
	case 1:
		arguments, _ := json.Marshal(map[string]any{"query": "AlphaQuestMarker", "max_chunks": 1, "max_chars": 4096})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "search-alpha", Name: "search_code", Arguments: arguments}})
	case 2:
		arguments, _ := json.Marshal(map[string]any{"path": "generated.go", "reason": "update both quest markers", "edits": []map[string]string{firstEdit, secondEdit}})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "patch-unseen-omega", Name: "propose_patch", Arguments: arguments}})
	case 3:
		arguments, _ := json.Marshal(map[string]any{"query": "OmegaQuestMarker", "max_chunks": 1, "max_chars": 4096})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "search-omega", Name: "search_code", Arguments: arguments}})
	case 4:
		arguments, _ := json.Marshal(map[string]any{"path": "generated.go", "reason": "update both inspected quest markers", "edits": []map[string]string{firstEdit, secondEdit}})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "patch-inspected", Name: "propose_patch", Arguments: arguments}})
	default:
		return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Оба найденных участка обновлены."})
	}
}

func TestAgentRepairsIndexedEditScopeWithoutReadingLargeFile(t *testing.T) {
	root := t.TempDir()
	var source strings.Builder
	source.WriteString("package generated\n\n")
	source.WriteString(`func AlphaQuestMarker() string { return "sealed" }` + "\n")
	for index := 0; index < 8_000; index++ {
		fmt.Fprintf(&source, "// filler rune %05d keeps distant fragments separate\n", index)
	}
	source.WriteString(`func OmegaQuestMarker() string { return "sealed" }` + "\n")
	original := source.String()
	if len(original) < 256*1024 || len(original) >= 512*1024 {
		t.Fatalf("large fixture size=%d", len(original))
	}
	if err := os.WriteFile(filepath.Join(root, "generated.go"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &indexedScopeRepairModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"search_code", "propose_patch"}
	profile.MaxSteps = 8
	profile.MaxDurationSeconds = 60
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws-indexed", Path: root},
		Task:          "update both quest markers",
	})
	if err != nil {
		t.Fatal(err)
	}
	resolveNextApproval(t, engine, run.ID, "", true)
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted {
		t.Fatalf("run=%#v", finished)
	}
	if slices.Contains(finished.ToolsUsed, "read_file") || !slices.Contains(finished.ToolsUsed, "search_code") || !slices.Contains(finished.ToolsUsed, "propose_patch") {
		t.Fatalf("unexpected tools=%v", finished.ToolsUsed)
	}
	data, err := os.ReadFile(filepath.Join(root, "generated.go"))
	if err != nil {
		t.Fatal(err)
	}
	updated := string(data)
	if !strings.Contains(updated, `func AlphaQuestMarker() string { return "opened" }`) || !strings.Contains(updated, `func OmegaQuestMarker() string { return "opened" }`) {
		t.Fatalf("indexed edits were not applied")
	}
	if strings.Count(updated, "// filler rune") != 8_000 || len(updated) != len(original) {
		t.Fatal("unrelated large-file content changed")
	}
	model.mu.Lock()
	calls, sawDigest, sawScope, maxSearchBytes := model.calls, model.sawDigest, model.sawScopeGuardrail, model.maxSearchMessageBytes
	model.mu.Unlock()
	if calls != 5 || !sawDigest || !sawScope || maxSearchBytes == 0 || maxSearchBytes > 10*1024 {
		t.Fatalf("model calls=%d digest=%v scope=%v max search message=%d", calls, sawDigest, sawScope, maxSearchBytes)
	}
	codes := guardrailCodes(t, repo, run.ID)
	if len(codes) != 1 || codes[0] != "inspection_scope_required" {
		t.Fatalf("guardrail codes=%v", codes)
	}
}

type retrievalRefinementModel struct {
	mu               sync.Mutex
	calls            int
	sawTruncation    bool
	sawMatchedTokens bool
	maxSearchBytes   int
}

func (m *retrievalRefinementModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	for _, message := range request.Messages {
		if message.Role != "tool" || !strings.Contains(message.Content, `"fileSha256"`) {
			continue
		}
		if strings.Contains(message.Content, `"truncated":true`) && strings.Contains(message.Content, `"candidateChunks":`) {
			m.sawTruncation = true
		}
		if strings.Contains(message.Content, `"matchedTokens"`) {
			m.sawMatchedTokens = true
		}
		if len(message.Content) > m.maxSearchBytes {
			m.maxSearchBytes = len(message.Content)
		}
	}
	switch m.calls {
	case 1:
		arguments := json.RawMessage(`{"query":"quest marker","max_chunks":1,"max_chars":1200}`)
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "broad-search", Name: "search_code", Arguments: arguments}})
	case 2:
		arguments := json.RawMessage(`{"query":"FinalizeRareArtifact","max_chunks":1,"max_chars":2400}`)
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "refined-search", Name: "search_code", Arguments: arguments}})
	case 3:
		arguments, _ := json.Marshal(map[string]any{
			"path": "target.go", "reason": "update the exact declaration found by refined retrieval",
			"edits": []map[string]string{{
				"oldText": `func FinalizeRareArtifact() string { return "sealed" }`,
				"newText": `func FinalizeRareArtifact() string { return "opened" }`,
			}},
		})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "refined-patch", Name: "propose_patch", Arguments: arguments}})
	default:
		return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Точный символ найден и обновлён."})
	}
}

func TestAgentRefinesTruncatedSearchBeforeEditing(t *testing.T) {
	root := t.TempDir()
	var common strings.Builder
	common.WriteString("package common\n")
	for line := 0; line < 4_000; line++ {
		fmt.Fprintf(&common, "// quest marker common lore %04d\n", line)
	}
	if err := os.WriteFile(filepath.Join(root, "common.go"), []byte(common.String()), 0600); err != nil {
		t.Fatal(err)
	}
	target := "package target\n\n" + `func FinalizeRareArtifact() string { return "sealed" }` + "\n"
	if err := os.WriteFile(filepath.Join(root, "target.go"), []byte(target), 0600); err != nil {
		t.Fatal(err)
	}
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &retrievalRefinementModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"search_code", "propose_patch"}
	profile.MaxSteps = 7
	profile.MaxDurationSeconds = 60
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws-retrieval", Path: root},
		Task:          "Update FinalizeRareArtifact without loading unrelated lore",
	})
	if err != nil {
		t.Fatal(err)
	}
	resolveNextApproval(t, engine, run.ID, "", true)
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted || slices.Contains(finished.ToolsUsed, "read_file") {
		t.Fatalf("run=%#v", finished)
	}
	data, err := os.ReadFile(filepath.Join(root, "target.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != strings.Replace(target, `return "sealed"`, `return "opened"`, 1) {
		t.Fatalf("target=%q", data)
	}
	model.mu.Lock()
	calls, sawTruncation, sawMatched, maxSearchBytes := model.calls, model.sawTruncation, model.sawMatchedTokens, model.maxSearchBytes
	model.mu.Unlock()
	if calls != 4 || !sawTruncation || !sawMatched || maxSearchBytes <= 0 || maxSearchBytes > 5*1024 {
		t.Fatalf("calls=%d truncation=%v matched=%v maxSearchBytes=%d", calls, sawTruncation, sawMatched, maxSearchBytes)
	}
}

type dependencyAwareMultiFileModel struct {
	mu                  sync.Mutex
	calls               int
	sawAPIImporter      bool
	sawRelatedTest      bool
	relatedMetadataOnly bool
}

func (m *dependencyAwareMultiFileModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	for _, message := range request.Messages {
		if message.Role != "tool" {
			continue
		}
		var result domain.ToolResult
		if json.Unmarshal([]byte(message.Content), &result) != nil || !result.OK {
			continue
		}
		var output struct {
			RelatedFiles []map[string]any `json:"relatedFiles"`
		}
		if json.Unmarshal(result.Output, &output) != nil || len(output.RelatedFiles) == 0 {
			continue
		}
		m.relatedMetadataOnly = true
		for _, related := range output.RelatedFiles {
			path, _ := related["path"].(string)
			relation, _ := related["relation"].(string)
			if _, leaked := related["content"]; leaked {
				m.relatedMetadataOnly = false
			}
			if path == "src/api.ts" && relation == "imported_by" {
				m.sawAPIImporter = true
			}
			if path == "src/service.test.ts" && relation == "test" {
				m.sawRelatedTest = true
			}
		}
	}
	switch m.calls {
	case 1:
		arguments := json.RawMessage(`{"query":"CreateSession","max_chunks":1,"max_chars":2400,"include_related":true}`)
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "dependency-search", Name: "search_code", Arguments: arguments}})
	case 2:
		arguments := json.RawMessage(`{"query":"src/service.test.ts CreateSession","max_chunks":1,"max_chars":2400}`)
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "test-search", Name: "search_code", Arguments: arguments}})
	case 3:
		servicePatch, _ := json.Marshal(map[string]any{
			"path": "src/service.ts", "reason": "update the inspected implementation",
			"edits": []map[string]string{{
				"oldText": `export function CreateSession() { return 'ok' }`,
				"newText": `export function CreateSession() { return 'secure' }`,
			}},
		})
		if err := emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "service-patch", Name: "propose_patch", Arguments: servicePatch}}); err != nil {
			return err
		}
		testPatch, _ := json.Marshal(map[string]any{
			"path": "src/service.test.ts", "reason": "update the inspected related test",
			"edits": []map[string]string{{
				"oldText": `expect(CreateSession()).toBe('ok')`,
				"newText": `expect(CreateSession()).toBe('secure')`,
			}},
		})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "test-patch", Name: "propose_patch", Arguments: testPatch}})
	default:
		return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Реализация и связанный тест обновлены."})
	}
}

func TestAgentUsesDependencyGraphForTwoInspectedFileEdits(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"src/service.ts":      "export function CreateSession() { return 'ok' }\n",
		"src/api.ts":          "import { CreateSession } from './service'\nexport const handler = CreateSession\n",
		"src/service.test.ts": "import { CreateSession } from './service'\ntest('session', () => { expect(CreateSession()).toBe('ok') })\n",
	}
	for relative, content := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &dependencyAwareMultiFileModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"search_code", "propose_patch"}
	profile.MaxSteps = 7
	profile.MaxDurationSeconds = 60
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws-dependencies", Path: root},
		Task:          "Update CreateSession and its related test",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstApproval := resolveNextApproval(t, engine, run.ID, "", true)
	resolveNextApproval(t, engine, run.ID, firstApproval, true)
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted || slices.Contains(finished.ToolsUsed, "read_file") || len(finished.ChangedFiles) != 2 {
		t.Fatalf("run=%#v", finished)
	}
	service, err := os.ReadFile(filepath.Join(root, "src", "service.ts"))
	if err != nil || !strings.Contains(string(service), "return 'secure'") {
		t.Fatalf("service=%q err=%v", service, err)
	}
	testFile, err := os.ReadFile(filepath.Join(root, "src", "service.test.ts"))
	if err != nil || !strings.Contains(string(testFile), "toBe('secure')") {
		t.Fatalf("test=%q err=%v", testFile, err)
	}
	model.mu.Lock()
	calls, sawAPI, sawTest, metadataOnly := model.calls, model.sawAPIImporter, model.sawRelatedTest, model.relatedMetadataOnly
	model.mu.Unlock()
	if calls != 4 || !sawAPI || !sawTest || !metadataOnly {
		t.Fatalf("calls=%d api=%v test=%v metadataOnly=%v", calls, sawAPI, sawTest, metadataOnly)
	}
	if codes := guardrailCodes(t, repo, run.ID); len(codes) != 0 {
		t.Fatalf("unchanged second file was unnecessarily invalidated: %v", codes)
	}
}

type malformedArgumentsModel struct {
	calls         int
	sawCorrection bool
}

func (m *malformedArgumentsModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.calls++
	if m.calls == 1 {
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "bad", Name: "read_file", Arguments: json.RawMessage(`{}`), ArgumentError: "tool arguments are not valid JSON"}})
	}
	for _, message := range request.Messages {
		if message.Role == "tool" && strings.Contains(message.Content, `"code":"invalid_input"`) {
			m.sawCorrection = true
		}
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Corrected after structured feedback."})
}

func TestMalformedToolArgumentsAreReturnedToModelWithoutExecuting(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &malformedArgumentsModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"read_file"}
	profile.MaxDurationSeconds = 5
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		Task:          "recover malformed call",
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		repo.mu.Lock()
		status := repo.runs[run.ID].Status
		repo.mu.Unlock()
		if status == domain.RunCompleted {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !model.sawCorrection {
		t.Fatal("model did not receive structured invalid_input feedback")
	}
	eventsList, _ := repo.ListByRun(context.Background(), run.ID)
	for _, event := range eventsList {
		if event.Type == domain.EventToolStarted {
			t.Fatalf("malformed tool call was executed: %#v", eventsList)
		}
	}
}

type emptyModel struct{}

func (emptyModel) Stream(context.Context, providers.ModelRequest, func(providers.ModelEvent) error) error {
	return nil
}

func TestEmptyModelResponseFailsInsteadOfReportingFalseSuccess(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return emptyModel{}, nil })
	profile := domain.DefaultProfile()
	profile.MaxDurationSeconds = 5
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		Task:          "do not accept empty",
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var final domain.Run
	for time.Now().Before(deadline) {
		repo.mu.Lock()
		final = repo.runs[run.ID]
		repo.mu.Unlock()
		if final.Status == domain.RunFailed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if final.Status != domain.RunFailed || !strings.Contains(final.Error, "empty response") {
		t.Fatalf("empty response was not rejected: %#v", final)
	}
}

type rollingContextModel struct {
	mu             sync.Mutex
	calls          int
	paths          []string
	budget         int
	maxInputTokens int
	overBudget     bool
	sawMemory      bool
}

func (m *rollingContextModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	estimated := EstimateModelInputTokens(request.Messages, request.Tools)
	if estimated > m.maxInputTokens {
		m.maxInputTokens = estimated
	}
	if estimated > m.budget {
		m.overBudget = true
	}
	for _, message := range request.Messages {
		if strings.Contains(message.Content, "<point_run_memory>") {
			m.sawMemory = true
		}
	}
	if m.calls <= len(m.paths) {
		if err := emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: strings.Repeat("evidence ", 180)}); err != nil {
			return err
		}
		arguments, _ := json.Marshal(map[string]string{"path": m.paths[m.calls-1]})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: fmt.Sprintf("rolling-%d", m.calls), Name: "read_file", Arguments: arguments}})
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Context stayed bounded and the repeated read was executed safely."})
}

func TestEngineRollsContextAndAllowsEvictedReadToRunAgain(t *testing.T) {
	root := t.TempDir()
	paths := make([]string, 0, 11)
	for index := 0; index < 10; index++ {
		name := fmt.Sprintf("file-%02d.txt", index)
		if err := os.WriteFile(filepath.Join(root, name), []byte(strings.Repeat(fmt.Sprintf("line-%02d evidence\n", index), 900)), 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, name)
	}
	paths = append(paths, paths[0])

	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"read_file"}
	profile.MaxSteps = 15
	profile.MaxDurationSeconds = 10
	profile.ContextWindowTokens = 4096
	profile.MaxOutputTokens = 1024
	budget := ModelInputBudgetTokens(profile)
	model := &rollingContextModel{paths: paths, budget: budget}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	configuration := domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC())
	run, err := engine.Start(StartInput{Configuration: configuration, Workspace: domain.Workspace{ID: "ws", Path: root}, Task: "inspect files with bounded memory"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		repo.mu.Lock()
		status := repo.runs[run.ID].Status
		repo.mu.Unlock()
		if status == domain.RunCompleted || status == domain.RunFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	repo.mu.Lock()
	finished := repo.runs[run.ID]
	repo.mu.Unlock()
	if finished.Status != domain.RunCompleted {
		t.Fatalf("run status=%s error=%q", finished.Status, finished.Error)
	}
	model.mu.Lock()
	overBudget, maxInput, sawMemory := model.overBudget, model.maxInputTokens, model.sawMemory
	model.mu.Unlock()
	if overBudget || maxInput > budget || !sawMemory {
		t.Fatalf("rolling context budget=%d max=%d over=%v memory=%v", budget, maxInput, overBudget, sawMemory)
	}
	eventsList, err := repo.ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	compactions, successfulReads := 0, 0
	for _, event := range eventsList {
		if event.Type == domain.EventContextCompacted {
			compactions++
		}
		if event.Type == domain.EventToolFinished {
			var payload struct {
				Tool   string            `json:"tool"`
				Result domain.ToolResult `json:"result"`
			}
			_ = json.Unmarshal(event.Data, &payload)
			if payload.Tool == "read_file" && payload.Result.OK {
				successfulReads++
			}
		}
	}
	if compactions == 0 || successfulReads != len(paths) {
		t.Fatalf("compactions=%d successful reads=%d want=%d", compactions, successfulReads, len(paths))
	}
}

type completionRepairModel struct {
	calls       int
	sawFeedback bool
}

func (m *completionRepairModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.calls++
	for _, message := range request.Messages {
		if message.Role == "user" && strings.Contains(message.Content, "<point_completion_gate>") {
			m.sawFeedback = true
		}
	}
	switch m.calls {
	case 1:
		arguments, _ := json.Marshal(map[string]any{"path": "main.go"})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "repair-read", Name: "read_file", Arguments: arguments}})
	case 2:
		arguments, _ := json.Marshal(map[string]any{
			"path": "main.go", "content": "package main\n\nfunc Ready() bool { return true }\n", "reason": "implement readiness",
		})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "repair-patch", Name: "propose_patch", Arguments: arguments}})
	case 3:
		return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Implemented successfully."})
	case 4:
		arguments, _ := json.Marshal(map[string]any{"command": "go test ./...", "reason": "verify the workspace after the accepted change"})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "repair-verify", Name: "run_command", Arguments: arguments}})
	default:
		return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Implemented and verified with go test ./...."})
	}
}

func TestEngineRepairsPrematureCompletionWithVerificationEvidence(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module completion-fixture\n\ngo 1.25\n"), 0600); err != nil {
		t.Fatal(err)
	}
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &completionRepairModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"read_file", "propose_patch", "run_command"}
	profile.MaxDurationSeconds = 10
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: root},
		Task:          "implement readiness",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstApproval := resolveNextApproval(t, engine, run.ID, "", true)
	resolveNextApproval(t, engine, run.ID, firstApproval, true)
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted || !strings.Contains(finished.Result, "verified with go test") {
		t.Fatalf("run=%#v", finished)
	}
	if !model.sawFeedback {
		t.Fatal("model did not receive deterministic completion feedback")
	}
	statuses := completionStatuses(t, repo, run.ID)
	if fmt.Sprint(statuses) != "[revision_required accepted_after_revision]" {
		t.Fatalf("completion statuses=%v", statuses)
	}
}

type completionRejectModel struct {
	sawFeedback bool
}

func (m *completionRejectModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	for _, message := range request.Messages {
		if message.Role == "user" && strings.Contains(message.Content, "<point_completion_gate>") {
			m.sawFeedback = true
		}
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Everything is done and tests pass."})
}

func TestEngineRejectsRepeatedUnevidencedCompletion(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &completionRejectModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"run_command"}
	profile.MaxDurationSeconds = 5
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		Task:          "Run tests before completion",
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunFailed || !strings.Contains(finished.Error, "completion gate") {
		t.Fatalf("unevidenced run was not rejected: %#v", finished)
	}
	if !model.sawFeedback {
		t.Fatal("model did not receive a correction opportunity")
	}
	statuses := completionStatuses(t, repo, run.ID)
	if fmt.Sprint(statuses) != "[revision_required revision_required rejected]" {
		t.Fatalf("completion statuses=%v", statuses)
	}
}

type repeatedFailedVerificationModel struct{ calls int }

func (m *repeatedFailedVerificationModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.calls++
	if m.calls == 1 || m.calls == 3 {
		arguments, _ := json.Marshal(map[string]any{"command": "go test ./point_agent_missing_package", "reason": "exercise failed verification handling"})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: fmt.Sprintf("failed-command-%d", m.calls), Name: "run_command", Arguments: arguments}})
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Tests pass."})
}

func TestEngineRetriesFailedCommandInsteadOfTreatingItAsCompleted(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &repeatedFailedVerificationModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"run_command"}
	profile.MaxDurationSeconds = 10
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		Task:          "Run tests before completion",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstApproval := resolveNextApproval(t, engine, run.ID, "", true)
	resolveNextApproval(t, engine, run.ID, firstApproval, true)
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunFailed || !strings.Contains(finished.Error, "verification_failed") {
		t.Fatalf("failed verification was accepted: %#v", finished)
	}
	eventsList, err := repo.ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	started, duplicates := 0, 0
	for _, event := range eventsList {
		if event.Type == domain.EventToolStarted && strings.Contains(string(event.Data), `"tool":"run_command"`) {
			started++
		}
		if event.Type == domain.EventToolFinished && strings.Contains(string(event.Data), "duplicate_tool_call") {
			duplicates++
		}
	}
	if started != 2 || duplicates != 0 {
		t.Fatalf("failed command starts=%d duplicate rejections=%d", started, duplicates)
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
	saved := repo.runs[run.ID]
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

func TestToolPlanRecoveryLetsModelChangeApproach(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &recoveringToolPlanModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"list_files"}
	profile.MaxSteps = 10
	profile.MaxDurationSeconds = 5
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		Task:          "inspect once then finish",
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted || !strings.Contains(finished.Result, "Recovered after plan gate") {
		t.Fatalf("recovery did not complete: %#v", finished)
	}
	if !model.sawRecovery {
		t.Fatal("model never received tool plan recovery feedback")
	}
	codes := guardrailCodes(t, repo, run.ID)
	if len(codes) < 2 || codes[len(codes)-1] == "agent_stalled" {
		t.Fatalf("expected soft recovery without stall, codes=%v", codes)
	}
	foundRecovery := false
	for _, code := range codes {
		if code == "tool_plan_recovery" {
			foundRecovery = true
		}
	}
	if !foundRecovery {
		t.Fatalf("missing tool_plan_recovery guardrail: %v", codes)
	}
}

type recoveringToolPlanModel struct {
	calls       int
	sawRecovery bool
}

func (m *recoveringToolPlanModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.calls++
	for _, message := range request.Messages {
		if message.Role == "user" && strings.Contains(message.Content, "<point_tool_plan_gate>") && strings.Contains(message.Content, "tool_plan_recovery") {
			m.sawRecovery = true
			return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Recovered after plan gate without repeating the same call."})
		}
	}
	return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: fmt.Sprintf("recover-%d", m.calls), Name: "list_files", Arguments: json.RawMessage(`{"maxDepth":1}`)}})
}

func TestInspectionHintMatchesRequirement(t *testing.T) {
	hint := inspectionHint(patchInspectionRequirement{Code: "inspection_required", RequiredTool: "list_files"})
	if !strings.Contains(hint, "list_files") {
		t.Fatalf("new-file hint=%q", hint)
	}
	hint = inspectionHint(patchInspectionRequirement{Code: "inspection_stale", RequiredTool: "read_file"})
	if !strings.Contains(hint, "read_file") {
		t.Fatalf("stale hint=%q", hint)
	}
}

func TestPlanHasUncompletedCalls(t *testing.T) {
	call := providers.ToolCall{Name: "list_files", Arguments: json.RawMessage(`{"maxDepth":1}`)}
	key := toolExecutionKey(call, 0)
	if !planHasUncompletedCalls([]providers.ToolCall{call}, map[string]struct{}{}, 0) {
		t.Fatal("empty completion map should be retryable")
	}
	if planHasUncompletedCalls([]providers.ToolCall{call}, map[string]struct{}{key: {}}, 0) {
		t.Fatal("completed plan should not be retryable")
	}
}

type strongBoundaryExecutor struct{}

func (strongBoundaryExecutor) PrepareProcess(context.Context, sandbox.ProcessRequest) (sandbox.PreparedProcess, error) {
	return sandbox.PreparedProcess{}, errors.New("strong boundary stub does not execute commands")
}

func (strongBoundaryExecutor) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{StrongOSBoundary: true, ProcessIsolation: true, NetworkIsolation: true}
}

// На официальном endpoint'е переключателя «не размышляй» нет: поле уходит сверх
// спецификации и возвращает 400. Признак живёт до конца прогона, поэтому один
// такой повтор отравил бы ошибкой формата все оставшиеся шаги.
func TestStrictEndpointNeverRetriesWithDisableThinking(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &reasoningDisableThinkingModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	engine.SetProcessExecutor(strongBoundaryExecutor{})
	profile := domain.DefaultProfile()
	profile.Provider = domain.ProviderOpenAI
	profile.ProviderPreset = "openai"
	profile.BaseURL = "https://api.openai.com/v1"
	profile.Model = "primary-model"
	profile.FallbackModels = nil
	profile.MaxDurationSeconds = 5
	profile.MaxSteps = 2
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		SandboxPath:   t.TempDir(),
		Task:          "Build the report",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForTerminalRun(t, repo, run.ID)
	model.mu.Lock()
	defer model.mu.Unlock()
	if model.disableRetries != 0 {
		t.Fatalf("строгий endpoint получил %d запросов с полем сверх спецификации", model.disableRetries)
	}
}
