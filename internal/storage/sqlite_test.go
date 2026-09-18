package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestEventOrderAndImmutability(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	for _, kind := range []domain.EventType{domain.EventRunStarted, domain.EventModelRequested, domain.EventModelResponded} {
		if err = s.Append(ctx, domain.Event{
			ID: domain.NewID("evt"), RunID: "run", AgentID: "agent",
			ExecutionID: "execution", QuestID: "quest", FlowRunID: "flow-run", FlowNodeID: "flow-node",
			Type: kind, Data: json.RawMessage(`{}`), CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := s.ListByRun(ctx, "run")
	if err != nil || len(events) != 3 {
		t.Fatalf("events=%#v err=%v", events, err)
	}
	if events[0].Type != domain.EventRunStarted || events[2].Type != domain.EventModelResponded {
		t.Fatalf("event order lost: %#v", events)
	}
	if events[1].ExecutionID != "execution" || events[1].QuestID != "quest" ||
		events[1].FlowRunID != "flow-run" || events[1].FlowNodeID != "flow-node" {
		t.Fatalf("event correlation lost: %#v", events[1])
	}
	if _, err = s.db.Exec(`UPDATE events SET actor='tampered' WHERE run_id='run'`); err == nil {
		t.Fatal("immutable event update unexpectedly succeeded")
	}
}

func TestProfileContextWindowPersistsAndMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	profile := domain.DefaultProfile()
	profile.ContextWindowTokens = 65536
	if err = store.SaveProfile(context.Background(), profile); err != nil {
		t.Fatal(err)
	}
	profiles, err := store.ListProfiles(context.Background())
	if err != nil || len(profiles) != 1 || profiles[0].ContextWindowTokens != 65536 {
		t.Fatalf("profiles=%#v err=%v", profiles, err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	legacyPath := filepath.Join(t.TempDir(), "legacy-profiles.db")
	db, err := sql.Open("sqlite", legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE profiles (
id TEXT PRIMARY KEY, name TEXT NOT NULL, role_description TEXT NOT NULL, system_prompt TEXT NOT NULL,
goals TEXT NOT NULL DEFAULT '[]', rules TEXT NOT NULL DEFAULT '[]', provider TEXT NOT NULL, provider_preset TEXT NOT NULL DEFAULT '', base_url TEXT NOT NULL, model TEXT NOT NULL,
temperature REAL NOT NULL DEFAULT 0.2, max_output_tokens INTEGER NOT NULL DEFAULT 4096, reasoning_effort TEXT NOT NULL DEFAULT 'medium', allowed_tools TEXT NOT NULL,
max_steps INTEGER NOT NULL, max_duration_seconds INTEGER NOT NULL, approval_mode TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
); INSERT INTO profiles VALUES ('legacy','Legacy','','prompt','[]','[]','ollama','ollama','http://127.0.0.1:11434','model',0.2,4096,'medium','[]',10,60,'safe','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	store, err = Open(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	profiles, err = store.ListProfiles(context.Background())
	if err != nil || len(profiles) != 1 || profiles[0].ContextWindowTokens != 32768 {
		t.Fatalf("migrated profiles=%#v err=%v", profiles, err)
	}
}

func TestRunningRunsBecomeInterrupted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	profile := domain.DefaultProfile()
	profile.ID, profile.SystemPrompt = "profile", "original prompt"
	customTool := domain.CustomTool{ID: "customtool_0123456789abcdef01234567", Kind: domain.CustomToolCommand, DisplayName: "Test", Description: "Test tool", Command: "go test ./...", CWD: ".", TimeoutSeconds: 30}
	profile.AllowedTools = append(profile.AllowedTools, customTool.ID)
	snapshot := domain.NewRunConfigurationSnapshot("test-version", profile, []domain.CustomTool{customTool}, time.Now().UTC())
	r := domain.Run{ID: "run", AgentID: "agent", ProfileID: "profile", WorkspaceID: "ws", Task: "task", ConfigurationSnapshot: snapshot, Provider: "ollama", Model: "m", Status: domain.RunRunning, StartedAt: time.Now().UTC(), ToolsUsed: []string{}, ChangedFiles: []string{}}
	r.ContextItems = []domain.RunContextItem{{ID: "context-1", Kind: domain.ContextText, Label: "Требования", Content: "Сохрани этот контекст", Size: 39}}
	if err = s.SaveRun(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	flow := domain.FlowRun{ID: "flow-running", FlowID: "flow", WorkspaceID: "ws", Status: domain.RunRunning, NodeStates: map[string]domain.FlowNodeState{}, StartedAt: r.StartedAt}
	if err = s.SaveFlowRun(context.Background(), flow); err != nil {
		t.Fatal(err)
	}
	execution := domain.ExecutionInstance{ID: "execution-running", WorkspaceID: "ws", ProjectAgentID: "agent", FlowRunID: flow.ID, RunID: r.ID, Task: "task", Status: domain.RunRunning, StartedAt: r.StartedAt}
	if err = s.SaveExecution(context.Background(), execution); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetRun(context.Background(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.RunInterrupted {
		t.Fatalf("status=%s", got.Status)
	}
	gotFlow, err := s.GetFlowRun(context.Background(), flow.ID)
	if err != nil || gotFlow.Status != domain.RunInterrupted || gotFlow.FinishedAt == nil {
		t.Fatalf("flow=%#v err=%v", gotFlow, err)
	}
	executions, err := s.ListExecutions(context.Background(), "ws", 10)
	if err != nil || len(executions) != 1 || executions[0].Status != domain.RunInterrupted || executions[0].FinishedAt == nil {
		t.Fatalf("executions=%#v err=%v", executions, err)
	}
	if len(got.ContextItems) != 1 || got.ContextItems[0].Content != r.ContextItems[0].Content {
		t.Fatalf("run context was not restored: %#v", got.ContextItems)
	}
	if got.ConfigurationSnapshot.ApplicationVersion != "test-version" || got.ConfigurationSnapshot.Profile.SystemPrompt != "original prompt" || len(got.ConfigurationSnapshot.CustomTools) != 1 {
		t.Fatalf("run configuration snapshot was not restored: %#v", got.ConfigurationSnapshot)
	}
}

func TestRunConfigurationSnapshotIsImmutable(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "snapshot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	profile := domain.DefaultProfile()
	profile.SystemPrompt = "captured prompt"
	run := domain.Run{ID: "run", AgentID: "agent", ProfileID: profile.ID, WorkspaceID: "ws", Task: "task", ConfigurationSnapshot: domain.NewRunConfigurationSnapshot("0.4.0", profile, nil, time.Now().UTC()), Provider: string(profile.Provider), Model: profile.Model, Status: domain.RunRunning, StartedAt: time.Now().UTC(), ToolsUsed: []string{}, ChangedFiles: []string{}}
	if err = store.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	run.ConfigurationSnapshot.Profile.SystemPrompt = "tampered after start"
	run.Status = domain.RunCompleted
	if err = store.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ConfigurationSnapshot.Profile.SystemPrompt != "captured prompt" {
		t.Fatalf("snapshot changed after run update: %#v", stored.ConfigurationSnapshot.Profile)
	}
}

func TestMigratesLegacyRunsWithEmptyContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE runs (
id TEXT PRIMARY KEY, agent_id TEXT NOT NULL, profile_id TEXT NOT NULL, workspace_id TEXT NOT NULL,
task TEXT NOT NULL, provider TEXT NOT NULL, model TEXT NOT NULL, status TEXT NOT NULL, step INTEGER NOT NULL,
request_count INTEGER NOT NULL, tools_used TEXT NOT NULL, changed_files TEXT NOT NULL, error TEXT NOT NULL,
result TEXT NOT NULL, started_at TEXT NOT NULL, finished_at TEXT, duration_ms INTEGER NOT NULL
)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO runs VALUES ('legacy','agent','profile','ws','task','ollama','model','completed',1,1,'[]','[]','','done','2026-01-01T00:00:00Z','2026-01-01T00:00:01Z',1000)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	run, err := store.GetRun(context.Background(), "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if run.ContextItems == nil || len(run.ContextItems) != 0 {
		t.Fatalf("legacy run context=%#v, want empty non-nil list", run.ContextItems)
	}
	if run.ConfigurationSnapshot.SchemaVersion != 0 || run.ConfigurationSnapshot.Profile.ID != "profile" || run.ConfigurationSnapshot.Profile.Model != "model" {
		t.Fatalf("legacy run snapshot=%#v", run.ConfigurationSnapshot)
	}
}

func TestMigratesLegacyPatchesWithDefaultSourceTool(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-patches.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE patches (
id TEXT PRIMARY KEY, run_id TEXT NOT NULL, approval_id TEXT NOT NULL, path TEXT NOT NULL,
original_hash TEXT NOT NULL, original_existed INTEGER NOT NULL DEFAULT 1, original TEXT NOT NULL,
proposed TEXT NOT NULL, diff TEXT NOT NULL, status TEXT NOT NULL, created_at TEXT NOT NULL
); INSERT INTO patches VALUES ('patch-legacy','run','approval','main.go','hash',1,'before','after','diff','applied','2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	patch, err := store.GetPatch(context.Background(), "patch-legacy")
	if err != nil || patch.SourceTool != "propose_patch" || patch.Original != "before" || patch.Proposed != "after" {
		t.Fatalf("migrated patch=%#v err=%v", patch, err)
	}
	patch.SourceTool = "run_command"
	if err = store.SavePatch(context.Background(), patch); err != nil {
		t.Fatal(err)
	}
	patch, err = store.GetPatch(context.Background(), patch.ID)
	if err != nil || patch.SourceTool != "run_command" {
		t.Fatalf("updated patch source=%#v err=%v", patch, err)
	}
}

func TestCustomToolPersistence(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "tools.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	tool := domain.CustomTool{
		ID: "customtool_0123456789abcdef01234567", Kind: domain.CustomToolCommand,
		DisplayName: "Проверить API", Description: "Запускает тесты API",
		Command: "go test ./internal/httpapi", ProvidesVerification: true, CWD: ".", TimeoutSeconds: 120,
		CreatedAt: now, UpdatedAt: now,
	}
	if err = ValidateCustomTool(tool); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveCustomTool(context.Background(), tool); err != nil {
		t.Fatal(err)
	}
	tools, err := store.ListCustomTools(context.Background())
	if err != nil || len(tools) != 1 || tools[0].Command != tool.Command || !tools[0].ProvidesVerification {
		t.Fatalf("custom tools=%#v err=%v", tools, err)
	}
	tool.Kind, tool.Command, tool.Program = domain.CustomToolProcess, "", "go"
	tool.Arguments = []string{"test", "{{package}}"}
	tool.Parameters = []domain.CustomToolParameter{{Name: "package", DisplayName: "Package", Description: "Package pattern", Type: domain.CustomToolParameterString, Required: true, MaxLength: 256}}
	tool.UpdatedAt = now.Add(time.Second)
	if err = ValidateCustomTool(tool); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveCustomTool(context.Background(), tool); err != nil {
		t.Fatal(err)
	}
	tools, err = store.ListCustomTools(context.Background())
	if err != nil || len(tools) != 1 || tools[0].Kind != domain.CustomToolProcess || tools[0].Program != "go" || len(tools[0].Arguments) != 2 || len(tools[0].Parameters) != 1 || tools[0].Parameters[0].Name != "package" || !tools[0].ProvidesVerification {
		t.Fatalf("process tool was not restored: %#v err=%v", tools, err)
	}
	if err = store.DeleteCustomTool(context.Background(), tool.ID); err != nil {
		t.Fatal(err)
	}
	tools, err = store.ListCustomTools(context.Background())
	if err != nil || len(tools) != 0 {
		t.Fatalf("custom tool was not deleted: %#v err=%v", tools, err)
	}
}

func TestCustomProcessRejectsCrossPlatformAbsoluteAndDriveRelativePrograms(t *testing.T) {
	base := domain.CustomTool{
		ID: "customtool_0123456789abcdef01234567", Kind: domain.CustomToolProcess,
		DisplayName: "Unsafe program", Description: "Must stay portable and bounded",
		Arguments: []string{"version"}, CWD: ".", TimeoutSeconds: 30,
	}
	for _, program := range []string{`C:\Windows\System32\cmd.exe`, `C:drive-relative.exe`, `\\server\share\tool.exe`, `/usr/bin/env`} {
		tool := base
		tool.Program = program
		if err := ValidateCustomTool(tool); err == nil {
			t.Fatalf("unsafe process program %q was accepted", program)
		}
	}
}

func TestMigratesLegacyCustomToolConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-tools.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE custom_tools (
id TEXT PRIMARY KEY, kind TEXT NOT NULL, display_name TEXT NOT NULL, description TEXT NOT NULL,
command TEXT NOT NULL, cwd TEXT NOT NULL, timeout_seconds INTEGER NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
); INSERT INTO custom_tools VALUES ('customtool_0123456789abcdef01234567','command','Legacy','Legacy command','go test ./...','.',120,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	tools, err := store.ListCustomTools(context.Background())
	if err != nil || len(tools) != 1 || tools[0].Command != "go test ./..." || tools[0].Program != "" {
		t.Fatalf("legacy custom tool=%#v err=%v", tools, err)
	}
}

func TestWorkflowAndImmutableExecutionSnapshotPersistence(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "workflow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	workflow := domain.AgentWorkflow{ID: "workflow_0123456789abcdef01234567", Name: "Review then implement", Description: "Two stages", CreatedAt: now, UpdatedAt: now, Steps: []domain.WorkflowStep{
		{ID: "step_0123456789abcdef01234567", Name: "Review", ProfileID: "reviewer", IncludeOriginalContext: true},
		{ID: "step_1123456789abcdef01234567", Name: "Implement", ProfileID: "developer", IncludePreviousResult: true},
	}}
	if err = ValidateWorkflow(workflow, "reviewer", "developer"); err != nil {
		t.Fatal(err)
	}
	hybrid := workflow
	hybrid.ID = "workflow_2123456789abcdef01234567"
	hybrid.Steps = []domain.WorkflowStep{
		{ID: "step_2123456789abcdef01234567", Name: "Cursor", ProfileID: "developer", Kind: "cursor", Condition: &domain.WorkflowCondition{Type: "always"}, OnFailure: "stop"},
		{ID: "step_3123456789abcdef01234567", Name: "Gate", ProfileID: "", Kind: "manual", OnFailure: "skip"},
	}
	if err = ValidateWorkflow(hybrid, "developer"); err != nil {
		t.Fatal(err)
	}
	bad := hybrid
	bad.Steps[0].Kind = "dag"
	if err = ValidateWorkflow(bad, "developer"); err == nil {
		t.Fatal("expected unsupported kind error")
	}
	if err = store.SaveWorkflow(context.Background(), workflow); err != nil {
		t.Fatal(err)
	}
	definitions, err := store.ListWorkflows(context.Background())
	if err != nil || len(definitions) != 1 || len(definitions[0].Steps) != 2 {
		t.Fatalf("workflows=%#v err=%v", definitions, err)
	}
	run := domain.WorkflowRun{ID: "workflowrun_1", WorkflowID: workflow.ID, WorkspaceID: "ws", Task: "task", ContextItems: []domain.RunContextItem{}, Snapshot: domain.NewWorkflowSnapshot("0.5.0", workflow, now), Status: domain.RunRunning, StepRuns: []domain.WorkflowStepRun{{StepID: workflow.Steps[0].ID, Status: domain.RunRunning}}, StartedAt: now}
	if err = store.SaveWorkflowRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	run.Snapshot.Workflow.Name = "tampered"
	run.Status = domain.RunCompleted
	run.Result = "done"
	if err = store.SaveWorkflowRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetWorkflowRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Snapshot.Workflow.Name != "Review then implement" || stored.Status != domain.RunCompleted || stored.Result != "done" {
		t.Fatalf("stored workflow run=%#v", stored)
	}
	if err = store.DeleteWorkflow(context.Background(), workflow.ID); err != nil {
		t.Fatal(err)
	}
	definitions, err = store.ListWorkflows(context.Background())
	if err != nil || len(definitions) != 0 {
		t.Fatalf("workflow deletion failed: %#v err=%v", definitions, err)
	}
	stored, err = store.GetWorkflowRun(context.Background(), run.ID)
	if err != nil || stored.Snapshot.Workflow.ID != workflow.ID {
		t.Fatalf("workflow history did not survive definition deletion: %#v err=%v", stored, err)
	}
}

func TestListQueriesStayInsideWorkspace(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "worlds.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	first := domain.Run{ID: "run-a", AgentID: "agent", ProfileID: "profile", WorkspaceID: "ws-a", Task: "a", Provider: "ollama", Model: "m", Status: domain.RunCompleted, StartedAt: now, ToolsUsed: []string{}, ChangedFiles: []string{}}
	second := domain.Run{ID: "run-b", AgentID: "agent", ProfileID: "profile", WorkspaceID: "ws-b", Task: "b", Provider: "ollama", Model: "m", Status: domain.RunCompleted, StartedAt: now.Add(time.Second), ToolsUsed: []string{}, ChangedFiles: []string{}}
	if err = store.SaveRun(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveRun(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if err = store.SavePatch(context.Background(), domain.PatchProposal{ID: "patch-a", RunID: first.ID, Path: "a.go", Status: "applied", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err = store.SavePatch(context.Background(), domain.PatchProposal{ID: "patch-b", RunID: second.ID, Path: "b.go", Status: "applied", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveWorkflowRun(context.Background(), domain.WorkflowRun{ID: "wf-a", WorkflowID: "wf", WorkspaceID: "ws-a", Task: "a", Status: domain.RunCompleted, StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveWorkflowRun(context.Background(), domain.WorkflowRun{ID: "wf-b", WorkflowID: "wf", WorkspaceID: "ws-b", Task: "b", Status: domain.RunCompleted, StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ListRunsForWorkspace(context.Background(), "ws-a", 50)
	if err != nil || len(runs) != 1 || runs[0].ID != first.ID {
		t.Fatalf("workspace runs=%#v err=%v", runs, err)
	}
	patches, err := store.ListPatchesForWorkspace(context.Background(), "ws-a", 50)
	if err != nil || len(patches) != 1 || patches[0].ID != "patch-a" {
		t.Fatalf("workspace patches=%#v err=%v", patches, err)
	}
	workflowRuns, err := store.ListWorkflowRunsForWorkspace(context.Background(), "ws-a", 50)
	if err != nil || len(workflowRuns) != 1 || workflowRuns[0].ID != "wf-a" {
		t.Fatalf("workspace workflow runs=%#v err=%v", workflowRuns, err)
	}
	empty, err := store.ListRunsForWorkspace(context.Background(), "", 50)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty workspace id must return no runs: %#v err=%v", empty, err)
	}
}

func TestPausedRunAfterRestartIsInterruptedWithProgressPreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "paused.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	run := domain.Run{ID: "paused", WorkspaceID: "ws", Status: domain.RunPaused, Task: "agreed task", Result: "saved partial result", StartedAt: time.Now().UTC(), ChangedFiles: []string{"partial.go"}}
	if err = store.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.RunInterrupted || got.FinishedAt == nil || got.Result != run.Result || len(got.ChangedFiles) != 1 {
		t.Fatalf("restart lost or misrepresented progress: %+v", got)
	}
}

func TestPausedRunWithCheckpointSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "paused-checkpoint.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	run := domain.Run{ID: "paused-ok", WorkspaceID: "ws", Status: domain.RunPaused, Task: "continue me", StartedAt: time.Now().UTC()}
	if err = store.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveRunCheckpoint(context.Background(), domain.RunCheckpoint{
		RunID: run.ID, Seq: 1, CreatedAt: time.Now().UTC(), NextStep: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.RunPaused || got.FinishedAt != nil {
		t.Fatalf("resumable paused run was interrupted: %+v", got)
	}
}
