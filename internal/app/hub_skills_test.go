package app

import (
	"context"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

func TestSkillCannotEscalateAgentTools(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	commanding, err := application.SaveSkill(domain.SkillDefinition{
		Name: "Command skill", Instructions: "Run the verification command.", RequiredTools: []string{"run_command"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Отказ приходит на экипировке — там, где человек принимает решение. Раньше
	// он ждал первого прогона: навык надевался, всё выглядело исправным, и
	// квест падал позже.
	_, err = application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Restricted", SystemPrompt: "Inspect only", SkillIDs: []string{commanding.ID},
		AllowedTools: []string{"read_file"}, PrimaryModel: "test-model", MaxSteps: 2, MaxDurationSeconds: 10,
	})
	if err == nil || !strings.Contains(err.Error(), "run_command") {
		t.Fatalf("навык с чужим инструментом надет на агента: %v", err)
	}

	// Навык правят и после экипировки — тогда несовпадение всплывает только на
	// подготовке прогона, и там отказ обязан остаться.
	reading, err := application.SaveSkill(domain.SkillDefinition{
		Name: "Reading skill", Instructions: "Inspect with read_file.", RequiredTools: []string{"read_file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Reader", SystemPrompt: "Inspect only", SkillIDs: []string{reading.ID},
		AllowedTools: []string{"read_file"}, PrimaryModel: "test-model", MaxSteps: 2, MaxDurationSeconds: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	reading.RequiredTools = []string{"run_command"}
	if _, err = application.SaveSkill(reading); err != nil {
		t.Fatal(err)
	}
	_, err = application.PreviewAgentRun(AgentRunPreviewRequest{ProfileID: agent.ID, Task: "Inspect the workspace"})
	if err == nil || !strings.Contains(err.Error(), "has not explicitly allowed") {
		t.Fatalf("expected explicit skill permission error, got %v", err)
	}
}

func TestEquippedSkillsStayActiveWhenAnotherProjectSkillExists(t *testing.T) {
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
	review, err := application.SaveSkill(domain.SkillDefinition{
		Name: "Review Practice", Instructions: "Inspect with read_file.", RequiredTools: []string{"read_file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	extra, err := application.SaveSkill(domain.SkillDefinition{
		Name: "Extra Practice", Instructions: "Also inspect with read_file.", RequiredTools: []string{"read_file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.EquipSkill(view.Workspace.ID, extra.ID, nil); err != nil {
		t.Fatal(err)
	}
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Reviewer", SystemPrompt: "Inspect carefully", SkillIDs: []string{review.ID, extra.ID},
		AllowedTools: []string{"read_file", "search_text"}, PrimaryModel: "test-model", MaxSteps: 2, MaxDurationSeconds: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := application.PreviewAgentRun(AgentRunPreviewRequest{ProfileID: agent.ID, Task: "Inspect the workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.SystemMessage, "Review Practice") || !strings.Contains(preview.SystemMessage, "Extra Practice") {
		t.Fatalf("both assigned skills should stay active: %s", preview.SystemMessage)
	}
	if !hasTool(preview.Profile.AllowedTools, "read_skill") {
		t.Fatal("read_skill should be auto-enabled when skills are equipped")
	}
	foundReadSkill := false
	for _, tool := range preview.Tools {
		if tool.Definition.Name == "read_skill" {
			foundReadSkill = true
			break
		}
	}
	if !foundReadSkill {
		t.Fatal("read_skill missing from run preview tools")
	}
}

func TestDisabledProjectSkillIsSkippedWithoutDroppingOthers(t *testing.T) {
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
	keep, err := application.SaveSkill(domain.SkillDefinition{
		Name: "Keep", Instructions: "Keep this practice.", RequiredTools: []string{"read_file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	drop, err := application.SaveSkill(domain.SkillDefinition{
		Name: "Drop", Instructions: "This practice is disabled.", RequiredTools: []string{"read_file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveProjectSkill(context.Background(), domain.ProjectSkillInstance{
		ID: "projectskill-disabled", WorkspaceID: view.Workspace.ID, SkillID: drop.ID, Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Mixed", SystemPrompt: "Inspect", SkillIDs: []string{keep.ID, drop.ID},
		AllowedTools: []string{"read_file"}, PrimaryModel: "test-model", MaxSteps: 2, MaxDurationSeconds: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := application.PreviewAgentRun(AgentRunPreviewRequest{ProfileID: agent.ID, Task: "Inspect"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.SystemMessage, "Keep this practice") {
		t.Fatalf("enabled skill missing: %s", preview.SystemMessage)
	}
	if strings.Contains(preview.SystemMessage, "This practice is disabled") {
		t.Fatalf("disabled skill leaked: %s", preview.SystemMessage)
	}
}

func TestSkillDefaultLowRiskPolicyDoesNotBlockEquip(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	skill, err := application.SaveSkill(domain.SkillDefinition{
		Name: "Review", Instructions: "Read files carefully.", RequiredTools: []string{"read_file"},
		PermissionDelta: map[string]domain.ToolPolicy{"read_file": domain.ToolPolicyAllow},
	})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Default Policy", SystemPrompt: "Inspect", SkillIDs: []string{skill.ID},
		AllowedTools: []string{"read_file"}, PrimaryModel: "test-model", MaxSteps: 2, MaxDurationSeconds: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := application.PreviewAgentRun(AgentRunPreviewRequest{ProfileID: agent.ID, Task: "Inspect"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.SystemMessage, "Read files carefully") {
		t.Fatalf("inlined skill missing: %s", preview.SystemMessage)
	}
}

func TestSaveSkillValidatesToolsAndRejectsDuplicates(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err = application.SaveSkill(domain.SkillDefinition{
		Name: " ", Instructions: "Need a real name.", RequiredTools: []string{"read_file"},
	}); err == nil {
		t.Fatal("expected empty skill name to fail")
	}
	if _, err = application.SaveSkill(domain.SkillDefinition{
		Name: "Review Assist", Instructions: "", RequiredTools: []string{"read_file"},
	}); err == nil {
		t.Fatal("expected empty instructions to fail")
	}
	if _, err = application.SaveSkill(domain.SkillDefinition{
		Name: "Review Assist", Instructions: "Inspect carefully.", RequiredTools: []string{"not_a_real_tool"},
	}); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("expected unknown tool rejection, got %v", err)
	}
	first, err := application.SaveSkill(domain.SkillDefinition{
		Name: "Review Assist", Description: "First", Instructions: "Inspect carefully.", RequiredTools: []string{"read_file", "search_text"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || first.PermissionDelta == nil || first.Configuration == nil {
		t.Fatalf("saved skill missing defaults=%#v", first)
	}
	if _, err = application.SaveSkill(domain.SkillDefinition{
		Name: "review assist", Instructions: "Duplicate name should fail.", RequiredTools: []string{"read_file"},
	}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected duplicate name rejection, got %v", err)
	}
	updated, err := application.SaveSkill(domain.SkillDefinition{
		ID: first.ID, Name: "Review Assist", Description: "Updated", Instructions: "Inspect and summarize.", RequiredTools: []string{"read_file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Description != "Updated" || !updated.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("update lost identity=%#v first=%#v", updated, first)
	}
}
