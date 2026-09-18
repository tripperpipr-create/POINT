package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestRunControlPlaneRejectsEveryCrossWorkspaceMutationAndInspection(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	first, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run := domain.Run{ID: "run-world-a", WorkspaceID: first.Workspace.ID, Status: domain.RunRunning, StartedAt: time.Now().UTC()}
	if err = application.store.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	approval := domain.Approval{ID: "approval-world-a", RunID: run.ID, Status: domain.ApprovalPending, CreatedAt: time.Now().UTC()}
	if err = application.store.SaveApproval(context.Background(), approval); err != nil {
		t.Fatal(err)
	}
	if _, err = application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name string
		call func() error
	}{
		{"approval", func() error { return application.ResolveApproval(approval.ID, true) }},
		{"message", func() error { return application.InjectRunMessage(run.ID, "continue", "") }},
		{"forbid-file", func() error { return application.ForbidRunFile(run.ID, "main.go") }},
		{"amend-context", func() error { return application.AmendRunContext(run.ID, domain.ContextAmendRemove, "item") }},
		{"inspect-context", func() error { _, inspectErr := application.RunContextInspector(run.ID); return inspectErr }},
	}
	for _, check := range checks {
		if callErr := check.call(); callErr == nil {
			t.Fatalf("%s crossed workspace boundary: %v", check.name, callErr)
		}
	}
}

func TestSaveFlowRejectsActiveRun(t *testing.T) {
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	ctx := context.Background()
	flow := domain.FlowGraph{
		ID: "flow-active", WorkspaceID: "ws-1", Name: "Active", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := application.store.SaveFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	run := domain.FlowRun{
		ID: "flowrun-1", FlowID: flow.ID, WorkspaceID: flow.WorkspaceID, Status: domain.RunRunning,
		NodeStates: map[string]domain.FlowNodeState{}, StartedAt: time.Now().UTC(),
	}
	if err := application.store.SaveFlowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	flow.Name = "Changed"
	if _, err = application.SaveFlow(flow); err == nil {
		t.Fatal("expected save to fail while flow run is active")
	}
}

func TestStatisticsIncludesBudgetWarnings(t *testing.T) {
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := application.store.SaveSetting(ctx, "hub.budgetDailyCents", "100"); err != nil {
		t.Fatal(err)
	}
	cost := int64(85)
	if err := application.store.InsertUsageRecord(ctx, domain.UsageRecord{
		ID: "usage-1", WorkspaceID: view.Workspace.ID, Provider: "test", Model: "test", TotalTokens: 10,
		CostCents: &cost, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	stats, err := application.Statistics(view.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stats["budgetDailyCents"] != int64(100) {
		t.Fatalf("budgetDailyCents=%v", stats["budgetDailyCents"])
	}
	if stats["dailyBudgetWarning"] == nil {
		t.Fatal("expected daily budget warning at 85%")
	}
}

func TestSaveHubBudgetIsProjectScopedAndValidated(t *testing.T) {
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	first, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := application.SaveHubBudget(HubBudgetSettings{DailyCents: 250, MonthlyCents: 5000, HardStop: true})
	if err != nil {
		t.Fatal(err)
	}
	if saved.WorkspaceID != first.Workspace.ID || saved.DailyCents != 250 || !saved.HardStop {
		t.Fatalf("saved budget=%#v", saved)
	}

	second, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	secondStats, err := application.Statistics(second.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if secondStats["budgetDailyCents"] != nil || secondStats["budgetHardStop"] != nil {
		t.Fatalf("budget leaked into another project: %#v", secondStats)
	}
	if _, err = application.Statistics(first.Workspace.ID); err == nil {
		t.Fatal("expected foreign-world statistics to fail")
	}
	if _, err = application.OpenWorkspace(first.Workspace.Path); err != nil {
		t.Fatal(err)
	}
	firstStats, err := application.Statistics(first.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if firstStats["budgetDailyCents"] != int64(250) || firstStats["budgetMonthlyCents"] != int64(5000) || firstStats["budgetHardStop"] != true {
		t.Fatalf("first project budget=%#v", firstStats)
	}
	if _, err = application.OpenWorkspace(second.Workspace.Path); err != nil {
		t.Fatal(err)
	}
	if _, err = application.SaveHubBudget(HubBudgetSettings{WorkspaceID: first.Workspace.ID, DailyCents: 1}); err == nil {
		t.Fatal("expected cross-project budget update to fail")
	}
	if _, err = application.SaveHubBudget(HubBudgetSettings{DailyCents: -1}); err == nil {
		t.Fatal("expected negative budget to fail")
	}
}

func TestEnforceHubBudgetBlocksStart(t *testing.T) {
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	ctx := context.Background()
	if err := application.store.SaveSetting(ctx, "hub.budgetDailyCents", "50"); err != nil {
		t.Fatal(err)
	}
	if err := application.store.SaveSetting(ctx, "hub.budgetHardStop", "true"); err != nil {
		t.Fatal(err)
	}
	cost := int64(60)
	if err := application.store.InsertUsageRecord(ctx, domain.UsageRecord{
		ID: "usage-over", WorkspaceID: "ws-over", Provider: "test", Model: "test", TotalTokens: 10,
		CostCents: &cost, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := application.enforceHubBudget("ws-over"); err == nil {
		t.Fatal("expected hard budget enforcement to fail")
	}
}

func TestQuestBudgetUsesPersistedExecutionLink(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projectAgent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Budgeted", SystemPrompt: "Inspect the project", Provider: domain.ProviderOpenAI,
		ProviderPreset: "openai", BaseURL: "https://api.openai.com/v1",
		PrimaryModel: "test", MaxOutputTokens: 128, ContextWindowTokens: 4096,
		MaxSteps: 2, MaxDurationSeconds: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	quest, err := application.SaveQuest(domain.Quest{
		Title: "Small budget", Status: domain.QuestActive, Importance: domain.QuestNormal, BudgetTokens: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	execution := domain.ExecutionInstance{
		ID: "execution-budget", WorkspaceID: view.Workspace.ID, ProjectAgentID: projectAgent.ID,
		QuestID: quest.ID, Task: "Inspect the repository", Status: domain.RunPending,
	}
	if err = application.store.SaveExecution(context.Background(), execution); err != nil {
		t.Fatal(err)
	}

	_, err = application.StartRun(StartRunRequest{
		ProfileID: projectAgent.ID, Task: execution.Task, ExecutionID: execution.ID,
	})
	if err == nil || !strings.Contains(err.Error(), "quest token budget") {
		t.Fatalf("expected persisted quest budget to block launch, got %v", err)
	}
}

// Потолок в токенах защищает кошелёк, а у бесплатного рантайма защищать нечего:
// там от бесконечной работы стоят предел ходов и активное время. Один и тот же
// квест с потолком в один токен обязан пускать бесплатный запуск и не пускать
// платный — иначе бесплатная модель стоит без дела при нулевой цене хода.
func TestFreeRuntimeIgnoresQuestTokenCeiling(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projectAgent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Free", SystemPrompt: "Inspect the project", Provider: domain.ProviderOllama,
		ProviderPreset: "ollama", BaseURL: "http://127.0.0.1:9",
		PrimaryModel: "test", MaxOutputTokens: 128, ContextWindowTokens: 4096,
		MaxSteps: 2, MaxDurationSeconds: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	quest, err := application.SaveQuest(domain.Quest{
		Title: "Small budget", Status: domain.QuestActive, Importance: domain.QuestNormal, BudgetTokens: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	execution := domain.ExecutionInstance{
		ID: "execution-free-budget", WorkspaceID: view.Workspace.ID, ProjectAgentID: projectAgent.ID,
		QuestID: quest.ID, Task: "Inspect the repository", Status: domain.RunPending,
	}
	if err = application.store.SaveExecution(context.Background(), execution); err != nil {
		t.Fatal(err)
	}

	run, err := application.StartRun(StartRunRequest{
		ProfileID: projectAgent.ID, Task: execution.Task, ExecutionID: execution.ID,
	})
	if err != nil {
		t.Fatalf("free runtime blocked by quest token ceiling: %v", err)
	}
	if run.ID == "" {
		t.Fatal("expected a started run")
	}
}

func TestUsageRecordCorrelatesAgentQuestAndModelLatency(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	profile := domain.AgentProfile{ID: "project-agent-usage", Name: "Usage", Provider: domain.ProviderOllama, Model: "latency-model"}
	run := domain.Run{
		ID: "run-usage", AgentID: "runtime-agent", ProfileID: profile.ID, WorkspaceID: view.Workspace.ID,
		Provider: string(profile.Provider), Model: profile.Model, Status: domain.RunRunning, StartedAt: now,
		ConfigurationSnapshot: domain.NewRunConfigurationSnapshot(Version, profile, nil, now),
	}
	if err = application.store.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	requestedAt := now.Add(time.Second)
	if err = application.store.Append(context.Background(), domain.Event{
		ID: "event-requested", RunID: run.ID, AgentID: run.AgentID, ExecutionID: "execution-usage", QuestID: "quest-usage",
		Type: domain.EventModelRequested, Step: 2, Actor: "agent", Data: json.RawMessage(`{}`), CreatedAt: requestedAt,
	}); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{"usage": map[string]any{"inputTokens": 30, "outputTokens": 12}})
	if err != nil {
		t.Fatal(err)
	}
	application.recordUsageFromEvent(domain.Event{
		ID: "event-usage", RunID: run.ID, AgentID: run.AgentID, ExecutionID: "execution-usage", QuestID: "quest-usage",
		Type: domain.EventModelUsage, Step: 2, Actor: "model", Data: payload, CreatedAt: requestedAt.Add(1500 * time.Millisecond),
	})

	records, err := application.store.ListUsageRecords(context.Background(), view.Workspace.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("usage records=%d, want 1", len(records))
	}
	record := records[0]
	if record.ExecutionID != "execution-usage" || record.QuestID != "quest-usage" || record.ProjectAgentID != profile.ID {
		t.Fatalf("usage correlation=%#v", record)
	}
	if record.Provider != string(profile.Provider) || record.Model != profile.Model || record.TotalTokens != 42 || record.LatencyMs != 1500 || record.Outcome != "usage_reported" {
		t.Fatalf("usage telemetry=%#v", record)
	}
}

func TestDirectRunCreatesAndCompletesFirstClassQuest(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{"role": "assistant", "content": "Audit complete."}, "done": true,
		})
	}))
	defer provider.Close()

	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projectAgent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Auditor", SystemPrompt: "Return a concise audit.", Provider: domain.ProviderOllama,
		BaseURL: provider.URL, PrimaryModel: "scripted", MaxOutputTokens: 128, ContextWindowTokens: 4096,
		MaxSteps: 2, MaxDurationSeconds: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := application.StartRun(StartRunRequest{
		ProfileID: projectAgent.ID, Task: "Summarize the module\nUse the current workspace.",
		Goal: "Describe the current state",
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		details, detailsErr := application.RunDetails(run.ID)
		if detailsErr != nil {
			t.Fatal(detailsErr)
		}
		if details.Run.Status == domain.RunCompleted {
			break
		}
		if details.Run.Status == domain.RunFailed {
			t.Fatalf("run failed: %s", details.Run.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}

	executions, err := application.store.ListExecutions(context.Background(), view.Workspace.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	var questID string
	for _, execution := range executions {
		if execution.RunID == run.ID {
			questID = execution.QuestID
			if execution.ProjectAgentID != projectAgent.ID || execution.Status != domain.RunCompleted {
				t.Fatalf("execution correlation=%#v", execution)
			}
		}
	}
	if questID == "" {
		t.Fatalf("run %s has no first-class quest: %#v", run.ID, executions)
	}
	quests, err := application.store.ListQuests(context.Background(), view.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, quest := range quests {
		if quest.ID == questID {
			if quest.Title != "Summarize the module" || quest.Status != domain.QuestCompleted || len(quest.Objectives) != 1 || quest.FinishedAt == nil {
				t.Fatalf("direct quest=%#v", quest)
			}
			return
		}
	}
	t.Fatalf("quest %s not found: %#v", questID, quests)
}

// Обещание бюджета проверяется на настоящем входе, а не на внутреннем правиле.
//
// Интерфейс обещает: «При достижении известного расхода ядро не запустит
// следующий квест». Соседний тест зовёт enforceHubBudget напрямую и доказывает
// только то, что правило считает верно; проводку он не проверяет. Убери вызов
// из StartRun — тот тест останется зелёным, а квесты пойдут сверх лимита.
func TestStartRunRefusesWhenHardBudgetSpent(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	world := openTestWorld(t, application)
	ctx := context.Background()

	profile := domain.DefaultProfile()
	profile.ID, profile.Model = "budget-test", "test-model"
	if _, err = application.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}

	start := func() error {
		_, runErr := application.StartRun(StartRunRequest{ProfileID: profile.ID, Task: "Починить сборку"})
		return runErr
	}

	// Проверка на холостой ход: пока лимита нет, запуск обязан доходить дальше
	// бюджета. Иначе отказ ниже ничего не доказывает — он мог бы прийти от
	// любой другой причины.
	if err := start(); err != nil && strings.Contains(err.Error(), "budget") {
		t.Fatalf("до включения лимита запуск уже отклонён бюджетом: %v", err)
	}

	if err := application.store.SaveSetting(ctx, "hub.budgetDailyCents", "50"); err != nil {
		t.Fatal(err)
	}
	if err := application.store.SaveSetting(ctx, "hub.budgetHardStop", "true"); err != nil {
		t.Fatal(err)
	}
	cost := int64(60)
	if err := application.store.InsertUsageRecord(ctx, domain.UsageRecord{
		ID: "usage-spent", WorkspaceID: world.ID, Provider: "test", Model: "test-model",
		TotalTokens: 10, CostCents: &cost, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	err = start()
	if err == nil {
		t.Fatal("прогон запустился при исчерпанном дневном лимите — обещание «не запустит следующий квест» не выполняется")
	}
	if !strings.Contains(err.Error(), "budget") {
		t.Fatalf("запуск отклонён, но не бюджетом: %v", err)
	}
}

// Месячный лимит — вторая половина того же обещания, и она не проверялась
// вовсе: отключение месячной ветки не роняло ни один тест бюджета.
func TestStartRunRefusesWhenMonthlyBudgetSpent(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	world := openTestWorld(t, application)
	ctx := context.Background()

	profile := domain.DefaultProfile()
	profile.ID, profile.Model = "budget-month", "test-model"
	if _, err = application.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	start := func() error {
		_, runErr := application.StartRun(StartRunRequest{ProfileID: profile.ID, Task: "Починить сборку"})
		return runErr
	}
	if err := start(); err != nil && strings.Contains(err.Error(), "budget") {
		t.Fatalf("до включения лимита запуск уже отклонён бюджетом: %v", err)
	}

	// Дневной лимит выключен намеренно: отказ обязан прийти именно от месячного.
	if err := application.store.SaveSetting(ctx, "hub.budgetDailyCents", "0"); err != nil {
		t.Fatal(err)
	}
	if err := application.store.SaveSetting(ctx, "hub.budgetMonthlyCents", "100"); err != nil {
		t.Fatal(err)
	}
	if err := application.store.SaveSetting(ctx, "hub.budgetHardStop", "true"); err != nil {
		t.Fatal(err)
	}
	cost := int64(150)
	if err := application.store.InsertUsageRecord(ctx, domain.UsageRecord{
		ID: "usage-month", WorkspaceID: world.ID, Provider: "test", Model: "test-model",
		TotalTokens: 10, CostCents: &cost, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	err = start()
	if err == nil {
		t.Fatal("прогон запустился при исчерпанном месячном лимите")
	}
	if !strings.Contains(err.Error(), "monthly") {
		t.Fatalf("отказ пришёл не от месячного лимита: %v", err)
	}
}
