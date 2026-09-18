package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
)

func outcomeWorld(t *testing.T) (*App, domain.Workspace) {
	t.Helper()
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	return application, openTestWorld(t, application)
}

// Квест закрывался, а определение готовности оставалось словами. Сверка обязана
// опираться на записанные факты, а не на статус квеста.
func TestQuestOutcomeCountsOnlyEvidencedPromises(t *testing.T) {
	application, world := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()

	quest := domain.Quest{
		ID: "q-1", WorkspaceID: world.ID, Title: "Починить биллинг",
		Status: domain.QuestStatus("completed"), CreatedAt: now, UpdatedAt: now,
		DefinitionOfDone: []string{
			"Изменения приняты",
			"Проверка завершилась успешно после последней правки",
			"Команда согласовала подход",
		},
	}
	if err := application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	// Набор применён — первое обещание подтверждено. Проверки нет, третий пункт
	// автоматически не проверяется.
	if err := application.store.SaveChangeSet(ctx, domain.ChangeSet{
		ID: "cs-1", WorkspaceID: world.ID, QuestID: quest.ID, Title: "Правка",
		Status: domain.ChangeSetApplied, CreatedAt: now,
		Items: []domain.ChangeItem{{ID: "i1", Path: "billing.go", Kind: "modify"}},
	}); err != nil {
		t.Fatal(err)
	}

	outcome, err := application.QuestOutcome(ctx, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Total != 3 {
		t.Fatalf("ожидалось 3 обещания, получено %d", outcome.Total)
	}
	if outcome.Met != 1 {
		t.Fatalf("подтверждено должно быть ровно одно обещание, получено %d: %+v", outcome.Met, outcome.Promises)
	}
	if outcome.Verified {
		t.Fatal("успешной проверки не было — квест не может считаться подтверждённым")
	}
	if len(outcome.AppliedFiles) != 1 || outcome.AppliedFiles[0] != "billing.go" {
		t.Fatalf("применённые файлы не собраны: %+v", outcome.AppliedFiles)
	}

	byText := map[string]Promise{}
	for _, promise := range outcome.Promises {
		byText[promise.Text] = promise
	}
	if !byText["Изменения приняты"].Met {
		t.Fatal("применённый набор обязан подтверждать пункт про изменения")
	}
	if byText["Проверка завершилась успешно после последней правки"].Met {
		t.Fatal("проверки не было — пункт не может считаться выполненным")
	}
	if !strings.Contains(byText["Проверка завершилась успешно после последней правки"].Evidence, "нет") {
		t.Fatalf("недостающее доказательство не названо: %+v", byText["Проверка завершилась успешно после последней правки"])
	}
	// Пункт, который нельзя проверить автоматически, не подгоняется под
	// ближайший факт: «не знаю» честнее, чем «наверное, выполнено».
	if byText["Команда согласовала подход"].Met {
		t.Fatal("непроверяемый пункт не может засчитываться автоматически")
	}
	if !strings.Contains(byText["Команда согласовала подход"].Evidence, "судите сами") {
		t.Fatalf("непроверяемость не объяснена: %+v", byText["Команда согласовала подход"])
	}
	if !strings.Contains(outcome.Honest, "1 из 3") {
		t.Fatalf("итог не назван числом: %q", outcome.Honest)
	}
}

// Закрытый квест без единого факта — самый опасный случай: статус говорит
// «готово», а подтвердить нечем.
func TestQuestOutcomeSaysWhenNothingIsProven(t *testing.T) {
	application, world := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()

	quest := domain.Quest{
		ID: "q-2", WorkspaceID: world.ID, Title: "Пустой", Status: domain.QuestStatus("completed"),
		CreatedAt: now, UpdatedAt: now,
		DefinitionOfDone: []string{"Изменения приняты", "Тесты прошли"},
	}
	if err := application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}

	outcome, err := application.QuestOutcome(ctx, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Met != 0 {
		t.Fatalf("фактов нет — подтверждать нечего: %+v", outcome.Promises)
	}
	if !strings.Contains(outcome.Honest, "Ни одно обещание не подтверждено") {
		t.Fatalf("итог смягчён вместо прямого: %q", outcome.Honest)
	}
	if outcome.Promises == nil {
		t.Fatal("Promises обязан быть пустым списком, а не nil: клиент рендерит его напрямую")
	}
}

func TestQuestOutcomeHandlesQuestWithoutDefinitionOfDone(t *testing.T) {
	application, world := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := application.store.SaveQuest(ctx, domain.Quest{
		ID: "q-3", WorkspaceID: world.ID, Title: "Без обещаний",
		Status: domain.QuestStatus("active"), CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	outcome, err := application.QuestOutcome(ctx, "q-3")
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Total != 0 || !strings.Contains(outcome.Honest, "сверять не с чем") {
		t.Fatalf("отсутствие определения готовности не признано: %+v", outcome)
	}
}

// Пункт может требовать двух вещей сразу: «проверить, что изменения приняты».
// Раньше побеждало первое совпадение — пункт засчитывался по проверке, хотя
// изменений не было, и половина требования пропадала молча.
func TestQuestOutcomeRequiresBothWhenPromiseNamesBoth(t *testing.T) {
	application, world := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()

	quest := domain.Quest{
		ID: "q-4", WorkspaceID: world.ID, Title: "Двойное требование",
		Status: domain.QuestStatus("completed"), CreatedAt: now, UpdatedAt: now,
		DefinitionOfDone: []string{"Проверить, что изменения приняты"},
	}
	if err := application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	// Изменения применены, проверки нет — половина требования закрыта.
	if err := application.store.SaveChangeSet(ctx, domain.ChangeSet{
		ID: "cs-4", WorkspaceID: world.ID, QuestID: quest.ID, Title: "Правка",
		Status: domain.ChangeSetApplied, CreatedAt: now,
		Items: []domain.ChangeItem{{ID: "i1", Path: "a.go", Kind: "modify"}},
	}); err != nil {
		t.Fatal(err)
	}

	outcome, err := application.QuestOutcome(ctx, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Met != 0 {
		t.Fatalf("половина требования не может считаться выполнением: %+v", outcome.Promises)
	}
	if !strings.Contains(outcome.Promises[0].Evidence, "провер") {
		t.Fatalf("не названо, чего именно не хватает: %q", outcome.Promises[0].Evidence)
	}
}

func TestQuestOutcomeRequiresMergedResultCheckForMultiWriters(t *testing.T) {
	application, world := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, Goal: "Parallel edits", ResultKind: "workspace_change",
		Criteria:    []domain.AcceptanceCriterion{{ID: "c1", Text: "Files correct", Kind: "manual"}},
		Permissions: domain.TaskPermissions{WriteFiles: true},
		Budget:      domain.TaskBudget{Tokens: 1000, ActiveSeconds: 60, MaxParallel: 2, MaxReplans: 6, MaxAttempts: 2},
	}))
	if err != nil {
		t.Fatal(err)
	}
	quest := domain.Quest{
		ID: "q-multi", WorkspaceID: world.ID, Title: "Multi", Status: domain.QuestCompleted,
		Brief: &brief, FlowID: "flow-multi", CreatedAt: now, UpdatedAt: now,
	}
	if err = application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	finished := now.Add(time.Minute)
	for i, id := range []string{"exec-a", "exec-b"} {
		runID := "run-" + id
		if err = application.store.SaveRun(ctx, domain.Run{
			ID: runID, AgentID: "ag", ProfileID: "ag", WorkspaceID: world.ID, Task: "edit",
			Status: domain.RunCompleted, StartedAt: now.Add(time.Duration(i) * time.Second), FinishedAt: &finished,
		}); err != nil {
			t.Fatal(err)
		}
		if err = application.store.SaveExecution(ctx, domain.ExecutionInstance{
			ID: id, WorkspaceID: world.ID, QuestID: quest.ID, RunID: runID,
			Status: domain.RunCompleted, StartedAt: now.Add(time.Duration(i) * time.Second), FinishedAt: &finished,
		}); err != nil {
			t.Fatal(err)
		}
	}
	outcome, err := application.QuestOutcome(ctx, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Verified || outcome.UnverifiedReason == "" {
		t.Fatalf("multi-writer without merged-result must stay unverified: %+v", outcome)
	}
	if !strings.Contains(outcome.UnverifiedReason, "merged-result") {
		t.Fatalf("reason=%q", outcome.UnverifiedReason)
	}

	// Dedicated merged-result completion check unlocks verification path.
	evidence := agent.CompletionEvidence{
		BriefVersion: brief.Version, Status: "verified",
		Criteria: []agent.CriterionEvidence{{CriterionID: "c1", Kind: "manual", Status: "satisfied"}},
	}
	data, _ := json.Marshal(map[string]any{
		"status": "accepted_after_revision", "checkKind": "merged-result", "evidence": evidence,
	})
	briefData, _ := json.Marshal(map[string]any{"taskBrief": brief})
	if err = application.store.Append(ctx, domain.Event{
		ID: "ev-start", RunID: "run-exec-b", Type: domain.EventRunStarted, Actor: "user",
		Data: briefData, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err = application.store.Append(ctx, domain.Event{
		ID: "ev-merged", RunID: "run-exec-b", Type: domain.EventCompletionChecked, Actor: "agent",
		Data: data, CreatedAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	outcome, err = application.QuestOutcome(ctx, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.UnverifiedReason != "" {
		t.Fatalf("merged-result check should clear unverified reason: %q", outcome.UnverifiedReason)
	}
	if outcome.Evidence == nil || outcome.Evidence.Status != "verified" {
		t.Fatalf("expected merged evidence: %+v", outcome)
	}
}

func TestQuestOutcomeAllowsSerialInheritedWritersWithoutMergedResult(t *testing.T) {
	application, world := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, Goal: "Serial stages", ResultKind: "workspace_change",
		Criteria:    []domain.AcceptanceCriterion{{ID: "c1", Text: "Feature works", Kind: "manual"}},
		Permissions: domain.TaskPermissions{WriteFiles: true},
		Budget:      domain.TaskBudget{Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxReplans: 6, MaxAttempts: 2},
	}))
	if err != nil {
		t.Fatal(err)
	}
	quest := domain.Quest{
		ID: "q-serial", WorkspaceID: world.ID, Title: "Serial", Status: domain.QuestCompleted,
		Brief: &brief, CreatedAt: now, UpdatedAt: now,
	}
	if err = application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	finished := now.Add(time.Minute)
	rootSandbox := domain.SandboxRecord{ID: "sb-root", WorkspaceID: world.ID, ExecutionID: "exec-root", Path: t.TempDir()}
	childSandbox := domain.SandboxRecord{
		ID: "sb-child", WorkspaceID: world.ID, ExecutionID: "exec-child", Path: t.TempDir(),
		ParentExecutionID: "exec-root",
	}
	if err = application.store.SaveSandbox(ctx, rootSandbox); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveSandbox(ctx, childSandbox); err != nil {
		t.Fatal(err)
	}
	for i, item := range []struct {
		id, run, sandbox string
	}{
		{"exec-root", "run-root", "sb-root"},
		{"exec-child", "run-child", "sb-child"},
	} {
		if err = application.store.SaveRun(ctx, domain.Run{
			ID: item.run, AgentID: "ag", ProfileID: "ag", WorkspaceID: world.ID, Task: "stage",
			Status: domain.RunCompleted, StartedAt: now.Add(time.Duration(i) * time.Second), FinishedAt: &finished,
		}); err != nil {
			t.Fatal(err)
		}
		if err = application.store.SaveExecution(ctx, domain.ExecutionInstance{
			ID: item.id, WorkspaceID: world.ID, QuestID: quest.ID, RunID: item.run, SandboxID: item.sandbox,
			Status: domain.RunCompleted, StartedAt: now.Add(time.Duration(i) * time.Second), FinishedAt: &finished,
		}); err != nil {
			t.Fatal(err)
		}
	}
	evidence := agent.CompletionEvidence{
		BriefVersion: brief.Version, Status: "verified",
		Criteria: []agent.CriterionEvidence{{CriterionID: "c1", Kind: "manual", Status: "satisfied"}},
	}
	briefData, _ := json.Marshal(map[string]any{"taskBrief": brief})
	checkData, _ := json.Marshal(map[string]any{
		"status": "accepted_after_revision", "evidence": evidence,
	})
	if err = application.store.Append(ctx, domain.Event{
		ID: "ev-serial-start", RunID: "run-child", Type: domain.EventRunStarted, Actor: "user",
		Data: briefData, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err = application.store.Append(ctx, domain.Event{
		ID: "ev-serial-check", RunID: "run-child", Type: domain.EventCompletionChecked, Actor: "agent",
		Data: checkData, CreatedAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	outcome, err := application.QuestOutcome(ctx, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.UnverifiedReason != "" {
		t.Fatalf("serial inheritance must not demand merged-result: %q", outcome.UnverifiedReason)
	}
	if outcome.Evidence == nil || outcome.Evidence.Status != "verified" {
		t.Fatalf("serial tip proof should remain usable: %+v", outcome)
	}
}

func TestQuestOutcomeKeepsParentBriefProofAfterScopedStage(t *testing.T) {
	application, world := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, Goal: "PHP feature", ResultKind: "workspace_change",
		Criteria: []domain.AcceptanceCriterion{{
			ID: "verify-1", Text: "phpunit passes", Kind: "verification",
			Tool: "run_command", Arguments: json.RawMessage(`{"command":"php bin/phpunit"}`), ExpectedExitCode: intPtr(0),
		}},
		Permissions: domain.TaskPermissions{WriteFiles: true},
		Budget:      domain.TaskBudget{Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxReplans: 6, MaxAttempts: 2},
	}))
	if err != nil {
		t.Fatal(err)
	}
	quest := domain.Quest{
		ID: "q-scoped", WorkspaceID: world.ID, Title: "Scoped", Status: domain.QuestCompleted,
		Brief: &brief, CreatedAt: now, UpdatedAt: now,
	}
	if err = application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	finished := now.Add(time.Minute)
	rootSandbox := domain.SandboxRecord{ID: "sb-impl", WorkspaceID: world.ID, ExecutionID: "exec-impl", Path: t.TempDir()}
	childSandbox := domain.SandboxRecord{
		ID: "sb-int", WorkspaceID: world.ID, ExecutionID: "exec-int", Path: t.TempDir(),
		ParentExecutionID: "exec-impl",
	}
	if err = application.store.SaveSandbox(ctx, rootSandbox); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveSandbox(ctx, childSandbox); err != nil {
		t.Fatal(err)
	}
	for i, item := range []struct {
		id, run, sandbox string
	}{
		{"exec-impl", "run-impl", "sb-impl"},
		{"exec-int", "run-int", "sb-int"},
	} {
		if err = application.store.SaveRun(ctx, domain.Run{
			ID: item.run, AgentID: "ag", ProfileID: "ag", WorkspaceID: world.ID, Task: "stage",
			Status: domain.RunCompleted, StartedAt: now.Add(time.Duration(i) * time.Second), FinishedAt: &finished,
		}); err != nil {
			t.Fatal(err)
		}
		if err = application.store.SaveExecution(ctx, domain.ExecutionInstance{
			ID: item.id, WorkspaceID: world.ID, QuestID: quest.ID, RunID: item.run, SandboxID: item.sandbox,
			Status: domain.RunCompleted, StartedAt: now.Add(time.Duration(i) * time.Second), FinishedAt: &finished,
		}); err != nil {
			t.Fatal(err)
		}
	}
	evidence := agent.CompletionEvidence{
		BriefVersion: brief.Version, Status: "verified",
		Criteria: []agent.CriterionEvidence{{CriterionID: "verify-1", Kind: "verification", Status: "satisfied"}},
	}
	implBrief, _ := json.Marshal(map[string]any{"taskBrief": brief})
	checkData, _ := json.Marshal(map[string]any{
		"status": "accepted_after_revision", "evidence": evidence,
	})
	if err = application.store.Append(ctx, domain.Event{
		ID: "ev-impl-start", RunID: "run-impl", Type: domain.EventRunStarted, Actor: "user",
		Data: implBrief, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err = application.store.Append(ctx, domain.Event{
		ID: "ev-impl-check", RunID: "run-impl", Type: domain.EventCompletionChecked, Actor: "agent",
		Data: checkData, CreatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	scoped := brief
	scoped.Criteria = []domain.AcceptanceCriterion{{ID: "stage-complete", Kind: "manual", Text: "Stage done"}}
	scoped.ApprovedDigest = domain.TaskBriefDigest(scoped)
	intBrief, _ := json.Marshal(map[string]any{"taskBrief": scoped})
	if err = application.store.Append(ctx, domain.Event{
		ID: "ev-int-start", RunID: "run-int", Type: domain.EventRunStarted, Actor: "user",
		Data: intBrief, CreatedAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	outcome, err := application.QuestOutcome(ctx, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Verified {
		t.Fatalf("parent-brief proof must survive later scoped integrate: %+v", outcome)
	}
}

func TestQuestOutcomeRequiresAcceptWhenFlowHasAcceptAgent(t *testing.T) {
	application, world := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, Goal: "PHP feature", ResultKind: "workspace_change",
		Criteria: []domain.AcceptanceCriterion{{
			ID: "verify-1", Text: "phpunit passes", Kind: "verification",
			Tool: "run_command", Arguments: json.RawMessage(`{"command":"php bin/phpunit"}`), ExpectedExitCode: intPtr(0),
		}},
		Permissions: domain.TaskPermissions{WriteFiles: true},
		Budget:      domain.TaskBudget{Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxReplans: 6, MaxAttempts: 2},
	}))
	if err != nil {
		t.Fatal(err)
	}
	flow := domain.FlowGraph{
		ID: "flow-accept", WorkspaceID: world.ID, Name: "pipe",
		Nodes: []domain.FlowNode{
			{ID: "impl", Kind: domain.FlowNodeAgent, Name: "Implement", Config: map[string]any{"stageRole": domain.StageRoleImplement}},
			{ID: "acc", Kind: domain.FlowNodeAgent, Name: "Accept", Config: map[string]any{"stageRole": domain.StageRoleAccept}},
		},
		CreatedAt: now, UpdatedAt: now,
	}
	if err = application.store.SaveFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	quest := domain.Quest{
		ID: "q-need-accept", WorkspaceID: world.ID, Title: "Need accept", Status: domain.QuestCompleted,
		Brief: &brief, FlowID: flow.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err = application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	finished := now.Add(time.Minute)
	if err = application.store.SaveRun(ctx, domain.Run{
		ID: "run-impl-only", AgentID: "ag", ProfileID: "ag", WorkspaceID: world.ID, Task: "impl",
		Status: domain.RunCompleted, StartedAt: now, FinishedAt: &finished,
	}); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveExecution(ctx, domain.ExecutionInstance{
		ID: "exec-impl-only", WorkspaceID: world.ID, QuestID: quest.ID, RunID: "run-impl-only",
		FlowNodeID: "impl", Status: domain.RunCompleted, StartedAt: now, FinishedAt: &finished,
	}); err != nil {
		t.Fatal(err)
	}
	evidence := agent.CompletionEvidence{
		BriefVersion: brief.Version, Status: "verified",
		Criteria: []agent.CriterionEvidence{{CriterionID: "verify-1", Kind: "verification", Status: "satisfied"}},
	}
	implBrief, _ := json.Marshal(map[string]any{"taskBrief": brief, "stageRole": domain.StageRoleImplement})
	checkData, _ := json.Marshal(map[string]any{"status": "accepted_after_revision", "evidence": evidence})
	if err = application.store.Append(ctx, domain.Event{
		ID: "ev-impl-only-start", RunID: "run-impl-only", Type: domain.EventRunStarted, Actor: "user",
		Data: implBrief, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err = application.store.Append(ctx, domain.Event{
		ID: "ev-impl-only-check", RunID: "run-impl-only", Type: domain.EventCompletionChecked, Actor: "agent",
		Data: checkData, CreatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	outcome, err := application.QuestOutcome(ctx, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Verified {
		t.Fatalf("implement proof must not finalize when accept stage exists: %+v", outcome)
	}
}

func intPtr(v int) *int { return &v }
