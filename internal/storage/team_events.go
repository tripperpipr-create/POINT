package storage

import (
	"context"
	"database/sql"

	"local-agent-workbench/internal/domain"
)

func (s *SQLite) SaveTeamEvent(ctx context.Context, event domain.TeamEvent) error {
	var delivered any
	if event.DeliveredAt != nil {
		delivered = formatTime(*event.DeliveredAt)
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO team_events(id,workspace_id,quest_id,flow_run_id,flow_node_id,from_agent_id,to_agent_id,kind,message,artifact_id,delivered_at,created_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET delivered_at=excluded.delivered_at`,
		event.ID, event.WorkspaceID, event.QuestID, event.FlowRunID, event.FlowNodeID,
		event.FromAgentID, event.ToAgentID, event.Kind, event.Message, event.ArtifactID,
		delivered, formatTime(event.CreatedAt))
	return err
}

func (s *SQLite) ListTeamEvents(ctx context.Context, workspaceID, flowRunID, toAgentID string, undeliveredOnly bool, limit int) ([]domain.TeamEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id,workspace_id,quest_id,flow_run_id,flow_node_id,from_agent_id,to_agent_id,kind,message,artifact_id,delivered_at,created_at
FROM team_events
WHERE workspace_id=? AND (?='' OR flow_run_id=?) AND (?='' OR to_agent_id='' OR to_agent_id=?)
  AND (?=0 OR delivered_at IS NULL)
ORDER BY created_at ASC LIMIT ?`,
		workspaceID, flowRunID, flowRunID, toAgentID, toAgentID, undeliveredOnly, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.TeamEvent
	for rows.Next() {
		var event domain.TeamEvent
		var delivered sql.NullString
		var created string
		if err = rows.Scan(&event.ID, &event.WorkspaceID, &event.QuestID, &event.FlowRunID, &event.FlowNodeID,
			&event.FromAgentID, &event.ToAgentID, &event.Kind, &event.Message, &event.ArtifactID,
			&delivered, &created); err != nil {
			return nil, err
		}
		event.CreatedAt = parseTime(created)
		if delivered.Valid {
			value := parseTime(delivered.String)
			event.DeliveredAt = &value
		}
		result = append(result, event)
	}
	return result, rows.Err()
}
