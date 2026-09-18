package dbconn

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"

	"github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

const (
	DefaultTimeout   = 15 * time.Second
	DefaultMaxRows   = 200
	HardMaxRows      = 1000
	DefaultPGPort    = 5432
	DefaultMySQLPort = 3306
)

// OpenConfig is a resolved connection (password may be empty for SQLite/trust auth).
type OpenConfig struct {
	Driver    domain.DBDriver
	Host      string
	Port      int
	Database  string
	Username  string
	Password  string
	SSLMode   string
	Workspace string // for resolving relative SQLite paths
	ReadOnly  bool
	Timeout   time.Duration
}

func Open(ctx context.Context, cfg OpenConfig) (*sql.DB, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	dsn, driverName, err := buildDSN(cfg)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(2 * time.Minute)
	pingCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	if err = db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("проверка подключения: %w", err)
	}
	return db, nil
}

func buildDSN(cfg OpenConfig) (dsn string, driverName string, err error) {
	switch cfg.Driver {
	case domain.DBDriverSQLite:
		path := strings.TrimSpace(cfg.Database)
		if path == "" {
			return "", "", fmt.Errorf("укажите путь к файлу SQLite")
		}
		if !filepath.IsAbs(path) {
			if strings.TrimSpace(cfg.Workspace) == "" {
				return "", "", fmt.Errorf("относительный путь SQLite требует открытый workspace")
			}
			path = filepath.Join(cfg.Workspace, path)
		}
		path = filepath.Clean(path)
		mode := "rwc"
		if cfg.ReadOnly {
			mode = "ro"
		} else if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return "", "", fmt.Errorf("создать каталог SQLite: %w", err)
		}
		// modernc driver name is "sqlite"
		return fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&mode=%s", filepath.ToSlash(path), mode), "sqlite", nil
	case domain.DBDriverPostgres:
		host := strings.TrimSpace(cfg.Host)
		if host == "" {
			host = "127.0.0.1"
		}
		port := cfg.Port
		if port <= 0 {
			port = DefaultPGPort
		}
		ssl := strings.TrimSpace(cfg.SSLMode)
		if ssl == "" {
			ssl = "prefer"
		}
		dbName := strings.TrimSpace(cfg.Database)
		if dbName == "" {
			return "", "", fmt.Errorf("укажите имя базы PostgreSQL")
		}
		user := strings.TrimSpace(cfg.Username)
		if user == "" {
			user = "postgres"
		}
		params := url.Values{"sslmode": {ssl}, "connect_timeout": {strconv.Itoa(int(cfg.Timeout.Seconds()))}}
		if cfg.ReadOnly {
			params.Set("default_transaction_read_only", "on")
		}
		address := &url.URL{Scheme: "postgres", Host: net.JoinHostPort(host, strconv.Itoa(port)), User: url.UserPassword(user, cfg.Password), Path: "/" + dbName, RawQuery: params.Encode()}
		return address.String(), "pgx", nil
	case domain.DBDriverMySQL:
		host := strings.TrimSpace(cfg.Host)
		if host == "" {
			host = "127.0.0.1"
		}
		port := cfg.Port
		if port <= 0 {
			port = DefaultMySQLPort
		}
		dbName := strings.TrimSpace(cfg.Database)
		if dbName == "" {
			return "", "", fmt.Errorf("укажите имя базы MySQL")
		}
		user := strings.TrimSpace(cfg.Username)
		if user == "" {
			user = "root"
		}
		// parseTime helps DATE/DATETIME round-trip; timeout is dial timeout.
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = DefaultTimeout
		}
		config := mysql.NewConfig()
		config.User, config.Passwd = user, cfg.Password
		config.Net, config.Addr, config.DBName = "tcp", net.JoinHostPort(host, strconv.Itoa(port)), dbName
		config.ParseTime = true
		config.Timeout, config.ReadTimeout, config.WriteTimeout = timeout, timeout, timeout
		if cfg.ReadOnly {
			config.Params = map[string]string{"transaction_read_only": "1"}
		}
		return config.FormatDSN(), "mysql", nil
	default:
		return "", "", fmt.Errorf("неподдерживаемый драйвер %q (доступны: sqlite, postgres, mysql)", cfg.Driver)
	}
}

// HostForPolicy returns the network host that must be allowlisted for remote drivers.
func HostForPolicy(conn domain.DBConnection) string {
	switch conn.Driver {
	case domain.DBDriverSQLite:
		return ""
	default:
		host := strings.TrimSpace(conn.Host)
		if host == "" {
			return "127.0.0.1"
		}
		return host
	}
}

// IsLoopbackHost treats local addresses as always allowable for DB tools.
func IsLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" || h == "localhost" || h == "127.0.0.1" || h == "::1" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
