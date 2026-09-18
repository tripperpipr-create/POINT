package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"local-agent-workbench/internal/domain"
)

func migrationTaskBriefsV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
ALTER TABLE quest_proposals ADD COLUMN brief_json TEXT;
ALTER TABLE quests ADD COLUMN brief_json TEXT;
CREATE TABLE task_brief_revisions (
 proposal_id TEXT NOT NULL,
 workspace_id TEXT NOT NULL,
 version INTEGER NOT NULL CHECK(version > 0),
 digest TEXT NOT NULL,
 brief_json TEXT NOT NULL,
 created_at TEXT NOT NULL,
 PRIMARY KEY(proposal_id,version),
 FOREIGN KEY(proposal_id) REFERENCES quest_proposals(id)
);
CREATE INDEX task_brief_revisions_workspace ON task_brief_revisions(workspace_id,proposal_id,version);
CREATE TRIGGER task_brief_revisions_immutable_update BEFORE UPDATE ON task_brief_revisions BEGIN SELECT RAISE(ABORT,'task brief revisions are immutable'); END;
CREATE TRIGGER task_brief_revisions_immutable_delete BEFORE DELETE ON task_brief_revisions BEGIN SELECT RAISE(ABORT,'task brief revisions are immutable'); END;
`)
	return err
}

func taskBriefJSON(brief *domain.TaskBrief) (any, error) {
	if brief == nil {
		return nil, nil
	}
	if err := domain.ValidateTaskBrief(*brief); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(brief)
	if err != nil {
		return nil, err
	}
	return string(raw), nil
}

func decodeTaskBrief(raw sql.NullString) (*domain.TaskBrief, error) {
	if !raw.Valid || raw.String == "" || raw.String == "null" {
		return nil, nil
	}
	var brief domain.TaskBrief
	if err := json.Unmarshal([]byte(raw.String), &brief); err != nil {
		return nil, fmt.Errorf("decode task brief: %w", err)
	}
	if err := domain.ValidateTaskBrief(brief); err != nil {
		return nil, fmt.Errorf("invalid stored task brief: %w", err)
	}
	return &brief, nil
}

// saveTaskBriefRevision runs in the same transaction as its proposal. The
// workspace is checked even for legacy clients, which may omit Brief entirely.
func saveTaskBriefRevision(ctx context.Context, tx *sql.Tx, proposal domain.QuestProposal) error {
	var owner string
	var currentJSON sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT workspace_id,brief_json FROM quest_proposals WHERE id=?`, proposal.ID).Scan(&owner, &currentJSON)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && owner != proposal.WorkspaceID {
		return fmt.Errorf("quest proposal %q belongs to another workspace", proposal.ID)
	}
	if proposal.Brief == nil {
		return nil
	}
	current, err := decodeTaskBrief(currentJSON)
	if err != nil {
		return err
	}
	brief := *proposal.Brief
	digest := domain.TaskBriefDigest(brief)
	if current == nil {
		if brief.Version != 1 {
			return fmt.Errorf("initial task brief version must be 1")
		}
	} else {
		if brief.Version < current.Version || brief.Version > current.Version+1 {
			return fmt.Errorf("task brief version must remain current or increment by one")
		}
		if brief.Version == current.Version && digest != domain.TaskBriefDigest(*current) {
			return fmt.Errorf("task brief content changed without a new version")
		}
	}
	var previousDigest, previousOwner string
	err = tx.QueryRowContext(ctx, `SELECT digest,workspace_id FROM task_brief_revisions WHERE proposal_id=? AND version=?`, proposal.ID, brief.Version).Scan(&previousDigest, &previousOwner)
	if err == nil {
		if previousDigest != digest || previousOwner != proposal.WorkspaceID {
			return fmt.Errorf("task brief revision is immutable")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// Content history is deliberately separate from current approval/execution
	// state: approving the same definition must not rewrite its original record.
	brief.ApprovedVersion, brief.ApprovedDigest, brief.State = 0, "", "discussion"
	raw, err := json.Marshal(brief)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO task_brief_revisions(proposal_id,workspace_id,version,digest,brief_json,created_at) VALUES(?,?,?,?,?,?)`, proposal.ID, proposal.WorkspaceID, brief.Version, digest, string(raw), formatTime(time.Now().UTC()))
	return err
}

// ListTaskBriefRevisions never leaks the existence or content of a proposal in
// another workspace. Callers must use the workspace from their trusted context.
func (s *SQLite) ListTaskBriefRevisions(ctx context.Context, workspaceID, proposalID string) ([]domain.TaskBriefRevision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT h.proposal_id,h.workspace_id,h.version,h.digest,h.brief_json,h.created_at
 FROM task_brief_revisions h JOIN quest_proposals p ON p.id=h.proposal_id AND p.workspace_id=h.workspace_id
 WHERE h.workspace_id=? AND h.proposal_id=? ORDER BY h.version`, workspaceID, proposalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.TaskBriefRevision{}
	for rows.Next() {
		var revision domain.TaskBriefRevision
		var raw, created string
		if err = rows.Scan(&revision.ProposalID, &revision.WorkspaceID, &revision.Version, &revision.Digest, &raw, &created); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &revision.Brief); err != nil {
			return nil, err
		}
		if err = domain.ValidateTaskBrief(revision.Brief); err != nil {
			return nil, err
		}
		if revision.Digest != domain.TaskBriefDigest(revision.Brief) {
			return nil, fmt.Errorf("task brief history digest mismatch")
		}
		revision.CreatedAt = parseTime(created)
		result = append(result, revision)
	}
	return result, rows.Err()
}
