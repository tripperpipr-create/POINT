package storage

import (
	"context"
	"encoding/json"
	"time"

	"local-agent-workbench/internal/domain"
)

func (s *SQLite) SaveModelCapabilityEvidence(ctx context.Context, item domain.ModelCapabilityEvidence) error {
	capabilities, _ := json.Marshal(item.Capabilities)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO model_capability_evidence(
  id, connection_id, model, provider, runtime, role, capabilities_json,
  tool_calls, json_contract, inspection_before_edit, verification_evidence,
  within_limits, latency_ms, input_tokens, output_tokens, healthy, created_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		item.ID, item.ConnectionID, item.Model, item.Provider, item.Runtime, item.Role, string(capabilities),
		item.ToolCalls, item.JSONContract, item.InspectionBeforeEdit, item.VerificationEvidence,
		item.WithinLimits, item.LatencyMs, item.InputTokens, item.OutputTokens, item.Healthy,
		item.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLite) ListModelCapabilityEvidence(ctx context.Context, connectionID, model string, limit int) ([]domain.ModelCapabilityEvidence, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, connection_id, model, provider, runtime, role, capabilities_json,
       tool_calls, json_contract, inspection_before_edit, verification_evidence,
       within_limits, latency_ms, input_tokens, output_tokens, healthy, created_at
FROM model_capability_evidence
WHERE (?='' OR connection_id=?) AND (?='' OR model=?)
ORDER BY created_at DESC LIMIT ?`, connectionID, connectionID, model, model, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.ModelCapabilityEvidence
	for rows.Next() {
		var item domain.ModelCapabilityEvidence
		var capabilities, created string
		if err = rows.Scan(
			&item.ID, &item.ConnectionID, &item.Model, &item.Provider, &item.Runtime, &item.Role, &capabilities,
			&item.ToolCalls, &item.JSONContract, &item.InspectionBeforeEdit, &item.VerificationEvidence,
			&item.WithinLimits, &item.LatencyMs, &item.InputTokens, &item.OutputTokens, &item.Healthy, &created,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(capabilities), &item.Capabilities)
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		result = append(result, item)
	}
	return result, rows.Err()
}
