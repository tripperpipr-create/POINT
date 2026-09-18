package app

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

func TestManualProjectMemoryRequiresFreshConfirmationAndRollsBackExactly(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Backend", WorkspaceID: view.Workspace.ID, Provider: domain.ProviderOllama, BaseURL: "http://127.0.0.1:11434", PrimaryModel: "qwen2.5-coder:7b",
		SkillIDs: []string{"skill-existing"}, AllowedTools: []string{"read_file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := ManualLearningRequest{ProjectAgentID: agent.ID, Kind: "memory", Scope: "project", Content: "Prefer boundary-focused tests for protocol adapters."}
	preview, err := application.PreviewManualLearning(request)
	if err != nil || preview.NoChange || preview.ConfirmationToken == "" || len(preview.Changes) == 0 {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
	if _, err = application.ApplyManualLearning(request); err == nil {
		t.Fatal("manual learning applied without confirmation token")
	}
	request.ConfirmationToken = preview.ConfirmationToken
	improvement, err := application.ApplyManualLearning(request)
	if err != nil {
		t.Fatal(err)
	}
	if !improvementIsApplied(improvement.Status) || improvement.Trigger != "manual_teach" || improvement.ReviewMode != "manual" || improvement.MemoryStatus != "confirmed" || improvement.AfterMemory == nil {
		t.Fatalf("manual improvement=%#v", improvement)
	}
	memory, err := application.store.GetMemory(context.Background(), improvement.MemoryID)
	if err != nil || memory.WorkspaceID != view.Workspace.ID || memory.Kind != domain.MemoryAgent || !memory.Pinned {
		t.Fatalf("manual memory=%#v err=%v", memory, err)
	}
	rolled, err := application.RollbackAgentImprovement(improvement.ID)
	if err != nil || rolled.Status != "rolled_back" {
		t.Fatalf("manual rollback=%#v err=%v", rolled, err)
	}
	if _, err = application.store.GetMemory(context.Background(), improvement.MemoryID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("manual memory survived rollback: %v", err)
	}
	storedAgent, _ := application.store.GetProjectAgent(context.Background(), agent.ID)
	if len(storedAgent.SkillIDs) != 1 || storedAgent.SkillIDs[0] != "skill-existing" {
		t.Fatalf("manual memory rollback damaged skills: %#v", storedAgent.SkillIDs)
	}
}

func TestManualProfileInstructionPropagatesAndRollsBack(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	blueprint, err := application.SaveBlueprint(domain.AgentBlueprint{Name: "QA", Provider: domain.ProviderOllama, BaseURL: "http://127.0.0.1:11434", PrimaryModel: "qwen2.5-coder:7b", Rules: []string{"Keep scope bounded."}})
	if err != nil {
		t.Fatal(err)
	}
	firstPath, secondPath := t.TempDir(), t.TempDir()
	firstView, _ := application.OpenWorkspace(firstPath)
	first, err := application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(firstView.Workspace.ID, blueprint))
	if err != nil {
		t.Fatal(err)
	}
	secondView, _ := application.OpenWorkspace(secondPath)
	second, err := application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(secondView.Workspace.ID, blueprint))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.OpenWorkspace(firstPath); err != nil {
		t.Fatal(err)
	}
	request := ManualLearningRequest{ProjectAgentID: first.ID, Kind: "instruction", Scope: "profile", Content: "Record an explicit verifier result before reporting completion."}
	preview, err := application.PreviewManualLearning(request)
	if err != nil {
		t.Fatal(err)
	}
	request.ConfirmationToken = preview.ConfirmationToken
	improvement, err := application.ApplyManualLearning(request)
	if err != nil {
		t.Fatal(err)
	}
	if improvement.InstructionStatus != "promoted" || len(improvement.BeforeAgentRules) != 2 {
		t.Fatalf("manual profile instruction=%#v", improvement)
	}
	storedBlueprint, _ := application.store.GetBlueprint(context.Background(), blueprint.ID)
	storedFirst, _ := application.store.GetProjectAgent(context.Background(), first.ID)
	storedSecond, _ := application.store.GetProjectAgent(context.Background(), second.ID)
	for label, rules := range map[string][]string{"blueprint": storedBlueprint.Rules, "first": storedFirst.Rules, "second": storedSecond.Rules} {
		if !containsString(rules, request.Content) {
			t.Fatalf("%s did not receive manual instruction: %#v", label, rules)
		}
	}
	if _, err = application.RollbackAgentImprovement(improvement.ID); err != nil {
		t.Fatal(err)
	}
	storedBlueprint, _ = application.store.GetBlueprint(context.Background(), blueprint.ID)
	storedFirst, _ = application.store.GetProjectAgent(context.Background(), first.ID)
	storedSecond, _ = application.store.GetProjectAgent(context.Background(), second.ID)
	for label, rules := range map[string][]string{"blueprint": storedBlueprint.Rules, "first": storedFirst.Rules, "second": storedSecond.Rules} {
		if containsString(rules, request.Content) {
			t.Fatalf("%s retained rolled-back instruction: %#v", label, rules)
		}
	}
}

func TestManualLearningRejectsStalePreviewAndSecrets(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	view, _ := application.OpenWorkspace(t.TempDir())
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{Name: "QA", WorkspaceID: view.Workspace.ID, Provider: domain.ProviderOllama, BaseURL: "http://127.0.0.1:11434", PrimaryModel: "qwen2.5-coder:7b"})
	if err != nil {
		t.Fatal(err)
	}
	request := ManualLearningRequest{ProjectAgentID: agent.ID, Kind: "instruction", Scope: "project", Content: "Reproduce a defect before editing it."}
	preview, err := application.PreviewManualLearning(request)
	if err != nil {
		t.Fatal(err)
	}
	agent.RoleDescription = "changed after preview"
	if agent, err = application.SaveProjectAgent(agent); err != nil {
		t.Fatal(err)
	}
	request.ConfirmationToken = preview.ConfirmationToken
	if _, err = application.ApplyManualLearning(request); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale preview error=%v", err)
	}
	if _, err = application.PreviewManualLearning(ManualLearningRequest{ProjectAgentID: agent.ID, Kind: "memory", Scope: "project", Content: "Authorization: Bearer manual-secret-token"}); err == nil {
		t.Fatal("secret was accepted as a manual lesson")
	}
}

func TestExperienceSearchFindsManualMemory(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	view, _ := application.OpenWorkspace(t.TempDir())
	agent, _ := application.SaveProjectAgent(domain.ProjectAgent{Name: "QA", WorkspaceID: view.Workspace.ID, Provider: domain.ProviderOllama, BaseURL: "http://127.0.0.1:11434", PrimaryModel: "qwen2.5-coder:7b"})
	request := ManualLearningRequest{ProjectAgentID: agent.ID, Kind: "memory", Scope: "project", Content: "Boundary-focused adapters require contract fixtures."}
	preview, err := application.PreviewManualLearning(request)
	if err != nil {
		t.Fatal(err)
	}
	request.ConfirmationToken = preview.ConfirmationToken
	if _, err = application.ApplyManualLearning(request); err != nil {
		t.Fatal(err)
	}
	items, err := application.SearchExperience("boundary-focused", 10)
	if err != nil || len(items) == 0 || items[0].Kind != "memory" || !strings.Contains(items[0].Summary, "Boundary-focused") {
		t.Fatalf("experience search=%#v err=%v", items, err)
	}
}
