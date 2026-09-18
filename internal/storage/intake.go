package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"local-agent-workbench/internal/domain"
)

func (s *SQLite) SaveSourceBundle(ctx context.Context, bundle domain.SourceBundle) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO source_bundles(id,primary_url,kind,digest,payload_json,created_at) VALUES(?,?,?,?,?,?)`,
		bundle.ID, bundle.PrimaryURL, bundle.Kind, bundle.Digest, marshalJSON(bundle), formatTime(bundle.CreatedAt))
	return err
}

func (s *SQLite) GetSourceBundle(ctx context.Context, id string) (domain.SourceBundle, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM source_bundles WHERE id=?`, id).Scan(&raw); err != nil {
		return domain.SourceBundle{}, err
	}
	var bundle domain.SourceBundle
	unmarshalJSON(raw, &bundle)
	return bundle, nil
}

func (s *SQLite) SaveIntakeSession(ctx context.Context, item domain.IntakeSession) error {
	brief, err := taskBriefJSON(item.Brief)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO intake_sessions(id,workspace_id,quest_id,proposal_id,source_bundle_id,url,status,environment_json,requirements_json,coverage_json,brief_json,delivery_json,blockers_json,error,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET workspace_id=excluded.workspace_id,quest_id=excluded.quest_id,proposal_id=excluded.proposal_id,status=excluded.status,
 environment_json=excluded.environment_json,requirements_json=excluded.requirements_json,coverage_json=excluded.coverage_json,
 brief_json=excluded.brief_json,delivery_json=excluded.delivery_json,blockers_json=excluded.blockers_json,error=excluded.error,updated_at=excluded.updated_at
WHERE intake_sessions.source_bundle_id=excluded.source_bundle_id AND intake_sessions.url=excluded.url`,
		item.ID, item.WorkspaceID, item.QuestID, item.ProposalID, item.Source.ID, item.URL, item.Status, marshalJSON(item.Environment),
		marshalJSON(item.Requirements), marshalJSON(item.Coverage), brief, marshalJSON(item.Delivery), marshalJSON(item.Blockers),
		item.Error, formatTime(item.CreatedAt), formatTime(item.UpdatedAt))
	return err
}

func (s *SQLite) GetIntakeSession(ctx context.Context, id string) (domain.IntakeSession, error) {
	item, err := s.scanIntakeSession(ctx, `SELECT id,workspace_id,quest_id,proposal_id,source_bundle_id,url,status,environment_json,requirements_json,coverage_json,brief_json,delivery_json,blockers_json,error,created_at,updated_at FROM intake_sessions WHERE id=?`, id)
	if err != nil {
		return domain.IntakeSession{}, err
	}
	s.attachEvidence(ctx, &item)
	return item, nil
}

func (s *SQLite) FindIntakeSessionByQuestID(ctx context.Context, questID string) (domain.IntakeSession, error) {
	if strings.TrimSpace(questID) == "" {
		return domain.IntakeSession{}, fmt.Errorf("quest id is required")
	}
	item, err := s.scanIntakeSession(ctx, `SELECT id,workspace_id,quest_id,proposal_id,source_bundle_id,url,status,environment_json,requirements_json,coverage_json,brief_json,delivery_json,blockers_json,error,created_at,updated_at FROM intake_sessions WHERE quest_id=? ORDER BY updated_at DESC LIMIT 1`, questID)
	if err != nil {
		return domain.IntakeSession{}, err
	}
	s.attachEvidence(ctx, &item)
	return item, nil
}

func (s *SQLite) scanIntakeSession(ctx context.Context, query string, arg string) (domain.IntakeSession, error) {
	var item domain.IntakeSession
	var sourceID, environment, requirements, coverage, delivery, blockers, created, updated string
	var brief sql.NullString
	err := s.db.QueryRowContext(ctx, query, arg).
		Scan(&item.ID, &item.WorkspaceID, &item.QuestID, &item.ProposalID, &sourceID, &item.URL, &item.Status, &environment, &requirements, &coverage, &brief, &delivery, &blockers, &item.Error, &created, &updated)
	if err != nil {
		return domain.IntakeSession{}, err
	}
	item.Source, err = s.GetSourceBundle(ctx, sourceID)
	if err != nil {
		return domain.IntakeSession{}, err
	}
	item.Brief, err = decodeTaskBrief(brief)
	if err != nil {
		return domain.IntakeSession{}, err
	}
	unmarshalJSON(environment, &item.Environment)
	unmarshalJSON(requirements, &item.Requirements)
	unmarshalJSON(coverage, &item.Coverage)
	unmarshalJSON(delivery, &item.Delivery)
	unmarshalJSON(blockers, &item.Blockers)
	item.CreatedAt, item.UpdatedAt = parseTime(created), parseTime(updated)
	return item, nil
}

func (s *SQLite) attachEvidence(ctx context.Context, item *domain.IntakeSession) {
	if item == nil || item.QuestID == "" {
		return
	}
	bundle, err := s.GetEvidenceBundle(ctx, item.QuestID)
	if err == nil {
		item.Evidence = &bundle
	}
}

func (s *SQLite) ListIntakeSessions(ctx context.Context, workspaceID string) ([]domain.IntakeSession, error) {
	query, args := `SELECT id FROM intake_sessions ORDER BY updated_at DESC`, []any{}
	if workspaceID != "" {
		query, args = `SELECT id FROM intake_sessions WHERE workspace_id=? ORDER BY updated_at DESC`, []any{workspaceID}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	result := make([]domain.IntakeSession, 0, len(ids))
	for _, id := range ids {
		item, getErr := s.GetIntakeSession(ctx, id)
		if getErr != nil {
			return nil, getErr
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *SQLite) SaveQuestToolLease(ctx context.Context, lease domain.QuestToolLease) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO quest_tool_leases(id,quest_id,workspace_id,environment_digest,tool_names_json,expires_at) VALUES(?,?,?,?,?,?)`,
		lease.ID, lease.QuestID, lease.WorkspaceID, lease.EnvironmentDigest, marshalJSON(lease.ToolNames), formatTime(lease.ExpiresAt))
	return err
}

func (s *SQLite) GetLatestQuestToolLease(ctx context.Context, questID string) (domain.QuestToolLease, error) {
	var lease domain.QuestToolLease
	var tools, expires string
	err := s.db.QueryRowContext(ctx, `SELECT id,quest_id,workspace_id,environment_digest,tool_names_json,expires_at FROM quest_tool_leases WHERE quest_id=? ORDER BY expires_at DESC LIMIT 1`, questID).
		Scan(&lease.ID, &lease.QuestID, &lease.WorkspaceID, &lease.EnvironmentDigest, &tools, &expires)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.QuestToolLease{}, fmt.Errorf("tool lease for quest %q: %w", questID, err)
		}
		return domain.QuestToolLease{}, err
	}
	unmarshalJSON(tools, &lease.ToolNames)
	lease.ExpiresAt = parseTime(expires)
	return lease, nil
}

func (s *SQLite) SaveEvidenceBundle(ctx context.Context, bundle domain.EvidenceBundle) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO evidence_bundles(id,quest_id,payload_json,created_at) VALUES(?,?,?,?)`,
		bundle.ID, bundle.QuestID, marshalJSON(bundle), formatTime(bundle.CreatedAt))
	return err
}

func (s *SQLite) GetEvidenceBundle(ctx context.Context, questID string) (domain.EvidenceBundle, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM evidence_bundles WHERE quest_id=?`, questID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.EvidenceBundle{}, fmt.Errorf("evidence for quest %q: %w", questID, err)
		}
		return domain.EvidenceBundle{}, err
	}
	var bundle domain.EvidenceBundle
	unmarshalJSON(raw, &bundle)
	return bundle, nil
}
