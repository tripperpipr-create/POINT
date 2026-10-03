package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
)

// Бэкенд, чьи команды можно заставить падать: проверка перед приёмкой обязана
// вернуть агенту провал, а приёмка — не переиспользовать его.
type scriptedSandboxBackend struct {
	recordingSandboxBackend
	fail atomic.Bool
}

func (b *scriptedSandboxBackend) PrepareProcess(ctx context.Context, request sandbox.ProcessRequest) (sandbox.PreparedProcess, error) {
	prepared, err := b.recordingSandboxBackend.PrepareProcess(ctx, request)
	if err == nil && b.fail.Load() {
		prepared.Command = exec.CommandContext(ctx, "go", "no-such-point-command")
	}
	return prepared, err
}

func (b *scriptedSandboxBackend) runs() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.processRequests)
}

type verificationFixture struct {
	app       *App
	backend   *scriptedSandboxBackend
	quest     domain.Quest
	flowRun   domain.FlowRun
	writer    domain.FlowNode
	accept    domain.FlowNode
	agent     domain.ProjectAgent
	root      string
	record    domain.SandboxRecord
	execution domain.ExecutionInstance
}

func newVerificationFixture(t *testing.T) verificationFixture {
	t.Helper()
	t.Setenv("REDIS_ADDR", "")
	backend := &scriptedSandboxBackend{recordingSandboxBackend: recordingSandboxBackend{Manager: &sandbox.Manager{Root: t.TempDir()}}}
	application, err := New(t.TempDir(), WithSandboxBackend(backend))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	world := openTestWorld(t, application)
	ctx := context.Background()
	projectAgent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Приёмщик", RoleDescription: "Приёмка", Mission: "Проверять", SystemPrompt: "Проверяй",
		AllowedTools: []string{"run_command"}, Provider: domain.ProviderOllama, PrimaryModel: "test",
		MaxOutputTokens: 128, ContextWindowTokens: 4096, MaxSteps: 4, MaxDurationSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	exit := 0
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, Goal: "Ship", ResultKind: "workspace_change",
		Criteria: []domain.AcceptanceCriterion{{
			ID: "verify", Text: "npm run verify", Kind: "verification", Tool: "run_command", Deterministic: true,
			Arguments: json.RawMessage(`{"command":"npm ci && npm run verify"}`), ExpectedExitCode: &exit,
		}},
		Permissions: domain.TaskPermissions{WriteFiles: true, ExecuteCommands: true},
	}))
	if err != nil {
		t.Fatal(err)
	}
	quest := domain.Quest{ID: "quest_verify", WorkspaceID: world.ID, Title: "Квест", Importance: domain.QuestNormal, Status: domain.QuestActive,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Brief: &brief}
	if err = application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	first := domain.FlowNode{ID: "node_first", Kind: domain.FlowNodeAgent, AgentID: projectAgent.ID, Config: map[string]any{"stageRole": domain.StageRoleImplement, "writeFiles": true}}
	writer := domain.FlowNode{ID: "node_integrate", Kind: domain.FlowNodeAgent, AgentID: projectAgent.ID, Config: map[string]any{"stageRole": domain.StageRoleIntegrate, "writeFiles": true}}
	review := domain.FlowNode{ID: "node_review", Kind: domain.FlowNodeAgent, AgentID: projectAgent.ID, Config: map[string]any{"stageRole": domain.StageRoleImplReview}}
	accept := domain.FlowNode{ID: "node_accept", Kind: domain.FlowNodeAgent, AgentID: projectAgent.ID, Config: map[string]any{"stageRole": domain.StageRoleAccept}}
	flow := domain.FlowGraph{
		ID: "flow_verify", WorkspaceID: world.ID, Name: "Наряд", Nodes: []domain.FlowNode{first, writer, review, accept},
		Edges:     []domain.FlowEdge{{ID: "e1", From: first.ID, To: writer.ID}, {ID: "e2", From: writer.ID, To: review.ID}, {ID: "e3", From: review.ID, To: accept.ID}},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err = application.store.SaveFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	flowRun := domain.FlowRun{ID: "flowrun_verify", FlowID: flow.ID, WorkspaceID: world.ID, QuestID: quest.ID, Status: domain.RunRunning, NodeStates: map[string]domain.FlowNodeState{}, StartedAt: time.Now().UTC()}
	if err = application.store.SaveFlowRun(ctx, flowRun); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for name, content := range map[string]string{"package.json": "{}\n", "node_modules/x/index.js": "junk"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		if err = os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	record := domain.SandboxRecord{
		ID: "sandbox_writer", WorkspaceID: world.ID, ExecutionID: "execution_writer", Kind: "copy", Backend: "docker",
		BackendImage: "point-agent-sandbox-node20:1.0.0", BackendImageDigest: "sha256:" + strings.Repeat("e", 64),
		Path: root, CreatedAt: time.Now().UTC(),
	}
	if err = application.store.SaveSandbox(ctx, record); err != nil {
		t.Fatal(err)
	}
	execution := domain.ExecutionInstance{ID: record.ExecutionID, WorkspaceID: world.ID, ProjectAgentID: projectAgent.ID, QuestID: quest.ID, FlowRunID: flowRun.ID, FlowNodeID: writer.ID, SandboxID: record.ID, Status: domain.RunRunning, StartedAt: time.Now().UTC()}
	if err = application.store.SaveExecution(ctx, execution); err != nil {
		t.Fatal(err)
	}
	return verificationFixture{app: application, backend: backend, quest: quest, flowRun: flowRun, writer: writer, accept: accept, agent: projectAgent, root: root, record: record, execution: execution}
}

func (f verificationFixture) verify(nodeID string) agent.StageVerifyOutcome {
	return f.app.VerifyBeforeCompletion(context.Background(), agent.StageVerifyRequest{
		RunID: "run_writer", ExecutionID: f.execution.ID, QuestID: f.quest.ID, FlowRunID: f.flowRun.ID, FlowNodeID: nodeID, SandboxPath: f.root,
	})
}

// Последний пишущий этап получает исход критериев приёмки до завершения:
// провал — с причиной и командой, чтобы исправить сразу; ранний писатель,
// после которого ещё пишут, проверку не получает.
func TestLastWriterGetsAcceptanceChecksBeforeFinishing(t *testing.T) {
	f := newVerificationFixture(t)
	if outcome := f.verify("node_first"); outcome.Ran {
		t.Fatalf("an early writer was judged by the whole work order: %+v", outcome)
	}
	// Писатель плана, собранного моделью, роли не несёт — и всё равно писатель.
	planned := domain.FlowGraph{
		Nodes: []domain.FlowNode{{ID: "w", Kind: domain.FlowNodeAgent, Config: map[string]any{"planner": "model"}}, f.accept},
		Edges: []domain.FlowEdge{{From: "w", To: f.accept.ID}},
	}
	if accept, ok := lastWriterBeforeAccept(planned, "w"); !ok || accept.ID != f.accept.ID {
		t.Fatal("a role-less planned writer is not recognized as the last writer before Accept")
	}
	f.backend.fail.Store(true)
	failed := f.verify(f.writer.ID)
	if !failed.Ran || failed.Passed {
		t.Fatalf("failed checks were not reported: %+v", failed)
	}
	if !strings.Contains(failed.Feedback, "npm ci && npm run verify") || !strings.Contains(failed.Feedback, "verify") {
		t.Fatalf("feedback does not name the failed check: %q", failed.Feedback)
	}
	requests := f.backend.runs()
	f.backend.mu.Lock()
	request := f.backend.processRequests[requests-1]
	f.backend.mu.Unlock()
	// Проверка идёт в чистой копии и в образе песочницы этапа.
	if request.WorkspaceRoot == f.root || request.Image != f.record.BackendImageDigest || !request.Authoritative {
		t.Fatalf("check did not run as acceptance would: root=%q image=%q authoritative=%v", request.WorkspaceRoot, request.Image, request.Authoritative)
	}
	if _, err := os.Stat(request.WorkspaceRoot); err == nil {
		t.Fatalf("the clean copy was left behind: %s", request.WorkspaceRoot)
	}
	// Провал не запоминается как готовый ответ: та же ревизия проверяется заново.
	f.backend.fail.Store(false)
	passed := f.verify(f.writer.ID)
	if !passed.Ran || !passed.Passed || passed.Reused || f.backend.runs() != requests+1 {
		t.Fatalf("a failure was reused or the check did not rerun: %+v runs=%d", passed, f.backend.runs())
	}
	// Успех на той же ревизии повторно не гоняется.
	again := f.verify(f.writer.ID)
	if !again.Passed || !again.Reused || f.backend.runs() != requests+1 {
		t.Fatalf("a pass on the same tree was run again: %+v runs=%d", again, f.backend.runs())
	}
	t.Setenv("POINT_VERIFY_SERVICE", "off")
	if outcome := f.verify(f.writer.ID); outcome.Ran {
		t.Fatalf("the switched-off service still ran: %+v", outcome)
	}
}

func (f verificationFixture) acceptOn(t *testing.T, root, suffix string) domain.Run {
	t.Helper()
	ctx := context.Background()
	record := f.record
	record.ID, record.ExecutionID, record.Path = "sandbox_accept_"+suffix, "execution_accept_"+suffix, root
	if err := f.app.store.SaveSandbox(ctx, record); err != nil {
		t.Fatal(err)
	}
	execution := domain.ExecutionInstance{ID: record.ExecutionID, WorkspaceID: f.record.WorkspaceID, ProjectAgentID: f.agent.ID, QuestID: f.quest.ID, FlowRunID: f.flowRun.ID, FlowNodeID: f.accept.ID, SandboxID: record.ID, StartedAt: time.Now().UTC()}
	handled, _, err := f.app.tryDeterministicAccept(f.quest, f.flowRun, f.accept, execution, f.agent)
	if err != nil || !handled {
		t.Fatalf("accept: handled=%v err=%v", handled, err)
	}
	saved, err := f.app.findExecution(execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.app.store.GetRun(ctx, saved.RunID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

// Приёмка не гоняет заново то, что Point уже проверил на том же дереве, в том
// же образе и теми же командами, — и говорит об этом в доказательстве. Любая
// правка дерева возвращает полный прогон; в режиме тени приёмка гоняет всегда.
func TestAcceptReusesAPassOnlyForTheSameTree(t *testing.T) {
	f := newVerificationFixture(t)
	ctx := context.Background()
	if outcome := f.verify(f.writer.ID); !outcome.Passed {
		t.Fatalf("pre-accept check failed: %+v", outcome)
	}
	afterCheck := f.backend.runs()

	// Песочница приёмки — чистая копия песочницы писателя.
	acceptRoot := filepath.Join(t.TempDir(), "accept")
	if err := sandbox.CopyCarried(f.root, acceptRoot); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POINT_VERIFY_SERVICE", "shadow")
	shadow := f.acceptOn(t, acceptRoot, "shadow")
	if f.backend.runs() != afterCheck+1 || shadow.Status != domain.RunCompleted {
		t.Fatalf("shadow mode must still run the checks: runs=%d status=%s", f.backend.runs(), shadow.Status)
	}

	t.Setenv("POINT_VERIFY_SERVICE", "on")
	reused := f.acceptOn(t, acceptRoot, "on")
	if f.backend.runs() != afterCheck+1 || reused.Status != domain.RunCompleted || !strings.Contains(reused.Result, "переиспользовано") {
		t.Fatalf("accept reran checks that already passed on this tree: runs=%d result=%q", f.backend.runs(), reused.Result)
	}
	events, err := f.app.store.ListByRun(ctx, reused.ID)
	if err != nil {
		t.Fatal(err)
	}
	var evidence agent.CompletionEvidence
	for _, event := range events {
		if event.Type != domain.EventCompletionChecked {
			continue
		}
		var payload struct {
			Evidence agent.CompletionEvidence `json:"evidence"`
		}
		_ = json.Unmarshal(event.Data, &payload)
		evidence = payload.Evidence
	}
	if len(evidence.Criteria) != 1 || evidence.Criteria[0].Check == nil || evidence.Criteria[0].Check.ReusedFrom == nil {
		t.Fatalf("evidence hides that the check was reused: %+v", evidence)
	}
	check := evidence.Criteria[0].Check
	if problem := f.app.reusedCheckProblem(ctx, f.record.WorkspaceID, check.TreeDigest, *check.ReusedFrom); problem != "" {
		t.Fatalf("a valid reuse was rejected: %s", problem)
	}
	foreign := *check.ReusedFrom
	foreign.TreeDigest = "sha256:" + strings.Repeat("0", 64)
	if f.app.reusedCheckProblem(ctx, f.record.WorkspaceID, check.TreeDigest, foreign) == "" {
		t.Fatal("the evidence gate accepted a reuse from another tree")
	}
	missing := *check.ReusedFrom
	missing.ResultID = "verification_missing"
	if f.app.reusedCheckProblem(ctx, f.record.WorkspaceID, check.TreeDigest, missing) == "" {
		t.Fatal("the evidence gate accepted a reuse without a record")
	}

	// Дерево изменилось после проверки: приёмка гоняет сама.
	if err = os.WriteFile(filepath.Join(acceptRoot, "package.json"), []byte("{\"name\":\"changed\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.acceptOn(t, acceptRoot, "changed")
	if f.backend.runs() != afterCheck+2 {
		t.Fatalf("accept reused a result from a different tree: runs=%d", f.backend.runs())
	}
}

func TestVerificationBatchKeyBindsEverythingTheOutcomeDependsOn(t *testing.T) {
	commands := []executedCriterion{{CriterionID: "verify", Tool: "run_command", Arguments: json.RawMessage(`{"command":"npm test","timeoutSeconds":600,"reason":"x"}`)}}
	base := verificationBatchKey("sha256:tree", "sha256:image", "DENY", nil, commands)
	same := verificationBatchKey("sha256:tree", "sha256:image", "deny", []string{}, []executedCriterion{{CriterionID: "verify", Tool: "run_command", Arguments: json.RawMessage(`{"reason":"other","command":"npm test","timeoutSeconds":300}`)}})
	if base != same {
		t.Fatal("the key depends on the reason or the time allowance of a command")
	}
	for name, other := range map[string]string{
		"tree":     verificationBatchKey("sha256:other", "sha256:image", "DENY", nil, commands),
		"image":    verificationBatchKey("sha256:tree", "sha256:other", "DENY", nil, commands),
		"network":  verificationBatchKey("sha256:tree", "sha256:image", "ALLOWLIST", []string{"registry.npmjs.org"}, commands),
		"command":  verificationBatchKey("sha256:tree", "sha256:image", "DENY", nil, []executedCriterion{{CriterionID: "verify", Tool: "run_command", Arguments: json.RawMessage(`{"command":"npm run build"}`)}}),
		"expected": verificationBatchKey("sha256:tree", "sha256:image", "DENY", nil, []executedCriterion{{CriterionID: "verify", Tool: "run_command", Arguments: commands[0].Arguments, ExpectedExitCode: 1}}),
		"rules":    verificationBatchKey("sha256:tree", "sha256:image", "DENY", nil, commands, "portable-v2"),
	} {
		if other == base {
			t.Fatalf("the key ignores a change of the %s", name)
		}
	}
}

// С 03.10 умолчание — переиспользование: приёмка не повторяет проверки,
// прошедшие на том же дереве, в том же образе и теми же командами.
func TestVerifyServiceDefaultsToReuse(t *testing.T) {
	t.Setenv("POINT_VERIFY_SERVICE", "")
	if mode := verifyServiceMode(); mode != verifyServiceOn {
		t.Fatalf("умолчание %q", mode)
	}
	t.Setenv("POINT_VERIFY_SERVICE", "shadow")
	if mode := verifyServiceMode(); mode != verifyServiceShadow {
		t.Fatalf("явный shadow потерян: %q", mode)
	}
}
