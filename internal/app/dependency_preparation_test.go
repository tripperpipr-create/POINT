package app

import (
	"context"
	"encoding/json"
	"fmt"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/filepolicy"
	"local-agent-workbench/internal/sandbox"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type dependencyTestBackend struct {
	*recordingSandboxBackend
	mode  string
	fail  string
	calls []sandbox.ProcessRequest
}

func (b *dependencyTestBackend) PrepareProcess(ctx context.Context, r sandbox.ProcessRequest) (sandbox.PreparedProcess, error) {
	b.calls = append(b.calls, r)
	mode := b.mode
	if strings.Contains(r.ShellCommand, "\ntest -e ") {
		mode = "ready"
	}
	if !strings.Contains(r.ShellCommand, "\ninstall-") && !strings.Contains(r.ShellCommand, "\ntest -e ") {
		mode = "verify"
	}
	if b.fail != "" && strings.Contains(r.ShellCommand, "\ninstall-") {
		mode = b.fail
	}
	c := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDependencyProcessHelper$", "--", mode, r.WorkspaceRoot, r.WorkingDirectory)
	c.Env = append(os.Environ(), "POINT_DEPENDENCY_TEST=1")
	return sandbox.PreparedProcess{Command: c}, nil
}
func TestDependencyProcessHelper(t *testing.T) {
	if os.Getenv("POINT_DEPENDENCY_TEST") != "1" {
		return
	}
	args := os.Args
	mode, root, cwd := args[len(args)-3], args[len(args)-2], args[len(args)-1]
	switch mode {
	case "npm":
		os.MkdirAll(filepath.Join(cwd, "node_modules"), 0755)
	case "composer":
		os.MkdirAll(filepath.Join(cwd, "vendor"), 0755)
		os.WriteFile(filepath.Join(cwd, "vendor/autoload.php"), []byte("<?php"), 0600)
	case "change":
		os.WriteFile(filepath.Join(root, "main.txt"), []byte("changed by installer"), 0600)
	case "network":
		fmt.Fprintln(os.Stderr, "network_denied: dependency registry is outside approved policy")
		os.Exit(1)
	case "badlock":
		fmt.Fprintln(os.Stderr, "npm ci: package.json and lock file are not in sync")
		os.Exit(1)
	case "timeout":
		time.Sleep(5 * time.Second)
	case "ready":
		if _, e := os.Stat(filepath.Join(root, "lk-backend/source/node_modules")); e != nil {
			if _, e := os.Stat(filepath.Join(root, "vendor/autoload.php")); e != nil {
				os.Exit(1)
			}
		}
	case "verify":
		fmt.Println("criteria passed")
	}
	os.Exit(0)
}
func TestDependencyPreparationOnceBeforeCriteria(t *testing.T) {
	for _, manager := range []string{"npm", "go", "composer"} {
		t.Run(manager, func(t *testing.T) {
			f := newVerificationFixture(t)
			b := &dependencyTestBackend{recordingSandboxBackend: &f.backend.recordingSandboxBackend, mode: manager}
			f.app.sandboxBackend = b
			cwd := ""
			expected := []string(nil)
			if manager == "npm" {
				cwd = "lk-backend/source"
				expected = []string{cwd + "/node_modules"}
			}
			if manager == "composer" {
				expected = []string{"vendor/autoload.php"}
			}
			manifest := filepath.ToSlash(filepath.Join(cwd, "lock.txt"))
			p := filepath.Join(f.root, manifest)
			os.MkdirAll(filepath.Dir(p), 0755)
			os.WriteFile(p, []byte("locked"), 0600)
			plan := &domain.DependencyPlan{Version: "1", Projects: []domain.DependencyProject{{Cwd: cwd, Manager: manager, Commands: []domain.SetupCommand{{Command: "install-" + manager, TimeoutSeconds: 10}}, ManifestPaths: []string{manifest}, ExpectedPaths: expected}}}
			criteria := []domain.AcceptanceCriterion{}
			for _, id := range []string{"types", "tests", "lint"} {
				criteria = append(criteria, domain.AcceptanceCriterion{ID: id, Kind: "verification", Tool: "run_command", Arguments: json.RawMessage(`{"command":"verify","reason":"test"}`)})
			}
			record := f.record
			record.FileRulesVersion = filepolicy.Current
			record.StorageMode = "bind"
			result, err := f.app.runCriteriaBatch(criteriaBatchInput{Sandbox: record, QuestID: f.quest.ID, FlowRunID: f.flowRun.ID, FlowNodeID: f.accept.ID, Criteria: criteria, WorkOrder: &domain.WorkOrder{Dependencies: plan}})
			if err != nil || !result.AllOK {
				t.Fatalf("%+v %v", result, err)
			}
			installs := 0
			for _, r := range b.calls {
				if strings.Contains(r.ShellCommand, "\ninstall-") {
					installs++
				}
				if r.WorkspaceRoot == f.root {
					t.Fatal("accept ran in writer workspace")
				}
			}
			if installs != 1 {
				t.Fatalf("installs %d", installs)
			}
		})
	}
}
func TestDependencyPreparationStopsAndPreservesWriter(t *testing.T) {
	for _, mode := range []string{"badlock", "timeout", "change", "missingmanifest", "network"} {
		t.Run(mode, func(t *testing.T) {
			f := newVerificationFixture(t)
			b := &dependencyTestBackend{recordingSandboxBackend: &f.backend.recordingSandboxBackend, mode: "npm", fail: mode}
			f.app.sandboxBackend = b
			os.WriteFile(filepath.Join(f.root, "main.txt"), []byte("writer result"), 0600)
			os.WriteFile(filepath.Join(f.root, "lock.txt"), []byte("locked"), 0600)
			manifest := "lock.txt"
			if mode == "missingmanifest" {
				manifest = "missing.lock"
			}
			plan := &domain.DependencyPlan{Version: "1", Projects: []domain.DependencyProject{{Manager: "npm", Commands: []domain.SetupCommand{{Command: "install-npm", TimeoutSeconds: 1}}, ManifestPaths: []string{manifest}}}}
			record := f.record
			record.FileRulesVersion = filepolicy.Current
			record.StorageMode = "bind"
			result, err := f.app.runCriteriaBatch(criteriaBatchInput{Sandbox: record, QuestID: f.quest.ID, Criteria: f.quest.Brief.Criteria, NetworkPolicy: "DENY", WorkOrder: &domain.WorkOrder{Dependencies: plan}})
			if err != nil || !result.PreparationFailed || result.AllOK {
				t.Fatalf("%+v %v", result, err)
			}
			for _, r := range b.calls {
				if r.NetworkPolicy != "DENY" {
					t.Fatal("preparation widened the network policy")
				}
				if !strings.Contains(r.ShellCommand, "\ninstall-") {
					t.Fatal("criteria executed after failed preparation")
				}
			}
			data, _ := os.ReadFile(filepath.Join(f.root, "main.txt"))
			if string(data) != "writer result" {
				t.Fatal("writer was modified")
			}
		})
	}
}
func TestDependencyFingerprintInvalidatesReuse(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "lock")
	os.WriteFile(p, []byte("one"), 0600)
	plan := &domain.DependencyPlan{Version: "1", Projects: []domain.DependencyProject{{Manager: "npm", Commands: []domain.SetupCommand{{Command: "npm ci", TimeoutSeconds: 600}}, ManifestPaths: []string{"lock"}}}}
	one, e := dependencyFingerprint(root, plan)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(p, []byte("two"), 0600)
	two, _ := dependencyFingerprint(root, plan)
	if one == two {
		t.Fatal("lock change reused")
	}
	plan.Projects[0].Commands[0].Command += " --ignore-scripts"
	three, _ := dependencyFingerprint(root, plan)
	if two == three {
		t.Fatal("plan change reused")
	}
}
