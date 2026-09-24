package app

import (
	"context"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestCloseUnfinishedFlowChildQuestsNeverCancelsRoot(t *testing.T) {
	application := newTestApp(t)
	world := openTestWorld(t, application)
	now := time.Now().UTC()
	root := domain.Quest{ID: "root-flow-quest", WorkspaceID: world.ID, Title: "Root", Status: domain.QuestRunning, FlowRunID: "flow-run-root", CreatedAt: now, UpdatedAt: now}
	skipped := domain.Quest{ID: "skipped-flow-child", WorkspaceID: world.ID, ParentID: root.ID, FlowNodeID: "optional", FlowRunID: root.FlowRunID, Title: "Optional", Status: domain.QuestDraft, CreatedAt: now, UpdatedAt: now}
	if err := application.store.SaveQuest(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if err := application.store.SaveQuest(context.Background(), skipped); err != nil {
		t.Fatal(err)
	}
	if err := application.store.SaveFlowRun(context.Background(), domain.FlowRun{ID: root.FlowRunID, FlowID: "flow", WorkspaceID: world.ID, QuestID: root.ID, Status: domain.RunCompleted, NodeStates: map[string]domain.FlowNodeState{}, StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	application.closeUnfinishedFlowChildQuests(root.FlowRunID, true)
	quests, err := application.store.ListQuests(context.Background(), world.ID)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]domain.QuestStatus{}
	for _, quest := range quests {
		statuses[quest.ID] = quest.Status
	}
	if statuses[root.ID] != domain.QuestRunning {
		t.Fatalf("root status=%s, want running", statuses[root.ID])
	}
	if statuses[skipped.ID] != domain.QuestCancelled {
		t.Fatalf("skipped child status=%s, want cancelled", statuses[skipped.ID])
	}
}

func TestMasterSymfonyPresetAndGenericWebCompletion(t *testing.T) {
	brief := domain.TaskBrief{Goal: "Создать API на Symfony 7", ResultKind: "api", Scope: []string{"Symfony 7 health controller"}}
	if preset := masterStackPresetV2(brief); preset != "php-symfony-7" {
		t.Fatalf("preset=%q", preset)
	}
	setup := masterSetupPlanV2("php-symfony-7", brief)
	if len(setup.Commands) != 1 || len(setup.ExpectedPaths) != 3 {
		t.Fatalf("unexpected setup plan: %#v", setup)
	}
	profile := masterCompletionProfileV2(domain.WorkOrder{Stack: domain.StackPresetRef{ID: "php-symfony-7", Category: "api"}})
	if len(profile.Checks) != 3 {
		t.Fatalf("unexpected Symfony completion profile: %#v", profile)
	}
	generic := masterCompletionProfileV2(domain.WorkOrder{Stack: domain.StackPresetRef{ID: "recommended-web", Category: "web"}})
	for _, check := range generic.Checks {
		if check.Kind == "service_start" || check.Kind == "health" {
			t.Fatalf("generic web profile invented service check: %#v", generic)
		}
	}
}
