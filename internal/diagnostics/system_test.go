package diagnostics

import (
	"reflect"
	"testing"
	"time"
)

func TestCompileSystemReportUsesFailClosedPrecedenceAndSourceReasons(t *testing.T) {
	now := time.Date(2026, time.August, 31, 1, 2, 3, 0, time.FixedZone("fixture", 7*60*60))
	report := CompileSystemReport([]SystemCheck{
		{Code: "core", Status: SystemReady, Summary: "Point Core доступен"},
		{Code: "backup", Status: SystemDegraded, Summary: "Проверенного backup пока нет"},
		{Code: "provider", Status: SystemBlocked, Summary: "Требуемая модель отсутствует"},
		{Code: "workspace", Status: "unknown", Summary: "Права workspace не подтверждены"},
	}, now)

	if report.SchemaVersion != SystemSchemaVersion || report.Status != SystemBlocked || !report.CheckedAt.Equal(now.UTC()) {
		t.Fatalf("report=%#v", report)
	}
	want := []string{"Права workspace не подтверждены", "Требуемая модель отсутствует"}
	if !reflect.DeepEqual(report.BlockingReasons, want) {
		t.Fatalf("blocking reasons=%#v want=%#v", report.BlockingReasons, want)
	}
	if report.Checks[3].Status != SystemBlocked {
		t.Fatalf("unknown status did not fail closed: %#v", report.Checks[3])
	}
}

func TestCompileSystemReportReportsDegradedWithoutInventingACombinedScore(t *testing.T) {
	report := CompileSystemReport([]SystemCheck{
		{Code: "database", Status: SystemReady, Summary: "SQLite цела", Metrics: map[string]any{"migrationVersion": 30}},
		{Code: "sandbox", Status: SystemDegraded, Summary: "Доступна только безопасная ограниченная работа"},
	}, time.Now())
	if report.Status != SystemDegraded || len(report.BlockingReasons) != 0 {
		t.Fatalf("report=%#v", report)
	}
}
