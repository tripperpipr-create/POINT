package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/diagnostics"
	"local-agent-workbench/internal/domain"
)

func TestSystemDiagnosticsExplainsEveryRequiredLocalLifecycleCheck(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	t.Setenv("HTTP_ADDR", "127.0.0.1:48123")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	workspaceRoot := t.TempDir()
	if _, err = application.OpenWorkspace(workspaceRoot); err != nil {
		t.Fatal(err)
	}
	report := application.SystemDiagnostics(context.Background())
	if report.Status != diagnostics.SystemDegraded {
		t.Fatalf("status=%s report=%#v", report.Status, report)
	}
	byCode := map[string]diagnostics.SystemCheck{}
	for _, check := range report.Checks {
		byCode[check.Code] = check
	}
	for _, code := range []string{"point_core", "sqlite", "migration", "disk_space", "sandbox", "workspace_permissions", "core_port", "backup", "provider_model"} {
		if _, ok := byCode[code]; !ok {
			t.Fatalf("missing system check %q in %#v", code, report.Checks)
		}
	}
	if byCode["sqlite"].Status != diagnostics.SystemReady || byCode["migration"].Status != diagnostics.SystemReady || byCode["workspace_permissions"].Status != diagnostics.SystemReady {
		t.Fatalf("unexpected core checks: %#v", byCode)
	}
	if byCode["backup"].Status != diagnostics.SystemDegraded || byCode["provider_model"].Status != diagnostics.SystemDegraded {
		t.Fatalf("missing explicit degraded evidence: %#v", byCode)
	}
	leftovers, err := filepath.Glob(filepath.Join(workspaceRoot, ".point-permission-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("permission probe leftovers=%#v err=%v", leftovers, err)
	}
}

func TestBlockedSystemLifecyclePreventsAgentLaunchWithSafeAction(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	t.Setenv("HTTP_ADDR", "not-an-address")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	profiles, err := application.storedProfiles(context.Background())
	if err != nil || len(profiles) == 0 {
		t.Fatalf("profiles=%d err=%v", len(profiles), err)
	}
	profileID := ""
	for _, profile := range profiles {
		if !domain.IsAgentCLIProvider(profile.Provider) {
			profileID = profile.ID
			break
		}
	}
	if profileID == "" {
		t.Fatal("no headless profile available")
	}
	_, err = application.StartRun(StartRunRequest{ProfileID: profileID, Task: "Inspect the workspace"})
	if err == nil || !strings.Contains(err.Error(), "system lifecycle BLOCKED") ||
		!strings.Contains(err.Error(), "Исправьте HTTP_ADDR и перезапустите Point Core") {
		t.Fatalf("blocked launch error=%v", err)
	}
}

func TestSystemDiagnosticsFailsClosedForInvalidCoreAddress(t *testing.T) {
	application := newTestApp(t)
	t.Setenv("HTTP_ADDR", "not-an-address")
	report := application.SystemDiagnostics(context.Background())
	if report.Status != diagnostics.SystemBlocked {
		t.Fatalf("status=%s report=%#v", report.Status, report)
	}
	found := false
	for _, check := range report.Checks {
		if check.Code == "core_port" && check.Status == diagnostics.SystemBlocked {
			found = true
		}
	}
	if !found {
		t.Fatalf("blocked port check missing: %#v", report.Checks)
	}
}
