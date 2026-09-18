package app

import (
	"context"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestReviseActiveQuestBriefRequiresPauseAndApproval(t *testing.T) {
	a, ws := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, Goal: "Old goal", ResultKind: "report",
		Criteria:    []domain.AcceptanceCriterion{{ID: "c1", Text: "Report", Kind: "manual"}},
		Permissions: domain.TaskPermissions{WriteFiles: false, ExecuteCommands: false},
	}))
	if err != nil {
		t.Fatal(err)
	}
	quest := domain.Quest{ID: "q-revise", WorkspaceID: ws.ID, Title: "Old", Status: domain.QuestActive, Brief: &brief, CreatedAt: now, UpdatedAt: now}
	if err = a.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	exec := domain.ExecutionInstance{ID: "ex1", WorkspaceID: ws.ID, QuestID: quest.ID, Status: domain.RunRunning, StartedAt: now}
	if err = a.store.SaveExecution(ctx, exec); err != nil {
		t.Fatal(err)
	}
	changed := brief
	changed.Goal = "New goal"
	if _, err = a.ReviseActiveQuestBrief(ctx, quest.ID, changed, brief.Version, brief.Version+1); err == nil {
		t.Fatal("running execution must block revise")
	}
	exec.Status = domain.RunPaused
	if err = a.store.SaveExecution(ctx, exec); err != nil {
		t.Fatal(err)
	}
	if _, err = a.ReviseActiveQuestBrief(ctx, quest.ID, changed, brief.Version, brief.Version); err == nil {
		t.Fatal("stale approval must fail")
	}
	updated, err := a.ReviseActiveQuestBrief(ctx, quest.ID, changed, brief.Version, brief.Version+1)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Brief.Goal != "New goal" || updated.Brief.Version != brief.Version+1 || !domain.IsTaskBriefApproved(*updated.Brief) {
		t.Fatalf("brief=%#v", updated.Brief)
	}
}
