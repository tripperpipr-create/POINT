package acceptance_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/connections"
	"local-agent-workbench/internal/domain"
)

// Public PHP/Composer sample used as the URL-intake vertical for Agent Hub.
const phpComposerFixtureURL = "https://github.com/systemeio/backend-test-task"

type phpComposerGit struct {
	branch string
	root   string
}

func (g *phpComposerGit) Run(_ context.Context, dir string, arguments ...string) ([]byte, error) {
	if len(arguments) == 0 {
		return nil, nil
	}
	switch arguments[0] {
	case "clone":
		project := filepath.Join(dir, arguments[len(arguments)-1])
		_ = os.MkdirAll(filepath.Join(project, ".git"), 0o700)
		_ = os.WriteFile(filepath.Join(project, "composer.json"), []byte(`{
  "name": "example/php-composer-app",
  "require": {"php": ">=8.3", "symfony/framework-bundle": "^7.0"},
  "require-dev": {"phpunit/phpunit": "^11.0"}
}`), 0o600)
		_ = os.WriteFile(filepath.Join(project, "Dockerfile"), []byte("FROM php:8.3-cli\n"), 0o600)
		_ = os.WriteFile(filepath.Join(project, "docker-compose.yml"), []byte("services:\n  app:\n    build: .\n"), 0o600)
		_ = os.WriteFile(filepath.Join(project, "README.md"), []byte("# PHP Composer sample\n\nImplement tax number formats, coupons, payment processors. Control total 116.56.\n"), 0o600)
		g.root = project
		return []byte("ok\n"), nil
	case "rev-parse":
		return []byte("deadbeef\n"), nil
	case "init", "switch":
		if len(arguments) >= 3 {
			g.branch = arguments[len(arguments)-1]
		}
		return nil, nil
	case "branch":
		return []byte(g.branch + "\n"), nil
	default:
		return nil, nil
	}
}

func TestPHPIntakeStructural(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	git := &phpComposerGit{}
	application, err := app.New(t.TempDir(), app.WithGitRunner(git))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })

	session, err := application.CreateIntake(context.Background(), app.CreateIntakeRequest{URL: phpComposerFixtureURL})
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != domain.IntakeAwaitingApproval {
		t.Fatalf("status=%s blockers=%v error=%s", session.Status, session.Blockers, session.Error)
	}
	if session.Environment.Runtime.Toolchains["php"] == "" {
		t.Fatalf("php toolchain missing: %#v", session.Environment.Runtime)
	}
	if session.Environment.Strategy != "project" {
		t.Fatalf("expected project strategy with Dockerfile, got %s", session.Environment.Strategy)
	}
	foundCompose := false
	for _, svc := range session.Environment.Services {
		if svc.Kind == "compose" {
			foundCompose = true
		}
	}
	if !foundCompose {
		t.Fatal("expected compose service")
	}
	if session.Environment.Runtime.Image != "" && !strings.Contains(session.Environment.Runtime.Image, "php") {
		// Project+Composer must still land on the PHP managed pack image.
		t.Fatalf("expected php managed image, got %q", session.Environment.Runtime.Image)
	}
	hosts := strings.Join(session.Environment.NetworkHosts, ",")
	if !strings.Contains(hosts, "repo.packagist.org") {
		t.Fatalf("packagist host missing: %s", hosts)
	}
	if !strings.Contains(hosts, "api.github.com") {
		t.Fatalf("composer github dist host missing: %s", hosts)
	}
	raw, _ := json.Marshal(session)
	if !strings.Contains(string(raw), "php") {
		t.Fatal("session payload missing php markers")
	}
}

func TestPHPIntakeLiveBenchmark(t *testing.T) {
	if os.Getenv("POINT_PHP_INTAKE_LIVE") != "1" {
		t.Skip("set POINT_PHP_INTAKE_LIVE=1 for live PHP URL intake benchmark")
	}
	if os.Getenv("POINT_SANDBOX_BACKEND") != "docker" {
		t.Fatal("POINT_SANDBOX_BACKEND=docker is required for live PHP intake")
	}
	t.Setenv("POINT_LIVE_WORKSPACE", "0")
	t.Setenv("POINT_FILE_ISOLATION", "sandbox")
	if os.Getenv("POINT_SANDBOX_REQUIRE_STRONG") == "" {
		t.Setenv("POINT_SANDBOX_REQUIRE_STRONG", "true")
	}

	model := firstNonEmpty(
		os.Getenv("POINT_PHP_INTAKE_MODEL"),
		os.Getenv("POINT_ACCEPTANCE_MODEL"),
		"Qwen3.6-35B-A3B",
	)
	baseURL := firstNonEmpty(
		os.Getenv("POINT_LLMUX_BASE_URL"),
		os.Getenv("POINT_OPENAI_BASE_URL"),
		"https://llmux.ds3.centrofinans.ru/v1",
	)
	apiKey := firstNonEmpty(
		os.Getenv("POINT_LLMUX_API_KEY"),
		os.Getenv("POINT_OPENAI_API_KEY"),
		os.Getenv("OPENAI_API_KEY"),
	)
	if apiKey == "" {
		t.Fatal("set POINT_LLMUX_API_KEY (LLMux token from Point SecretStorage) for live PHP intake")
	}

	t.Setenv("REDIS_ADDR", "")
	t.Setenv("POINT_DEFAULT_MODEL", model)
	application, err := app.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })

	conn, err := application.SaveConnection(connections.UpsertRequest{
		ID: "conn-php-intake", Provider: domain.ProviderOpenAI, PresetID: "llmux",
		DisplayName: "LLMux", BaseURL: baseURL, SecretRef: "secret:php-intake",
		DefaultModel: model,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = application.SetDefaultConnection(conn.ID); err != nil {
		t.Fatal(err)
	}
	if err = wireCodingProfileToConnection(application, conn, model, baseURL); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour+30*time.Minute)
	defer cancel()
	session, err := application.CreateIntake(ctx, app.CreateIntakeRequest{
		URL: phpComposerFixtureURL, Model: model, APIKey: apiKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != domain.IntakeAwaitingApproval || session.Brief == nil {
		t.Fatalf("intake not ready: status=%s blockers=%v", session.Status, session.Blockers)
	}
	if session.Environment.Runtime.Toolchains["php"] == "" {
		t.Fatal("live intake did not detect PHP")
	}
	approved, err := application.ApproveIntake(ctx, session.ID, app.ApproveIntakeRequest{
		ExpectedVersion: session.Brief.Version, OrchestratorAPIKey: apiKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if approved.QuestID == "" {
		t.Fatal("approve did not start a quest")
	}
	deadline := time.Now().Add(2*time.Hour + 15*time.Minute)
	var final domain.IntakeSession
	for time.Now().Before(deadline) {
		final, err = application.IntakeSession(ctx, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		// Headless live: clear tool gates, plan egress, and Master-watch pauses.
		if approved.QuestID != "" {
			hosts, remotes := headlessAllowlists(final)
			autoApprovePendingTools(t, application, final.WorkspaceID, hosts, remotes)
		}
		switch final.Status {
		case domain.IntakeCompleted, domain.IntakeNeedsReview, domain.IntakeBlocked:
			goto done
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("timed out waiting for intake terminal status; last=%s", final.Status)
done:
	if final.Status != domain.IntakeCompleted {
		t.Fatalf("benchmark PASS requires completed+verified; status=%s error=%s", final.Status, final.Error)
	}
	if final.Evidence == nil {
		ev, evErr := application.IntakeEvidence(ctx, session.ID)
		if evErr != nil {
			t.Fatalf("evidence missing: %v", evErr)
		}
		final.Evidence = &ev
	}
	if len(final.Evidence.Criteria) == 0 {
		t.Fatal("completed without criterion evidence")
	}
	for _, criterion := range final.Evidence.Criteria {
		if !criterion.Satisfied && session.Brief != nil {
			for _, spec := range session.Brief.Criteria {
				if spec.ID == criterion.CriterionID && spec.Kind != "manual" {
					t.Fatalf("verification criterion %s not satisfied: %s", criterion.CriterionID, criterion.Summary)
				}
			}
		}
	}
	if final.Evidence.WorkspaceRevision == "" {
		t.Fatal("evidence missing workspaceRevision")
	}
	if !strings.HasPrefix(final.Delivery.Branch, "point/") {
		t.Fatalf("delivery branch=%q", final.Delivery.Branch)
	}
	root := repoRoot(t)
	implDigest := implementationDigest(root)
	var runID string
	if approved.QuestID != "" {
		runID = approved.QuestID
	}
	criteria := []map[string]any{}
	for _, item := range final.Evidence.Criteria {
		criteria = append(criteria, map[string]any{"id": item.CriterionID, "satisfied": item.Satisfied, "summary": item.Summary})
	}
	ledger := map[string]any{
		"case": "php-composer-symfony", "status": final.Status, "questId": final.QuestID, "intakeId": session.ID,
		"runId": runID, "implementationDigest": implDigest, "fixtureURL": phpComposerFixtureURL,
		"fixtureRevision": session.Source.Digest, "workspaceRevision": final.Evidence.WorkspaceRevision,
		"image": os.Getenv("POINT_SANDBOX_IMAGE"), "model": model, "provider": "openai-compatible",
		"sandboxBackend": os.Getenv("POINT_SANDBOX_BACKEND"), "strongSandbox": os.Getenv("POINT_SANDBOX_REQUIRE_STRONG"),
		"criteria": criteria, "falseComplete": false, "branch": final.Delivery.Branch, "at": time.Now().UTC(),
	}
	_ = os.MkdirAll(filepath.Join(root, ".tmp"), 0o755)
	pass := firstNonEmpty(os.Getenv("POINT_PHP_INTAKE_PASS"), "1")
	path := filepath.Join(root, ".tmp", "php-intake-pass"+pass+".json")
	raw, _ := json.MarshalIndent(ledger, "", "  ")
	if err = os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s status=%s", path, final.Status)
}

func headlessAllowlists(session domain.IntakeSession) (hosts, remotes []string) {
	hosts = append([]string{
		"repo.packagist.org", "packagist.org", "github.com",
		"api.github.com", "codeload.github.com", "objects.githubusercontent.com",
	}, session.Environment.NetworkHosts...)
	if session.Brief != nil {
		hosts = append(hosts, session.Brief.Permissions.NetworkHosts...)
		remotes = append(remotes, session.Brief.Permissions.ConfirmedGitRemotes...)
	}
	if session.Source.PrimaryURL != "" {
		remotes = append(remotes, session.Source.PrimaryURL)
	}
	if session.URL != "" {
		remotes = append(remotes, session.URL)
	}
	return hosts, remotes
}

// headlessDecisionAction chooses the headless resolve action for Master policy decisions.
// Returns empty action when the item should be left for a human.
func headlessDecisionAction(kind app.DecisionKind, target string, allowedHosts, allowedRemotes []string, supervisionCount int) string {
	switch kind {
	case app.DecisionEgress:
		target = strings.TrimSpace(strings.ToLower(target))
		if target == "" {
			return "deny"
		}
		for _, host := range allowedHosts {
			host = strings.TrimSpace(strings.ToLower(host))
			if host != "" && (target == host || strings.HasSuffix(target, "."+host) || strings.Contains(target, host)) {
				return "allow_quest"
			}
		}
		for _, remote := range allowedRemotes {
			remote = strings.TrimSpace(strings.ToLower(remote))
			if remote != "" && (target == remote || strings.Contains(target, remote) || strings.Contains(remote, target)) {
				return "allow_quest"
			}
		}
		return "deny"
	case app.DecisionSupervision:
		if supervisionCount < 1 {
			return "continue"
		}
		return "stop"
	default:
		return ""
	}
}

// Возвращает число решений, которые харнесс закрыл за человека.
//
// Раньше сюда передавали карту supervisionContinues, чтобы посчитать
// вмешательства, но функция в неё ничего не писала. Вызывающий в приёмке MVP
// клал `len(карты)` в отчёт как `Interventions` — и число всегда было ноль.
// Отчёт, уверенно сообщающий ноль вместо неизвестного, хуже отсутствующего.
func autoApprovePendingTools(t *testing.T, application *app.App, workspaceID string, allowedHosts, allowedRemotes []string) int {
	t.Helper()
	if strings.TrimSpace(workspaceID) == "" {
		return 0
	}
	ctx := context.Background()
	queue, err := application.Decisions(ctx)
	if err != nil {
		t.Logf("decisions: %v", err)
		return 0
	}
	resolved := 0
	for _, item := range queue.Items {
		switch item.Kind {
		case app.DecisionApproval:
			if err = application.ResolveApproval(item.ID, true); err != nil {
				t.Logf("auto-approve %s: %v", item.ID, err)
			} else {
				resolved++
				t.Logf("auto-approved tool gate %s (%s)", item.ID, item.Detail)
			}
		case app.DecisionFlowGate:
			if item.FlowRunID == "" || item.NodeID == "" {
				continue
			}
			if _, err = application.ResumeFlowApproval(item.FlowRunID, item.NodeID, true); err != nil {
				t.Logf("auto-approve flow gate %s: %v", item.ID, err)
			} else {
				resolved++
				t.Logf("auto-approved flow gate %s", item.ID)
			}
		case app.DecisionChangeSet:
			if _, err = application.ApplyChangeSet(item.ID); err != nil {
				t.Logf("auto-apply change set %s: %v", item.ID, err)
			} else {
				resolved++
				t.Logf("auto-applied change set %s", item.ID)
			}
		case app.DecisionEgress:
			ask, getErr := application.GetEgressAsk(ctx, item.ID)
			if getErr != nil {
				t.Logf("egress ask %s: %v", item.ID, getErr)
				continue
			}
			action := headlessDecisionAction(item.Kind, ask.Target, allowedHosts, allowedRemotes, 0)
			if _, err = application.ResolveEgressAsk(ctx, item.ID, app.ResolveEgressAskRequest{Action: action}); err != nil {
				t.Logf("auto-resolve egress %s (%s): %v", item.ID, action, err)
			} else {
				resolved++
				t.Logf("auto-resolved egress %s target=%s action=%s", item.ID, ask.Target, action)
			}
		case app.DecisionSupervision:
			ask, getErr := application.GetEgressAsk(ctx, item.ID)
			if getErr != nil {
				t.Logf("supervision ask %s: %v", item.ID, getErr)
				continue
			}
			durable := application.QuestSupervisionCount(ctx, workspaceID, ask.QuestID)
			action := headlessDecisionAction(item.Kind, "", nil, nil, durable)
			continueRun := action == "continue"
			if err = application.ResolveSupervisionContinue(ctx, item.ID, continueRun); err != nil {
				t.Logf("auto-resolve supervision %s (%s): %v", item.ID, action, err)
			} else {
				resolved++
				t.Logf("auto-resolved supervision %s action=%s durable=%d", item.ID, action, durable)
			}
		}
	}
	return resolved
}

func wireCodingProfileToConnection(application *app.App, conn domain.Connection, model, baseURL string) error {
	profile := domain.DefaultProfile()
	profile.ID = "default"
	// Провайдер берётся у подключения: живой прогон бывает и на локальной
	// Ollama, и жёстко зашитый OpenAI отправил бы запрос не тому рантайму.
	profile.Provider = conn.Provider
	profile.ProviderPreset = conn.PresetID
	profile.BaseURL = baseURL
	profile.Model = model
	profile.ConnectionID = conn.ID
	// ApprovalSafe + project brief + strong Docker sandbox auto-approves
	// propose_patch/run_command. ApprovalAlways means "always ask" and blocks headless.
	profile.ApprovalMode = domain.ApprovalSafe
	// Cap completion budget: 65k + reasoning models often burn the entire
	// budget on thinking (finish_reason=length) with no tool call. 16k still
	// covers multi-file propose_patch chunks without that failure mode.
	profile.MaxOutputTokens = 16384
	profile.ContextWindowTokens = 131072
	// Medium on Qwen3.6 spent the full output budget on thinking and
	// never emitted a tool call. Low leaves room for an actual first action.
	profile.ReasoningEffort = "low"
	profile.MaxSteps = 100
	profile.MaxDurationSeconds = 3600
	profile.FallbackModels = []string{"Qwen3.8-27B"}
	profile.UpdatedAt = time.Now().UTC()
	_, err := application.SaveProfile(profile)
	return err
}

// wireMasterToConnection настраивает диспетчера так же, как это делает онбординг
// в продукте: без сохранённой политики Мастера разговор вообще невозможен
// (ErrMasterNotConfigured), и живой сценарий обрывался на первом же ходе.
func wireMasterToConnection(application *app.App, conn domain.Connection, model string) error {
	_, err := application.SaveOrchestratorConfig(domain.OrchestratorConfig{
		Preset: "conductor", ConnectionID: conn.ID, Model: model,
		Temperature: 0.2, MaxOutputTokens: 16384,
		PlanningDepth: 70, Parallelism: 60, ApprovalStrictness: 40, TeamPreference: 85,
	})
	return err
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func implementationDigest(root string) string {
	cmd := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	if out, err := cmd.Output(); err == nil {
		if hash := strings.TrimSpace(string(out)); hash != "" {
			return hash
		}
	}
	sum := sha256.New()
	for _, rel := range []string{
		"internal/app/intake.go", "internal/app/intake_evidence.go", "internal/orchestrator/work_graph.go",
		"internal/sandbox/container.go", "scripts/run-php-intake-benchmark.mjs",
	} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err == nil {
			sum.Write(data)
		}
	}
	return hex.EncodeToString(sum.Sum(nil))
}
