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
)

func TestNormalizeCustomToolDefaultsToProcessAndFillsDescription(t *testing.T) {
	tool, err := normalizeCustomTool(domain.CustomTool{
		DisplayName: "Echo", Program: "go", Arguments: []string{"version"}, CWD: ".",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tool.Kind != domain.CustomToolProcess {
		t.Fatalf("kind=%q, want process", tool.Kind)
	}
	if tool.TimeoutSeconds != 120 {
		t.Fatalf("timeout=%d, want 120", tool.TimeoutSeconds)
	}
	if !strings.Contains(tool.Description, "Echo") {
		t.Fatalf("description was not filled from name: %q", tool.Description)
	}

	legacy, err := normalizeCustomTool(domain.CustomTool{
		DisplayName: "Shell", Description: "legacy", Command: "go test ./...", CWD: ".", TimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Kind != domain.CustomToolCommand {
		t.Fatalf("legacy command without kind became %q", legacy.Kind)
	}
}

func TestDeleteCustomToolBlockedByProjectAgentAndBlueprint(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600)
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	view, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if len(boot.Blueprints) == 0 {
		t.Fatal("expected seeded blueprints")
	}

	custom, err := application.SaveCustomTool(domain.CustomTool{
		Kind: domain.CustomToolProcess, DisplayName: "Go version", Description: "Shows go version",
		Program: "go", Arguments: []string{"version"}, CWD: ".", TimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}

	agent := domain.ProjectAgentFromBlueprint(view.Workspace.ID, boot.Blueprints[0])
	agent.AllowedTools = []string{custom.ID}
	if _, err = application.SaveProjectAgent(agent); err != nil {
		t.Fatal(err)
	}
	if err = application.DeleteCustomTool(custom.ID); err == nil || !strings.Contains(err.Error(), "agent") {
		t.Fatalf("expected agent delete guard, got %v", err)
	}
	agent.AllowedTools = nil
	if _, err = application.SaveProjectAgent(agent); err != nil {
		t.Fatal(err)
	}

	// Persist blueprint allowlist without the legacy profile mirror so the
	// blueprint-specific delete guard is exercised on its own.
	blueprint := boot.Blueprints[0]
	blueprint.AllowedTools = append(append([]string{}, blueprint.AllowedTools...), custom.ID)
	if err = application.store.SaveBlueprint(context.Background(), blueprint); err != nil {
		t.Fatal(err)
	}
	if err = application.DeleteCustomTool(custom.ID); err == nil || !strings.Contains(err.Error(), "blueprint") {
		t.Fatalf("expected blueprint delete guard, got %v", err)
	}
	blueprint.AllowedTools = filterOut(blueprint.AllowedTools, custom.ID)
	if err = application.store.SaveBlueprint(context.Background(), blueprint); err != nil {
		t.Fatal(err)
	}
	if err = application.DeleteCustomTool(custom.ID); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteToolHonorsNetworkDeny(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600)
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(root); err != nil {
		t.Fatal(err)
	}
	custom, err := application.SaveCustomTool(domain.CustomTool{
		Kind: domain.CustomToolProcess, DisplayName: "Curl probe", Description: "Outbound probe",
		Program: "curl", Arguments: []string{"https://evil.example/data"}, CWD: ".", TimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments := json.RawMessage(`{"reason":"hub execute"}`)
	approvalID := approveManualToolExecution(t, application, custom.ID, arguments)
	result, err := application.ExecuteTool(ToolExecutionRequest{
		ToolName:   custom.ID,
		Arguments:  arguments,
		Mode:       "execute_readonly",
		ApprovalID: approvalID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Error == nil || result.Error.Code != "network_denied" {
		t.Fatalf("hub execute bypassed network deny: %#v", result)
	}
}

func filterOut(values []string, skip string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != skip {
			out = append(out, value)
		}
	}
	return out
}

// Имя занято одним инструментом, а повторное сохранение — его новая редакция.
//
// Иначе самосоздание разложит рядом `run_tests`, `run_tests_2` и `run_tests_v3`,
// и группировка окажется способом аккуратно разложить мусор: живой из них никто
// уже не назовёт.
func TestCustomToolNameIsUniqueAndSaveBumpsRevision(t *testing.T) {
	application := newTestApp(t)
	var err error
	if _, err = application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	first, err := application.SaveCustomTool(domain.CustomTool{
		Kind: domain.CustomToolProcess, DisplayName: "Прогон тестов", Description: "go test",
		Program: "go", Arguments: []string{"test", "./..."}, CWD: ".", TimeoutSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 {
		t.Fatalf("первая редакция должна быть первой: %d", first.Revision)
	}
	if _, err = application.SaveCustomTool(domain.CustomTool{
		Kind: domain.CustomToolProcess, DisplayName: "прогон тестов", Description: "тот же смысл",
		Program: "go", Arguments: []string{"test", "-run", "TestX"}, CWD: ".", TimeoutSeconds: 60,
	}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("дубликат по имени принят: %v", err)
	}
	first.Arguments = []string{"test", "-count=1", "./..."}
	second, err := application.SaveCustomTool(first)
	if err != nil {
		t.Fatalf("правка существующего отклонена: %v", err)
	}
	if second.ID != first.ID || second.Revision != 2 {
		t.Fatalf("правка не стала второй редакцией того же инструмента: %#v", second)
	}
	saved, err := application.store.ListCustomTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0].Revision != 2 {
		t.Fatalf("номер редакции не пережил storage: %#v", saved)
	}
}

// Правило доверия целиком: порог, кому оно вообще положено и что его обнуляет.
func TestCustomToolTrustAccumulatesResetsAndStaysEarned(t *testing.T) {
	application := newTestApp(t)
	var err error
	if _, err = application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	verifier, err := application.SaveCustomTool(domain.CustomTool{
		Kind: domain.CustomToolProcess, DisplayName: "Прогон тестов", Description: "go test",
		Program: "go", Arguments: []string{"test", "./..."}, CWD: ".", TimeoutSeconds: 60,
		ProvidesVerification: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if verifier.TrustedRuns != 0 || application.trustedCustomTool(verifier.ID) {
		t.Fatalf("новый инструмент начал с доверия: %#v", verifier)
	}

	// Порог: до него окно остаётся.
	for index := 1; index < domain.CustomToolTrustThreshold; index++ {
		application.noteCustomToolApproval(verifier.ID)
		if application.trustedCustomTool(verifier.ID) {
			t.Fatalf("доверие выдано на %d-м прогоне вместо %d", index, domain.CustomToolTrustThreshold)
		}
	}
	application.noteCustomToolApproval(verifier.ID)
	if !application.trustedCustomTool(verifier.ID) {
		t.Fatalf("после %d подтверждённых прогонов доверия нет", domain.CustomToolTrustThreshold)
	}

	// Правка описания доверие сохраняет: запускается та же команда.
	saved := currentCustomTool(t, application, verifier.ID)
	saved.Description = "go test всего проекта"
	if _, err = application.SaveCustomTool(saved); err != nil {
		t.Fatal(err)
	}
	if !application.trustedCustomTool(verifier.ID) {
		t.Fatal("правка описания обнулила доверие")
	}

	// Правка исполняемой части — обнуляет.
	saved = currentCustomTool(t, application, verifier.ID)
	saved.Arguments = []string{"test", "-count=1", "./..."}
	if _, err = application.SaveCustomTool(saved); err != nil {
		t.Fatal(err)
	}
	if application.trustedCustomTool(verifier.ID) {
		t.Fatal("доверие пережило смену команды")
	}
}

// Доверие положено не всем: только проверяющим и только без свободных
// параметров, значение которых выбирает модель.
func TestCustomToolTrustIsNotForEveryTool(t *testing.T) {
	application := newTestApp(t)
	var err error
	if _, err = application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	plain, err := application.SaveCustomTool(domain.CustomTool{
		Kind: domain.CustomToolProcess, DisplayName: "Сборка", Description: "go build",
		Program: "go", Arguments: []string{"build", "./..."}, CWD: ".", TimeoutSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	parameterized, err := application.SaveCustomTool(domain.CustomTool{
		Kind: domain.CustomToolProcess, DisplayName: "Точечный тест", Description: "go test -run",
		Program: "go", Arguments: []string{"test", "-run", "{{name}}", "./..."}, CWD: ".", TimeoutSeconds: 60,
		ProvidesVerification: true,
		Parameters: []domain.CustomToolParameter{
			{Name: "name", DisplayName: "Тест", Description: "Имя теста", Type: domain.CustomToolParameterString, Required: true, MaxLength: 200},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < domain.CustomToolTrustThreshold*2; index++ {
		application.noteCustomToolApproval(plain.ID)
		application.noteCustomToolApproval(parameterized.ID)
	}
	if application.trustedCustomTool(plain.ID) {
		t.Fatal("доверие досталось инструменту без пометки «даёт доказательство»")
	}
	if application.trustedCustomTool(parameterized.ID) {
		t.Fatal("доверие досталось инструменту со свободным параметром")
	}
}

// Счётчик растёт от события завершения инструмента, и только от успешного.
func TestCustomToolTrustCountsOnlySuccessfulRuns(t *testing.T) {
	application := newTestApp(t)
	if _, err := application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	verifier, err := application.SaveCustomTool(domain.CustomTool{
		Kind: domain.CustomToolProcess, DisplayName: "Линт", Description: "go vet",
		Program: "go", Arguments: []string{"vet", "./..."}, CWD: ".", TimeoutSeconds: 60,
		ProvidesVerification: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	failed, _ := json.Marshal(map[string]any{"tool": verifier.ID, "result": map[string]any{"ok": false}})
	succeeded, _ := json.Marshal(map[string]any{"tool": verifier.ID, "result": map[string]any{"ok": true}})
	for index := 0; index < domain.CustomToolTrustThreshold; index++ {
		application.noteCustomToolApprovalFromEvent(domain.Event{Type: domain.EventToolFinished, Data: failed})
	}
	if currentCustomTool(t, application, verifier.ID).TrustedRuns != 0 {
		t.Fatal("неудачные прогоны засчитаны в доверие")
	}
	for index := 0; index < domain.CustomToolTrustThreshold; index++ {
		application.noteCustomToolApprovalFromEvent(domain.Event{Type: domain.EventToolFinished, Data: succeeded})
	}
	if !application.trustedCustomTool(verifier.ID) {
		t.Fatal("успешные прогоны не сложились в доверие")
	}
}

func currentCustomTool(t *testing.T, application *App, id string) domain.CustomTool {
	t.Helper()
	tools, err := application.store.ListCustomTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		if tool.ID == id {
			return tool
		}
	}
	t.Fatalf("инструмент %q пропал", id)
	return domain.CustomTool{}
}

// Предложение инструмента: агент готовит черновик, создаёт человек, а прав
// созданный инструмент не приносит никому.
func TestCompanionToolProposalCreatesNothingUntilConfirmed(t *testing.T) {
	application := newTestApp(t)
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	draft := domain.CustomTool{
		ID: domain.NewID("customtool"), Kind: domain.CustomToolCommand,
		DisplayName: "go test", Description: "Запускает go test.",
		Command: "go test ./...", CWD: ".", TimeoutSeconds: 120,
		CreatedAt: now, UpdatedAt: now,
	}
	proposal := domain.CompanionActionProposal{
		ID: domain.NewID("companionaction"), WorkspaceID: view.Workspace.ID,
		Kind: domain.CompanionActionCreateTool, Title: "Создать инструмент · go test",
		Rationale: "Команда из сообщения человека.", Tool: &draft, Status: "pending",
		CreatedAt: now, UpdatedAt: now,
	}
	if err = application.store.SaveCompanionActionProposal(ctx, proposal); err != nil {
		t.Fatal(err)
	}
	// Пока предложение не подтверждено, инструмента нет.
	before, err := application.store.ListCustomTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 0 {
		t.Fatalf("инструмент создан до подтверждения: %#v", before)
	}

	result, err := application.DecideCompanionAction(CompanionActionDecision{
		ProposalID: proposal.ID, Action: CompanionActionApply,
	})
	if err != nil {
		t.Fatalf("подтверждение отклонено: %v", err)
	}
	if result.Tool == nil || result.Tool.Command != "go test ./..." {
		t.Fatalf("инструмент создан не тот: %#v", result.Tool)
	}
	// Предложение пережило хранилище вместе с полезной нагрузкой.
	stored, err := application.store.ListCompanionActionProposals(ctx, view.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].Status != "applied" || stored[0].Tool == nil {
		t.Fatalf("предложение не сохранилось целиком: %#v", stored)
	}

	// И главное: созданный инструмент никому не выдан.
	agents, err := application.store.ListProjectAgents(ctx, view.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range agents {
		for _, tool := range agent.AllowedTools {
			if tool == result.Tool.ID {
				t.Fatalf("созданный инструмент сам попал в права агента %q", agent.Name)
			}
		}
	}
	// Доверия у него тоже нет: он не помечен как дающий доказательство.
	if application.trustedCustomTool(result.Tool.ID) {
		t.Fatal("новый инструмент оказался доверенным")
	}
}
