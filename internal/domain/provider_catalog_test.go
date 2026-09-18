package domain

import "testing"

func TestBuiltInProviderCatalogIncludesCompanyGateway(t *testing.T) {
	var llmux, custom ProviderPreset
	for _, item := range BuiltInProviderCatalog() {
		switch item.ID {
		case "llmux":
			llmux = item
		case "custom":
			custom = item
		}
	}
	if llmux.ID != "llmux" || llmux.Kind != ProviderOpenAI || !llmux.RequiresAPIKey {
		t.Fatalf("llmux preset=%#v", llmux)
	}
	if custom.ID != "custom" || custom.Kind != ProviderOpenAI || !custom.RequiresAPIKey {
		t.Fatalf("custom preset=%#v", custom)
	}
	ids := make([]string, 0, 8)
	for _, item := range BuiltInProviderCatalog() {
		if item.ID == "llmux" || item.ID == "custom" || item.ID == "openai" {
			ids = append(ids, item.ID)
		}
	}
	if len(ids) < 3 || ids[0] != "llmux" || ids[1] != "custom" {
		t.Fatalf("company gateways should precede cloud presets, got %v", ids)
	}
}
