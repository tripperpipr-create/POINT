package storage_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/storage"
)

func TestAutonomyStateRoundTrips(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "autonomy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	quest := domain.Quest{
		ID: "quest", WorkspaceID: "ws", Kind: "project", ControllerState: "waiting_prerequisite",
		Controller: map[string]any{"wave": float64(2)}, PrerequisiteIDs: []string{"prep"},
		Title: "App", Status: domain.QuestPaused, CreatedAt: now, UpdatedAt: now,
	}
	if err = store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	quests, err := store.ListQuests(ctx, "ws")
	if err != nil || len(quests) != 1 || quests[0].Kind != "project" || len(quests[0].PrerequisiteIDs) != 1 {
		t.Fatalf("quests=%#v err=%v", quests, err)
	}
	execution := domain.ExecutionInstance{
		ID: "execution", WorkspaceID: "ws", ProjectAgentID: "agent", Task: "task",
		Status: domain.RunInterrupted, Runtime: "codex", RuntimeSessionID: "thread-1", StartedAt: now,
	}
	if err = store.SaveExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetExecution(ctx, execution.ID)
	if err != nil || loaded.Runtime != "codex" || loaded.RuntimeSessionID != "thread-1" {
		t.Fatalf("execution=%#v err=%v", loaded, err)
	}
	event := domain.TeamEvent{
		ID: "event", WorkspaceID: "ws", QuestID: "quest", FlowRunID: "flow", FlowNodeID: "node",
		FromAgentID: "a", ToAgentID: "b", Kind: "question", Message: "API?", CreatedAt: now,
	}
	if err = store.SaveTeamEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListTeamEvents(ctx, "ws", "flow", "b", true, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%#v err=%v", events, err)
	}
}
