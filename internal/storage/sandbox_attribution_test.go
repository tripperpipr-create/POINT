package storage_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/storage"
)

func TestSandboxBackendAttributionRoundTrip(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "workbench.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	want := domain.SandboxRecord{
		ID: "sandbox-1", WorkspaceID: "workspace-1", ExecutionID: "execution-1", Kind: "copy",
		Backend: "docker", BackendVersion: "27.1.0", BackendImage: "point-agent-sandbox:1.2.2",
		BackendImageDigest: "sha256:immutable", Path: t.TempDir(), CreatedAt: time.Now().UTC(),
	}
	if err = store.SaveSandbox(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSandbox(context.Background(), want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Backend != want.Backend || got.BackendVersion != want.BackendVersion || got.BackendImage != want.BackendImage || got.BackendImageDigest != want.BackendImageDigest {
		t.Fatalf("sandbox attribution lost: got=%#v want=%#v", got, want)
	}
}
