package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/executors"
	"local-agent-workbench/internal/mcp"
)

type fakeCLI struct {
	last executors.Request
}

func (f *fakeCLI) Kind() executors.Kind { return executors.KindCodex }
func (f *fakeCLI) Capabilities() executors.Capabilities {
	return executors.Capabilities{Headless: true, MCP: true, NativeWrite: true, NativeShell: true}
}
func (f *fakeCLI) Probe(context.Context) error { return nil }
func (f *fakeCLI) Start(ctx context.Context, request executors.Request, onEvent func(executors.Event) error) (executors.Result, error) {
	return f.Run(ctx, request, onEvent)
}
func (f *fakeCLI) Run(_ context.Context, request executors.Request, onEvent func(executors.Event) error) (executors.Result, error) {
	f.last = request
	if onEvent != nil {
		_ = onEvent(executors.Event{Text: "stream"})
	}
	return executors.Result{Text: "CLI completed via MCP", ExitCode: 0}, nil
}
func (f *fakeCLI) Resume(context.Context, executors.Request, func(executors.Event) error) (executors.Result, error) {
	return executors.Result{}, nil
}
func (f *fakeCLI) Stop(string) error { return nil }

type fakeToolSessions struct{}

func (fakeToolSessions) OpenRunToolSession(_ string, executor mcp.Executor) (string, func(), error) {
	if executor == nil {
		return "", nil, errToolJournalIntegrity
	}
	_ = executor.Definitions()
	return `{"mcpServers":{"point":{"type":"http","url":"http://127.0.0.1/mcp"}}}`, func() {}, nil
}

func TestHeadlessCLIUsesMCPOnlyAndCompletes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	fake := &fakeCLI{}
	engine.SetCLIFactory(func(executors.Kind) executors.Executor { return fake })
	engine.SetToolSessionOpener(fakeToolSessions{})
	profile := domain.DefaultProfile()
	profile.Provider = domain.ProviderCodexCLI
	profile.Model = "auto"
	profile.AllowedTools = []string{"read_file"}
	profile.MaxDurationSeconds = 5
	profile.MaxSteps = 2
	done := make(chan domain.Run, 1)
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: root},
		SandboxPath:   root,
		Task:          "summarize the workspace",
		OnFinished:    func(finished domain.Run) { done <- finished },
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.ID == "" {
		t.Fatal("run id missing")
	}
	select {
	case finished := <-done:
		if finished.Status != domain.RunCompleted {
			t.Fatalf("run=%#v", finished)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CLI run did not finish")
	}
	if !fake.last.MCPOnly || fake.last.WriteFiles || fake.last.ExecuteCommands {
		t.Fatalf("CLI request escaped MCP-only: %#v", fake.last)
	}
}
