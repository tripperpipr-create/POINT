package storage

import (
	"context"
	"encoding/json"
	"time"

	"local-agent-workbench/internal/domain"
)

func (s *SQLite) SaveAgentPrepChain(ctx context.Context, chain domain.AgentPrepChain) error {
	requirement, _ := json.Marshal(chain.Requirement)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO agent_prep_chains(
 id,workspace_id,parent_quest_id,prep_quest_id,deferred_task_hash,requirement_json,
 candidate_agent_id,state,error,attempts,created_at,updated_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET candidate_agent_id=excluded.candidate_agent_id,
 state=excluded.state,error=excluded.error,attempts=excluded.attempts,updated_at=excluded.updated_at`,
		chain.ID, chain.WorkspaceID, chain.ParentQuestID, chain.PrepQuestID, chain.DeferredTaskHash,
		string(requirement), chain.CandidateAgentID, chain.State, chain.Error, chain.Attempts,
		formatTime(chain.CreatedAt), formatTime(chain.UpdatedAt))
	return err
}

func (s *SQLite) ListAgentPrepChains(ctx context.Context, workspaceID string) ([]domain.AgentPrepChain, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id,workspace_id,parent_quest_id,prep_quest_id,deferred_task_hash,requirement_json,
       candidate_agent_id,state,error,attempts,created_at,updated_at
FROM agent_prep_chains WHERE workspace_id=? ORDER BY updated_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.AgentPrepChain
	for rows.Next() {
		var chain domain.AgentPrepChain
		var requirement, created, updated string
		if err = rows.Scan(&chain.ID, &chain.WorkspaceID, &chain.ParentQuestID, &chain.PrepQuestID,
			&chain.DeferredTaskHash, &requirement, &chain.CandidateAgentID, &chain.State, &chain.Error,
			&chain.Attempts, &created, &updated); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(requirement), &chain.Requirement)
		chain.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		chain.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		result = append(result, chain)
	}
	return result, rows.Err()
}
