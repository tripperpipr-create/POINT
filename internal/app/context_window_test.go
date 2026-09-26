package app

import (
	"testing"

	"local-agent-workbench/internal/domain"
)

func TestFreeRuntimeRunGetsTheModelWindowInsteadOfTheLegacyDefault(t *testing.T) {
	free := domain.AgentProfile{Provider: domain.ProviderOpenAI, ProviderPreset: "llmux", Model: "Qwen3.8-27B", ContextWindowTokens: legacyAgentContextWindow}
	widenLegacyContextWindowOnFreeRuntime(&free)
	if free.ContextWindowTokens != 131072 {
		t.Fatalf("free Qwen3 run window = %d, want the model's 131072", free.ContextWindowTokens)
	}
	chosen := domain.AgentProfile{Provider: domain.ProviderOpenAI, ProviderPreset: "llmux", Model: "Qwen3.8-27B", ContextWindowTokens: 16384}
	widenLegacyContextWindowOnFreeRuntime(&chosen)
	if chosen.ContextWindowTokens != 16384 {
		t.Fatalf("a window the owner chose was overridden: %d", chosen.ContextWindowTokens)
	}
	paid := domain.AgentProfile{Provider: domain.ProviderOpenAI, ProviderPreset: "openai", Model: "Qwen3.8-27B", ContextWindowTokens: legacyAgentContextWindow}
	widenLegacyContextWindowOnFreeRuntime(&paid)
	if paid.ContextWindowTokens != legacyAgentContextWindow {
		t.Fatalf("a billed runtime got a larger window without the owner: %d", paid.ContextWindowTokens)
	}
}

// Живой прогон 26.09 шёл с бюджетом ввода 24576: расширение окна стояло
// только в снимке для наборов правок, а путь запуска его не вызывал.
func TestRunPathWidensTheLegacyWindowOfAFreeModel(t *testing.T) {
	application := newTestApp(t)
	if _, err := application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	profile := domain.DefaultProfile()
	profile.ID = "qwen3-local"
	profile.Model = "qwen3:8b"
	profile.ContextWindowTokens = legacyAgentContextWindow
	if _, err := application.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	preview, err := application.PreviewAgentRun(AgentRunPreviewRequest{ProfileID: profile.ID, Task: "Прочитай README"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Tokens.ContextWindow != 131072 || preview.Tokens.AvailableInput <= 100000 {
		t.Fatalf("run path kept the legacy window: %#v", preview.Tokens)
	}
}
