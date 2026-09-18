package app

import (
	"os"
	"strings"
)

const (
	legacyDatabaseFile = "workbench.db"
	hubV2DatabaseFile  = "hub-v2.db"
)

// databaseFileForRuntime selects the Agent Hub database. v2 is the product now;
// the legacy database is left on disk untouched, so reopening it is a restart
// with POINT_AGENT_HUB_V2=0 rather than a restore from backup. Nothing is
// migrated between the two: a value moved silently is a value nobody can audit.
func databaseFileForRuntime() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("POINT_AGENT_HUB_V2"))) {
	case "0", "false", "legacy":
		return legacyDatabaseFile
	default:
		return hubV2DatabaseFile
	}
}
