package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/filepolicy"
)

func volumeFixture(t *testing.T) (*ContainerBackend, CreateRequest) {
	t.Helper()
	if os.Getenv("POINT_VOLUME_INTEGRATION") != "1" {
		t.Skip("set POINT_VOLUME_INTEGRATION=1 with Docker and a static helper")
	}
	root := t.TempDir()
	source := filepath.Join(root, "project")
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "hello.txt"), []byte("original\r\n"), 0644); err != nil {
		t.Fatal(err)
	}
	b := NewContainerBackend(filepath.Join(root, "sandboxes"))
	if os.Getenv("POINT_SANDBOX_BACKEND") == "embedded" {
		var err error
		b, err = NewEmbeddedBackend(filepath.Join(root, "sandboxes"))
		if err != nil {
			t.Fatal(err)
		}
	}
	if image := os.Getenv("POINT_SANDBOX_IMAGE"); image != "" {
		b.Image = image
	}
	if err := b.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	return b, CreateRequest{WorkspaceID: "volume-fixture", WorkspacePath: source, ExecutionID: "fixture", StorageMode: "volume", FileRulesVersion: filepolicy.Current}
}

func TestVolumeIntegrationInheritanceAndRecovery(t *testing.T) {
	b, req := volumeFixture(t)
	r, err := b.Create(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background(), r, req.WorkspacePath)
	run := func(recordRoot, command string) (ProcessOutcome, error) {
		return b.RunVolumeProcess(context.Background(), ProcessRequest{WorkspaceRoot: recordRoot, WorkingDirectory: recordRoot, ShellCommand: command, NetworkPolicy: "DENY", RunID: "fixture"}, &bytes.Buffer{}, &bytes.Buffer{})
	}
	if out, err := run(r.Path, "mkdir -p node_modules/pkg; printf dependency > node_modules/pkg/dep; ln -s pkg node_modules/link"); err != nil || out.ExitCode != 0 {
		t.Fatalf("seed %+v %v", out, err)
	}
	childReq := req
	childReq.SeedPath = r.Path
	childReq.SeedVolume = r.WorkspaceVolume
	childReq.SeedImageDigest = r.BackendImageDigest
	childReq.ParentSandboxID = r.ID
	child, err := b.Create(context.Background(), childReq)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background(), child, req.WorkspacePath)
	if out, err := run(child.Path, "test -L node_modules/link && test $(cat node_modules/link/dep) = dependency; printf child > node_modules/pkg/dep"); err != nil || out.ExitCode != 0 {
		t.Fatalf("inherit %+v %v", out, err)
	}
	if out, err := run(r.Path, "test $(cat node_modules/pkg/dep) = dependency"); err != nil || out.ExitCode != 0 {
		t.Fatalf("parent mutated %+v %v", out, err)
	}
	childReq.PortableOnly = true
	clean, err := b.Create(context.Background(), childReq)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background(), clean, req.WorkspacePath)
	if out, err := run(clean.Path, "test ! -e node_modules"); err != nil || out.ExitCode != 0 {
		t.Fatalf("clean %+v %v", out, err)
	}
	// A disconnected CLI is recovered without replaying a command whose outcome is unknown.
	s, err := b.stateFor(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	s.Phase = "running"
	s.Before = s.Manifest
	s.Operation = "interrupted"
	if err = saveVolumeState(s); err != nil {
		t.Fatal(err)
	}
	if err = b.runInfrastructure(context.Background(), "exec", s.Container, "/bin/sh", "-c", "echo recovered > recovered.txt"); err != nil {
		t.Fatal(err)
	}
	b.workspaceStates.Delete(r.Path)
	s.Watcher.Close()
	if out, err := run(r.Path, "test -f recovered.txt"); err != nil || out.ExitCode != 0 || !out.AuditIncomplete {
		t.Fatalf("recover %+v %v", out, err)
	}
	status, err := b.CheckWorkspace(context.Background(), r)
	if err != nil || !status.Incomplete {
		t.Fatalf("recovery integrity %+v %v", status, err)
	}
	if err = b.StopWorkspace(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if err = b.runInfrastructure(context.Background(), "volume", "rm", r.WorkspaceVolume); err != nil {
		t.Fatal(err)
	}
	if out, err := run(r.Path, "test -f recovered.txt && test ! -e node_modules"); err != nil || out.ExitCode != 0 || !out.AuditIncomplete {
		t.Fatalf("lost volume %+v %v", out, err)
	}
	if _, err = os.Stat(filepath.Join(r.Path, "hello.txt")); err != nil {
		t.Fatalf("lost volume erased mirror: %v", err)
	}
}

func TestVolumeIntegrationAllowlistAndIsolation(t *testing.T) {
	b, req := volumeFixture(t)
	r, err := b.Create(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background(), r, req.WorkspacePath)
	request := ProcessRequest{WorkspaceRoot: r.Path, WorkingDirectory: r.Path, NetworkPolicy: "ALLOWLIST", AllowedNetworkHosts: []string{"registry.npmjs.org"}, RunID: "fixture", ShellCommand: `python3 -c "import urllib.request; r=urllib.request.urlopen('https://registry.npmjs.org/-/ping', timeout=15); assert r.status == 200"`}
	var stdout, stderr bytes.Buffer
	out, err := b.RunVolumeProcess(context.Background(), request, &stdout, &stderr)
	if err != nil || out.ExitCode != 0 || len(out.EgressDecisions) == 0 {
		t.Fatalf("allowlist %+v %v: %s %s", out, err, stdout.String(), stderr.String())
	}
	// Reattach after an idle core restart while the old stage still holds its network.
	old, err := b.stateFor(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	if old.Gateway.input != nil {
		_ = old.Gateway.input.Close()
	}
	if old.Gateway.command != nil && old.Gateway.command.Process != nil {
		_ = old.Gateway.command.Process.Kill()
		_ = old.Gateway.command.Wait()
	}
	old.Watcher.Close()
	b.workspaceStates.Delete(r.Path)
	out, err = b.RunVolumeProcess(context.Background(), request, &stdout, &stderr)
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("idle restart allowlist %+v %v", out, err)
	}
	request.NetworkPolicy = "DENY"
	request.AllowedNetworkHosts = nil
	request.ShellCommand = `test $(id -u) -ne 0 && test ! -S /var/run/docker.sock && test ! -e /source && ! touch /root/leak && ! python3 -c "import urllib.request; urllib.request.urlopen('https://registry.npmjs.org/-/ping', timeout=2)"`
	out, err = b.RunVolumeProcess(context.Background(), request, &stdout, &stderr)
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("deny/isolation %+v %v: %s", out, err, stderr.String())
	}
	request.NetworkPolicy = "ALLOWLIST"
	request.AllowedNetworkHosts = []string{"example.com"}
	request.ShellCommand = `! python3 -c "import urllib.request; urllib.request.urlopen('https://registry.npmjs.org/-/ping', timeout=2)"`
	out, err = b.RunVolumeProcess(context.Background(), request, &stdout, &stderr)
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("policy replaced %+v %v", out, err)
	}
}

func TestVolumeIntegrationRunResetAndDelta(t *testing.T) {
	b, req := volumeFixture(t)
	r, err := b.Create(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := b.Close(context.Background(), r, req.WorkspacePath); err != nil {
			t.Error(err)
		}
	}()
	run := func(command string) ProcessOutcome {
		t.Helper()
		var stdout, stderr bytes.Buffer
		out, err := b.RunVolumeProcess(context.Background(), ProcessRequest{WorkspaceRoot: r.Path, WorkingDirectory: r.Path, Image: ExecutionImageForRecord(r), ShellCommand: command, NetworkPolicy: "DENY", RunID: "fixture"}, &stdout, &stderr)
		if err != nil {
			t.Fatalf("run: %v, %s %s", err, stdout.String(), stderr.String())
		}
		if out.ExitCode != 0 {
			t.Logf("command exit %d: %s %s", out.ExitCode, stdout.String(), stderr.String())
		}
		return out
	}
	if out := run("printf 'changed\\r\\n' > hello.txt; mkdir -p node_modules; printf dependency > node_modules/dep; touch /tmp/left; (sleep 60) >/tmp/bg.log 2>&1 & exit 7"); out.ExitCode != 7 {
		t.Fatalf("exit %d", out.ExitCode)
	}
	data, err := os.ReadFile(filepath.Join(r.Path, "hello.txt"))
	if err != nil || string(data) != "changed\r\n" {
		t.Fatalf("mirror %q: %v", data, err)
	}
	if _, err = os.Stat(filepath.Join(r.Path, "node_modules")); !os.IsNotExist(err) {
		t.Fatalf("dependencies leaked to mirror: %v", err)
	}
	if out := run("test ! -e /tmp/left || { echo temporary-state-remained; exit 1; }; test -f node_modules/dep || { echo dependency-state-lost; exit 1; }; test $(find /proc -maxdepth 1 -name '[0-9]*' | wc -l) -lt 10 || { ps -ef; exit 1; }"); out.ExitCode != 0 {
		t.Fatal(out)
	}
	// Moby's default tmpfs is noexec; Podman's default differs. Generated test
	// binaries must run inside the bounded unprivileged command namespace.
	if out := run(`printf 'package compile\nimport "testing"\nfunc TestCompile(t *testing.T) {}\n' > /tmp/compiler_test.go; go test -count=1 /tmp/compiler_test.go`); out.ExitCode != 0 || out.AuditIncomplete {
		t.Fatalf("temporary compiler/test execution: %+v", out)
	}
	if out := run("test ! -e /tmp/compiler_test.go"); out.ExitCode != 0 {
		t.Fatalf("compiler temporary state was not reset: %+v", out)
	}
	if err = os.WriteFile(filepath.Join(r.Path, "host.txt"), []byte("host edit"), 0644); err != nil {
		t.Fatal(err)
	}
	if out := run("test \"$(cat host.txt)\" = 'host edit'"); out.ExitCode != 0 {
		t.Fatal(out)
	}
	// A custom process can execute inside a dependency directory absent from the mirror.
	if out := run("mkdir -p node_modules/pkg; printf 'import pathlib; pathlib.Path(\"../../from-program.txt\").write_text(\"custom cwd\")' > node_modules/pkg/tool.py"); out.ExitCode != 0 {
		t.Fatal(out)
	}
	cwd, err := b.ResolveProcessDirectory(r.Path, "node_modules/pkg")
	if err != nil {
		t.Fatal(err)
	}
	custom, err := b.RunVolumeProcess(context.Background(), ProcessRequest{WorkspaceRoot: r.Path, WorkingDirectory: cwd, Program: "python3", Arguments: []string{"tool.py"}, NetworkPolicy: "DENY", RunID: "fixture"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil || custom.ExitCode != 0 {
		t.Fatalf("custom cwd %+v: %v", custom, err)
	}
	if content, err := os.ReadFile(filepath.Join(r.Path, "from-program.txt")); err != nil || string(content) != "custom cwd" {
		t.Fatalf("custom cwd mirror %q: %v", content, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := b.RunVolumeProcess(ctx, ProcessRequest{WorkspaceRoot: r.Path, WorkingDirectory: r.Path, ShellCommand: "echo timeout > timeout.txt; sleep 30", NetworkPolicy: "DENY", RunID: "fixture"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil || !out.TimedOut {
		t.Fatalf("timeout outcome %+v: %v", out, err)
	}
	if _, err = os.Stat(filepath.Join(r.Path, "timeout.txt")); err != nil {
		t.Fatal(err)
	}
	status, err := b.CheckWorkspace(context.Background(), r)
	if err != nil || status.Incomplete {
		t.Fatalf("integrity %+v %v", status, err)
	}
}

func TestVolumeIntegrationInterruptedCommandIsNotReplayed(t *testing.T) {
	b, req := volumeFixture(t)
	r, err := b.Create(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background(), r, req.WorkspacePath)
	type completed struct {
		outcome ProcessOutcome
		err     error
	}
	done := make(chan completed, 1)
	go func() {
		out, err := b.RunVolumeProcess(context.Background(), ProcessRequest{WorkspaceRoot: r.Path, WorkingDirectory: r.Path, ShellCommand: "echo once >> invocation.txt; echo ready > ready.txt; sleep 60", NetworkPolicy: "DENY", RunID: "interruption"}, &bytes.Buffer{}, &bytes.Buffer{})
		done <- completed{out, err}
	}()
	name := "point-stage-" + r.ID
	deadline := time.Now().Add(10 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		if err := b.runInfrastructure(context.Background(), "exec", name, "/bin/sh", "-c", "test -f /workspace/ready.txt"); err == nil {
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatal("command never wrote its readiness marker")
	}
	daemonSignal := os.Getenv("POINT_VOLUME_DAEMON_RESTART_READY")
	if daemonSignal != "" {
		body, _ := json.Marshal(r)
		if err := os.WriteFile(daemonSignal, body, 0600); err != nil {
			t.Fatal(err)
		}
		t.Log("DAEMON_RESTART_READY")
	} else if err := b.runInfrastructure(context.Background(), "stop", "--time", "0", name); err != nil {
		t.Fatal(err)
	}
	select {
	case stopped := <-done:
		if stopped.err == nil || !stopped.outcome.AuditIncomplete {
			t.Fatalf("interruption: %+v", stopped)
		}
	case <-time.After(3 * time.Minute):
		t.Fatal("command recovery did not finish")
	}
	if daemonSignal != "" {
		deadline := time.Now().Add(3 * time.Minute)
		available := false
		for time.Now().Before(deadline) {
			probeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := b.Probe(probeCtx)
			cancel()
			if err == nil {
				available = true
				break
			}
			time.Sleep(time.Second)
		}
		if !available {
			t.Fatal("Docker daemon did not return")
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := b.CheckWorkspace(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(r.Path, "invocation.txt"))
	if err != nil || string(data) != "once\n" {
		t.Fatalf("unknown command was replayed or its delta lost: %q %v", data, err)
	}
	status, err := b.CheckWorkspace(context.Background(), r)
	if err != nil || !status.Incomplete || status.FreshCheck {
		t.Fatalf("interruption integrity: %+v %v", status, err)
	}
}

func TestVolumeIntegrationDetachedProcessAndControlDescriptors(t *testing.T) {
	b, req := volumeFixture(t)
	r, err := b.Create(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background(), r, req.WorkspacePath)
	run := func(command string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		out, err := b.RunVolumeProcess(context.Background(), ProcessRequest{WorkspaceRoot: r.Path, WorkingDirectory: r.Path, ShellCommand: command, NetworkPolicy: "DENY", RunID: "isolation"}, &stdout, &stderr)
		if err != nil || out.ExitCode != 0 {
			t.Fatalf("isolation: %+v %v %s %s", out, err, stdout.String(), stderr.String())
		}
	}
	run(`python3 -c 'import os,time; p=os.fork(); os._exit(0) if p else None; os.setsid(); p=os.fork(); os._exit(0) if p else None; open("daemon.pid","w").write(str(os.getpid())); time.sleep(60)'`)
	run(`test ! -e /proc/$(cat daemon.pid); test $(id -u) -ne 0; test ! -e /var/run/docker.sock; test ! -e /source; test "$(awk '/CapEff/ {print $2}' /proc/self/status)" = 0000000000000000; test "$(awk '/NoNewPrivs/ {print $2}' /proc/self/status)" = 1; ! touch /root-write-probe; ! cat /proc/1/environ; ! cat /proc/1/fd/0`)
	state, err := b.stateFor(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := b.output(context.Background(), "inspect", state.Container, "--format", `{{.HostConfig.ReadonlyRootfs}}|{{.HostConfig.PidsLimit}}|{{.HostConfig.Memory}}|{{.HostConfig.NanoCpus}}|{{.HostConfig.PidMode}}|{{.HostConfig.IpcMode}}`)
	if err != nil || !strings.HasPrefix(strings.TrimSpace(settings), "true|") || strings.Contains(settings, "|host") {
		t.Fatalf("resource/isolation configuration: %s %v", settings, err)
	}
}

func TestVolumeIntegrationJoinMatchesBindAndStartsClean(t *testing.T) {
	var diffs [][]DiffEntry
	for _, mode := range []string{"bind", "volume"} {
		b, req := volumeFixture(t)
		// Embedded runtimes deliberately reject host bind mounts. Compare their
		// merge result to the same local portable merge used by Docker bind.
		var merger MergeBackend = b
		if mode == "bind" && b.Engine != nil {
			merger = &Manager{Root: b.Root + "-reference"}
		}
		req.StorageMode = mode
		var seeds []MergeSeed
		for _, name := range []string{"left", "right"} {
			branch, err := merger.Create(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			defer merger.Close(context.Background(), branch, req.WorkspacePath)
			if err := os.WriteFile(filepath.Join(branch.Path, name+".txt"), []byte(name+"\r\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if mode == "volume" {
				out, err := b.RunVolumeProcess(context.Background(), ProcessRequest{WorkspaceRoot: branch.Path, WorkingDirectory: branch.Path, ShellCommand: "mkdir -p node_modules; echo dependency > node_modules/dep", NetworkPolicy: "DENY", RunID: "join"}, &bytes.Buffer{}, &bytes.Buffer{})
				if err != nil || out.ExitCode != 0 {
					t.Fatalf("seed %+v %v", out, err)
				}
			}
			seeds = append(seeds, MergeSeed{ExecutionID: name, SandboxID: branch.ID, Path: branch.Path})
		}
		joined, err := merger.Merge(context.Background(), MergeRequest{StorageMode: mode, FileRulesVersion: filepolicy.Current, WorkspaceID: req.WorkspaceID, ExecutionID: "join", BasePath: req.WorkspacePath, Seeds: seeds})
		if err != nil || joined.Record.ID == "" {
			t.Fatalf("join %s: %+v %v", mode, joined, err)
		}
		defer merger.Close(context.Background(), joined.Record, req.WorkspacePath)
		diff, err := (&Manager{}).DiffWithRules(context.Background(), req.WorkspacePath, joined.Record.Path, filepolicy.Current)
		if err != nil {
			t.Fatal(err)
		}
		diffs = append(diffs, diff)
		if mode == "volume" {
			out, err := b.RunVolumeProcess(context.Background(), ProcessRequest{WorkspaceRoot: joined.Record.Path, WorkingDirectory: joined.Record.Path, ShellCommand: "test ! -e node_modules && test -f left.txt && test -f right.txt", NetworkPolicy: "DENY", RunID: "join"}, &bytes.Buffer{}, &bytes.Buffer{})
			if err != nil || out.ExitCode != 0 {
				t.Fatalf("joined volume is not clean: %+v %v", out, err)
			}
		}
	}
	if len(diffs[0]) != 2 || !reflect.DeepEqual(diffs[0], diffs[1]) {
		t.Fatalf("join changes differ: %+v %+v", diffs[0], diffs[1])
	}
}

func TestVolumeIntegrationCollectorPreservesLiveRecentAndForeignVolumes(t *testing.T) {
	b, _ := volumeFixture(t)
	ctx := context.Background()
	names := map[string]string{}
	for _, kind := range []string{"orphan", "live", "recent", "foreign"} {
		r := domain.SandboxRecord{ID: domain.NewID("gc-" + kind), WorkspaceID: "gc-fixture"}
		name := domain.NewID("point-gc")
		names[kind] = name
		labels := b.labels(r, "workspace")
		for i := range labels {
			if strings.HasPrefix(labels[i], "point.created=") && kind != "recent" {
				labels[i] = "point.created=2000-01-01T00:00:00Z"
			}
			if strings.HasPrefix(labels[i], "point.owner=") && kind == "foreign" {
				labels[i] = "point.owner=foreign-fixture"
			}
		}
		args := append([]string{"volume", "create"}, labels...)
		if err := b.runInfrastructure(ctx, append(args, name)...); err != nil {
			t.Fatal(err)
		}
		defer b.runInfrastructure(ctx, "volume", "rm", name)
		if kind == "live" {
			names["live-id"] = r.ID
		}
	}
	if err := b.CleanupOrphans(ctx, []domain.SandboxRecord{{ID: names["live-id"]}}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"orphan", "live", "recent", "foreign"} {
		_, err := b.output(ctx, "volume", "inspect", names[kind])
		if kind == "orphan" {
			if !missingDockerResource(err) {
				t.Fatalf("orphan survived: %v", err)
			}
		} else if err != nil {
			t.Fatalf("removed %s: %v", kind, err)
		}
	}
}
