package httpapi

import (
	"fmt"
	"os"
	"testing"
)

// Applications built by these tests would otherwise leave their sandbox copies
// in the machine-wide %TEMP%\point-sandboxes; see app.sandboxRoot.
func TestMain(m *testing.M) {
	sandboxes, err := os.MkdirTemp("", "point-httpapi-sandboxes-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = os.Setenv("POINT_SANDBOX_ROOT", sandboxes)
	code := m.Run()
	_ = os.RemoveAll(sandboxes)
	os.Exit(code)
}
