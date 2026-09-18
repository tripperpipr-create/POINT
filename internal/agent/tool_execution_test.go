package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
	"local-agent-workbench/internal/workspace"
)

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
