package domain

import "testing"

func TestInitialOutputBudgetGivesThinkingModelRoomOnFreeRuntime(t *testing.T) {
	// llmux бесплатен: думающая Qwen сразу получает потолок роста.
	if got := InitialOutputBudget(8192, "Qwen3.8-27B", "low", ProviderOpenAI, "llmux", 131072); got != MaxThinkingOutputTokens {
		t.Fatalf("llmux budget=%d", got)
	}
	// Узкое окно режет предел половиной.
	if got := InitialOutputBudget(4096, "qwen3-27b", "low", ProviderOllama, "", 32768); got != 16384 {
		t.Fatalf("narrow window budget=%d", got)
	}
	// Платный рантайм остаётся при полу размышления.
	if got := InitialOutputBudget(4096, "qwen3-27b", "low", ProviderOpenAI, "openai", 131072); got != MinThinkingOutputTokens {
		t.Fatalf("paid budget=%d", got)
	}
	// Модель без размышления не трогается.
	if got := InitialOutputBudget(4096, "llama3", "", ProviderOllama, "", 131072); got != 4096 {
		t.Fatalf("non-thinking budget=%d", got)
	}
}
