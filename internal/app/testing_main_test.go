package app

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Package tests historically assume isolated sandbox copies. Live workspace
	// is the Point IDE default (extension sets POINT_LIVE_WORKSPACE=1); opt into
	// it per test when covering Cursor-style writes.
	if os.Getenv("POINT_LIVE_WORKSPACE") == "" {
		_ = os.Setenv("POINT_LIVE_WORKSPACE", "0")
	}
	if os.Getenv("POINT_FILE_ISOLATION") == "" {
		_ = os.Setenv("POINT_FILE_ISOLATION", "sandbox")
	}
	os.Exit(m.Run())
}
