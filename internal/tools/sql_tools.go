package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"local-agent-workbench/internal/dbconn"
	"local-agent-workbench/internal/domain"
)

// DBConnectionSource loads saved DB profiles and opens them for agent tools.
type DBConnectionSource interface {
	List(ctx context.Context) ([]domain.DBConnection, error)
	OpenConfig(ctx context.Context, id string, readOnly bool) (domain.DBConnection, dbconn.OpenConfig, error)
	NetworkAllowed(host string, allowed []string, policy string) bool
}

// DBToolConfig is shared by database agent tools.
type DBToolConfig struct {
	Source              DBConnectionSource
	NetworkPolicy       string
	AllowedNetworkHosts []string
}

func (c DBToolConfig) ensureSource() *domain.ToolResult {
	if c.Source == nil {
		result := FailWithHint("db_unavailable", "каталог подключений к БД недоступен", "сохраните профиль в Гильдии → Базы данных")
		return &result
	}
	return nil
}

func (c DBToolConfig) guardNetwork(conn domain.DBConnection) *domain.ToolResult {
	host := dbconn.HostForPolicy(conn)
	if host == "" || dbconn.IsLoopbackHost(host) {
		return nil
	}
	if c.Source != nil && c.Source.NetworkAllowed(host, c.AllowedNetworkHosts, c.NetworkPolicy) {
		return nil
	}
	policy := strings.ToUpper(strings.TrimSpace(c.NetworkPolicy))
	if policy == "" || policy == "ALLOW" || policy == "ASK" {
		return nil
	}
	if networkHostAllowed(host, c.AllowedNetworkHosts) {
		return nil
	}
	result := FailWithHint("network_denied",
		fmt.Sprintf("исходящее подключение к БД %q запрещено сетевой политикой агента", host),
		"разрешите хост через toolPolicies network:<host>=ALLOW")
	return &result
}

// DBListConnections lists saved database profiles (no secrets).
type DBListConnections struct{ Config DBToolConfig }

func (t DBListConnections) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "db_list_connections",
		Description: "Список сохранённых подключений к БД (sqlite/postgres/mysql) без паролей и секретов.",
		InputSchema: schema(`{"type":"object","properties":{"reason":{"type":"string"}},"required":["reason"],"additionalProperties":false}`),
	}
}

func (t DBListConnections) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	if bad := t.Config.ensureSource(); bad != nil {
		return *bad
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if bad := Decode(raw, &input); bad != nil {
		return *bad
	}
	if strings.TrimSpace(input.Reason) == "" {
		return Fail("invalid_input", "reason is required")
	}
	items, err := t.Config.Source.List(ctx)
	if err != nil {
		return Fail("db_error", err.Error())
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"id": item.ID, "displayName": item.DisplayName, "driver": item.Driver,
			"host": item.Host, "port": item.Port, "database": item.Database,
			"username": item.Username, "status": item.Status, "readOnlyDefault": item.ReadOnlyDefault,
			"hasSecret": item.SecretRef != "",
		})
	}
	return OK(map[string]any{"connections": out, "supportedDrivers": domain.SupportedDBDrivers()})
}

// DBSchema introspects tables/columns for a saved connection.
type DBSchema struct{ Config DBToolConfig }

func (t DBSchema) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "db_schema",
		Description: "Читает схему (таблицы/колонки) сохранённого подключения к БД. Только чтение.",
		InputSchema: schema(`{"type":"object","properties":{"connectionId":{"type":"string"},"reason":{"type":"string"}},"required":["connectionId","reason"],"additionalProperties":false}`),
	}
}

func (t DBSchema) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	if bad := t.Config.ensureSource(); bad != nil {
		return *bad
	}
	var input struct {
		ConnectionID, Reason string
	}
	if bad := Decode(raw, &input); bad != nil {
		return *bad
	}
	if strings.TrimSpace(input.Reason) == "" {
		return Fail("invalid_input", "reason is required")
	}
	conn, cfg, err := t.Config.Source.OpenConfig(ctx, input.ConnectionID, true)
	if err != nil {
		return Fail("db_error", err.Error())
	}
	if bad := t.Config.guardNetwork(conn); bad != nil {
		return *bad
	}
	db, err := dbconn.Open(ctx, cfg)
	if err != nil {
		return FailWithHint("db_error", err.Error(), "проверьте профиль и разблокируйте пароль через проверку подключения в Гильдии")
	}
	defer db.Close()
	summary, err := dbconn.InspectSchema(ctx, db, conn.Driver, 100)
	if err != nil {
		return Fail("db_error", err.Error())
	}
	return OK(summary)
}

// DBQuery runs a read-only SQL query against a saved connection.
type DBQuery struct{ Config DBToolConfig }

func (t DBQuery) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "db_query",
		Description: "Выполняет только читающий SQL (SELECT/SHOW/EXPLAIN/…) по сохранённому подключению. Запись запрещена — используйте db_exec.",
		InputSchema: schema(`{"type":"object","properties":{"connectionId":{"type":"string"},"sql":{"type":"string"},"maxRows":{"type":"integer"},"reason":{"type":"string"}},"required":["connectionId","sql","reason"],"additionalProperties":false}`),
	}
}

func (t DBQuery) ValidateArguments(raw json.RawMessage) *domain.ToolResult {
	var input struct {
		SQL string `json:"sql"`
	}
	if bad := Decode(raw, &input); bad != nil {
		return bad
	}
	kind := dbconn.ClassifySQL(input.SQL)
	if kind != dbconn.StatementRead {
		result := FailWithHint("write_blocked", "db_query принимает только читающий SQL", "для INSERT/UPDATE/DELETE используйте db_exec после подтверждения")
		return &result
	}
	return nil
}

func (t DBQuery) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	if bad := t.Config.ensureSource(); bad != nil {
		return *bad
	}
	var input struct {
		ConnectionID, SQL, Reason string
		MaxRows                   int
	}
	if bad := Decode(raw, &input); bad != nil {
		return *bad
	}
	if strings.TrimSpace(input.Reason) == "" {
		return Fail("invalid_input", "reason is required")
	}
	if invalid := t.ValidateArguments(raw); invalid != nil {
		return *invalid
	}
	conn, cfg, err := t.Config.Source.OpenConfig(ctx, input.ConnectionID, true)
	if err != nil {
		return Fail("db_error", err.Error())
	}
	if bad := t.Config.guardNetwork(conn); bad != nil {
		return *bad
	}
	db, err := dbconn.Open(ctx, cfg)
	if err != nil {
		return Fail("db_error", err.Error())
	}
	defer db.Close()
	result, err := dbconn.RunQuery(ctx, db, dbconn.QueryRequest{SQL: input.SQL, AllowWrite: false, MaxRows: input.MaxRows})
	if err != nil {
		return Fail("db_error", err.Error())
	}
	return OK(result)
}

// DBExec runs mutating SQL only after policy approval (always ASK/Critical).
type DBExec struct{ Config DBToolConfig }

func (t DBExec) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        "db_exec",
		Description: "Выполняет записывающий SQL (INSERT/UPDATE/DELETE/DDL) по сохранённому подключению. Всегда требует явного подтверждения пользователя.",
		InputSchema: schema(`{"type":"object","properties":{"connectionId":{"type":"string"},"sql":{"type":"string"},"reason":{"type":"string"}},"required":["connectionId","sql","reason"],"additionalProperties":false}`),
	}
}

func (t DBExec) ApprovalArguments(raw json.RawMessage) json.RawMessage {
	var input struct {
		ConnectionID, SQL, Reason string
	}
	_ = json.Unmarshal(raw, &input)
	preview, _ := json.Marshal(map[string]any{
		"connectionId": input.ConnectionID,
		"sql":          input.SQL,
		"reason":       input.Reason,
		"warning":      "Запись в БД необратима через Change Set sandbox — подтвердите только если уверены.",
	})
	return preview
}

func (t DBExec) Execute(ctx context.Context, raw json.RawMessage) domain.ToolResult {
	if bad := t.Config.ensureSource(); bad != nil {
		return *bad
	}
	var input struct {
		ConnectionID, SQL, Reason string
	}
	if bad := Decode(raw, &input); bad != nil {
		return *bad
	}
	if strings.TrimSpace(input.Reason) == "" {
		return Fail("invalid_input", "reason is required")
	}
	kind := dbconn.ClassifySQL(input.SQL)
	if kind == dbconn.StatementRead {
		return FailWithHint("use_db_query", "для чтения используйте db_query", "db_exec предназначен только для записи")
	}
	if kind == dbconn.StatementUnknown {
		return Fail("invalid_input", "не удалось классифицировать SQL")
	}
	conn, cfg, err := t.Config.Source.OpenConfig(ctx, input.ConnectionID, false)
	if err != nil {
		return Fail("db_error", err.Error())
	}
	if bad := t.Config.guardNetwork(conn); bad != nil {
		return *bad
	}
	db, err := dbconn.Open(ctx, cfg)
	if err != nil {
		return Fail("db_error", err.Error())
	}
	defer db.Close()
	result, err := dbconn.RunQuery(ctx, db, dbconn.QueryRequest{SQL: input.SQL, AllowWrite: true})
	if err != nil {
		return Fail("db_error", err.Error())
	}
	return OK(result)
}
