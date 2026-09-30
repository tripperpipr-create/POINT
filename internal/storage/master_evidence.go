package storage

import (
	"context"
	"encoding/json"
	"local-agent-workbench/internal/domain"
)

func (s *SQLite) SaveMasterEvidence(ctx context.Context, v domain.MasterEvidenceSignal) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO master_evidence_signals VALUES(?,?,?,?,?)`, v.ID, v.WorkspaceID, v.ProposalID, v.QuestID, marshalJSON(v))
	return err
}
func (s *SQLite) MasterEvidence(ctx context.Context, op domain.MasterOperation) ([]domain.MasterEvidenceSignal, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM master_evidence_signals WHERE workspace_id=? AND ((proposal_id!='' AND proposal_id=?) OR (quest_id!='' AND quest_id=?)) ORDER BY rowid DESC LIMIT 20`, op.WorkspaceID, op.ProposalID, op.QuestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.MasterEvidenceSignal
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var v domain.MasterEvidenceSignal
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

// PendingMasterRevisionNotes returns human revisions of work orders from one
// conversation that the Master has not yet been told about.
func (s *SQLite) PendingMasterRevisionNotes(ctx context.Context, ws, conversationID string) ([]domain.MasterEvidenceSignal, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM master_evidence_signals s WHERE workspace_id=? AND json_extract(payload,'$.kind')='revision' AND json_extract(payload,'$.outcome')='user_revised' AND json_extract(payload,'$.conversationId')=? AND NOT EXISTS(SELECT 1 FROM master_evidence_signals n WHERE n.id=s.id||'-noted') ORDER BY rowid LIMIT 5`, ws, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.MasterEvidenceSignal
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var v domain.MasterEvidenceSignal
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

// MasterEvidenceProposals returns the proposals of one world that received a
// given signal. One query per canary pass instead of one per operation.
func (s *SQLite) MasterEvidenceProposals(ctx context.Context, ws, kind, outcome string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT proposal_id FROM master_evidence_signals WHERE workspace_id=? AND proposal_id!='' AND json_extract(payload,'$.kind')=? AND json_extract(payload,'$.outcome')=?`, ws, kind, outcome)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}
