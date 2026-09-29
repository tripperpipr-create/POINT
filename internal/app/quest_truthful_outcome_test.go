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

// Форма E1: YAML прошёл, сборка упала, но приёмка отклонена целиком — и итог
// писал «нет привязанного результата» по обоим критериям. E3: три файла ждали
// в pending-наборе этапа, а итог говорил «Изменено файлов: 0».

var truthfulCriteria = []domain.AcceptanceCriterion{
	{ID: "yaml-syntax", Kind: "verification", Text: "CI YAML разбирается", Tool: "run_command", Arguments: json.RawMessage(`{"command":"npx js-yaml .gitlab-ci.yml"}`), ExpectedExitCode: intPtr(0)},
	{ID: "verify-pack", Kind: "verification", Text: "Сборка пакета проходит", Tool: "run_command", Arguments: json.RawMessage(`{"command":"npm run verify"}`), ExpectedExitCode: intPtr(0)},
}

func truthfulOutcomeWorkOrder(t *testing.T, application *App, key string) (domain.Workspace, domain.WorkOrderApproval, domain.Quest) {
	t.Helper()
	ctx := context.Background()
	workspace := openTestWorld(t, application)
	now := time.Now().UTC()
	order := domain.WorkOrder{
		WorkspaceID: workspace.ID, State: "ready", Goal: "deploy pipeline", Scope: []string{"ci"},
		Criteria:  truthfulCriteria,
		Workspace: domain.WorkspacePlan{Mode: "existing", Path: workspace.Path, Isolation: "snapshot"},
		Stack:     domain.StackPresetRef{ID: "node", Version: "1", Category: "web", Source: "benchmark"},
		Routing:   domain.ModelRoutingPolicy{Mode: "fixed", FixedConnectionID: "connection", FixedModel: "model"},
		Budget:    domain.BudgetEnvelope{Preset: "small", Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxAttempts: 1},
		Delivery:  domain.DeliveryPolicy{ApplyMode: "automatic", CommitMode: "none", KeepPartialDays: 30},
		CreatedAt: now, UpdatedAt: now,
	}
	order, err := application.store.SaveWorkOrderV2(ctx, order)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := application.store.ApproveWorkOrderV2(ctx, order.ID, order.Version, domain.WorkOrderDigest(order), key)
	if err != nil {
		t.Fatal(err)
	}
	quest, err := application.workOrderQuestV2(ctx, workspace.ID, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, Goal: "deploy pipeline", ResultKind: "workspace_change",
		Criteria:    truthfulCriteria,
		Permissions: domain.TaskPermissions{WriteFiles: true},
		Budget:      domain.TaskBudget{Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxReplans: 6, MaxAttempts: 2},
	}))
	if err != nil {
		t.Fatal(err)
	}
	quest.Brief = &brief
	if err = application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	return workspace, approval, quest
}

// recordAcceptVerdict stores a finished accept stage of the quest: a child
// quest, its execution and run, and the final completion.checked verdict.
func recordAcceptVerdict(t *testing.T, application *App, workspace domain.Workspace, quest domain.Quest, suffix, verdict string, runStatus domain.RunStatus, startedAt time.Time, criteria []agent.CriterionEvidence) domain.ExecutionInstance {
	t.Helper()
	ctx := context.Background()
	child := domain.Quest{
		ID: "stage-accept-" + suffix, WorkspaceID: workspace.ID, ParentID: quest.ID, Title: "Accept",
		Status: domain.QuestFailed, CreatedAt: startedAt, UpdatedAt: startedAt,
	}
	if err := application.store.SaveQuest(ctx, child); err != nil {
		t.Fatal(err)
	}
	finished := startedAt.Add(time.Minute)
	runID, execID := "run-accept-"+suffix, "exec-accept-"+suffix
	if err := application.store.SaveRun(ctx, domain.Run{
		ID: runID, AgentID: "ag", ProfileID: "ag", WorkspaceID: workspace.ID, Task: "accept",
		Status: runStatus, StartedAt: startedAt, FinishedAt: &finished,
	}); err != nil {
		t.Fatal(err)
	}
	execution := domain.ExecutionInstance{
		ID: execID, WorkspaceID: workspace.ID, QuestID: child.ID, RunID: runID, FlowNodeID: "accept",
		Status: runStatus, StartedAt: startedAt, FinishedAt: &finished,
	}
	if err := application.store.SaveExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	started, _ := json.Marshal(map[string]any{"taskBrief": quest.Brief, "stageRole": domain.StageRoleAccept})
	evidenceStatus := "verified"
	if verdict == "rejected" {
		evidenceStatus = "blocked"
	}
	checked, _ := json.Marshal(map[string]any{"status": verdict, "checkKind": "accept", "evidence": agent.CompletionEvidence{
		BriefVersion: quest.Brief.Version, Status: evidenceStatus, Criteria: criteria,
	}})
	for i, event := range []domain.Event{
		{ID: "ev-start-" + suffix, RunID: runID, Type: domain.EventRunStarted, Actor: "system", Data: started},
		{ID: "ev-check-" + suffix, RunID: runID, Type: domain.EventCompletionChecked, Actor: "system", Data: checked},
	} {
		event.CreatedAt = startedAt.Add(time.Duration(i) * time.Second)
		if err := application.store.Append(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	return execution
}

func criterionCheck(id, command, status string, exit int) agent.CriterionEvidence {
	checkStatus := "passed"
	if status != "satisfied" {
		checkStatus = "unresolved"
	}
	return agent.CriterionEvidence{
		CriterionID: id, Kind: "verification", Status: status,
		Check: &agent.CheckEvidence{Tool: "run_command", Arguments: json.RawMessage(`{"command":"` + command + `"}`), ExitCode: &exit, Status: checkStatus, Detail: "exit " + command},
	}
}

func finalQuestMessage(t *testing.T, application *App, workspaceID, questID string) string {
	t.Helper()
	messages, err := application.store.ListChatMessages(context.Background(), workspaceID, "master", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if message.ID == "master-workorder-final-"+questID {
			return message.Content
		}
	}
	t.Fatalf("итоговое сообщение квеста %s не опубликовано", questID)
	return ""
}

func TestRejectedAcceptKeepsPerCriterionFactsAndPreparedFiles(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	workspace, approval, quest := truthfulOutcomeWorkOrder(t, application, "e1-rejected-accept")
	execution := recordAcceptVerdict(t, application, workspace, quest, "e1", "rejected", domain.RunFailed, time.Now().UTC(), []agent.CriterionEvidence{
		criterionCheck("yaml-syntax", "npx js-yaml .gitlab-ci.yml", "satisfied", 0),
		criterionCheck("verify-pack", "npm run verify", "failed", 1),
	})
	now := time.Now().UTC()
	if err := application.store.SaveChangeSet(ctx, domain.ChangeSet{
		ID: "cs-e3", WorkspaceID: workspace.ID, ExecutionID: execution.ID, QuestID: execution.QuestID, Title: "stage result",
		Status: domain.ChangeSetPending, CreatedAt: now, UpdatedAt: now,
		Items: []domain.ChangeItem{
			{ID: "i1", Path: ".gitlab-ci.yml", Kind: "modify"},
			{ID: "i2", Path: "package.json", Kind: "modify"},
			{ID: "i3", Path: "scripts/deploy.mjs", Kind: "create"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	outcome, err := application.QuestOutcome(ctx, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Verified || outcome.Evidence == nil || outcome.Met != 1 || outcome.Total != 2 {
		t.Fatalf("отклонённая приёмка должна дать поштучные факты без подтверждения: %+v", outcome)
	}
	if !strings.Contains(outcome.Promises[1].Evidence, "код 1") {
		t.Fatalf("проваленный критерий без кода выхода: %q", outcome.Promises[1].Evidence)
	}

	bundle := application.buildWorkOrderEvidenceV2(ctx, approval, quest, false)
	markWorkOrderCriterionStatusesV2(approval.WorkOrder, &bundle)
	if len(bundle.ChangedFiles) != 0 || len(bundle.PreparedFiles) != 3 {
		t.Fatalf("подготовленное не отделено от доставленного: changed=%v prepared=%v", bundle.ChangedFiles, bundle.PreparedFiles)
	}
	statuses := map[string]domain.CriterionEvidence{}
	for _, item := range bundle.Criteria {
		statuses[item.CriterionID] = item
	}
	if yaml := statuses["yaml-syntax"]; yaml.Status != domain.CriterionStatusPassed || !yaml.Satisfied || yaml.ExitCode == nil || *yaml.ExitCode != 0 {
		t.Fatalf("успешный YAML потерян при падении сборки: %+v", yaml)
	}
	if verify := statuses["verify-pack"]; verify.Status != domain.CriterionStatusFailed || verify.Satisfied || verify.ExitCode == nil || *verify.ExitCode != 1 {
		t.Fatalf("проваленная сборка не названа провалом: %+v", verify)
	}
	if verdict := domain.WorkOrderEvidenceVerdict(approval.WorkOrder, bundle); verdict.Status != domain.QuestBlocked {
		t.Fatalf("поштучные факты не должны открывать шлюз: %+v", verdict)
	}

	application.publishWorkOrderOutcomeV2(ctx, approval, quest, domain.QuestBlocked, bundle)
	content := finalQuestMessage(t, application, workspace.ID, quest.ID)
	for _, want := range []string{
		"Доставлено в проект: 0",
		"Подготовлено, не доставлено: 3",
		"scripts/deploy.mjs",
		"Пройдены: CI YAML разбирается (`npx js-yaml .gitlab-ci.yml`, код 0)",
		"Провалены: Сборка пакета проходит (`npm run verify`, код 1)",
		"Не запускались: нет",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("в итоге нет %q:\n%s", want, content)
		}
	}
}

// Форма E2: приёмка не запускалась вовсе. Это не провал и не ручная оценка.
func TestAcceptThatNeverRanIsReportedAsNotRun(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	workspace, approval, quest := truthfulOutcomeWorkOrder(t, application, "e2-not-run")
	bundle := application.buildWorkOrderEvidenceV2(ctx, approval, quest, false)
	markWorkOrderCriterionStatusesV2(approval.WorkOrder, &bundle)
	for _, item := range bundle.Criteria {
		if item.Status != domain.CriterionStatusNotRun || item.ExitCode != nil {
			t.Fatalf("незапущенная проверка названа иначе: %+v", item)
		}
	}
	for _, check := range bundle.VerificationChecks {
		if check.Summary != "Проверка не запускалась" {
			t.Fatalf("незапущенная проверка без объяснения: %+v", check)
		}
	}
	application.publishWorkOrderOutcomeV2(ctx, approval, quest, domain.QuestBlocked, bundle)
	content := finalQuestMessage(t, application, workspace.ID, quest.ID)
	if !strings.Contains(content, "Не запускались: CI YAML разбирается; Сборка пакета проходит") || !strings.Contains(content, "Провалены: нет") {
		t.Fatalf("итог смешал незапущенное с проваленным:\n%s", content)
	}
}

// Старый успех не прячет свежий отказ: решает последний вердикт приёмки.
func TestNewerRejectedAcceptOverridesOlderAccepted(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	workspace, _, quest := truthfulOutcomeWorkOrder(t, application, "newer-rejected")
	earlier := time.Now().UTC().Add(-time.Hour)
	recordAcceptVerdict(t, application, workspace, quest, "old", "accepted_after_revision", domain.RunCompleted, earlier, []agent.CriterionEvidence{
		criterionCheck("yaml-syntax", "npx js-yaml .gitlab-ci.yml", "satisfied", 0),
		criterionCheck("verify-pack", "npm run verify", "satisfied", 0),
	})
	outcome, err := application.QuestOutcome(ctx, quest.ID)
	if err != nil || !outcome.Verified {
		t.Fatalf("исходный принятый вердикт должен подтверждать: %+v err=%v", outcome, err)
	}
	recordAcceptVerdict(t, application, workspace, quest, "new", "rejected", domain.RunFailed, earlier.Add(30*time.Minute), []agent.CriterionEvidence{
		criterionCheck("yaml-syntax", "npx js-yaml .gitlab-ci.yml", "satisfied", 0),
		criterionCheck("verify-pack", "npm run verify", "failed", 1),
	})
	outcome, err = application.QuestOutcome(ctx, quest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Verified || outcome.Met != 1 || !strings.Contains(outcome.Honest, "отклонила") {
		t.Fatalf("свежий отказ спрятан за старым успехом: %+v", outcome)
	}
}
