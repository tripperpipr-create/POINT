package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/filepolicy"
	"local-agent-workbench/internal/sandboxsync"
	"local-agent-workbench/internal/workspace"
)

type volumeState struct {
	mu               sync.Mutex
	Record           domain.SandboxRecord    `json:"record"`
	Manifest         sandboxsync.Manifest    `json:"manifest"`
	Before           sandboxsync.Manifest    `json:"before,omitempty"`
	PreparedHost     *sandboxsync.Manifest   `json:"-"`
	HostCache        sandboxsync.Manifest    `json:"-"`
	MirrorDirty      atomic.Bool             `json:"-"`
	Incomplete       bool                    `json:"incomplete"`
	IncompleteReason string                  `json:"incompleteReason,omitempty"`
	IncompleteAt     time.Time               `json:"incompleteAt,omitempty"`
	FreshCheckDigest string                  `json:"freshCheckDigest,omitempty"`
	Phase            string                  `json:"phase,omitempty"`
	Operation        string                  `json:"operation,omitempty"`
	AuditPending     bool                    `json:"auditPending,omitempty"`
	Pending          []sandboxsync.Change    `json:"pending,omitempty"`
	PendingManifest  *sandboxsync.Manifest   `json:"pendingManifest,omitempty"`
	Container        string                  `json:"-"`
	CacheSignature   string                  `json:"-"`
	Gateway          *warmGateway            `json:"-"`
	Watcher          mirrorWatcher           `json:"-"`
	Audit            *workspace.TextSnapshot `json:"-"`
}

func workspaceMode() string {
	if strings.TrimSpace(os.Getenv("POINT_SANDBOX_WORKSPACE")) == "volume" {
		return "volume"
	}
	return "bind"
}
func controlPath(root string) string { return root + ".point-control" }
func atomicJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "state-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}
func writeWorkspaceRecord(r domain.SandboxRecord) error {
	return atomicJSON(filepath.Join(controlPath(r.Path), "record.json"), r)
}
func saveVolumeState(s *volumeState) error {
	return atomicJSON(filepath.Join(controlPath(s.Record.Path), "state.json"), s)
}
func (b *ContainerBackend) stateFor(root string) (*volumeState, error) {
	if v, ok := b.workspaceStates.Load(root); ok {
		return v.(*volumeState), nil
	}
	data, err := os.ReadFile(filepath.Join(controlPath(root), "record.json"))
	if err != nil {
		return nil, err
	}
	var r domain.SandboxRecord
	if err = json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if r.Path != root {
		return nil, errors.New("sandbox mirror identity differs")
	}
	if b.Engine != nil && (r.Backend != b.engineName() || r.BackendVersion != b.engineVersion()) {
		return nil, errors.New("sandbox belongs to a different pinned engine/runtime; resume it with its original backend")
	}
	s := &volumeState{Record: r}
	if data, err = os.ReadFile(filepath.Join(controlPath(root), "state.json")); err == nil {
		if err = json.Unmarshal(data, s); err != nil {
			return nil, err
		}
		if s.Record.ID != r.ID || s.Record.Path != root || s.Record.Backend != r.Backend || s.Record.BackendVersion != r.BackendVersion || s.Record.StorageMode != r.StorageMode || s.Record.WorkspaceVolume != r.WorkspaceVolume || s.Record.FileRulesVersion != r.FileRulesVersion || s.Record.SandboxdDigest != r.SandboxdDigest || s.Record.BackendImageDigest != r.BackendImageDigest {
			return nil, errors.New("sandbox state identity differs")
		}
		for _, manifest := range []*sandboxsync.Manifest{&s.Manifest, &s.Before, s.PendingManifest} {
			if manifest != nil && manifest.Digest != "" {
				if err = sandboxsync.ValidateManifest(*manifest); err != nil {
					return nil, fmt.Errorf("invalid durable sandbox manifest: %w", err)
				}
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	} else if r.StorageMode == "volume" {
		s.taint("Host synchronization journal was lost; recover the volume without replaying commands")
		s.Phase = "journal-lost"
	}
	if r.StorageMode == "volume" {
		if s.AuditPending {
			s.taint("Core interrupted before command changes and result were committed to the audit journal")
			s.AuditPending = false
			if err = saveVolumeState(s); err != nil {
				return nil, err
			}
		}
		s.Watcher = newMirrorWatcher(root)
	}
	actual, loaded := b.workspaceStates.LoadOrStore(root, s)
	if loaded && s.Watcher != nil {
		s.Watcher.Close()
	}
	return actual.(*volumeState), nil
}
func (b *ContainerBackend) UsesVolume(root string) bool {
	s, err := b.stateFor(root)
	if err != nil {
		return !os.IsNotExist(err)
	}
	return s.Record.StorageMode == "volume"
}
func (b *ContainerBackend) RulesForWorkspace(root string) string {
	s, err := b.stateFor(root)
	if err != nil {
		return filepolicy.Legacy
	}
	return s.Record.FileRulesVersion
}
func (b *ContainerBackend) RegisterWorkspace(r domain.SandboxRecord) error {
	if (b.coldContainers || b.disableDownloadCache || strings.Contains(r.BackendVersion, "|execution:")) && r.BackendVersion != b.engineVersion() {
		return errors.New("workspace execution optimizations differ from its pinned version")
	}
	if b.Engine != nil {
		if r.Backend != b.engineName() || r.BackendVersion != b.engineVersion() || r.StorageMode != "volume" || r.Kind == "live" {
			return errors.New("embedded workspace does not match its pinned engine and isolated Linux volume")
		}
		if err := b.retainRuntimeExecution(r.Path); err != nil {
			return err
		}
	}
	if r.StorageMode == "" {
		r.StorageMode = "bind"
	}
	if r.FileRulesVersion == "" {
		r.FileRulesVersion = filepolicy.Legacy
	}
	if r.Kind == "live" {
		r.StorageMode = "bind"
		r.FileRulesVersion = filepolicy.Legacy
	}
	if previous, err := b.stateFor(r.Path); err == nil && previous.Record.ID == r.ID {
		r.QuestID = previous.Record.QuestID
	}
	return writeWorkspaceRecord(r)
}
func ownerIdentity(root string) string {
	abs, _ := filepath.Abs(root)
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	sum := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(sum[:12])
}
func (b *ContainerBackend) labels(r domain.SandboxRecord, kind string) []string {
	return []string{"--label", "point.owner=" + ownerIdentity(b.Root), "--label", "point.sandbox=" + r.ID, "--label", "point.workspace=" + r.WorkspaceID, "--label", "point.quest=" + r.QuestID, "--label", "point.resource=" + kind, "--label", "point.created=" + time.Now().UTC().Format(time.RFC3339)}
}

func (b *ContainerBackend) hostManifest(ctx context.Context, s *volumeState) (sandboxsync.Manifest, error) {
	dirty := s.MirrorDirty.Swap(false)
	if s.Watcher != nil {
		dirty = s.Watcher.Dirty() || dirty
	} else {
		dirty = true
	}
	if s.HostCache.Digest != "" && !dirty {
		return s.HostCache, nil
	}
	m, err := sandboxsync.Scan(ctx, s.Record.Path, s.Record.FileRulesVersion)
	if err == nil {
		s.HostCache = m
	}
	return m, err
}

// File tools signal their committed writes immediately; native events cover IDE/external edits.
func (b *ContainerBackend) WatchMirror(fs *workspace.FS) {
	s, err := b.stateFor(fs.Root())
	if err != nil || s.Record.StorageMode != "volume" {
		return
	}
	fs.MutationHook = func() { s.MirrorDirty.Store(true) }
}
func (b *ContainerBackend) CaptureVolumeAudit(ctx context.Context, root string, previous *workspace.TextSnapshot, after bool) (workspace.TextSnapshot, error) {
	s, err := b.stateFor(root)
	if err != nil {
		return workspace.TextSnapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.Manifest
	if !after {
		m, err = b.hostManifest(ctx, s)
		if err != nil {
			return workspace.TextSnapshot{}, err
		}
		s.PreparedHost = &m
		s.Audit = previous
	}
	fs, err := workspace.Open(root)
	if err != nil {
		return workspace.TextSnapshot{}, err
	}
	fs.FileRules = s.Record.FileRulesVersion
	if previous == nil {
		previous = s.Audit
	}
	snap, err := fs.CaptureManifestSnapshot(ctx, m, previous)
	if err == nil {
		s.Audit = &snap
	}
	if err != nil || !snap.Complete {
		s.taint("Workspace audit exceeded its bounds or failed to read exact files")
		if saveErr := saveVolumeState(s); err == nil {
			err = saveErr
		}
	}
	if s.Incomplete {
		snap.Complete = false
	}
	return snap, err
}

// Integrity is checked before handing off, verifying or delivering a sandbox.
type WorkspaceIntegrity struct {
	Digest       string    `json:"digest"`
	Incomplete   bool      `json:"incomplete"`
	FreshCheck   bool      `json:"freshCheck"`
	Reason       string    `json:"reason,omitempty"`
	IncompleteAt time.Time `json:"incompleteAt,omitempty"`
}
type IntegrityBackend interface {
	CheckWorkspace(context.Context, domain.SandboxRecord) (WorkspaceIntegrity, error)
}

func (b *ContainerBackend) CheckWorkspace(ctx context.Context, r domain.SandboxRecord) (WorkspaceIntegrity, error) {
	if r.StorageMode != "volume" {
		digest, err := TreeDigestWithRules(r.Path, r.FileRulesVersion)
		return WorkspaceIntegrity{Digest: digest}, err
	}
	s, err := b.stateFor(r.Path)
	if err != nil {
		return WorkspaceIntegrity{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Phase != "" && s.Phase != "idle" {
		s.taint("Interrupted sandbox operation")
		if err = b.recoverVolume(ctx, s); err != nil {
			return WorkspaceIntegrity{Incomplete: true}, err
		}
	}
	if err = b.ensureWarmContainer(ctx, s, ProcessRequest{WorkspaceRoot: r.Path, Image: ExecutionImageForRecord(r), NetworkPolicy: "DENY"}); err != nil {
		return WorkspaceIntegrity{}, err
	}
	host, err := sandboxsync.Scan(ctx, r.Path, r.FileRulesVersion)
	if err != nil {
		return WorkspaceIntegrity{}, err
	}
	changes := sandboxsync.Changes(s.Manifest, host)
	if len(changes) > 0 {
		for i := range changes {
			changes[i], err = sandboxsync.ReadChange(r.Path, changes[i])
			if err != nil {
				return WorkspaceIntegrity{}, err
			}
		}
		if err = b.applyVolume(ctx, s, host, changes); err != nil {
			return WorkspaceIntegrity{}, err
		}
		s.Manifest = host
		if err = saveVolumeState(s); err != nil {
			return WorkspaceIntegrity{}, err
		}
	}
	m, err := b.volumeManifest(ctx, s)
	if err != nil {
		s.Container = ""
		if retryErr := b.ensureWarmContainer(ctx, s, ProcessRequest{WorkspaceRoot: r.Path, Image: ExecutionImageForRecord(r), NetworkPolicy: "DENY"}); retryErr != nil {
			return WorkspaceIntegrity{}, retryErr
		}
		m, err = b.volumeManifest(ctx, s)
		if err != nil {
			return WorkspaceIntegrity{}, err
		}
	}
	if m.Digest != host.Digest {
		s.taint("Volume and mirror digests differed; full resynchronization was required")
		backup := filepath.Join(controlPath(r.Path), "conflicts", domain.NewID("sync"))
		if err = CopyPortable(ctx, r.Path, backup, r.FileRulesVersion); err != nil {
			return WorkspaceIntegrity{Incomplete: true}, err
		}
		request := sandboxsync.Request{Version: 1, Operation: domain.NewID("resync"), Rules: r.FileRulesVersion, Before: host}
		_, after, delta, err := b.executeVolume(ctx, s, "delta", request, io.Discard, io.Discard)
		if err != nil {
			return WorkspaceIntegrity{Incomplete: true}, err
		}
		s.Before = host
		s.Pending = delta
		s.PendingManifest = &after
		s.Phase = "received"
		if err = saveVolumeState(s); err != nil {
			return WorkspaceIntegrity{Incomplete: true}, err
		}
		if err = sandboxsync.Apply(r.Path, delta); err != nil {
			return WorkspaceIntegrity{Incomplete: true}, err
		}
		s.Manifest = after
		s.Phase = "idle"
		s.Before = sandboxsync.Manifest{}
		s.Pending = nil
		s.PendingManifest = nil
		if err = saveVolumeState(s); err != nil {
			return WorkspaceIntegrity{Incomplete: true}, err
		}
		return WorkspaceIntegrity{Digest: after.Digest, Incomplete: true}, fmt.Errorf("sandbox digests differed; mirror resynchronized, previous mirror retained at %s; a clean check and audit review are required", backup)
	}
	return WorkspaceIntegrity{Digest: m.Digest, Incomplete: s.Incomplete, FreshCheck: s.FreshCheckDigest == m.Digest, Reason: s.IncompleteReason, IncompleteAt: s.IncompleteAt}, nil
}

func (s *volumeState) taint(reason string) {
	s.Incomplete = true
	s.IncompleteReason = reason
	s.IncompleteAt = time.Now().UTC()
	s.FreshCheckDigest = ""
}

// AuditStatus reads the host journal without starting Docker or recovering operations.
func (b *ContainerBackend) AuditStatus(r domain.SandboxRecord) (WorkspaceIntegrity, error) {
	if r.StorageMode != "volume" {
		return WorkspaceIntegrity{}, nil
	}
	s, err := b.stateFor(r.Path)
	if err != nil {
		return WorkspaceIntegrity{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return WorkspaceIntegrity{Digest: s.Manifest.Digest, Incomplete: s.Incomplete, FreshCheck: s.FreshCheckDigest == s.Manifest.Digest, Reason: s.IncompleteReason, IncompleteAt: s.IncompleteAt}, nil
}
func (b *ContainerBackend) RecordCleanVerification(ctx context.Context, r domain.SandboxRecord, digest string) error {
	status, err := b.CheckWorkspace(ctx, r)
	if err != nil {
		return err
	}
	if status.Digest != digest {
		return errors.New("verified tree no longer matches sandbox")
	}
	if r.StorageMode != "volume" {
		return nil
	}
	s, err := b.stateFor(r.Path)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.FreshCheckDigest = digest
	return saveVolumeState(s)
}
