package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

func (s *SQLite) SaveModelCertificationV2(ctx context.Context, certification domain.ModelCertification) error {
	certification.ID = strings.TrimSpace(certification.ID)
	certification.ConnectionID = strings.TrimSpace(certification.ConnectionID)
	certification.Model = strings.TrimSpace(certification.Model)
	if certification.ID == "" || certification.ConnectionID == "" || certification.Model == "" {
		return errors.New("certification id, connection and model are required")
	}
	if certification.Status != "certified" && certification.Status != "experimental" {
		return errors.New("certification status must be certified or experimental")
	}
	if certification.RequiredRuns < 1 || certification.PassedRuns < 0 || certification.PassedRuns > certification.RequiredRuns {
		return errors.New("invalid certification run matrix")
	}
	if certification.Status == "certified" && (certification.PassedRuns != certification.RequiredRuns || certification.RequiredRuns < 3 || !certification.CostKnown) {
		return errors.New("certified status requires a complete matrix and known cost")
	}
	if certification.LastEvaluatedAt.IsZero() {
		certification.LastEvaluatedAt = time.Now().UTC()
	}
	if certification.Status == "certified" && certification.CertifiedAt == nil {
		value := certification.LastEvaluatedAt
		certification.CertifiedAt = &value
	}
	raw, err := json.Marshal(certification)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO model_certifications_v2(id,connection_id,model,status,matrix_version,payload_json,evaluated_at) VALUES(?,?,?,?,?,?,?)`,
		certification.ID, certification.ConnectionID, certification.Model, certification.Status, certification.MatrixVersion, string(raw), formatTime(certification.LastEvaluatedAt))
	return err
}

func (s *SQLite) ListModelCertificationsV2(ctx context.Context, connectionID, model string) ([]domain.ModelCertification, error) {
	connectionID, model = strings.TrimSpace(connectionID), strings.TrimSpace(model)
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM model_certifications_v2 WHERE (?='' OR connection_id=?) AND (?='' OR model=?) ORDER BY evaluated_at DESC`, connectionID, connectionID, model, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.ModelCertification{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item domain.ModelCertification
		if err = json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
