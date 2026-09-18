package sandbox

import (
	"os"
	"strings"
)

// LiveFileMutationEnabled is the Cursor/Claude default: agent tools write the
// open workspace. Opt out with POINT_LIVE_WORKSPACE=0 or
// POINT_FILE_ISOLATION=sandbox.
func LiveFileMutationEnabled() bool {
	isolation := strings.ToLower(strings.TrimSpace(os.Getenv("POINT_FILE_ISOLATION")))
	if isolation == "sandbox" || isolation == "copy" || isolation == "isolated" {
		return false
	}
	if value, ok := os.LookupEnv("POINT_LIVE_WORKSPACE"); ok {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "0", "false", "no", "off":
			return false
		case "1", "true", "yes", "on", "":
			return true
		}
	}
	return true
}
