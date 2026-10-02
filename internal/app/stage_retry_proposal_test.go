package app

import (
	"context"
	"errors"
	"local-agent-workbench/internal/domain"
	"strings"
	"testing"
)

// Предложение Мастера из правок, не трогающих договор, — указание исполнителю
// — окно применяет само, и оно достаётся ровно проваленному этапу.
func TestSafeStageRetryProposalAppliesWithoutHuman(t *testing.T) {
	application, quest, run := failedStageQuestWithErrorForTest(t, "npm run verify: \"hasInjectionContext\" is not exported by \"vue-demi/lib/index.mjs\"")
	ctx := context.Background()
	application.finalizeQuestAfterFlow(quest.ID, false)
	proposal, err := application.ProposeStageRetryV2(ctx, quest.WorkspaceID, StageRetryProposalInput{
		QuestID: quest.ID, Instruction: "Собирай под Node 20: npm 12 блокирует скрипты установки",
		Diagnosis: "npm 12 заблокировал postinstall vue-demi",
	})
	if err != nil {
		t.Fatal(err)
	}
	if proposal.NeedsApproval || !proposal.AutoApply {
		t.Fatalf("safe proposal must apply by itself: %#v", proposal)
	}
	result, err := application.ControlWorkOrderQuestV2(ctx, quest.ID, "retry", WorkOrderQuestControlRequest{Source: StageRetrySourceAuto, ProposalDigest: proposal.Digest})
	if err != nil && !strings.Contains(err.Error(), "agent") {
		t.Fatalf("automatic retry with a safe proposal refused: %v", err)
	}
	if result.Status == "" {
		t.Fatal("retry result has no status")
	}
	stored, _ := application.store.GetFlowRun(ctx, run.ID)
	if instruction, _ := stored.NodeStates["verify"].Output[retryInstructionOutputKey].(string); !strings.Contains(instruction, "Node 20") {
		t.Fatalf("instruction not delivered to the stage: %#v", stored.NodeStates["verify"].Output)
	}
	if context, ok := retryInstructionContext(stored, "verify"); !ok || !strings.Contains(context.Content, "Node 20") {
		t.Fatal("instruction does not reach the stage prompt")
	}
	retried, _ := application.workOrderQuestV2(ctx, quest.WorkspaceID, quest.ID)
	if _, has := retried.Controller[stageRetryProposalKey]; has {
		t.Fatal("applied proposal stayed on the quest")
	}
	if node, _ := stageAutoRetryCounts(retried, "verify"); node != 1 {
		t.Fatalf("a retry without the human was not counted: %d", node)
	}
}

// Статус называет, кто повторил и что поменял: повтор без человека виден так
// же, как нажатие.
func TestStageRetryStatusNamesWhoAndWhat(t *testing.T) {
	message := stageRetryStatusMessage("Accept", stageRetryPlan{Source: StageRetrySourceAuto, Proposal: "sha256:x", Instruction: "собери заново",
		Criteria: []StageRetryCriterionChange{{CriterionID: "tgz-content"}}})
	for _, want := range []string{"Мастер повторяет этап «Accept»", "с указанием исполнителю", "tgz-content"} {
		if !strings.Contains(message, want) {
			t.Fatalf("%q lacks %q", message, want)
		}
	}
	if message := stageRetryStatusMessage("Accept", stageRetryPlan{Source: StageRetrySourceAuto}); !strings.HasPrefix(message, "Point повторяет этап") {
		t.Fatalf("environment retry: %q", message)
	}
}

// Правка команды проверки меняет утверждённое: окно само её не применит,
// человек — да, и после этого команда проверки другая.
func TestCriterionProposalWaitsForHumanAndAmendsTheCheck(t *testing.T) {
	application, quest, _ := failedStageQuestWithErrorForTest(t, "npm error code ENOENT")
	ctx := context.Background()
	application.finalizeQuestAfterFlow(quest.ID, false)
	proposal, err := application.ProposeStageRetryV2(ctx, quest.WorkspaceID, StageRetryProposalInput{
		QuestID: quest.ID, Diagnosis: "каталога нет",
		Criteria: []StageRetryCriterionChange{{CriterionID: "build", Command: "mkdir -p out && docker compose build", Reason: "каталог назначения не создаётся"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !proposal.NeedsApproval || proposal.AutoApply || proposal.Criteria[0].PreviousCommand != "docker compose build" {
		t.Fatalf("criterion change must wait for the human: %#v", proposal)
	}
	if _, err = application.ControlWorkOrderQuestV2(ctx, quest.ID, "retry", WorkOrderQuestControlRequest{Source: StageRetrySourceAuto, ProposalDigest: proposal.Digest}); !errors.Is(err, errStageRetryLimit) {
		t.Fatalf("the window applied a criterion change by itself: %v", err)
	}
	if _, err = application.ControlWorkOrderQuestV2(ctx, quest.ID, "retry", WorkOrderQuestControlRequest{ProposalDigest: "sha256:stale"}); !errors.Is(err, errStageRetryProposal) {
		t.Fatalf("a stale proposal was applied: %v", err)
	}
	if _, err = application.ControlWorkOrderQuestV2(ctx, quest.ID, "retry", WorkOrderQuestControlRequest{ProposalDigest: proposal.Digest}); err != nil && !strings.Contains(err.Error(), "agent") {
		t.Fatalf("human approval refused: %v", err)
	}
	commands := application.effectiveCriterionCommands(ctx, quest)
	if commands["build"] != "mkdir -p out && docker compose build" {
		t.Fatalf("check not amended: %v", commands)
	}
	amendments := application.criterionAmendments(ctx, quest.ID)
	if limitation := amendments["build"].Limitation(); !strings.Contains(limitation, "было «docker compose build»") {
		t.Fatalf("the outcome would not name the change: %q", limitation)
	}
}

// Сервер отвергает правки, которые ослабили бы проверку или вышли бы за
// закрытый список сред.
func TestStageRetryProposalRejectsUnsafeChanges(t *testing.T) {
	application, quest, _ := failedStageQuestWithErrorForTest(t, "build failed")
	ctx := context.Background()
	application.finalizeQuestAfterFlow(quest.ID, false)
	cases := map[string]StageRetryProposalInput{
		"masked":   {QuestID: quest.ID, Criteria: []StageRetryCriterionChange{{CriterionID: "build", Command: "docker compose build || true", Reason: "x"}}},
		"unknown":  {QuestID: quest.ID, Criteria: []StageRetryCriterionChange{{CriterionID: "deploy", Command: "true", Reason: "x"}}},
		"empty":    {QuestID: quest.ID, Diagnosis: "ничего не меняю"},
		"same":     {QuestID: quest.ID, Criteria: []StageRetryCriterionChange{{CriterionID: "build", Command: "docker compose build", Reason: "x"}}},
		"no_quest": {QuestID: "quest_missing", Instruction: "повтори"},
	}
	for name, input := range cases {
		if _, err := application.ProposeStageRetryV2(ctx, quest.WorkspaceID, input); err == nil {
			t.Fatalf("%s: unsafe proposal accepted", name)
		}
	}
}

func TestDependencyRetryRequiresApprovalAndPreservesCompletedStages(t *testing.T) {
	a, q, run := failedStageQuestWithErrorForTest(t, "dependency preparation: lock missing")
	ctx := context.Background()
	a.finalizeQuestAfterFlow(q.ID, false)
	plan := &domain.DependencyPlan{Version: "1", Projects: []domain.DependencyProject{{Cwd: "lk-backend/source", Manager: "npm", Commands: []domain.SetupCommand{{Command: "npm ci --include=dev", TimeoutSeconds: 600}}, ManifestPaths: []string{"lk-backend/source/package.json", "lk-backend/source/package-lock.json"}}}}
	proposal, err := a.ProposeStageRetryV2(ctx, q.WorkspaceID, StageRetryProposalInput{QuestID: q.ID, Dependencies: plan, Diagnosis: "Prepare once before original criteria"})
	if err != nil {
		t.Fatal(err)
	}
	if !proposal.NeedsApproval || proposal.AutoApply {
		t.Fatal("dependency plan changed without approval")
	}
	if _, err = a.ControlWorkOrderQuestV2(ctx, q.ID, "retry", WorkOrderQuestControlRequest{Source: StageRetrySourceAuto, ProposalDigest: proposal.Digest}); err == nil {
		t.Fatal("automatic amendment accepted")
	}
	if _, err = a.ControlWorkOrderQuestV2(ctx, q.ID, "retry", WorkOrderQuestControlRequest{ProposalDigest: proposal.Digest}); err != nil && !strings.Contains(err.Error(), "agent") {
		t.Fatal(err)
	}
	latest, _ := a.workOrderQuestV2(ctx, q.WorkspaceID, q.ID)
	if effectiveDependencyPlan(latest, nil) == nil {
		t.Fatal("approved amendment not applied")
	}
	after, _ := a.store.GetFlowRun(ctx, run.ID)
	for id, node := range run.NodeStates {
		if node.Status == "completed" && after.NodeStates[id].Status != "completed" {
			t.Fatal("completed writer restarted")
		}
	}
	proposal.Dependencies.Projects[0].Commands[0].Command += " --ignore-scripts"
	if stageRetryProposalDigest(proposal) == proposal.Digest {
		t.Fatal("dependency command excluded from digest")
	}
}
