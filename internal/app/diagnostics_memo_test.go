package app

import (
	"context"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// Диагностика завершённого прогона неизменяема, поэтому её не пересчитывают.
// Диагностика идущего — меняется на каждом шаге, и кэшировать её нельзя.
func TestRunDiagnosticsAreMemoizedOnlyForFinishedRuns(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()
	started := time.Now().UTC().Add(-time.Hour)
	finished := started.Add(30 * time.Minute)

	done := domain.Run{
		ID: "run-done", WorkspaceID: world.ID, ProfileID: "default", Task: "готово",
		Status: domain.RunCompleted, StartedAt: started, FinishedAt: &finished,
	}
	live := domain.Run{
		ID: "run-live", WorkspaceID: world.ID, ProfileID: "default", Task: "идёт",
		Status: domain.RunRunning, StartedAt: started,
	}
	for _, run := range []domain.Run{done, live} {
		if err = application.store.SaveRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}

	if key := diagnosticsMemoKey(done); key == "" {
		t.Fatal("завершённый прогон с FinishedAt обязан мемоизироваться")
	}
	if key := diagnosticsMemoKey(live); key != "" {
		t.Fatal("идущий прогон нельзя мемоизировать: его диагностика меняется")
	}
	// Терминальный, но без времени завершения — разбор в этом случае смотрит на
	// текущее время, значит результат не воспроизводим и кэшу не подлежит.
	if key := diagnosticsMemoKey(domain.Run{ID: "x", Status: domain.RunFailed}); key != "" {
		t.Fatal("терминальный прогон без FinishedAt не должен мемоизироваться")
	}

	runs := []domain.Run{done, live}
	first, err := application.recentRunDiagnostics(ctx, runs, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 {
		t.Fatalf("ожидалось 2 разбора, получено %d", len(first))
	}

	application.diagnosticsMu.Lock()
	memoSize := len(application.diagnosticsMemo)
	application.diagnosticsMu.Unlock()
	if memoSize != 1 {
		t.Fatalf("в памяти должен осесть ровно один разбор — завершённого прогона, получено %d", memoSize)
	}

	second, err := application.recentRunDiagnostics(ctx, runs, 0)
	if err != nil {
		t.Fatal(err)
	}
	if second[0].DurationMs != first[0].DurationMs || second[0].Health != first[0].Health {
		t.Fatalf("повторный разбор завершённого прогона разошёлся: %+v против %+v", second[0], first[0])
	}
}

// Ключ обязан разойтись, если запись прогона изменилась: иначе кэш отдал бы
// устаревший разбор вместо нового.
func TestDiagnosticsMemoKeyTracksRunIdentity(t *testing.T) {
	base := time.Now().UTC()
	later := base.Add(time.Minute)
	completed := domain.Run{ID: "r", Status: domain.RunCompleted, FinishedAt: &base}

	failed := completed
	failed.Status = domain.RunFailed
	if diagnosticsMemoKey(completed) == diagnosticsMemoKey(failed) {
		t.Fatal("смена статуса обязана менять ключ")
	}

	rescheduled := completed
	rescheduled.FinishedAt = &later
	if diagnosticsMemoKey(completed) == diagnosticsMemoKey(rescheduled) {
		t.Fatal("смена времени завершения обязана менять ключ")
	}
}
