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
