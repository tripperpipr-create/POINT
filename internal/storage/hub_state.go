package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"local-agent-workbench/internal/domain"
)

func (s *SQLite) SaveMemory(ctx context.Context, memory domain.MemoryRecord) error {
	var existingWorkspace string
	err := s.db.QueryRowContext(ctx, `SELECT workspace_id FROM memories WHERE id=?`, memory.ID).Scan(&existingWorkspace)
	if err == nil && existingWorkspace != memory.WorkspaceID {
		return fmt.Errorf("memory %q belongs to another workspace", memory.ID)
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	pinned := 0
	if memory.Pinned {
		pinned = 1
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO memories(id,workspace_id,kind,owner_id,content,source,confidence,pinned,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET content=excluded.content, source=excluded.source, confidence=excluded.confidence,
	  kind=excluded.kind, owner_id=excluded.owner_id, pinned=excluded.pinned, updated_at=excluded.updated_at`,
		memory.ID, memory.WorkspaceID, memory.Kind, memory.OwnerID, memory.Content, memory.Source, memory.Confidence,
		pinned, formatTime(memory.CreatedAt), formatTime(memory.UpdatedAt))
	return err
}

func (s *SQLite) GetMemory(ctx context.Context, id string) (domain.MemoryRecord, error) {
	var memory domain.MemoryRecord
	var pinned int
	var created, updated string
	err := s.db.QueryRowContext(ctx, `
SELECT id,workspace_id,kind,owner_id,content,source,confidence,pinned,created_at,updated_at
FROM memories WHERE id=?`, id).Scan(&memory.ID, &memory.WorkspaceID, &memory.Kind, &memory.OwnerID, &memory.Content,
		&memory.Source, &memory.Confidence, &pinned, &created, &updated)
	if err != nil {
		return memory, err
	}
	memory.Pinned = pinned == 1
	memory.CreatedAt, memory.UpdatedAt = parseTime(created), parseTime(updated)
	return memory, nil
}

func (s *SQLite) ListMemories(ctx context.Context, workspaceID string) ([]domain.MemoryRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id,workspace_id,kind,owner_id,content,source,confidence,pinned,created_at,updated_at
FROM memories WHERE workspace_id=? OR workspace_id='' ORDER BY pinned DESC, updated_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.MemoryRecord
	for rows.Next() {
		var memory domain.MemoryRecord
		var created, updated string
		var pinned int
		if err = rows.Scan(&memory.ID, &memory.WorkspaceID, &memory.Kind, &memory.OwnerID, &memory.Content,
			&memory.Source, &memory.Confidence, &pinned, &created, &updated); err != nil {
			return nil, err
		}
		memory.Pinned = pinned == 1
		memory.CreatedAt, memory.UpdatedAt = parseTime(created), parseTime(updated)
		result = append(result, memory)
	}
	return result, rows.Err()
}

func (s *SQLite) DeleteMemory(ctx context.Context, workspaceID, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM memories WHERE workspace_id=? AND id=?`, workspaceID, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLite) SaveConnection(ctx context.Context, conn domain.Connection) error {
	var probed any
	if conn.LastProbeAt != nil {
		probed = formatTime(*conn.LastProbeAt)
	}
	var catalogUpdated any
	if conn.CatalogUpdatedAt != nil {
		catalogUpdated = formatTime(*conn.CatalogUpdatedAt)
	}
	catalog := ""
	if len(conn.Models) > 0 {
		encoded, err := json.Marshal(conn.Models)
		if err != nil {
			return err
		}
		catalog = string(encoded)
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO connections(id,provider,preset_id,display_name,base_url,status,secret_ref,last_error,last_probe_at,
  default_model,is_default,api_version,model_catalog_json,catalog_updated_at,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET display_name=excluded.display_name, base_url=excluded.base_url, status=excluded.status,
  secret_ref=excluded.secret_ref, last_error=excluded.last_error, last_probe_at=excluded.last_probe_at,
  default_model=excluded.default_model, is_default=excluded.is_default, api_version=excluded.api_version,
  model_catalog_json=excluded.model_catalog_json, catalog_updated_at=excluded.catalog_updated_at,
  updated_at=excluded.updated_at`,
		conn.ID, conn.Provider, conn.PresetID, conn.DisplayName, conn.BaseURL, conn.Status, conn.SecretRef,
		conn.LastError, probed, conn.DefaultModel, boolToInt(conn.IsDefault), conn.APIVersion, catalog, catalogUpdated,
		formatTime(conn.CreatedAt), formatTime(conn.UpdatedAt))
	return err
}

// Подключение по умолчанию ровно одно: снятие флага у остальных выполняется той
// же транзакцией, иначе два «по умолчанию» жили бы одновременно и выбор снова
// стал бы угадыванием.
func (s *SQLite) SetDefaultConnection(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE connections SET is_default=1 WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("подключение не найдено")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE connections SET is_default=0 WHERE id<>?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLite) DeleteConnection(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM connections WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("подключение не найдено")
	}
	return nil
}

// ConnectionUsage перечисляет, кто ссылается на подключение. Удаление без этой
// проверки тихо ломало бы следующий квест агента: связь исчезла бы, а профиль
// продолжал бы утверждать, что настроен.
func (s *SQLite) ConnectionUsage(ctx context.Context, id string) ([]string, error) {
	var users []string
	for _, source := range []struct{ table, column, label string }{
		{"agent_blueprints", "name", "основной профиль"},
		{"project_agents", "name", "проектный агент"},
		{"companion_config", "preset", "компаньон"},
		{"orchestrator_config", "preset", "Мастер"},
	} {
		rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT %s FROM %s WHERE connection_id=?`, source.column, source.table), id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var name string
			if err = rows.Scan(&name); err != nil {
				rows.Close()
				return nil, err
			}
			users = append(users, source.label+" «"+name+"»")
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	for _, route := range []struct {
		column string
		label  string
	}{
		{"coding_connection_id", "coding-маршрут workspace"},
		{"cheap_connection_id", "cheap-маршрут workspace"},
	} {
		rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT workspace_id FROM workspace_model_routing WHERE %s=?`, route.column), id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var workspaceID string
			if err = rows.Scan(&workspaceID); err != nil {
				rows.Close()
				return nil, err
			}
			users = append(users, route.label+" «"+workspaceID+"»")
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return users, nil
}

func (s *SQLite) ListConnections(ctx context.Context) ([]domain.Connection, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id,provider,preset_id,display_name,base_url,status,secret_ref,last_error,last_probe_at,
  default_model,is_default,api_version,model_catalog_json,catalog_updated_at,created_at,updated_at
FROM connections ORDER BY is_default DESC, updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Connection
	for rows.Next() {
		var conn domain.Connection
		var created, updated, catalog string
		var isDefault int
		var probed, catalogUpdated sql.NullString
		if err = rows.Scan(&conn.ID, &conn.Provider, &conn.PresetID, &conn.DisplayName, &conn.BaseURL, &conn.Status,
			&conn.SecretRef, &conn.LastError, &probed, &conn.DefaultModel, &isDefault, &conn.APIVersion,
			&catalog, &catalogUpdated, &created, &updated); err != nil {
			return nil, err
		}
		conn.CreatedAt, conn.UpdatedAt = parseTime(created), parseTime(updated)
		conn.IsDefault = isDefault != 0
		if probed.Valid && probed.String != "" {
			t := parseTime(probed.String)
			conn.LastProbeAt = &t
		}
		if catalogUpdated.Valid && catalogUpdated.String != "" {
			t := parseTime(catalogUpdated.String)
			conn.CatalogUpdatedAt = &t
		}
		if catalog != "" {
			// Испорченный кэш каталога — не повод не отдать подключение:
			// список моделей восстановится следующей проверкой.
			_ = json.Unmarshal([]byte(catalog), &conn.Models)
		}
		result = append(result, conn)
	}
	return result, rows.Err()
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *SQLite) InsertUsageRecord(ctx context.Context, record domain.UsageRecord) error {
	var cost any
	if record.CostCents != nil {
		cost = *record.CostCents
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO usage_records(id,workspace_id,execution_id,quest_id,project_agent_id,provider,model,input_tokens,output_tokens,total_tokens,cost_cents,latency_ms,outcome,created_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		record.ID, record.WorkspaceID, record.ExecutionID, record.QuestID, record.ProjectAgentID, record.Provider,
		record.Model, record.InputTokens, record.OutputTokens, record.TotalTokens, cost, record.LatencyMs,
		record.Outcome, formatTime(record.CreatedAt))
	return err
}

func (s *SQLite) ListUsageRecords(ctx context.Context, workspaceID string, limit int) ([]domain.UsageRecord, error) {
	if limit <= 0 || limit > 5000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id,workspace_id,execution_id,quest_id,project_agent_id,provider,model,input_tokens,output_tokens,total_tokens,cost_cents,latency_ms,outcome,created_at
FROM usage_records WHERE workspace_id=? ORDER BY created_at DESC LIMIT ?`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.UsageRecord
	for rows.Next() {
		var record domain.UsageRecord
		var created string
		var cost sql.NullInt64
		if err = rows.Scan(&record.ID, &record.WorkspaceID, &record.ExecutionID, &record.QuestID, &record.ProjectAgentID,
			&record.Provider, &record.Model, &record.InputTokens, &record.OutputTokens, &record.TotalTokens, &cost,
			&record.LatencyMs, &record.Outcome, &created); err != nil {
			return nil, err
		}
		if cost.Valid {
			value := cost.Int64
			record.CostCents = &value
		}
		record.CreatedAt = parseTime(created)
		result = append(result, record)
	}
	return result, rows.Err()
}

func (s *SQLite) SaveOrchestratorConfig(ctx context.Context, cfg domain.OrchestratorConfig) error {
	result, err := s.db.ExecContext(ctx, `
INSERT INTO orchestrator_config(id,workspace_id,preset,connection_id,provider,provider_preset,base_url,api_version,model,temperature,max_output_tokens,planning_depth,parallelism,approval_strictness,team_preference,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET preset=excluded.preset, connection_id=excluded.connection_id, provider=excluded.provider, provider_preset=excluded.provider_preset,
  base_url=excluded.base_url, api_version=excluded.api_version, model=excluded.model, temperature=excluded.temperature, max_output_tokens=excluded.max_output_tokens,
  planning_depth=excluded.planning_depth, parallelism=excluded.parallelism, approval_strictness=excluded.approval_strictness,
  team_preference=excluded.team_preference, updated_at=excluded.updated_at
WHERE orchestrator_config.workspace_id=excluded.workspace_id`,
		cfg.ID, cfg.WorkspaceID, cfg.Preset, cfg.ConnectionID, cfg.Provider, cfg.ProviderPreset, cfg.BaseURL, cfg.APIVersion, cfg.Model, cfg.Temperature, cfg.MaxOutputTokens,
		cfg.PlanningDepth, cfg.Parallelism, cfg.ApprovalStrictness, cfg.TeamPreference, formatTime(cfg.CreatedAt), formatTime(cfg.UpdatedAt))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("orchestrator config %q belongs to another workspace", cfg.ID)
	}
	return nil
}

func (s *SQLite) GetOrchestratorConfig(ctx context.Context, workspaceID string) (domain.OrchestratorConfig, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id,workspace_id,preset,connection_id,provider,provider_preset,base_url,api_version,model,temperature,max_output_tokens,planning_depth,parallelism,approval_strictness,team_preference,created_at,updated_at
FROM orchestrator_config WHERE workspace_id=? ORDER BY updated_at DESC LIMIT 1`, workspaceID)
	var cfg domain.OrchestratorConfig
	var created, updated string
	if err := row.Scan(&cfg.ID, &cfg.WorkspaceID, &cfg.Preset, &cfg.ConnectionID, &cfg.Provider, &cfg.ProviderPreset, &cfg.BaseURL, &cfg.APIVersion, &cfg.Model,
		&cfg.Temperature, &cfg.MaxOutputTokens, &cfg.PlanningDepth, &cfg.Parallelism, &cfg.ApprovalStrictness, &cfg.TeamPreference,
		&created, &updated); err != nil {
		return domain.OrchestratorConfig{}, err
	}
	cfg.CreatedAt, cfg.UpdatedAt = parseTime(created), parseTime(updated)
	return cfg, nil
}

func (s *SQLite) SaveWorkspaceModelRouting(ctx context.Context, routing domain.WorkspaceModelRouting) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO workspace_model_routing(workspace_id,coding_connection_id,coding_model,cheap_connection_id,cheap_model,updated_at)
VALUES(?,?,?,?,?,?)
ON CONFLICT(workspace_id) DO UPDATE SET
  coding_connection_id=excluded.coding_connection_id, coding_model=excluded.coding_model,
  cheap_connection_id=excluded.cheap_connection_id, cheap_model=excluded.cheap_model,
  updated_at=excluded.updated_at`,
		routing.WorkspaceID, routing.CodingConnectionID, routing.CodingModel,
		routing.CheapConnectionID, routing.CheapModel, formatTime(routing.UpdatedAt))
	return err
}

func (s *SQLite) GetWorkspaceModelRouting(ctx context.Context, workspaceID string) (domain.WorkspaceModelRouting, error) {
	var routing domain.WorkspaceModelRouting
	var updated string
	err := s.db.QueryRowContext(ctx, `
SELECT workspace_id,coding_connection_id,coding_model,cheap_connection_id,cheap_model,updated_at
FROM workspace_model_routing WHERE workspace_id=?`, workspaceID).
		Scan(&routing.WorkspaceID, &routing.CodingConnectionID, &routing.CodingModel,
			&routing.CheapConnectionID, &routing.CheapModel, &updated)
	if err != nil {
		return domain.WorkspaceModelRouting{}, err
	}
	routing.UpdatedAt = parseTime(updated)
	return routing, nil
}
