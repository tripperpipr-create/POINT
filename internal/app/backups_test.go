package app

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestSignificantEventCreatesDebouncedVerifiedBackup(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	application.backupDebounce = 5 * time.Millisecond
	application.emitEvent(domain.Event{Type: domain.EventRunCompleted})

	deadline := time.Now().Add(3 * time.Second)
	for {
		snapshot, latestErr := application.backupManager.Latest(context.Background())
		if latestErr == nil {
			if snapshot.Reason != "event" || snapshot.Database.Integrity != "ok" || snapshot.Database.SHA256 == "" {
				t.Fatalf("unexpected background snapshot: %+v", snapshot)
			}
			break
		}
		if !errors.Is(latestErr, os.ErrNotExist) {
			t.Fatal(latestErr)
		}
		if time.Now().After(deadline) {
			t.Fatal("background backup did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	application.Shutdown(context.Background())
	if _, err = application.CreateBackup(context.Background(), "after-shutdown"); err == nil {
		t.Fatal("backup after shutdown should fail closed")
	}
}
