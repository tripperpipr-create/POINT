package app

import (
	"context"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

type countedUsageModel struct{ calls int }

func (m *countedUsageModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.calls++
	return emit(providers.ModelEvent{Kind: providers.EventUsage, InputTokens: 120, OutputTokens: 30})
}

func TestBudgetedModelBlocksUnknownHardCostAndReconcilesKnownUsage(t *testing.T) {
	application := newTestApp(t)
	var err error
	world := openTestWorld(t, application)
	if _, err = application.SaveHubBudget(HubBudgetSettings{DailyCents: 100, HardStop: true}); err != nil {
		t.Fatal(err)
	}
	inner := &countedUsageModel{}
	model := application.wrapBudgetedModel(inner, domain.ProviderOpenAI, modelBudgetScope{
		WorkspaceID: world.ID, ProjectAgentID: "master", Outcome: "master_model",
	})
	request := providers.ModelRequest{Model: "priced-model", MaxOutputTokens: 200, Messages: []providers.Message{{Role: "user", Content: "plan"}}}
	if err = model.Stream(context.Background(), request, func(providers.ModelEvent) error { return nil }); err == nil || !strings.Contains(err.Error(), "pricing profile") {
		t.Fatalf("unknown cloud pricing must block a hard-budget call, got %v", err)
	}
	if inner.calls != 0 {
		t.Fatalf("provider was called before budget authority: %d", inner.calls)
	}
	if _, err = application.SaveModelPricingProfile(domain.ModelPricingProfile{
		Provider: string(domain.ProviderOpenAI), Model: "priced-model", InputCentsPerMillion: 100, OutputCentsPerMillion: 200,
	}); err != nil {
		t.Fatal(err)
	}
	if err = model.Stream(context.Background(), request, func(providers.ModelEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	reservations, err := application.store.ListBudgetReservations(context.Background(), world.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(reservations) != 1 || reservations[0].Status != domain.BudgetReconciled || reservations[0].ActualInputTokens != 120 || reservations[0].ActualOutputTokens != 30 {
		t.Fatalf("reservation=%#v", reservations)
	}
	usage, err := application.store.ListUsageRecords(context.Background(), world.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 || usage[0].Outcome != "master_model" || usage[0].ProjectAgentID != "master" {
		t.Fatalf("usage=%#v", usage)
	}
}
