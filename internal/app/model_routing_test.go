package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestResolveModelRouteFallbacksAndRunOverride(t *testing.T) {
	application := newTestApp(t)
	workspaceView, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	empty, err := application.ResolveModelRoute(ModelRouteCoding)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Configured || empty.Required || empty.Ready {
		t.Fatalf("пустой route не оставил профиль fallback-путём: %#v", empty)
	}

	now := time.Now().UTC()
	connection := domain.Connection{
		ID: "coding-route", Provider: domain.ProviderOpenAI, PresetID: "openai",
		DisplayName: "Coding", BaseURL: "https://coding.example.invalid/v1",
		Status: domain.ConnectionConnected, DefaultModel: "coding-default",
		CreatedAt: now, UpdatedAt: now,
	}
	if err = application.store.SaveConnection(context.Background(), connection); err != nil {
		t.Fatal(err)
	}
	saved, err := application.SaveWorkspaceModelRouting(domain.WorkspaceModelRouting{
		CodingConnectionID: connection.ID,
		// Пустая codingModel наследует defaultModel выбранного подключения.
		CheapModel: "cheap-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !saved.CodingRequired || !saved.CodingReady || saved.WorkspaceID != workspaceView.Workspace.ID {
		t.Fatalf("bootstrap-состояние route неверно: %#v", saved)
	}
	coding, err := application.ResolveModelRoute(ModelRouteCoding)
	if err != nil {
		t.Fatal(err)
	}
	if !coding.Ready || coding.Model != connection.DefaultModel || coding.ConnectionID != connection.ID {
		t.Fatalf("coding route не использовал default model подключения: %#v", coding)
	}
	prepared, err := application.prepareAgentRun("default", "Inspect the project", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.profile.ConnectionID != connection.ID || prepared.profile.Model != connection.DefaultModel || prepared.profile.BaseURL != connection.BaseURL {
		t.Fatalf("run не получил workspace override: %#v", prepared.profile)
	}

	base := domain.OrchestratorConfig{
		ID: "master-route-test", WorkspaceID: workspaceView.Workspace.ID, Preset: "conductor",
		Provider: domain.ProviderOllama, BaseURL: "http://127.0.0.1:11434", Model: "master-model",
		CreatedAt: now, UpdatedAt: now,
	}
	cheap, err := application.applyCheapModelRoute(context.Background(), workspaceView.Workspace.ID, base)
	if err != nil {
		t.Fatal(err)
	}
	if cheap.Model != "cheap-model" || cheap.ConnectionID != base.ConnectionID || cheap.Provider != base.Provider {
		t.Fatalf("model-only cheap route не сохранил подключение Мастера: %#v", cheap)
	}
	if err = application.store.SaveOrchestratorConfig(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	plannerConfig, ok := application.loadOrchestratorConfig(workspaceView.Workspace.ID)
	if !ok || plannerConfig.Model != "cheap-model" {
		t.Fatalf("planner не получил cheap route: ok=%v config=%#v", ok, plannerConfig)
	}
	chatConfig, err := application.masterConfig(context.Background(), workspaceView.Workspace.ID)
	if err != nil || chatConfig.Model != "cheap-model" {
		t.Fatalf("чат Мастера не получил cheap route: config=%#v err=%v", chatConfig, err)
	}

	connection.Status = domain.ConnectionError
	connection.LastError = "probe failed"
	connection.UpdatedAt = time.Now().UTC()
	if err = application.store.SaveConnection(context.Background(), connection); err != nil {
		t.Fatal(err)
	}
	blocked, err := application.ResolveModelRoute(ModelRouteCoding)
	if err != nil {
		t.Fatal(err)
	}
	if !blocked.Required || blocked.Ready || !strings.Contains(blocked.BlockReason, "probe failed") {
		t.Fatalf("обязательный неготовый route не заблокирован: %#v", blocked)
	}
	before, err := application.store.ListQuests(context.Background(), workspaceView.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.StartFastAgent(FastAgentRequest{ProfileID: "default", Task: "Change one file"}); err == nil || !strings.Contains(err.Error(), "coding-маршрут") {
		t.Fatalf("FastAgent молча использовал fallback: %v", err)
	}
	after, err := application.store.ListQuests(context.Background(), workspaceView.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("заблокированный FastAgent оставил квест: before=%d after=%d", len(before), len(after))
	}
}
