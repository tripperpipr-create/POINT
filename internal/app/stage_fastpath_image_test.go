package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
)

// Приёмка проверяет в том же образе, в котором работали исполнители этапа.
// 30.09.2026 квест на Node 20 (npm 10) принимался в образе по умолчанию с
// Node 24 и npm 12: тот заблокировал postinstall vue-demi, и `npm run verify`
// упал на сборке, которая в образе песочницы проходила.
func TestDeterministicAcceptRunsInSandboxRecordImage(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	backend := &recordingSandboxBackend{Manager: &sandbox.Manager{Root: t.TempDir()}}
	application, err := New(t.TempDir(), WithSandboxBackend(backend))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	world := openTestWorld(t, application)
	ctx := context.Background()

	sandboxRoot := t.TempDir()
	if err = os.WriteFile(filepath.Join(sandboxRoot, "package.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const digest = "sha256:e275fe9b63da3b04b78fcc8b90036215971cc27cc7e648f87d3f79e741332874"
	record := domain.SandboxRecord{
		ID: "sandbox_accept", WorkspaceID: world.ID, ExecutionID: "execution_accept", Kind: "copy",
		Backend: "docker", BackendImage: "point-agent-sandbox-node20:1.0.0", BackendImageDigest: digest,
		Path: sandboxRoot, CreatedAt: time.Now().UTC(),
	}
	if err = application.store.SaveSandbox(ctx, record); err != nil {
		t.Fatal(err)
	}
	exit := 0
	quest := domain.Quest{ID: "quest_accept", WorkspaceID: world.ID, Brief: &domain.TaskBrief{Criteria: []domain.AcceptanceCriterion{{
		ID: "verify", Text: "npm run verify", Kind: "verification", Tool: "run_command",
		Arguments: json.RawMessage(`{"command":"npm ci && npm run verify"}`), ExpectedExitCode: &exit,
	}}}}
	flowRun := domain.FlowRun{ID: "flowrun_accept", WorkspaceID: world.ID, QuestID: quest.ID, NodeStates: map[string]domain.FlowNodeState{}}
	node := domain.FlowNode{ID: "node_accept", Kind: domain.FlowNodeAgent, Name: "Accept", Config: map[string]any{"stageRole": domain.StageRoleAccept}}
	if err = application.store.SaveFlowRun(ctx, flowRun); err != nil {
		t.Fatal(err)
	}
	execution := domain.ExecutionInstance{ID: record.ExecutionID, WorkspaceID: world.ID, QuestID: quest.ID, FlowRunID: flowRun.ID, FlowNodeID: node.ID, SandboxID: record.ID}

	// Разрешённая человеком поправка: приёмка запускает её, а не утверждённую.
	if err = application.store.SaveCriterionAmendmentV2(ctx, domain.CriterionAmendment{
		QuestID: quest.ID, CriterionID: "verify", PreviousCommand: "npm ci && npm run verify",
		Command: "mkdir -p /tmp/pack && npm ci && npm run verify", Reason: "каталог назначения",
	}); err != nil {
		t.Fatal(err)
	}
	handled, _, err := application.tryDeterministicAccept(quest, flowRun, node, execution, domain.ProjectAgent{ID: "projectagent_accept"})
	if err != nil || !handled {
		t.Fatalf("deterministic accept not handled: handled=%v err=%v", handled, err)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.processRequests) != 1 {
		t.Fatalf("process requests: %d", len(backend.processRequests))
	}
	request := backend.processRequests[0]
	if request.Image != digest {
		t.Fatalf("accept ran in image %q, not the sandbox image %q", request.Image, digest)
	}
	if !strings.Contains(request.ShellCommand, "mkdir -p /tmp/pack && npm ci && npm run verify") {
		t.Fatalf("accept ignored the approved amendment: %q", request.ShellCommand)
	}
}
