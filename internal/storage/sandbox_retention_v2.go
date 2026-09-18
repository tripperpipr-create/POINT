package storage

import (
	"context"
	"database/sql"
	"time"

	"local-agent-workbench/internal/domain"
)

type SandboxRetentionCandidate struct {
	Sandbox       domain.SandboxRecord
	WorkspacePath string
}

// ListExpiredQuestSandboxes returns only partial/cancelled quest workspaces.
// Completed quest artifacts are covered by their DeliveryReceipt and are not
// silently removed by this retention policy.
func (s *SQLite) ListExpiredQuestSandboxes(ctx context.Context, before time.Time) ([]SandboxRetentionCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT sandbox.id,sandbox.workspace_id,sandbox.execution_id,sandbox.kind,sandbox.backend,sandbox.backend_version,
       sandbox.backend_image,sandbox.backend_image_digest,sandbox.path,sandbox.base_commit,sandbox.parent_sandbox_id,
       sandbox.parent_execution_id,sandbox.parent_sandbox_ids,sandbox.parent_execution_ids,sandbox.baseline_path,
       sandbox.baseline_change_set_ids,sandbox.created_at,sandbox.closed_at,workspace.path
FROM sandboxes sandbox
INNER JOIN executions execution ON execution.id=sandbox.execution_id
INNER JOIN quests quest ON quest.id=execution.quest_id
INNER JOIN workspaces workspace ON workspace.id=sandbox.workspace_id
WHERE sandbox.closed_at IS NULL AND sandbox.created_at<?
  AND sandbox.kind<>'live'
  AND quest.status IN (?,?,?)
ORDER BY sandbox.created_at`, formatTime(before), domain.QuestCancelled, domain.QuestNeedsReview, domain.QuestBlocked)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []SandboxRetentionCandidate
	for rows.Next() {
		var candidate SandboxRetentionCandidate
		var created, parentSandboxIDs, parentExecutionIDs, baselineChangeSetIDs string
		var closed sql.NullString
		sandbox := &candidate.Sandbox
		if err = rows.Scan(&sandbox.ID, &sandbox.WorkspaceID, &sandbox.ExecutionID, &sandbox.Kind, &sandbox.Backend,
			&sandbox.BackendVersion, &sandbox.BackendImage, &sandbox.BackendImageDigest, &sandbox.Path, &sandbox.BaseCommit,
			&sandbox.ParentSandboxID, &sandbox.ParentExecutionID, &parentSandboxIDs, &parentExecutionIDs,
			&sandbox.BaselinePath, &baselineChangeSetIDs, &created, &closed, &candidate.WorkspacePath); err != nil {
			return nil, err
		}
		unmarshalJSON(parentSandboxIDs, &sandbox.ParentSandboxIDs)
		unmarshalJSON(parentExecutionIDs, &sandbox.ParentExecutionIDs)
		unmarshalJSON(baselineChangeSetIDs, &sandbox.BaselineChangeSetIDs)
		sandbox.CreatedAt = parseTime(created)
		result = append(result, candidate)
	}
	return result, rows.Err()
}

func (s *SQLite) MarkSandboxClosed(ctx context.Context, id string, closedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sandboxes SET closed_at=? WHERE id=? AND closed_at IS NULL`, formatTime(closedAt), id)
	return err
}
