package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/filepolicy"
	"local-agent-workbench/internal/sandboxsync"
)

type quietMirrorWatcher struct{}

func (quietMirrorWatcher) Dirty() bool { return false }
func (quietMirrorWatcher) Close()      {}

func TestGuestCommitCannotHideAnUnrelatedEditorWrite(t *testing.T) {
	root := t.TempDir()
	mirror := filepath.Join(root, "mirror")
	if err := os.Mkdir(mirror, 0755); err != nil {
		t.Fatal(err)
	}
	before, err := sandboxsync.Scan(context.Background(), mirror, filepolicy.Current)
	if err != nil {
		t.Fatal(err)
	}
	s := &volumeState{Record: domain.SandboxRecord{ID: "editor-race", Path: mirror, StorageMode: "volume", FileRulesVersion: filepolicy.Current}, Manifest: before, HostCache: before, Watcher: quietMirrorWatcher{}}
	b := NewContainerBackend(root)
	if err = b.commitDelta(s, before, before, nil); err != nil {
		t.Fatal(err)
	}
	// Simulate an editor write before its asynchronous Windows notification.
	if err = os.WriteFile(filepath.Join(mirror, "editor.txt"), []byte("fresh"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := b.hostManifest(context.Background(), s)
	if err != nil || got.Digest == before.Digest || len(got.Entries) != 1 {
		t.Fatalf("editor write hidden by guest cache: %+v %v", got, err)
	}
}

func TestOptimizationAblationsArePinnedAndNeverMountUnverifiedCaches(t *testing.T) {
	b := NewContainerBackend(t.TempDir())
	b.DockerVersion = "29.1.3"
	version := b.engineVersion()
	t.Setenv("POINT_SANDBOX_WARM_CONTAINER", "off")
	t.Setenv("POINT_SANDBOX_DOWNLOAD_CACHE", "off")
	ablated := NewContainerBackend(t.TempDir())
	ablated.DockerVersion = "29.1.3"
	if !ablated.coldContainers || ablated.engineVersion() == version {
		t.Fatal("ablation lost from pinned engine version")
	}
	ablated.infrastructure = func(context.Context, ...string) error { t.Fatal("disabled cache created infrastructure"); return nil }
	args, env, err := ablated.cacheMounts(context.Background(), "approved-scope", true, "image")
	if err != nil || len(args) != 0 || len(env) != 1 || env[0] != "GOMODCACHE=/workspace/.point/gomod" {
		t.Fatalf("disabled cache mounted: %v %v %v", args, env, err)
	}
	if err = ablated.RegisterWorkspace(domain.SandboxRecord{BackendVersion: version, Path: t.TempDir()}); err == nil {
		t.Fatal("active workspace moved to a different optimization mode")
	}
	t.Setenv("POINT_SANDBOX_WARM_CONTAINER", "unknown")
	if err = NewContainerBackend(t.TempDir()).validate(); err == nil {
		t.Fatal("unknown optimization mode accepted")
	}
}

func TestVolumePendingDeltaReplayAndConflict(t *testing.T) {
	root := t.TempDir()
	mirror := filepath.Join(root, "mirror")
	next := filepath.Join(root, "next")
	if err := os.MkdirAll(mirror, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mirror, "file"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := sandboxsync.Scan(context.Background(), mirror, filepolicy.Current)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(next, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(next, "file"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	after, err := sandboxsync.Scan(context.Background(), next, filepolicy.Current)
	if err != nil {
		t.Fatal(err)
	}
	delta := sandboxsync.Changes(before, after)
	for i := range delta {
		delta[i], err = sandboxsync.ReadChange(next, delta[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	s := &volumeState{Record: domain.SandboxRecord{ID: "test", Path: mirror, StorageMode: "volume", FileRulesVersion: filepolicy.Current}, Manifest: before, Before: before, Pending: delta, PendingManifest: &after, Phase: "received"}
	b := NewContainerBackend(root)
	if err = b.commitDelta(s, before, after, delta); err != nil {
		t.Fatal(err)
	}
	if err = b.commitDelta(s, before, after, delta); err != nil {
		t.Fatal("replay", err)
	}
	if err = os.WriteFile(filepath.Join(mirror, "file"), []byte("user edit"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = b.commitDelta(s, before, after, delta); err == nil {
		t.Fatal("overwrote concurrent edit")
	}
	data, _ := os.ReadFile(filepath.Join(mirror, "file"))
	if string(data) != "user edit" {
		t.Fatal("lost conflict")
	}
}

func TestVolumeJournalReloadAndBindCompatibility(t *testing.T) {
	root := t.TempDir()
	r := domain.SandboxRecord{ID: "pinned", Path: root, StorageMode: "volume", FileRulesVersion: filepolicy.Current, WorkspaceVolume: "pinned-volume", SandboxdDigest: "pinned-helper"}
	if err := writeWorkspaceRecord(r); err != nil {
		t.Fatal(err)
	}
	s := &volumeState{Record: r, Phase: "running", Incomplete: true, Operation: "do-not-replay"}
	if err := saveVolumeState(s); err != nil {
		t.Fatal(err)
	}
	b := NewContainerBackend(filepath.Dir(root))
	restored, err := b.stateFor(root)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Watcher.Close()
	if restored.Operation != s.Operation || restored.Record.WorkspaceVolume != r.WorkspaceVolume || !restored.Incomplete {
		t.Fatal("lost durable operation")
	}
	if !restored.Watcher.Dirty() {
		t.Fatal("restored mirror was assumed unchanged")
	}
	if b.UsesVolume(t.TempDir()) {
		t.Fatal("legacy workspace became volume")
	}
	t.Setenv("POINT_SANDBOX_WORKSPACE", "bind")
	if !b.UsesVolume(root) {
		t.Fatal("changed a pinned mechanism")
	}
}

func TestVolumeAuditCommitCrashMarksHistoryIncompleteOnlyOnce(t *testing.T) {
	root := t.TempDir()
	r := domain.SandboxRecord{ID: "audit-crash", Path: root, StorageMode: "volume", FileRulesVersion: filepolicy.Current}
	if err := writeWorkspaceRecord(r); err != nil {
		t.Fatal(err)
	}
	if err := saveVolumeState(&volumeState{Record: r, Phase: "idle"}); err != nil {
		t.Fatal(err)
	}
	b := NewContainerBackend(filepath.Dir(root))
	if err := b.BeginVolumeAudit(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	before, _ := b.stateFor(root)
	before.Watcher.Close()
	b.workspaceStates.Delete(root)
	recovered, err := b.stateFor(root)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered.Incomplete || recovered.AuditPending || recovered.Phase != "idle" {
		t.Fatalf("lost audit interruption: %+v", recovered)
	}
	at := recovered.IncompleteAt
	recovered.Watcher.Close()
	b.workspaceStates.Delete(root)
	again, err := b.stateFor(root)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Watcher.Close()
	if !again.IncompleteAt.Equal(at) {
		t.Fatal("recovery changed the interruption epoch twice")
	}
	if err := b.BeginVolumeAudit(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if err := b.FinishVolumeAudit(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if again.AuditPending {
		t.Fatal("completed audit still pending")
	}
}

func TestVolumeDeltaTypeTransitionsAndConcurrentDeletion(t *testing.T) {
	for _, directoryFirst := range []bool{false, true} {
		root := t.TempDir()
		mirror, next := filepath.Join(root, "mirror"), filepath.Join(root, "next")
		for _, tree := range []struct {
			root      string
			directory bool
		}{{mirror, directoryFirst}, {next, !directoryFirst}} {
			if err := os.MkdirAll(tree.root, 0755); err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(tree.root, "item")
			if tree.directory {
				if err := os.MkdirAll(name, 0755); err != nil {
					t.Fatal(err)
				}
				name = filepath.Join(name, "child")
			}
			if err := os.WriteFile(name, []byte("content"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		before, err := sandboxsync.Scan(context.Background(), mirror, filepolicy.Current)
		if err != nil {
			t.Fatal(err)
		}
		after, err := sandboxsync.Scan(context.Background(), next, filepolicy.Current)
		if err != nil {
			t.Fatal(err)
		}
		delta := sandboxsync.Changes(before, after)
		for i := range delta {
			delta[i], err = sandboxsync.ReadChange(next, delta[i])
			if err != nil {
				t.Fatal(err)
			}
		}
		s := &volumeState{Record: domain.SandboxRecord{Path: mirror}, Manifest: before}
		b := NewContainerBackend(root)
		for i := 0; i < 2; i++ {
			if err = b.commitDelta(s, before, after, delta); err != nil {
				t.Fatalf("directory first %v, replay %d: %v", directoryFirst, i, err)
			}
		}
		actual, err := sandboxsync.Scan(context.Background(), mirror, filepolicy.Current)
		if err != nil || actual.Digest != after.Digest {
			t.Fatalf("transition digest: %v", err)
		}
	}
	root := t.TempDir()
	name := filepath.Join(root, "file")
	if err := os.WriteFile(name, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	before, _ := sandboxsync.Scan(context.Background(), root, filepolicy.Current)
	if err := os.WriteFile(name, []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	after, _ := sandboxsync.Scan(context.Background(), root, filepolicy.Current)
	delta := sandboxsync.Changes(before, after)
	delta[0], _ = sandboxsync.ReadChange(root, delta[0])
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	s := &volumeState{Record: domain.SandboxRecord{Path: root}}
	if err := NewContainerBackend(root).commitDelta(s, before, after, delta); err == nil {
		t.Fatal("concurrent mirror deletion was overwritten")
	}
}
