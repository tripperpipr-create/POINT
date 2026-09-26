package storage

import (
	"context"

	"local-agent-workbench/internal/domain"
)

type InterruptedQuestV2 struct {
	QuestID     string
	WorkspaceID string
	Status      domain.QuestStatus
}

// ListInterruptedWorkOrderQuestsV2 returns approved v2 quests that were still
// in a live state when the core stopped. No executor survives a process exit,
// so at startup every such row describes work nobody is doing any more: left
// alone it would keep claiming progress that cannot happen.
func (s *SQLite) ListInterruptedWorkOrderQuestsV2(ctx context.Context) ([]InterruptedQuestV2, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT quest.id,quest.workspace_id,quest.status
FROM quests quest
INNER JOIN work_order_approvals_v2 approval ON approval.quest_id=quest.id
WHERE quest.status IN (?,?,?,?)
ORDER BY quest.updated_at`,
		domain.QuestPreflight, domain.QuestRunning, domain.QuestVerifying, domain.QuestApplying)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []InterruptedQuestV2
	for rows.Next() {
		var item InterruptedQuestV2
		if err = rows.Scan(&item.QuestID, &item.WorkspaceID, &item.Status); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// ListWorkOrderQuestsWaitingForSandboxV2 returns quests the core paused
// because Docker Desktop was not running. They continue on their own once the
// daemon answers, so the core has to find them again after a restart.
func (s *SQLite) ListWorkOrderQuestsWaitingForSandboxV2(ctx context.Context) ([]InterruptedQuestV2, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT quest.id,quest.workspace_id,quest.status
FROM quests quest
INNER JOIN work_order_approvals_v2 approval ON approval.quest_id=quest.id
WHERE quest.status=? AND quest.controller_json LIKE '%"waitingForSandbox":true%'
ORDER BY quest.updated_at`, domain.QuestPaused)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []InterruptedQuestV2
	for rows.Next() {
		var item InterruptedQuestV2
		if err = rows.Scan(&item.QuestID, &item.WorkspaceID, &item.Status); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
