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
