package domain

import "time"

// DBDriver identifies a user-facing database backend (not Point Core's store).
type DBDriver string

const (
	DBDriverSQLite   DBDriver = "sqlite"
	DBDriverPostgres DBDriver = "postgres"
	DBDriverMySQL    DBDriver = "mysql"
)

// DBConnection is a saved profile for querying an external or workspace database.
// Passwords stay in IDE SecretStorage; Go persists only secretRef.
type DBConnection struct {
	ID              string           `json:"id"`
	WorkspaceID     string           `json:"workspaceId,omitempty"`
	DisplayName     string           `json:"displayName"`
	Driver          DBDriver         `json:"driver"`
	Host            string           `json:"host,omitempty"`
	Port            int              `json:"port,omitempty"`
	Database        string           `json:"database"` // DB name, or SQLite file path
	Username        string           `json:"username,omitempty"`
	SecretRef       string           `json:"secretRef,omitempty"`
	SSLMode         string           `json:"sslMode,omitempty"`
	ReadOnlyDefault bool             `json:"readOnlyDefault"`
	Status          ConnectionStatus `json:"status"`
	LastError       string           `json:"lastError,omitempty"`
	LastProbeAt     *time.Time       `json:"lastProbeAt,omitempty"`
	CreatedAt       time.Time        `json:"createdAt"`
	UpdatedAt       time.Time        `json:"updatedAt"`
}

// SupportedDBDrivers documents engines available without native CGO deps on Windows.
func SupportedDBDrivers() []DBDriver {
	return []DBDriver{DBDriverSQLite, DBDriverPostgres, DBDriverMySQL}
}
