package app

import (
	"context"
	"testing"

	"local-agent-workbench/internal/connections"
	"local-agent-workbench/internal/domain"
)

func TestGlobalModelDefaultsInheritAndProjectOverride(t *testing.T) {
	application := newTestApp(t)
	world := openTestWorld(t, application)
	ctx := context.Background()
	oldConnection, err := application.SaveConnection(connections.UpsertRequest{
		ID: "old-model-connection", Provider: domain.ProviderOpenAI, PresetID: "custom",
		DisplayName: "Old", BaseURL: "https://example.com/v1", DefaultModel: "old-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	sharedConnection, err := application.SaveConnection(connections.UpsertRequest{
		ID: "shared-model-connection", Provider: domain.ProviderOpenAI, PresetID: "custom",
		DisplayName: "Shared", BaseURL: "https://example.com/v1", DefaultModel: "shared-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	project := domain.OrchestratorConfig{ID: "project-master", WorkspaceID: world.ID, Preset: "conductor", ConnectionID: oldConnection.ID, Model: "old-model"}
	if err = application.store.SaveOrchestratorConfig(ctx, project); err != nil {
		t.Fatal(err)
	}
	_, err = application.SaveGlobalModelDefaults(ctx, domain.GlobalModelDefaults{
		Master: domain.GlobalModelChoice{ConnectionID: sharedConnection.ID},
		Agent:  domain.GlobalModelChoice{ConnectionID: sharedConnection.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	master, err := application.masterConfig(ctx, world.ID)
	if err != nil {
		t.Fatal(err)
	}
	if master.ConnectionID != sharedConnection.ID || master.Model != "shared-model" {
		t.Fatalf("global Master default not inherited: %#v", master)
	}
	stored, err := application.store.GetOrchestratorConfig(ctx, world.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ConnectionID != oldConnection.ID {
		t.Fatal("previous project model was not retained for restoration")
	}
	stored.ProjectModelOverride = true
	if err = application.store.SaveOrchestratorConfig(ctx, stored); err != nil {
		t.Fatal(err)
	}
	master, err = application.masterConfig(ctx, world.ID)
	if err != nil {
		t.Fatal(err)
	}
	if master.ConnectionID != oldConnection.ID || master.Model != "old-model" {
		t.Fatalf("explicit project exception ignored: %#v", master)
	}
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "New Character", RoleDescription: "Reviewer", Mission: "Review", AllowedTools: []string{"read_file"},
		MaxOutputTokens: 128, ContextWindowTokens: 4096, MaxSteps: 4, MaxDurationSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if agent.ConnectionID != sharedConnection.ID || agent.PrimaryModel != "shared-model" {
		t.Fatalf("new agent default not applied: %#v", agent)
	}
}
