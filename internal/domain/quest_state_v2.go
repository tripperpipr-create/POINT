package domain

// CanTransitionWorkOrderQuest defines the public v2 lifecycle. Completion is
// intentionally absent here: only the evidence gate may perform that edge.
func CanTransitionWorkOrderQuest(from, to QuestStatus) bool {
	allowed := map[QuestStatus]map[QuestStatus]bool{
		QuestDraft:            {QuestAwaitingApproval: true, QuestCancelled: true},
		QuestAwaitingApproval: {QuestPreflight: true, QuestCancelled: true},
		QuestPreflight:        {QuestRunning: true, QuestPaused: true, QuestAwaitingUser: true, QuestBlocked: true, QuestCancelled: true},
		QuestRunning:          {QuestVerifying: true, QuestPaused: true, QuestAwaitingUser: true, QuestBlocked: true, QuestCancelled: true},
		QuestVerifying:        {QuestApplying: true, QuestPaused: true, QuestAwaitingUser: true, QuestNeedsReview: true, QuestBlocked: true, QuestCancelled: true},
		QuestApplying:         {QuestPaused: true, QuestAwaitingUser: true, QuestNeedsReview: true, QuestBlocked: true, QuestCancelled: true},
		QuestPaused:           {QuestPreflight: true, QuestRunning: true, QuestVerifying: true, QuestApplying: true, QuestCancelled: true},
		QuestAwaitingUser:     {QuestPreflight: true, QuestRunning: true, QuestVerifying: true, QuestApplying: true, QuestPaused: true, QuestCancelled: true},
		QuestNeedsReview:      {QuestCancelled: true},
		QuestBlocked:          {QuestPreflight: true, QuestRunning: true, QuestCancelled: true},
	}
	return allowed[from][to]
}
