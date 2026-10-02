package sandbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/egress"
	"local-agent-workbench/internal/sandboxsync"
)

func (b *ContainerBackend) RunVolumeProcess(ctx context.Context, req ProcessRequest, stdout, stderr io.Writer) (result ProcessOutcome, resultErr error) {
	if err := b.validate(); err != nil {
		return ProcessOutcome{}, err
	}
	if err := b.ensureAvailable(ctx); err != nil {
		return ProcessOutcome{}, err
	}
	s, err := b.stateFor(req.WorkspaceRoot)
	if err != nil {
		return ProcessOutcome{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if b.coldContainers {
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := b.stopWarmWorkspace(cleanupCtx, s); err != nil {
				s.taint("Per-command container cleanup failed")
				_ = saveVolumeState(s)
				result.AuditIncomplete = true
				resultErr = errors.Join(resultErr, err)
			}
		}()
	}
	if s.Record.StorageMode != "volume" {
		return ProcessOutcome{}, errors.New("workspace is not a volume sandbox")
	}
	root, cwd, err := volumeContainerPaths(req.WorkspaceRoot, req.WorkingDirectory)
	if err != nil {
		return ProcessOutcome{}, err
	}
	req.Program, err = workspaceProgram(root, req.Program)
	if err != nil {
		return ProcessOutcome{}, err
	}
	if req.Image != "" {
		_, digest, err := b.resolveExecutionImage(ctx, req.Image)
		if err != nil {
			return ProcessOutcome{}, err
		}
		if digest != s.Record.BackendImageDigest {
			return ProcessOutcome{}, errors.New("process image differs from pinned sandbox image")
		}
	}
	policy, err := egress.Compile(req.NetworkPolicy, req.AllowedNetworkHosts, egress.Quota{})
	if err != nil {
		return ProcessOutcome{}, err
	}
	if s.Phase != "" && s.Phase != "idle" && s.Phase != "restored" {
		if err = b.recoverVolume(ctx, s); err != nil {
			return ProcessOutcome{AuditIncomplete: true}, err
		}
	}
	if policy.Mode == "ALLOWLIST" {
		if err = b.startWarmGateway(ctx, s); err != nil {
			return ProcessOutcome{}, err
		}
	}
	if err = b.ensureWarmContainer(ctx, s, req); err != nil {
		return ProcessOutcome{}, err
	}
	host := sandboxsync.Manifest{}
	if s.PreparedHost != nil {
		host = *s.PreparedHost
		s.PreparedHost = nil
	} else {
		host, err = b.hostManifest(ctx, s)
		if err != nil {
			return ProcessOutcome{}, err
		}
	}
	changes := sandboxsync.Changes(s.Manifest, host)
	for i := range changes {
		changes[i], err = sandboxsync.ReadChange(s.Record.Path, changes[i])
		if err != nil {
			return ProcessOutcome{}, err
		}
	}
	operation := domain.NewID("sandboxop")
	s.Phase = "dispatching"
	s.Operation = operation
	s.Before = host
	s.PendingManifest = &host
	if err = saveVolumeState(s); err != nil {
		return ProcessOutcome{}, err
	}
	env := containerEnvironment(req.Environment)
	_, cacheEnv, err := b.cacheMounts(ctx, req.CacheScope, req.Authoritative, s.Record.BackendImageDigest)
	if err != nil {
		return ProcessOutcome{}, err
	}
	env = withEnvironmentOverrides(env, cacheEnv)
	if policy.Mode == "ALLOWLIST" {
		gateCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = s.Gateway.control(gateCtx, "begin", operation, req.RunID, policy)
		cancel()
		if err != nil {
			return ProcessOutcome{}, err
		}
		env = append(env, "HTTP_PROXY=http://point-egress-gateway:8080", "HTTPS_PROXY=http://point-egress-gateway:8080", "http_proxy=http://point-egress-gateway:8080", "https_proxy=http://point-egress-gateway:8080", "NO_PROXY=localhost,127.0.0.1,::1", "POINT_EGRESS_POLICY_DIGEST="+policy.Digest)
	}
	request := sandboxsync.Request{Version: 1, Operation: operation, Rules: s.Record.FileRulesVersion, Before: host, Changes: changes, Program: req.Program, Arguments: req.Arguments, Shell: req.ShellCommand, CWD: strings.TrimPrefix(strings.TrimPrefix(cwd, "/workspace"), "/"), Environment: env}
	if deadline, ok := ctx.Deadline(); ok {
		request.TimeoutMS = max(1, time.Until(deadline).Milliseconds())
	}
	outcome, after, delta, runErr := b.executeVolume(ctx, s, "run", request, stdout, stderr)
	if policy.Mode == "ALLOWLIST" {
		decisions, gateErr := b.endWarmGateway(s, operation, req.RunID, policy)
		outcome.EgressDecisions = decisions
		if runErr == nil {
			runErr = gateErr
		}
	}
	if runErr != nil {
		s.taint("Command transport was interrupted; its outcome is unknown and the command was not replayed")
		s.Container = ""
		_ = saveVolumeState(s)
		recoveryCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_ = b.recoverVolume(recoveryCtx, s)
		cancel()
		outcome.AuditIncomplete = true
		return outcome, fmt.Errorf("sandbox command outcome requires recovery: %w", runErr)
	}
	s.Phase = "received"
	s.Pending = delta
	s.PendingManifest = &after
	if err = saveVolumeState(s); err != nil {
		return outcome, err
	}
	if err = b.commitDelta(s, host, after, delta); err != nil {
		s.taint("Delta conflicts with mirror edits: " + err.Error())
		_ = saveVolumeState(s)
		return ProcessOutcome{AuditIncomplete: true}, err
	}
	outcome.AuditIncomplete = s.Incomplete
	return outcome, nil
}

func (b *ContainerBackend) executeVolume(ctx context.Context, s *volumeState, kind string, r sandboxsync.Request, stdout, stderr io.Writer) (ProcessOutcome, sandboxsync.Manifest, []sandboxsync.Change, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return ProcessOutcome{}, sandboxsync.Manifest{}, nil, err
	}
	// The helper owns the command timeout; allow it to kill descendants and emit its delta.
	transportCtx := ctx
	cancel := func() {}
	if deadline, ok := ctx.Deadline(); ok {
		transportCtx, cancel = context.WithDeadline(context.WithoutCancel(ctx), deadline.Add(10*time.Second))
	}
	defer cancel()
	cmd := b.commandFor(transportCtx, "exec", "-i", s.Container, helperMount, kind)
	cmd.Env = dockerClientEnvironment()
	cmd.Stdin = bytes.NewReader(data)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return ProcessOutcome{}, sandboxsync.Manifest{}, nil, err
	}
	var transportError bytes.Buffer
	cmd.Stderr = &transportError
	if err = cmd.Start(); err != nil {
		return ProcessOutcome{}, sandboxsync.Manifest{}, nil, err
	}
	transportDone := make(chan struct{})
	defer close(transportDone)
	go func() {
		select {
		case <-ctx.Done():
			if ctx.Err() == context.Canceled {
				stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = b.runInfrastructure(stopCtx, "exec", s.Container, helperMount, "reset")
				cancel()
			}
		case <-transportDone:
		}
	}()
	decode := json.NewDecoder(pipe)
	outcome := ProcessOutcome{ExitCode: -1}
	after := sandboxsync.Manifest{}
	delta := []sandboxsync.Change{}
	finished := false
	for {
		var frame sandboxsync.Frame
		err = decode.Decode(&frame)
		if errors.Is(err, io.EOF) {
			err = nil
			break
		}
		if err != nil {
			break
		}
		if frame.Version != 1 || frame.Operation != r.Operation || finished {
			err = errors.New("sandbox response identity or frame sequence differs")
			break
		}
		switch frame.Kind {
		case "started":
			if kind == "run" {
				s.Phase = "running"
				err = saveVolumeState(s)
			}
		case "stdout":
			_, err = stdout.Write(frame.Data)
		case "stderr":
			_, err = stderr.Write(frame.Data)
		case "change":
			if frame.Change == nil {
				err = errors.New("missing change frame")
			} else {
				delta = append(delta, *frame.Change)
			}
		case "result":
			if frame.Manifest == nil || finished {
				err = errors.New("invalid result frame")
			} else {
				after = *frame.Manifest
				outcome.ExitCode = frame.ExitCode
				outcome.TimedOut = frame.TimedOut
				finished = true
			}
		default:
			err = errors.New("unknown sandbox frame")
		}
		if err != nil {
			break
		}
	}
	if err != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if err == nil {
		err = waitErr
	}
	if err == nil && !finished {
		err = errors.New("sandbox stream ended without final manifest")
	}
	if err != nil {
		return outcome, after, delta, fmt.Errorf("%w: %s", err, transportError.String())
	}
	if err = sandboxsync.ValidateManifest(after); err != nil {
		return outcome, after, delta, err
	}
	return outcome, after, delta, nil
}

func (b *ContainerBackend) commitDelta(s *volumeState, before, after sandboxsync.Manifest, delta []sandboxsync.Change) error {
	expected := sandboxsync.Changes(before, after)
	if len(expected) != len(delta) {
		return errors.New("delta does not cover manifest changes")
	}
	old := map[string]sandboxsync.Entry{}
	for _, e := range before.Entries {
		old[e.Path] = e
	}
	next := map[string]sandboxsync.Entry{}
	for _, e := range after.Entries {
		next[e.Path] = e
	}
	apply := make([]sandboxsync.Change, 0, len(delta))
	virtualDirectories := map[string]bool{}
	for i, c := range delta {
		if c.Entry != expected[i].Entry || c.Delete != expected[i].Delete {
			return errors.New("delta differs from resulting manifest")
		}
		p, err := sandboxsync.Resolve(s.Record.Path, c.Path)
		if err != nil {
			// A preceding file-to-directory transition makes descendants absent
			// until Apply executes the already-validated replacement.
			virtualParent := false
			for parent := filepath.ToSlash(filepath.Dir(c.Path)); parent != "."; parent = filepath.ToSlash(filepath.Dir(parent)) {
				if virtualDirectories[parent] {
					virtualParent = true
					break
				}
			}
			if !virtualParent {
				return err
			}
			apply = append(apply, c)
			continue
		}
		info, err := os.Lstat(p)
		if os.IsNotExist(err) {
			if !c.Delete {
				if e, existed := old[c.Path]; existed && e.Directory == c.Directory {
					return fmt.Errorf("mirror deletion conflicts with command at %s; edits retained", c.Path)
				}
				apply = append(apply, c)
			}
			continue
		}
		if err != nil {
			return err
		}
		if info.IsDir() {
			if desired, ok := next[c.Path]; ok && desired.Directory {
				continue
			}
			if e, existed := old[c.Path]; !existed || !e.Directory {
				return fmt.Errorf("mirror directory conflicts with command at %s; edits retained", c.Path)
			}
			apply = append(apply, c)
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		h := sha256.Sum256(data)
		hash := hex.EncodeToString(h[:])
		if desired, ok := next[c.Path]; ok && !desired.Directory && hash == desired.Hash {
			continue
		}
		if e, ok := old[c.Path]; !ok || e.Directory || hash != e.Hash {
			return fmt.Errorf("mirror edit conflicts with command at %s; edits retained", c.Path)
		}
		if desired, ok := next[c.Path]; ok && desired.Directory {
			virtualDirectories[c.Path] = true
		}
		apply = append(apply, c)
	}
	if err := sandboxsync.Apply(s.Record.Path, apply); err != nil {
		return err
	}
	s.Manifest = after
	// The Linux result cannot certify the current Windows mirror: an editor
	// may have changed an unrelated file while the command was running, and
	// ReadDirectoryChangesW delivery is asynchronous. Rescan before the next
	// command instead of adopting the guest manifest as a host cache.
	s.HostCache = sandboxsync.Manifest{}
	s.Phase = "idle"
	s.Pending = nil
	s.PendingManifest = nil
	s.Before = sandboxsync.Manifest{}
	s.Operation = ""
	return saveVolumeState(s)
}

func (b *ContainerBackend) recoverVolume(ctx context.Context, s *volumeState) error {
	if s.Phase == "received" && s.PendingManifest != nil {
		return b.commitDelta(s, s.Before, *s.PendingManifest, s.Pending)
	}
	before := s.Manifest
	if s.Phase == "running" && s.Before.Digest != "" {
		before = s.Before
	}
	s.taint("Recovered an interrupted operation; audit history is incomplete")
	s.Container = ""
	b.removeWarmGateway(ctx, s.Gateway)
	s.Gateway = nil
	if err := b.ensureWarmContainer(ctx, s, ProcessRequest{WorkspaceRoot: s.Record.Path, Image: s.Record.BackendImageDigest, NetworkPolicy: "DENY"}); err != nil {
		return err
	}
	if s.Phase == "idle" {
		return nil
	} // A missing volume was restored from the mirror.
	r := sandboxsync.Request{Version: 1, Operation: domain.NewID("recovery"), Rules: s.Record.FileRulesVersion, Before: before}
	_, after, delta, err := b.executeVolume(ctx, s, "delta", r, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	s.Phase = "received"
	s.Before = before
	s.Pending = delta
	s.PendingManifest = &after
	if err = saveVolumeState(s); err != nil {
		return err
	}
	return b.commitDelta(s, before, after, delta)
}

func (b *ContainerBackend) Close(ctx context.Context, r domain.SandboxRecord, workspacePath string) error {
	if r.Kind != "live" {
		if err := b.Manager.validateManagedPath(r.Path); err != nil {
			return err
		}
	}
	if r.BaselinePath != "" {
		if err := b.Manager.validateManagedPath(r.BaselinePath); err != nil {
			return err
		}
	}
	if err := b.removeWorkspaceResources(ctx, r); err != nil {
		return err
	}
	if err := b.Manager.Close(ctx, r, workspacePath); err != nil {
		return err
	}
	return os.RemoveAll(controlPath(r.Path))
}

// RegisterCheckWorkspace keeps temporary verification copies in their own clean volume.
func (b *ContainerBackend) RegisterCheckWorkspace(ctx context.Context, source domain.SandboxRecord, root string) (func(), error) {
	if source.FileRulesVersion != "portable-v2" || source.Kind == "live" || source.StorageMode != "volume" {
		return func() {}, nil
	}
	r := source
	r.ID = domain.NewID("check")
	r.Path = root
	r.BaselinePath = ""
	r.Kind = "copy"
	r.StorageMode = "volume"
	r.WorkspaceVolume = ""
	r.FileRulesVersion = "portable-v2"
	if err := b.initializeWorkspace(ctx, &r, CreateRequest{PortableOnly: true}); err != nil {
		_ = b.removeWorkspaceResources(context.Background(), r)
		return nil, err
	}
	return func() { _ = b.removeWorkspaceResources(context.Background(), r); _ = os.RemoveAll(controlPath(root)) }, nil
}

var _ DeltaProcessExecutor = (*ContainerBackend)(nil)
