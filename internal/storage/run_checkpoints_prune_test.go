package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// Прогон хранит только несколько последних контрольных точек, а читается
// по-прежнему самая новая.
func TestRunCheckpointsKeepOnlyTheLatest(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "checkpoints.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	run := domain.Run{ID: "run-prune", WorkspaceID: "ws", Status: domain.RunRunning, Task: "t", StartedAt: time.Now().UTC()}
	if err = store.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	for seq := 1; seq <= 10; seq++ {
		if err = store.SaveRunCheckpoint(ctx, domain.RunCheckpoint{RunID: run.ID, Seq: seq, NextStep: seq + 1}); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_checkpoints WHERE run_id=?`, run.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != keptRunCheckpoints {
		t.Fatalf("stored checkpoints=%d", count)
	}
	latest, err := store.LatestRunCheckpoint(ctx, run.ID)
	if err != nil || latest.Seq != 10 || latest.NextStep != 11 {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
}
