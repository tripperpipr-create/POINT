package acceptance_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/filepolicy"
	"local-agent-workbench/internal/sandbox"
)

// Fixed commands and source bytes remove model variability. These measurements
// are deliberately operation_replay and cannot qualify a full-task speedup.
func TestRuntimeMatrixReplay(t *testing.T) {
	if os.Getenv("POINT_RUNTIME_MATRIX") != "1" {
		t.Skip("explicit native replay opt-in required")
	}
	dir := os.Getenv("POINT_BENCH_TRIAL_DIR")
	if !filepath.IsAbs(dir) {
		t.Fatal("absolute isolated trial directory required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	matrixJSON(t, dir, "started.json", map[string]any{"pid": os.Getpid()})
	matrixGate(t, dir, "start.gate")
	stack := os.Getenv("POINT_BENCH_PROJECT")
	f, err := matrixFixture(stack)
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(dir, "project")
	if err = os.Mkdir(project, 0755); err != nil {
		t.Fatal(err)
	}
	f.Files[f.Source] = f.Solution
	for name, body := range f.Files {
		if err = os.WriteFile(filepath.Join(project, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	b := sandbox.NewContainerBackend(filepath.Join(dir, "sandboxes"))
	if os.Getenv("POINT_SANDBOX_BACKEND") == "embedded" {
		b, err = sandbox.NewEmbeddedBackend(b.Root)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = b.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	matrixJSON(t, dir, "prepared.json", map[string]any{"runtime": b.Capabilities(), "sourceDigest": matrixDigest(f.Files), "dependencyPlanDigest": matrixDigest(f.Dependencies)})
	matrixGate(t, dir, "approve.gate")
	started := time.Now().UTC()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	record, err := b.Create(ctx, sandbox.CreateRequest{WorkspaceID: "matrix-replay", WorkspacePath: project, ExecutionID: "replay", Image: os.Getenv("POINT_BENCH_IMAGE"), StorageMode: os.Getenv("POINT_SANDBOX_WORKSPACE"), FileRulesVersion: filepolicy.Current})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background(), record, project)
	var commands []any
	run := func(command, policy string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		before := time.Now()
		outcome, e := matrixReplayProcess(ctx, b, sandbox.ProcessRequest{WorkspaceRoot: record.Path, WorkingDirectory: record.Path, ShellCommand: command, NetworkPolicy: policy, AllowedNetworkHosts: f.Hosts, RunID: "replay", CacheScope: matrixDigest(filepath.Clean(dir)), Authoritative: true}, &stdout, &stderr)
		commands = append(commands, map[string]any{"command": command, "durationMs": time.Since(before).Milliseconds(), "outcome": outcome, "error": fmt.Sprint(e), "stdout": stdout.String(), "stderr": stderr.String()})
		matrixJSON(t, dir, "operations.json", commands)
		if e != nil || outcome.ExitCode != 0 || outcome.TimedOut || outcome.AuditIncomplete {
			t.Fatalf("fixed operation failed: %v %+v %s", e, outcome, stderr.String())
		}
	}
	for _, p := range f.Dependencies.Projects {
		for _, c := range p.Commands {
			run(c.Command, "ALLOWLIST")
		}
	}
	run(f.Command, "DENY")
	run(f.Command, "DENY")
	run("printf 'fixed-byte-delta\\n' > replay-delta.txt; touch /tmp/replay-left; (sleep 60) >/tmp/replay-background 2>&1 & exit 0", "DENY")
	run("test ! -e /tmp/replay-left && test \"$(cat replay-delta.txt)\" = fixed-byte-delta; rm replay-delta.txt", "DENY")
	status, err := b.CheckWorkspace(ctx, record)
	if err != nil || status.Incomplete {
		t.Fatalf("replay full tree audit: %+v %v", status, err)
	}
	ended := time.Now().UTC()
	matrixJSON(t, dir, "receipt.json", map[string]any{"kind": "operation_replay", "approvedAt": started, "deliveredAt": ended, "modelNetworkBytes": 0, "integrity": status})
	matrixGate(t, dir, "inspect.gate")
	matrixJSON(t, dir, "evidence.json", map[string]any{"kind": "operation_replay", "runtime": b.Capabilities(), "sandbox": record, "operations": commands, "integrity": status, "modelUsed": false})
	matrixJSON(t, dir, "sample.json", map[string]any{"kind": "operation_replay", "configuration": os.Getenv("POINT_BENCH_CONFIGURATION"), "project": stack, "regime": os.Getenv("POINT_BENCH_REGIME"), "approvedAt": started, "deliveredAt": ended, "evidence": "evidence.json"})
}

func matrixReplayProcess(ctx context.Context, b *sandbox.ContainerBackend, request sandbox.ProcessRequest, stdout, stderr io.Writer) (sandbox.ProcessOutcome, error) {
	if b.UsesVolume(request.WorkspaceRoot) {
		return b.RunVolumeProcess(ctx, request, stdout, stderr)
	}
	prepared, err := b.PrepareProcess(ctx, request)
	if err != nil {
		return sandbox.ProcessOutcome{}, err
	}
	prepared.Command.Stdout, prepared.Command.Stderr = stdout, stderr
	err = prepared.Command.Run()
	outcome := sandbox.ProcessOutcome{TimedOut: ctx.Err() != nil}
	if prepared.Command.ProcessState != nil {
		outcome.ExitCode = prepared.Command.ProcessState.ExitCode()
	}
	if _, ok := err.(*exec.ExitError); ok {
		err = nil // A known nonzero exit is a failed operation, not lost transport.
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if prepared.EgressDecisions != nil {
		var auditErr error
		outcome.EgressDecisions, auditErr = prepared.EgressDecisions(cleanup)
		err = errors.Join(err, auditErr)
		outcome.AuditIncomplete = auditErr != nil
	}
	if cleanupErr := prepared.Cleanup(cleanup); cleanupErr != nil {
		outcome.AuditIncomplete = true
		return outcome, fmt.Errorf("bind operation cleanup: %w", cleanupErr)
	}
	return outcome, err
}
