package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/storage"
)

func TestLocalizeLegacyDefaultProfile(t *testing.T) {
	legacy := domain.AgentProfile{
		ID:              "default",
		Name:            "Local coding agent",
		RoleDescription: "Careful repository assistant",
		SystemPrompt:    "You are a careful coding agent. Inspect the workspace, make the smallest correct change, and verify it. Explain every tool call. Never claim a change before it is accepted.",
		Model:           "custom-model",
	}
	if !localizeLegacyDefaultProfile(&legacy) {
		t.Fatal("expected untouched legacy profile to be localized")
	}
	if legacy.Name != "Локальный агент" || legacy.RoleDescription == "Careful repository assistant" {
		t.Fatalf("profile was not localized: %#v", legacy)
	}
	if legacy.Model != "custom-model" {
		t.Fatal("localization must preserve provider settings")
	}

	customized := legacy
	customized.Name = "Мой агент"
	if localizeLegacyDefaultProfile(&customized) {
		t.Fatal("customized profile must not be overwritten")
	}
}

func TestProbeProviderReturnsModelsWithoutPersistingKey(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer transient-key" {
			t.Errorf("authorization=%q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "coder-model", "owned_by": "test"}}})
	}))
	defer provider.Close()
	dataDir := t.TempDir()
	application, err := New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	result, err := application.ProbeProvider(ProviderProbeRequest{Provider: domain.ProviderOpenAI, BaseURL: provider.URL, APIKey: "transient-key"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Connected || len(result.Models) != 1 || result.Models[0].ID != "coder-model" {
		t.Fatalf("probe=%#v", result)
	}
	databaseFiles, err := filepath.Glob(filepath.Join(dataDir, "workbench.db*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, databaseFile := range databaseFiles {
		databaseBytes, readErr := os.ReadFile(databaseFile)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.Contains(string(databaseBytes), "transient-key") {
			t.Fatalf("provider API key was persisted in %s", filepath.Base(databaseFile))
		}
	}
	if _, err = application.ProbeProvider(ProviderProbeRequest{Provider: domain.ProviderOpenAI, BaseURL: "https://user:secret@example.com/v1"}); err == nil {
		t.Fatal("provider URL credentials were accepted")
	}
}

func TestBootstrapIncludesAgentStudioCatalog(t *testing.T) {
	application := newTestApp(t)

	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if len(boot.ProfileTemplates) < 4 {
		t.Fatalf("profile templates=%d, want at least 4", len(boot.ProfileTemplates))
	}
	if len(boot.ToolCatalog) < 19 {
		t.Fatalf("tool catalog=%d, want at least 19", len(boot.ToolCatalog))
	}
	foundSSHRead := false
	for _, item := range boot.ToolCatalog {
		if item.Name == "ssh_read_remote" {
			foundSSHRead = true
			break
		}
	}
	if !foundSSHRead {
		t.Fatal("tool catalog is missing ssh_read_remote")
	}
	if len(boot.CustomToolTemplates) < 4 {
		t.Fatalf("custom tool templates=%d, want at least 4", len(boot.CustomToolTemplates))
	}
	for _, template := range boot.ProfileTemplates {
		if template.ID == "" || template.Name == "" || template.SystemPrompt == "" || len(template.AllowedTools) == 0 {
			t.Fatalf("incomplete profile template: %#v", template)
		}
		if err = storage.ValidateProfile(domain.AgentProfile{
			ID: template.ID, Name: template.Name, RoleDescription: template.RoleDescription,
			SystemPrompt: template.SystemPrompt, Provider: domain.ProviderOllama,
			BaseURL: "http://127.0.0.1:11434", Model: "test-model",
			AllowedTools: template.AllowedTools, MaxSteps: template.MaxSteps,
			MaxDurationSeconds: template.MaxDurationSeconds, ApprovalMode: template.ApprovalMode,
		}); err != nil {
			t.Fatalf("template %q is not a valid profile: %v", template.ID, err)
		}
	}
	for _, template := range boot.CustomToolTemplates {
		tool := template.Tool
		tool.ID = "customtool_0123456789abcdef01234567"
		if err = storage.ValidateCustomTool(tool); err != nil {
			t.Fatalf("custom tool template %q is invalid: %v", template.ID, err)
		}
	}
}

func TestProfileLifecycleFromTemplate(t *testing.T) {
	application := newTestApp(t)

	template := domain.BuiltInAgentTemplates()[1]
	created, err := application.SaveProfile(domain.AgentProfile{
		Name: template.Name + " API", RoleDescription: template.RoleDescription,
		SystemPrompt: template.SystemPrompt, Provider: domain.ProviderOllama,
		BaseURL: "http://127.0.0.1:11434", Model: "qwen-test",
		AllowedTools: template.AllowedTools, MaxSteps: template.MaxSteps,
		MaxDurationSeconds: template.MaxDurationSeconds, ApprovalMode: template.ApprovalMode,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.ID == "default" {
		t.Fatalf("unexpected generated profile ID %q", created.ID)
	}

	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, profile := range boot.Profiles {
		if profile.ID == created.ID {
			found = true
			if fmt.Sprint(profile.AllowedTools) != fmt.Sprint(template.AllowedTools) {
				t.Fatalf("allowed tools changed: %v", profile.AllowedTools)
			}
		}
	}
	if !found {
		t.Fatal("created profile is absent from bootstrap")
	}
	if err = application.DeleteProfile(created.ID); err != nil {
		t.Fatal(err)
	}
	boot, err = application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range boot.Profiles {
		if profile.ID == created.ID {
			t.Fatal("deleted profile is still present")
		}
	}
}

func TestSequentialWorkflowExecutionAndHandoff(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	workspaceRoot := t.TempDir()
	var mu sync.Mutex
	requestNumber := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode provider request: %v", err)
		}
		mu.Lock()
		requestNumber++
		current := requestNumber
		mu.Unlock()
		var combined strings.Builder
		for _, message := range body.Messages {
			combined.WriteString(message.Content)
			combined.WriteByte('\n')
		}
		answer := "analysis output"
		if current == 2 {
			if !strings.Contains(combined.String(), "analysis output") || !strings.Contains(combined.String(), "Результат предыдущего этапа") {
				t.Errorf("second stage did not receive the first result: %s", combined.String())
			}
			answer = "final workflow output"
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "content": answer}, "done": true})
	}))
	defer provider.Close()
	workflowBackend := &recordingSandboxBackend{Manager: &sandbox.Manager{Root: t.TempDir()}}
	application, err := New(t.TempDir(), WithSandboxBackend(workflowBackend))
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(workspaceRoot); err != nil {
		t.Fatal(err)
	}
	analyst := domain.DefaultProfile()
	analyst.ID, analyst.Name, analyst.BaseURL, analyst.Model = "workflow-analyst", "Аналитик", provider.URL, "scripted"
	analyst.AllowedTools = nil
	developer := analyst
	developer.ID, developer.Name = "workflow-developer", "Разработчик"
	if _, err = application.SaveProfile(analyst); err != nil {
		t.Fatal(err)
	}
	if _, err = application.SaveProfile(developer); err != nil {
		t.Fatal(err)
	}
	workflow, err := application.SaveWorkflow(domain.AgentWorkflow{Name: "Анализ и реализация", Description: "Два последовательных агента", Steps: []domain.WorkflowStep{
		{Name: "Анализ", ProfileID: analyst.ID, Instruction: "Проанализируй задачу", IncludeOriginalContext: true},
		{Name: "Реализация", ProfileID: developer.ID, Instruction: "Подготовь финальный план", IncludePreviousResult: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	workflowRun, err := application.StartWorkflow(StartWorkflowRequest{WorkflowID: workflow.ID, Task: "Добавить безопасный endpoint", ContextItems: []domain.RunContextInput{{Kind: domain.ContextText, Label: "Ограничение", Content: "Не ломать API"}}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		workflowRun, err = application.WorkflowRunDetails(workflowRun.ID)
		if err != nil {
			t.Fatal(err)
		}
		if workflowRun.Status == domain.RunCompleted {
			break
		}
		if workflowRun.Status == domain.RunFailed {
			t.Fatalf("workflow failed: %s", workflowRun.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if workflowRun.Status != domain.RunCompleted || workflowRun.Result != "final workflow output" || len(workflowRun.StepRuns) != 2 || workflowRun.StepRuns[0].RunID == "" || workflowRun.StepRuns[1].RunID == "" {
		t.Fatalf("workflow run=%#v", workflowRun)
	}
	workflowBackend.mu.Lock()
	if len(workflowBackend.createRequests) != 1 || len(workflowBackend.records) != 1 {
		workflowBackend.mu.Unlock()
		t.Fatalf("legacy workflow sandbox creates=%d records=%d", len(workflowBackend.createRequests), len(workflowBackend.records))
	}
	workflowSandboxPath := workflowBackend.records[0].Path
	workflowSourcePath := workflowBackend.createRequests[0].WorkspacePath
	workflowBackend.mu.Unlock()
	if filepath.Clean(workflowSourcePath) != filepath.Clean(workspaceRoot) || filepath.Clean(workflowSandboxPath) == filepath.Clean(workspaceRoot) {
		t.Fatalf("legacy workflow source=%q sandbox=%q live=%q", workflowSourcePath, workflowSandboxPath, workspaceRoot)
	}
	if info, statErr := os.Stat(workflowSandboxPath); statErr != nil || !info.IsDir() {
		t.Fatalf("legacy workflow audit sandbox unavailable: %q err=%v", workflowSandboxPath, statErr)
	}
	if workflowRun.Snapshot.SchemaVersion != 1 || workflowRun.Snapshot.Workflow.Name != workflow.Name || len(workflowRun.ContextItems) != 1 {
		t.Fatalf("workflow snapshot/context=%#v", workflowRun)
	}
	if err = application.DeleteProfile(analyst.ID); err == nil {
		t.Fatal("profile referenced by workflow was deleted")
	}
	if err = application.DeleteWorkflow(workflow.ID); err != nil {
		t.Fatal(err)
	}
	workflowRun, err = application.WorkflowRunDetails(workflowRun.ID)
	if err != nil || workflowRun.Snapshot.Workflow.ID != workflow.ID {
		t.Fatalf("workflow history lost after definition deletion: %#v err=%v", workflowRun, err)
	}
}

func TestEndToEndPatchCommandAndHistory(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	workspaceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "go.mod"), []byte("module fixture.local/health\n\ngo 1.24.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	original := "package health\n\nfunc Status() string { return \"starting\" }\n"
	if err := os.WriteFile(filepath.Join(workspaceRoot, "health.go"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "health_test.go"), []byte("package health\n\nimport \"testing\"\n\nfunc TestStatus(t *testing.T) { if Status() != \"ok\" { t.Fatal(Status()) } }\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	requestNumber := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestNumber++
		current := requestNumber
		mu.Unlock()
		w.Header().Set("Content-Type", "application/x-ndjson")
		switch current {
		case 1:
			arguments := map[string]any{"maxDepth": 3}
			_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"function": map[string]any{"name": "list_files", "arguments": arguments}}}}, "done": true})
		case 2:
			arguments := map[string]any{"path": "health.go"}
			_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"function": map[string]any{"name": "read_file", "arguments": arguments}}}}, "done": true})
		case 3:
			arguments := map[string]any{"path": "health.go", "content": "package health\n\nfunc Status() string { return \"ok\" }\n", "reason": "make the health status ready"}
			_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"function": map[string]any{"name": "propose_patch", "arguments": arguments}}}}, "done": true})
		case 4:
			arguments := map[string]any{"command": "go test ./...", "cwd": ".", "reason": "verify the health behavior", "timeoutSeconds": 30}
			_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"function": map[string]any{"name": "run_command", "arguments": arguments}}}}, "done": true})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "content": "Health status updated and tests pass."}, "done": true})
		}
	}))
	defer provider.Close()

	dataDir := t.TempDir()
	application, err := New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.OpenWorkspace(workspaceRoot); err != nil {
		t.Fatal(err)
	}
	profile := domain.DefaultProfile()
	profile.ID = "e2e"
	profile.Name = "E2E agent"
	profile.BaseURL = provider.URL
	profile.Model = "scripted"
	profile.MaxDurationSeconds = 30
	if _, err = application.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	run, err := application.StartRun(StartRunRequest{ProfileID: profile.ID, Task: "Make Status return ok and run its test"})
	if err != nil {
		t.Fatal(err)
	}

	resolved := map[string]bool{}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		details, detailsErr := application.RunDetails(run.ID)
		if detailsErr != nil {
			t.Fatal(detailsErr)
		}
		for _, approval := range details.Approvals {
			if approval.Status == domain.ApprovalPending && !resolved[approval.ID] {
				if err = application.ResolveApproval(approval.ID, true); err != nil {
					t.Fatal(err)
				}
				resolved[approval.ID] = true
			}
		}
		if details.Run.Status == domain.RunCompleted {
			if len(resolved) != 2 {
				t.Fatalf("resolved %d approvals, want 2", len(resolved))
			}
			if details.Run.RequestCount != 5 {
				t.Fatalf("model requests=%d", details.Run.RequestCount)
			}
			if fmt.Sprint(details.Run.ToolsUsed) != "[list_files read_file propose_patch run_command]" {
				t.Fatalf("tools=%v", details.Run.ToolsUsed)
			}
			break
		}
		if details.Run.Status == domain.RunFailed {
			t.Fatalf("run failed: %s", details.Run.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	details, err := application.RunDetails(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if details.Run.Status != domain.RunCompleted {
		t.Fatalf("run status=%s", details.Run.Status)
	}
	if len(details.Patches) != 1 || details.Patches[0].Status != "applied" {
		t.Fatalf("patches=%#v", details.Patches)
	}
	commandRecorded := false
	for _, event := range details.Events {
		if event.Type == domain.EventToolFinished && strings.Contains(string(event.Data), `"tool":"run_command"`) && strings.Contains(string(event.Data), `"exitCode":0`) {
			commandRecorded = true
			break
		}
	}
	if !commandRecorded {
		t.Fatal("successful command exit code was not retained in the event history")
	}
	if err = waitAndApplyPendingChangeSets(t, application); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(workspaceRoot, "health.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "package health\n\nfunc Status() string { return \"ok\" }\n" {
		t.Fatalf("file=%q", data)
	}
	reverted, err := application.RevertPatch(details.Patches[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if reverted.Status != "reverted" {
		t.Fatalf("reverted patch=%#v", reverted)
	}
	data, err = os.ReadFile(filepath.Join(workspaceRoot, "health.go"))
	if err != nil || string(data) != original {
		t.Fatalf("rollback file=%q err=%v", data, err)
	}
	application.Shutdown(context.Background())

	reopened, err := New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Shutdown(context.Background())
	if _, err = reopened.OpenWorkspace(workspaceRoot); err != nil {
		t.Fatal(err)
	}
	runs, err := reopened.Runs()
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ID != run.ID || runs[0].Status != domain.RunCompleted {
		t.Fatalf("restored runs=%#v", runs)
	}
	restored, err := reopened.RunDetails(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Events) == 0 || len(restored.Approvals) != 2 || len(restored.Patches) != 1 || restored.Patches[0].Status != "reverted" {
		t.Fatalf("incomplete restored history: events=%d approvals=%d patches=%d", len(restored.Events), len(restored.Approvals), len(restored.Patches))
	}
}

func waitAndApplyPendingChangeSets(t *testing.T, application *App) error {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		boot, err := application.Bootstrap()
		if err != nil {
			return err
		}
		pending := 0
		for _, set := range boot.ChangeSets {
			if set.Status == domain.ChangeSetPending || set.Status == domain.ChangeSetApproved {
				pending++
				if _, err = application.ApplyChangeSet(set.ID); err != nil {
					return err
				}
			}
		}
		if pending > 0 {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	// No pending change set yet — sandbox may match workspace (no file delta) or journal still flushing.
	boot, err := application.Bootstrap()
	if err != nil {
		return err
	}
	for _, set := range boot.ChangeSets {
		if set.Status == domain.ChangeSetPending || set.Status == domain.ChangeSetApproved {
			_, err = application.ApplyChangeSet(set.ID)
			return err
		}
	}
	return nil
}

// Несостоявшаяся связь — это состояние, а не сбой запроса.
//
// Раньше проверка возвращала ошибку, и человек видел красный
// «provider connection failed: dial tcp …» — сообщение про сокет, из которого
// не следует ни одного действия. Не запущен Ollama, не тот адрес, отклонён
// ключ — всё выглядело одинаково.
func TestProbeProviderExplainsFailureInsteadOfErroring(t *testing.T) {
	application := newTestApp(t)

	// Порт, на котором заведомо никто не слушает.
	result, err := application.ProbeProvider(ProviderProbeRequest{
		Provider: domain.ProviderOllama, BaseURL: "http://127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("недоступный провайдер обязан возвращать состояние, а не ошибку: %v", err)
	}
	if result.Connected {
		t.Fatal("связи нет, но результат утверждает обратное")
	}
	if strings.TrimSpace(result.Problem) == "" {
		t.Fatal("причина неудачи не названа")
	}
	if strings.TrimSpace(result.Fix) == "" {
		t.Fatal("не сказано, что делать дальше")
	}
	if !strings.Contains(result.Fix, "ollama") && !strings.Contains(result.Fix, "Ollama") {
		t.Fatalf("для локального провайдера подсказка обязана называть Ollama: %q", result.Fix)
	}
	if result.Models == nil {
		t.Fatal("Models обязан быть пустым списком, а не nil: клиент рендерит его напрямую")
	}
	// Ключ в адресе — по-прежнему ошибка запроса: это не связь, а недопустимый ввод.
	if _, err = application.ProbeProvider(ProviderProbeRequest{
		Provider: domain.ProviderOpenAI, BaseURL: "https://user:secret@example.com/v1",
	}); err == nil {
		t.Fatal("учётные данные в адресе обязаны отклоняться")
	}
}
