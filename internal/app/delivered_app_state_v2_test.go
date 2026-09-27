package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// streamingDeliveredAppRunner — раннер с построчным выводом и пробами, как
// настоящий Docker: построчно отдаёт вывод, может задержать команду до
// сигнала и отвечает на пробу адреса заданным типом содержимого.
type streamingDeliveredAppRunner struct {
	mu       sync.Mutex
	calls    [][]string
	release  chan struct{}
	started  chan struct{}
	services int
}

func (runner *streamingDeliveredAppRunner) Run(_ context.Context, _ string, arguments ...string) (string, error) {
	runner.mu.Lock()
	runner.calls = append(runner.calls, append([]string(nil), arguments...))
	runner.mu.Unlock()
	return "", nil
}

func (runner *streamingDeliveredAppRunner) RunStreaming(_ context.Context, _ string, onLine func(string), arguments ...string) (string, error) {
	runner.mu.Lock()
	runner.calls = append(runner.calls, append([]string(nil), arguments...))
	runner.mu.Unlock()
	onLine("Container app  Creating")
	if runner.started != nil {
		close(runner.started)
	}
	if runner.release != nil {
		<-runner.release
	}
	onLine("Container app  Started")
	return "Container app  Creating\nContainer app  Started\n", nil
}

func (runner *streamingDeliveredAppRunner) RunningServices(context.Context, string, string) (int, error) {
	return runner.services, nil
}

func (runner *streamingDeliveredAppRunner) ProbeURL(context.Context, string) (int, string, error) {
	return 200, "text/html; charset=utf-8", nil
}

type deliveredAppFixtureV2 struct {
	application *App
	questID     string
	request     DeliveredApplicationControlRequestV2
}

func newDeliveredAppFixtureV2(t *testing.T, runner DeliveredAppRunner, withCompose bool, category string, checks []domain.CompletionCheck, url string) deliveredAppFixtureV2 {
	t.Helper()
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir(), WithDeliveredAppRunner(runner))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	workspace := openTestWorld(t, application)
	composeFile := ""
	if withCompose {
		composeFile = "compose.yaml"
		if err = os.WriteFile(filepath.Join(workspace.Path, composeFile), []byte("services: {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	exitCode := 0
	order := domain.WorkOrder{
		WorkspaceID: workspace.ID, State: "ready", Goal: "deliver app", Scope: []string{"app"},
		Criteria:  []domain.AcceptanceCriterion{{ID: "tests", Kind: "verification", Text: "tests pass", Tool: "run_command", Arguments: json.RawMessage(`{"command":"go test ./..."}`), ExpectedExitCode: &exitCode}},
		Workspace: domain.WorkspacePlan{Mode: "existing", Path: workspace.Path, Isolation: "snapshot"},
		Stack:     domain.StackPresetRef{ID: "stack", Version: "1", Category: category, Source: "benchmark"},
		Routing:   domain.ModelRoutingPolicy{Mode: "fixed", FixedConnectionID: "connection", FixedModel: "model"},
		Budget:    domain.BudgetEnvelope{Preset: "small", Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxAttempts: 1},
		Delivery:  domain.DeliveryPolicy{ApplyMode: "automatic", CommitMode: "none", KeepPartialDays: 30},
		CreatedAt: now, UpdatedAt: now,
	}
	if len(checks) > 0 {
		order.Completion = domain.CompletionProfile{ID: "profile", Version: "1", Checks: checks}
	}
	order, err = application.store.SaveWorkOrderV2(context.Background(), order)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := application.store.ApproveWorkOrderV2(context.Background(), order.ID, order.Version, domain.WorkOrderDigest(order), "approve-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	revision := "sha256:workspace"
	bundle := domain.EvidenceBundle{
		Version: domain.CurrentWorkOrderEvidenceVersion, ID: "evidence-" + t.Name(), QuestID: approval.QuestID, PointVersion: Version,
		BriefDigest: domain.WorkOrderDigest(order), SourceDigest: domain.WorkOrderSourceDigest(order),
		EnvironmentDigest: workOrderEnvironmentDigestV2(order), SourceVersions: []domain.SourceSnapshotRef{}, StackPreset: order.Stack,
		Criteria:           []domain.CriterionEvidence{{CriterionID: "tests", Satisfied: true, Tool: "run_command", Command: "go test ./...", ExitCode: &exitCode}},
		VerificationChecks: []domain.VerificationCheck{{ID: "tests", Kind: "acceptance", Command: "go test ./...", ExitCode: &exitCode, Satisfied: true}},
		ModelCalls:         []domain.ModelCallLedgerEntry{{ID: "model-call-" + t.Name(), Provider: "test", Model: "model", Role: "writer", CostKnown: true, UsageReported: true, CreatedAt: now}},
		DeliveryVerified:   true, DeliveryTarget: workspace.Path, WorkspaceRevision: revision, CreatedAt: now,
		DeliveryReceipt: &domain.DeliveryReceipt{
			ID: "delivery-" + t.Name(), QuestID: approval.QuestID, WorkOrderDigest: domain.WorkOrderDigest(order),
			Target: workspace.Path, WorkspaceRevision: revision, ComposeFile: composeFile, URL: url, DeliveredAt: now,
		},
	}
	if _, err = application.store.FinalizeWorkOrderQuestV2(context.Background(), approval.QuestID, bundle); err != nil {
		t.Fatal(err)
	}
	return deliveredAppFixtureV2{application: application, questID: approval.QuestID, request: DeliveredApplicationControlRequestV2{
		Version: order.Version, WorkOrderDigest: domain.WorkOrderDigest(order), DeliveryReceiptID: "delivery-" + t.Name(),
	}}
}

func TestDeliveredApplicationStartStreamsOutputAndWaitsForPage(t *testing.T) {
	runner := &streamingDeliveredAppRunner{services: 1}
	fixture := newDeliveredAppFixtureV2(t, runner, true, "web", nil, "http://127.0.0.1:18080")
	request := fixture.request
	request.IdempotencyKey = "start-web"
	control, err := fixture.application.ControlDeliveredApplicationV2(context.Background(), fixture.questID, "start", request)
	if err != nil || control.Status != "running" || !control.Ready || control.HTTPStatus != 200 || !strings.HasPrefix(control.ContentType, "text/html") {
		t.Fatalf("start = %#v err=%v", control, err)
	}
	state, err := fixture.application.DeliveredApplicationStateV2(context.Background(), fixture.questID, false)
	if err != nil {
		t.Fatal(err)
	}
	if state.Kind != "web" || state.Launch != "compose" || state.Status != "running" || state.InFlight || !state.Ready {
		t.Fatalf("state = %#v", state)
	}
	for _, want := range []string{"$ docker compose -f compose.yaml up -d", "Container app  Started", "Ждём ответа http://127.0.0.1:18080 …", "Отвечает: 200 · text/html"} {
		if !slices.Contains(state.Lines, want) {
			t.Fatalf("state lines lost %q: %#v", want, state.Lines)
		}
	}
}

// Журнал помнит последнее действие, а не то, что есть сейчас: проба живых
// контейнеров исправляет «работает» на «остановлено», если их нет.
func TestDeliveredApplicationStateProbeOverridesStaleJournal(t *testing.T) {
	runner := &streamingDeliveredAppRunner{services: 0}
	fixture := newDeliveredAppFixtureV2(t, runner, true, "web", nil, "")
	request := fixture.request
	request.IdempotencyKey = "start-service"
	if _, err := fixture.application.ControlDeliveredApplicationV2(context.Background(), fixture.questID, "start", request); err != nil {
		t.Fatal(err)
	}
	stale, _ := fixture.application.DeliveredApplicationStateV2(context.Background(), fixture.questID, false)
	probed, err := fixture.application.DeliveredApplicationStateV2(context.Background(), fixture.questID, true)
	if err != nil || stale.Kind != "service" || stale.Status != "running" || !probed.Probed || probed.Status != "stopped" {
		t.Fatalf("stale=%#v probed=%#v err=%v", stale, probed, err)
	}
}

// Второй запуск поверх идущего отвергается, а идущий виден со своим выводом.
func TestDeliveredApplicationRejectsConcurrentActionAndShowsLiveOutput(t *testing.T) {
	runner := &streamingDeliveredAppRunner{services: 1, release: make(chan struct{}), started: make(chan struct{})}
	fixture := newDeliveredAppFixtureV2(t, runner, true, "web", nil, "")
	done := make(chan error, 1)
	go func() {
		request := fixture.request
		request.IdempotencyKey = "start-slow"
		_, err := fixture.application.ControlDeliveredApplicationV2(context.Background(), fixture.questID, "start", request)
		done <- err
	}()
	<-runner.started
	live, err := fixture.application.DeliveredApplicationStateV2(context.Background(), fixture.questID, true)
	if err != nil || !live.InFlight || live.Status != "executing" || !slices.Contains(live.Lines, "Container app  Creating") || live.Probed {
		t.Fatalf("live = %#v err=%v", live, err)
	}
	second := fixture.request
	second.IdempotencyKey = "start-again"
	if _, err = fixture.application.ControlDeliveredApplicationV2(context.Background(), fixture.questID, "start", second); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("concurrent start err=%v", err)
	}
	close(runner.release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

// Консольное приложение без compose запускают командой в терминале — той,
// которую профиль завершения уже проверял.
func TestDeliveredApplicationKindForConsoleApp(t *testing.T) {
	runner := &streamingDeliveredAppRunner{}
	fixture := newDeliveredAppFixtureV2(t, runner, false, "cli", []domain.CompletionCheck{{Kind: "acceptance"}, {Kind: "cli_smoke", Command: "go run . --help"}}, "")
	state, err := fixture.application.DeliveredApplicationStateV2(context.Background(), fixture.questID, true)
	if err != nil || state.Kind != "cli" || state.Launch != "terminal" || state.Command != "go run . --help" || state.Probed {
		t.Fatalf("state = %#v err=%v", state, err)
	}
	request := fixture.request
	request.IdempotencyKey = "start-cli"
	if _, err = fixture.application.ControlDeliveredApplicationV2(context.Background(), fixture.questID, "start", request); err == nil {
		t.Fatal("compose start must be refused for an app without compose")
	}
}

// Договор без applicationUrl: адрес берётся из проверок, которые уже ходили
// на приложение, и только если он на этой машине.
func TestDeliveredApplicationURLFromVerifiedChecks(t *testing.T) {
	target := deliveredAppTargetV2{
		approval: domain.WorkOrderApproval{WorkOrder: domain.WorkOrder{
			Criteria:   []domain.AcceptanceCriterion{{ID: "health", Arguments: json.RawMessage(`{"command":"curl -sf http://localhost:8080/health"}`)}},
			Completion: domain.CompletionProfile{Checks: []domain.CompletionCheck{{Kind: "automated_tests", Command: "curl https://example.com/install.sh"}}},
		}},
	}
	if got := deliveredAppURLV2(target); got != "http://localhost:8080/health" {
		t.Fatalf("derived url = %q", got)
	}
	target.bundle.Criteria = []domain.CriterionEvidence{{CriterionID: "health", Command: "curl -fsS http://127.0.0.1:9090/ready"}}
	if got := deliveredAppURLV2(target); got != "http://127.0.0.1:9090/ready" {
		t.Fatalf("evidence url must win over criterion arguments: %q", got)
	}
	target.receipt.URL = "http://localhost:3000"
	if got := deliveredAppURLV2(target); got != "http://localhost:3000" {
		t.Fatalf("receipt url must win: %q", got)
	}
	if got := deliveredAppURLV2(deliveredAppTargetV2{approval: domain.WorkOrderApproval{WorkOrder: domain.WorkOrder{Completion: domain.CompletionProfile{Checks: []domain.CompletionCheck{{Kind: "health", Command: "curl https://example.com/health"}}}}}}); got != "" {
		t.Fatalf("remote url must never be derived: %q", got)
	}
}
