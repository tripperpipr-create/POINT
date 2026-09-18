package domain

import "testing"

func TestWorkOrderQuestStateMachineHasNoPublicCompletionEdge(t *testing.T) {
	for _, from := range []QuestStatus{QuestDraft, QuestAwaitingApproval, QuestPreflight, QuestRunning, QuestVerifying, QuestApplying, QuestPaused, QuestAwaitingUser, QuestNeedsReview, QuestBlocked} {
		if CanTransitionWorkOrderQuest(from, QuestCompleted) {
			t.Fatalf("public transition %s -> completed bypasses evidence gate", from)
		}
	}
	if !CanTransitionWorkOrderQuest(QuestPreflight, QuestPaused) || !CanTransitionWorkOrderQuest(QuestPaused, QuestPreflight) {
		t.Fatal("preflight pause/resume transition is missing")
	}
}
