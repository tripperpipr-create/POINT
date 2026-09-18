package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/dbconn"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
)

type DBConnectionUpsert struct {
	ID              string                  `json:"id"`
	DisplayName     string                  `json:"displayName"`
	Driver          domain.DBDriver         `json:"driver"`
	Host            string                  `json:"host"`
	Port            int                     `json:"port"`
	Database        string                  `json:"database"`
	Username        string                  `json:"username"`
	SecretRef       string                  `json:"secretRef"`
	SSLMode         string                  `json:"sslMode"`
	ReadOnlyDefault *bool                   `json:"readOnlyDefault"`
	Status          domain.ConnectionStatus `json:"status"`
	Password        string                  `json:"password,omitempty"` // ephemeral; unlock vault only
}

type DBQueryRequest struct {
	ConnectionID string `json:"connectionId"`
	SQL          string `json:"sql"`
	AllowWrite   bool   `json:"allowWrite"`
	MaxRows      int    `json:"maxRows"`
	Password     string `json:"password,omitempty"`
	Approved     bool   `json:"approved"` // required for writes from Hub UI
}

type DBUnlockRequest struct {
	SecretRef string `json:"secretRef"`
	Password  string `json:"password"`
}

func (a *App) SaveDBConnection(req DBConnectionUpsert) (domain.DBConnection, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.DBConnection{}, err
	}
	driver := domain.DBDriver(strings.TrimSpace(string(req.Driver)))
	if !validDBDriver(driver) {
		return domain.DBConnection{}, fmt.Errorf("поддерживаются драйверы: sqlite, postgres, mysql")
	}
	now := time.Now().UTC()
	conn := domain.DBConnection{
		ID: strings.TrimSpace(req.ID), WorkspaceID: ws.ID, DisplayName: strings.TrimSpace(req.DisplayName),
		Driver: driver, Host: strings.TrimSpace(req.Host), Port: req.Port, Database: strings.TrimSpace(req.Database),
		Username: strings.TrimSpace(req.Username), SecretRef: strings.TrimSpace(req.SecretRef),
		SSLMode: strings.TrimSpace(req.SSLMode), Status: req.Status, CreatedAt: now, UpdatedAt: now,
	}
	if conn.ID == "" {
		conn.ID = domain.NewID("dbconn")
	} else if existing, getErr := a.store.GetDBConnection(context.Background(), conn.ID); getErr == nil {
		conn.CreatedAt = existing.CreatedAt
		if conn.SecretRef == "" {
			conn.SecretRef = existing.SecretRef
		}
	}
	if conn.DisplayName == "" {
		conn.DisplayName = string(conn.Driver)
	}
	if conn.Status == "" {
		conn.Status = domain.ConnectionUnknown
	}
	conn.ReadOnlyDefault = true
	if req.ReadOnlyDefault != nil {
		conn.ReadOnlyDefault = *req.ReadOnlyDefault
	}
	if conn.SecretRef == "" && driver != domain.DBDriverSQLite {
		conn.SecretRef = "point.db." + conn.ID
	}
	if err = validateDBConnection(conn); err != nil {
		return domain.DBConnection{}, err
	}
	if err = a.store.SaveDBConnection(context.Background(), conn); err != nil {
		return domain.DBConnection{}, err
	}
	if strings.TrimSpace(req.Password) != "" && conn.SecretRef != "" {
		a.dbSecrets.Put(conn.SecretRef, req.Password)
	}
	return conn, nil
}

func (a *App) ListDBConnections() ([]domain.DBConnection, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return nil, err
	}
	return a.store.ListDBConnections(context.Background(), ws.ID)
}

func (a *App) DeleteDBConnection(id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("id is required")
	}
	conn, err := a.store.GetDBConnection(context.Background(), id)
	if err != nil {
		return err
	}
	if err = a.store.DeleteDBConnection(context.Background(), id); err != nil {
		return err
	}
	if conn.SecretRef != "" {
		a.dbSecrets.Delete(conn.SecretRef)
	}
	return nil
}

func (a *App) UnlockDBSecret(req DBUnlockRequest) error {
	ref := strings.TrimSpace(req.SecretRef)
	if ref == "" {
		return errors.New("secretRef is required")
	}
	a.dbSecrets.Put(ref, req.Password)
	return nil
}

func (a *App) TestDBConnection(id string, password string) (domain.DBConnection, error) {
	conn, cfg, err := a.resolveDBOpen(id, password, true)
	if err != nil {
		conn.Status = domain.ConnectionError
		// Текст ошибки драйвера содержит строку подключения с паролем.
		conn.LastError = security.Redact(err.Error())
		now := time.Now().UTC()
		conn.LastProbeAt = &now
		conn.UpdatedAt = now
		_ = a.store.SaveDBConnection(context.Background(), conn)
		return conn, err
	}
	db, err := dbconn.Open(context.Background(), cfg)
	if err != nil {
		conn.Status = domain.ConnectionError
		// Текст ошибки драйвера содержит строку подключения с паролем.
		conn.LastError = security.Redact(err.Error())
		now := time.Now().UTC()
		conn.LastProbeAt = &now
		conn.UpdatedAt = now
		_ = a.store.SaveDBConnection(context.Background(), conn)
		return conn, err
	}
	_ = db.Close()
	now := time.Now().UTC()
	conn.Status = domain.ConnectionConnected
	conn.LastError = ""
	conn.LastProbeAt = &now
	conn.UpdatedAt = now
	if err = a.store.SaveDBConnection(context.Background(), conn); err != nil {
		return conn, err
	}
	return conn, nil
}

func (a *App) QueryDBConnection(req DBQueryRequest) (dbconn.QueryResult, error) {
	sqlText := strings.TrimSpace(req.SQL)
	if sqlText == "" {
		return dbconn.QueryResult{}, errors.New("sql is required")
	}
	kind := dbconn.ClassifySQL(sqlText)
	if kind == dbconn.StatementWrite || kind == dbconn.StatementUnknown {
		if !req.AllowWrite || !req.Approved {
			return dbconn.QueryResult{}, errors.New("записывающий SQL требует явного подтверждения (AllowWrite + Approved)")
		}
	}
	_, cfg, err := a.resolveDBOpen(req.ConnectionID, req.Password, kind != dbconn.StatementWrite)
	if err != nil {
		return dbconn.QueryResult{}, err
	}
	db, err := dbconn.Open(context.Background(), cfg)
	if err != nil {
		return dbconn.QueryResult{}, err
	}
	defer db.Close()
	return dbconn.RunQuery(context.Background(), db, dbconn.QueryRequest{
		SQL: sqlText, AllowWrite: req.AllowWrite && req.Approved, MaxRows: req.MaxRows,
	})
}

func (a *App) SchemaDBConnection(id string, password string) (dbconn.SchemaSummary, error) {
	conn, cfg, err := a.resolveDBOpen(id, password, true)
	if err != nil {
		return dbconn.SchemaSummary{}, err
	}
	db, err := dbconn.Open(context.Background(), cfg)
	if err != nil {
		return dbconn.SchemaSummary{}, err
	}
	defer db.Close()
	return dbconn.InspectSchema(context.Background(), db, conn.Driver, 100)
}

func (a *App) resolveDBOpen(id, password string, readOnly bool) (domain.DBConnection, dbconn.OpenConfig, error) {
	conn, err := a.store.GetDBConnection(context.Background(), id)
	if err != nil {
		return domain.DBConnection{}, dbconn.OpenConfig{}, err
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return conn, dbconn.OpenConfig{}, err
	}
	if conn.WorkspaceID != "" && conn.WorkspaceID != ws.ID {
		return conn, dbconn.OpenConfig{}, errors.New("подключение принадлежит другому workspace")
	}
	pass := strings.TrimSpace(password)
	if pass == "" && conn.SecretRef != "" {
		if cached, ok := a.dbSecrets.Get(conn.SecretRef); ok {
			pass = cached
		}
	}
	if pass != "" && conn.SecretRef != "" {
		a.dbSecrets.Put(conn.SecretRef, pass)
	}
	cfg := dbconn.OpenConfig{
		Driver: conn.Driver, Host: conn.Host, Port: conn.Port, Database: conn.Database,
		Username: conn.Username, Password: pass, SSLMode: conn.SSLMode,
		Workspace: ws.Path, ReadOnly: readOnly,
	}
	if conn.Driver != domain.DBDriverSQLite && pass == "" && conn.SecretRef != "" {
		return conn, cfg, fmt.Errorf("пароль не разблокирован: сохраните секрет в SecretStorage и выполните проверку подключения")
	}
	return conn, cfg, nil
}

func (a *App) dbToolAccess() *dbToolBridge {
	return &dbToolBridge{app: a}
}

type dbToolBridge struct{ app *App }

func (b *dbToolBridge) List(ctx context.Context) ([]domain.DBConnection, error) {
	return b.app.ListDBConnections()
}

func (b *dbToolBridge) Get(ctx context.Context, id string) (domain.DBConnection, error) {
	return b.app.store.GetDBConnection(ctx, id)
}

func (b *dbToolBridge) OpenConfig(ctx context.Context, id string, readOnly bool) (domain.DBConnection, dbconn.OpenConfig, error) {
	return b.app.resolveDBOpen(id, "", readOnly)
}

func (b *dbToolBridge) NetworkAllowed(host string, allowed []string, policy string) bool {
	if host == "" || dbconn.IsLoopbackHost(host) {
		return true
	}
	policy = strings.ToUpper(strings.TrimSpace(policy))
	if policy == "ALLOW" {
		return true
	}
	host = strings.ToLower(strings.TrimSpace(host))
	for _, item := range allowed {
		if strings.EqualFold(strings.TrimSpace(item), host) {
			return true
		}
	}
	return false
}

func validDBDriver(driver domain.DBDriver) bool {
	for _, item := range domain.SupportedDBDrivers() {
		if item == driver {
			return true
		}
	}
	return false
}

func validateDBConnection(conn domain.DBConnection) error {
	if strings.TrimSpace(conn.Database) == "" {
		return errors.New("укажите базу данных или путь к файлу SQLite")
	}
	switch conn.Driver {
	case domain.DBDriverSQLite:
		return nil
	case domain.DBDriverPostgres, domain.DBDriverMySQL:
		return nil
	default:
		return fmt.Errorf("неподдерживаемый драйвер")
	}
}
