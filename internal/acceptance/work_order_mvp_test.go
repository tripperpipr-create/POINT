package acceptance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/connections"
	"local-agent-workbench/internal/domain"
)

// The MVP live scenario: one production-shaped application, delivered and
// checked by its own approved profile. See docs/AGENT-HUB-V2-MVP.md.
const mvpScenarioPrompt = `Сделай приложение на Symfony с Docker Compose и PostgreSQL: ` +
	`REST-эндпоинты для списка и создания записей, миграция схемы, автотесты и README ` +
	`с командами запуска. Приложение должно подниматься одной командой compose.`

type mvpLedgerRow struct {
	Run            int      `json:"run"`
	Model          string   `json:"model"`
	WorkOrderID    string   `json:"workOrderId"`
	QuestID        string   `json:"questId"`
	Status         string   `json:"status"`
	Seconds        float64  `json:"seconds"`
	Interventions  int      `json:"interventions"`
	ProfileChecks  int      `json:"profileChecks"`
	FalseComplete  bool     `json:"falseComplete"`
	Violations     []string `json:"violations"`
	EvidenceID     string   `json:"evidenceId"`
	DeliveredURL   string   `json:"deliveredUrl,omitempty"`
	ServicesUp     bool     `json:"servicesUp"`
	KnownLimits    []string `json:"knownLimitations,omitempty"`
	ModelCallCount int      `json:"modelCalls"`
}

// mvpViolations lists every reason a finished run fails the MVP contract. It is
// deliberately separate from the live driver: the rules are what the acceptance
// means, so they have to be verifiable without a model and a Docker host.
func mvpViolations(order domain.WorkOrder, status domain.QuestStatus, bundle domain.EvidenceBundle) []string {
	problems := []string{}
	if status != domain.QuestCompleted {
		problems = append(problems, "quest finished as "+string(status)+" instead of completed")
	}
	if bundle.Version != domain.CurrentWorkOrderEvidenceVersion {
		problems = append(problems, fmt.Sprintf("evidence version %d, expected %d", bundle.Version, domain.CurrentWorkOrderEvidenceVersion))
	}
	if bundle.BriefDigest != domain.WorkOrderDigest(order) {
		problems = append(problems, "evidence is bound to a different work order digest")
	}
	if len(bundle.ModelCalls) == 0 {
		problems = append(problems, "model call ledger is empty")
	}
	recorded := make(map[string]domain.VerificationCheck, len(bundle.VerificationChecks))
	for _, check := range bundle.VerificationChecks {
		recorded[check.ID] = check
	}
	for _, required := range order.Completion.Checks {
		if required.Kind == domain.CompletionCheckAcceptance {
			continue
		}
		check, ok := recorded[domain.CompletionCheckEvidenceID(required.Kind)]
		switch {
		case !ok:
			problems = append(problems, "profile check "+required.Kind+" was never executed")
		case strings.TrimSpace(check.Command) != strings.TrimSpace(required.Command):
			problems = append(problems, "profile check "+required.Kind+" ran a different command")
		case check.ExitCode == nil || !check.Satisfied:
			problems = append(problems, "profile check "+required.Kind+" did not pass")
		}
	}
	for _, item := range bundle.Criteria {
		if !item.Satisfied {
			problems = append(problems, "acceptance criterion "+item.CriterionID+" is not satisfied")
		}
	}
	receipt := bundle.DeliveryReceipt
	if receipt == nil {
		problems = append(problems, "delivery receipt is missing")
		return problems
	}
	if strings.TrimSpace(bundle.WorkspaceRevision) == "" || receipt.WorkspaceRevision != bundle.WorkspaceRevision {
		problems = append(problems, "receipt does not name the delivered revision")
	}
	if order.Delivery.CommitMode == "squash" && strings.TrimSpace(receipt.CommitID) == "" {
		problems = append(problems, "squash delivery produced no commit")
	}
	if order.Delivery.KeepServicesRunning {
		if !receipt.ServicesRunning {
			problems = append(problems, "application was promised running but the receipt does not prove it")
		}
		if receipt.URL != order.Delivery.ApplicationURL {
			problems = append(problems, "receipt URL does not match the approved application URL")
		}
		if strings.TrimSpace(receipt.ComposeFile) == "" {
			problems = append(problems, "receipt has no compose file to stop and start the application")
		}
	}
	return problems
}

func mvpAcceptanceOrder() domain.WorkOrder {
	exitCode := 0
	return domain.NormalizeWorkOrder(domain.WorkOrder{
		State: "ready", Goal: "Symfony API", Scope: []string{"endpoints"},
		Criteria: []domain.AcceptanceCriterion{{
			ID: "endpoints", Kind: "verification", Text: "эндпоинты отвечают",
			Tool: "run_command", Arguments: json.RawMessage(`{"command":"vendor/bin/phpunit"}`), ExpectedExitCode: &exitCode,
		}},
		Workspace: domain.WorkspacePlan{Mode: "managed", Path: filepath.Join("C:", "Point", "Projects", "symfony-api"), Isolation: "snapshot"},
		Stack:     domain.StackPresetRef{ID: "recommended-api", Version: "1", Category: "api", Source: "benchmark"},
		Routing:   domain.ModelRoutingPolicy{Mode: "fixed", FixedConnectionID: "connection", FixedModel: "model"},
		Budget:    domain.BudgetEnvelope{Preset: "medium", Tokens: 200000, ActiveSeconds: 3600, MaxParallel: 1, MaxAttempts: 3},
		Delivery: domain.DeliveryPolicy{
			ApplyMode: "automatic", CommitMode: "none", KeepPartialDays: 30,
			KeepServicesRunning: true, ApplicationURL: "http://localhost:8080",
		},
		Completion: domain.CompletionProfile{ID: "mvp-api", Version: "1", Checks: []domain.CompletionCheck{
			{Kind: domain.CompletionCheckAcceptance},
			{Kind: "build", Command: "composer install --no-interaction"},
			{Kind: "automated_tests", Command: "vendor/bin/phpunit"},
			{Kind: "service_start", Command: "docker compose up -d --wait"},
			{Kind: "health", Command: "curl -fsS http://localhost:8080"},
		}},
	})
}

func mvpAcceptanceBundle(order domain.WorkOrder) domain.EvidenceBundle {
	exitCode := 0
	bundle := domain.EvidenceBundle{
		Version: domain.CurrentWorkOrderEvidenceVersion, ID: "evidence-mvp", QuestID: "quest-mvp", PointVersion: "test",
		BriefDigest: domain.WorkOrderDigest(order), SourceDigest: domain.WorkOrderSourceDigest(order),
		EnvironmentDigest: "env", StackPreset: order.Stack, WorkspaceRevision: "sha256:tree",
		Criteria:         []domain.CriterionEvidence{{CriterionID: "endpoints", Satisfied: true, Command: "vendor/bin/phpunit", ExitCode: &exitCode}},
		ModelCalls:       []domain.ModelCallLedgerEntry{{ID: "call-1", Provider: "test", Model: "model", CreatedAt: time.Now().UTC()}},
		DeliveryVerified: true,
		DeliveryReceipt: &domain.DeliveryReceipt{
			ID: "receipt-mvp", QuestID: "quest-mvp", WorkOrderDigest: domain.WorkOrderDigest(order),
			Target: order.Workspace.Path, WorkspaceRevision: "sha256:tree", URL: order.Delivery.ApplicationURL,
			ComposeFile: "compose.yaml", ServicesRunning: true, DeliveredAt: time.Now().UTC(),
		},
	}
	for _, check := range order.Completion.Checks {
		if check.Kind == domain.CompletionCheckAcceptance {
			continue
		}
		bundle.VerificationChecks = append(bundle.VerificationChecks, domain.VerificationCheck{
			ID: domain.CompletionCheckEvidenceID(check.Kind), Kind: check.Kind, Command: check.Command,
			ExitCode: &exitCode, Satisfied: true,
		})
	}
	return bundle
}

func TestWorkOrderMVPVerdictRejectsFalseCompletion(t *testing.T) {
	order := mvpAcceptanceOrder()
	if problems := mvpViolations(order, domain.QuestCompleted, mvpAcceptanceBundle(order)); len(problems) != 0 {
		t.Fatalf("a fully proven run must pass the acceptance: %v", problems)
	}
	for _, testCase := range []struct {
		name    string
		status  domain.QuestStatus
		mutate  func(*domain.EvidenceBundle)
		expects string
	}{
		{name: "not completed", status: domain.QuestNeedsReview, mutate: func(*domain.EvidenceBundle) {}, expects: "instead of completed"},
		{
			name: "profile check missing", status: domain.QuestCompleted,
			mutate:  func(b *domain.EvidenceBundle) { b.VerificationChecks = b.VerificationChecks[:1] },
			expects: "was never executed",
		},
		{
			name: "profile check substituted", status: domain.QuestCompleted,
			mutate:  func(b *domain.EvidenceBundle) { b.VerificationChecks[1].Command = "true" },
			expects: "ran a different command",
		},
		{
			name: "profile check failed", status: domain.QuestCompleted,
			mutate:  func(b *domain.EvidenceBundle) { b.VerificationChecks[1].Satisfied = false },
			expects: "did not pass",
		},
		{
			name: "services not proven", status: domain.QuestCompleted,
			mutate:  func(b *domain.EvidenceBundle) { b.DeliveryReceipt.ServicesRunning = false },
			expects: "promised running",
		},
		{
			name: "no receipt", status: domain.QuestCompleted,
			mutate:  func(b *domain.EvidenceBundle) { b.DeliveryReceipt = nil },
			expects: "delivery receipt is missing",
		},
		{
			name: "empty ledger", status: domain.QuestCompleted,
			mutate:  func(b *domain.EvidenceBundle) { b.ModelCalls = nil },
			expects: "model call ledger is empty",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			bundle := mvpAcceptanceBundle(order)
			testCase.mutate(&bundle)
			problems := mvpViolations(order, testCase.status, bundle)
			if !strings.Contains(strings.Join(problems, " | "), testCase.expects) {
				t.Fatalf("acceptance missed %q: %v", testCase.expects, problems)
			}
		})
	}
}

// TestWorkOrderMVPLiveScenario drives the MVP scenario end to end on a real
// model and a real Docker host. It is opt-in: without POINT_V2_MVP_LIVE the
// rules above are still checked, but nothing is executed.
func TestWorkOrderMVPLiveScenario(t *testing.T) {
	if os.Getenv("POINT_V2_MVP_LIVE") != "1" {
		t.Skip("set POINT_V2_MVP_LIVE=1 for the live MVP scenario")
	}
	if os.Getenv("POINT_SANDBOX_BACKEND") != "docker" {
		t.Fatal("POINT_SANDBOX_BACKEND=docker is required for the live MVP scenario")
	}
	model := firstNonEmpty(os.Getenv("POINT_V2_MVP_MODEL"), os.Getenv("POINT_ACCEPTANCE_MODEL"))
	// Локальная Ollama — такой же поддерживаемый рантайм, как удалённый
	// OpenAI-совместимый сервис, и у неё нет ключа. Требовать ключ у всех
	// значило бы закрыть живой прогон там, где он и запускается чаще всего.
	provider, preset := domain.ProviderOpenAI, "llmux"
	baseURL := firstNonEmpty(os.Getenv("POINT_LLMUX_BASE_URL"), os.Getenv("POINT_OPENAI_BASE_URL"))
	apiKey := firstNonEmpty(os.Getenv("POINT_LLMUX_API_KEY"), os.Getenv("POINT_OPENAI_API_KEY"), os.Getenv("OPENAI_API_KEY"))
	if strings.EqualFold(strings.TrimSpace(os.Getenv("POINT_V2_MVP_PROVIDER")), "ollama") {
		provider, preset = domain.ProviderOllama, "ollama"
		baseURL = firstNonEmpty(os.Getenv("POINT_OLLAMA_BASE_URL"), baseURL, "http://127.0.0.1:11434")
	}
	if model == "" || baseURL == "" {
		t.Fatal("live MVP scenario needs POINT_V2_MVP_MODEL and a base URL")
	}
	if apiKey == "" && provider != domain.ProviderOllama {
		t.Fatal("live MVP scenario needs an API key for a remote provider")
	}
	run := 1
	if value := strings.TrimSpace(os.Getenv("POINT_V2_MVP_RUN")); value != "" {
		fmt.Sscanf(value, "%d", &run)
	}

	// The MVP contract lives in the v2 database; legacy state stays untouched.
	t.Setenv("POINT_AGENT_HUB_V2", "1")
	t.Setenv("REDIS_ADDR", "")
	t.Setenv("POINT_DEFAULT_MODEL", model)
	t.Setenv("POINT_LIVE_WORKSPACE", "0")
	t.Setenv("POINT_FILE_ISOLATION", "sandbox")

	dataDir := firstNonEmpty(os.Getenv("POINT_V2_MVP_DATA_DIR"), t.TempDir())
	application, err := app.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	// Политика Мастера и разговор принадлежат открытому проекту: без него
	// SaveOrchestratorConfig отвечает «workspace is not open». Greenfield-наряд
	// всё равно уйдёт в свой managed-каталог, здесь нужен только дом разговора.
	home := filepath.Join(dataDir, "master-home")
	if err = os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err = application.OpenWorkspace(home); err != nil {
		t.Fatal(err)
	}
	secretRef := "secret:v2-mvp"
	if provider == domain.ProviderOllama {
		secretRef = ""
	}
	connection, err := application.SaveConnection(connections.UpsertRequest{
		ID: "conn-v2-mvp", Provider: provider, PresetID: preset,
		DisplayName: "MVP", BaseURL: baseURL, SecretRef: secretRef, DefaultModel: model,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = application.SetDefaultConnection(connection.ID); err != nil {
		t.Fatal(err)
	}
	if err = wireCodingProfileToConnection(application, connection, model, baseURL); err != nil {
		t.Fatal(err)
	}
	// Мастер — отдельная настройка от исполнителя: без политики диспетчера
	// StartMasterTurnV2 отвечает «мастер не настроен» и сценарий не начинается.
	if err = wireMasterToConnection(application, connection, model); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour)
	defer cancel()
	started := time.Now()
	turn, err := application.StartMasterTurnV2(ctx, app.MasterTurnV2Request{
		Message: mvpScenarioPrompt, Model: model, APIKey: apiKey, TaskIntake: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	turn = awaitMasterTurn(t, ctx, application, turn)
	if turn.WorkOrderID == "" {
		t.Fatalf("master produced no work order: status=%s error=%s", turn.Status, turn.Error)
	}
	order, err := application.WorkOrderV2(ctx, turn.WorkOrderID)
	if err != nil {
		t.Fatal(err)
	}

	// Открытые вопросы — это разговор, а не сбой: Мастер имеет право уточнять,
	// и карточка с вопросами намеренно не утверждается. Приёмка отвечает так же,
	// как ответил бы человек по фиксированному сценарию — «решай разумными
	// умолчаниями», — и даёт Мастеру пересобрать карточку.
	for round := 0; round < 2 && len(order.OpenQuestions) > 0; round++ {
		t.Logf("master asks: %s", strings.Join(order.OpenQuestions, " | "))
		followUp, followErr := application.StartMasterTurnV2(ctx, app.MasterTurnV2Request{
			ConversationID: order.ConversationID, Model: model, APIKey: apiKey, TaskIntake: true,
			Message: "Выбирай сам: свежие стабильные версии и стандартные умолчания стека. " +
				"Вопросов больше нет — собери финальную карточку запуска.",
		})
		if followErr != nil {
			t.Fatal(followErr)
		}
		followUp = awaitMasterTurn(t, ctx, application, followUp)
		if followUp.WorkOrderID == "" {
			continue
		}
		if order, err = application.WorkOrderV2(ctx, followUp.WorkOrderID); err != nil {
			t.Fatal(err)
		}
	}

	// Greenfield has no manifests yet, so the proposed profile cannot carry
	// build and test commands. Supplying them is exactly the human step the
	// MVP asks for before approval.
	order.Completion = mvpAcceptanceOrder().Completion
	// Оставшиеся вопросы снимает правка карточки: ответ уже дан разговором, и
	// человек утверждает наряд целиком, а не пересказывает его Мастеру заново.
	if len(order.OpenQuestions) > 0 {
		t.Logf("clearing unanswered questions on the card: %s", strings.Join(order.OpenQuestions, " | "))
		order.OpenQuestions = nil
	}
	order.State = "ready"
	order, err = application.ReviseWorkOrderV2(ctx, order.ID, app.ReviseWorkOrderV2Request{
		ExpectedVersion: order.Version, ExpectedDigest: domain.WorkOrderDigest(order),
		IdempotencyKey: fmt.Sprintf("mvp-profile-%d-%d", run, started.UnixNano()), WorkOrder: order,
	})
	if err != nil {
		t.Fatal(err)
	}
	approval, err := application.ApproveWorkOrderV2(ctx, order.ID, app.ApproveWorkOrderV2Request{
		Version: order.Version, Digest: domain.WorkOrderDigest(order),
		IdempotencyKey: fmt.Sprintf("mvp-approve-%d-%d", run, started.UnixNano()), APIKey: apiKey,
	})
	if err != nil {
		t.Fatal(err)
	}

	hosts := make([]string, 0, len(order.Network))
	for _, grant := range order.Network {
		hosts = append(hosts, grant.Host)
	}
	// Счётчик вмешательств считает то, что харнесс действительно закрыл.
	interventions := 0
	var quest domain.Quest
	for deadline := time.Now().Add(2*time.Hour + 30*time.Minute); time.Now().Before(deadline); {
		if quest, err = application.WorkOrderQuestV2(ctx, approval.QuestID); err != nil {
			t.Fatal(err)
		}
		if domain.IsTerminalQuestStatus(quest.Status) {
			break
		}
		// Headless: the run must not stall on a gate a person would clear.
		interventions += autoApprovePendingTools(t, application, order.WorkspaceID, hosts, nil)
		time.Sleep(5 * time.Second)
	}

	bundle, bundleErr := application.EvidenceBundle(ctx, approval.QuestID)
	if bundleErr != nil {
		t.Logf("no evidence bundle: %v", bundleErr)
	}
	violations := mvpViolations(order, quest.Status, bundle)
	row := mvpLedgerRow{
		Run: run, Model: model, WorkOrderID: order.ID, QuestID: approval.QuestID,
		Status: string(quest.Status), Seconds: time.Since(started).Seconds(),
		Interventions: interventions, ProfileChecks: len(bundle.VerificationChecks),
		FalseComplete: quest.Status == domain.QuestCompleted && len(violations) > 0,
		Violations:    violations, EvidenceID: bundle.ID, ModelCallCount: len(bundle.ModelCalls),
		KnownLimits: bundle.KnownLimitations,
	}
	if bundle.DeliveryReceipt != nil {
		row.DeliveredURL, row.ServicesUp = bundle.DeliveryReceipt.URL, bundle.DeliveryReceipt.ServicesRunning
	}
	writeMVPLedger(t, model, run, row)
	if len(violations) > 0 {
		t.Fatalf("MVP scenario run %d failed: %s", run, strings.Join(violations, "; "))
	}
}


// awaitMasterTurn ждёт ход Мастера до его собственного срока. Ход живёт дольше
// одного запроса к модели: внутри и раунды инструментов, и починка формата.
func awaitMasterTurn(t *testing.T, ctx context.Context, application *app.App, turn domain.MasterTurn) domain.MasterTurn {
	t.Helper()
	var err error
	for deadline := time.Now().Add(30 * time.Minute); time.Now().Before(deadline); {
		if turn, err = application.MasterTurn(ctx, turn.ID); err != nil {
			t.Fatal(err)
		}
		if turn.Status == "ready" || turn.Status == "failed" || turn.WorkOrderID != "" {
			return turn
		}
		time.Sleep(3 * time.Second)
	}
	return turn
}

func writeMVPLedger(t *testing.T, model string, run int, row mvpLedgerRow) {
	t.Helper()
	root := repoRoot(t)
	dir := filepath.Join(root, ".tmp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("v2-mvp-%s-run%d.jsonl", sanitizeModel(model), run)
	if err = os.WriteFile(filepath.Join(dir, name), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", filepath.Join(dir, name))
}
