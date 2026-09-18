package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/companion"
	"local-agent-workbench/internal/domain"
)

func TestCompanionFlowActionRequiresReviewAndIsWorkspaceScoped(t *testing.T) {
	application := newTestApp(t)
	var err error

	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	if err = os.WriteFile(filepath.Join(firstRoot, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(secondRoot, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	firstView, err := application.OpenWorkspace(firstRoot)
	if err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if len(boot.Blueprints) == 0 {
		t.Fatal("expected blueprint catalog")
	}
	if _, err = application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(firstView.Workspace.ID, boot.Blueprints[0])); err != nil {
		t.Fatal(err)
	}
	before, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	response, err := application.CompanionChat(context.Background(), companion.ChatRequest{Message: "Create an important Flow for backend review"})
	if err != nil {
		t.Fatal(err)
	}
	if response.ActionProposal == nil || response.ActionProposal.Flow == nil {
		t.Fatalf("response=%#v", response)
	}
	proposalID := response.ActionProposal.ID
	afterDraft, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if len(afterDraft.Flows) != len(before.Flows) || len(afterDraft.CompanionActionProposals) != 1 {
		t.Fatalf("draft mutated flows or was not bootstrapped: flows=%d proposals=%#v", len(afterDraft.Flows), afterDraft.CompanionActionProposals)
	}

	if _, err = application.OpenWorkspace(secondRoot); err != nil {
		t.Fatal(err)
	}
	if _, err = application.DecideCompanionAction(CompanionActionDecision{ProposalID: proposalID, Action: CompanionActionApply}); err == nil {
		t.Fatal("proposal from another workspace must not be applicable")
	}
	if _, err = application.OpenWorkspace(firstRoot); err != nil {
		t.Fatal(err)
	}
	modified, err := application.DecideCompanionAction(CompanionActionDecision{
		ProposalID: proposalID, Action: CompanionActionModify, Name: "Reviewed companion flow", Description: "Approved draft after review",
	})
	if err != nil {
		t.Fatal(err)
	}
	if modified.Proposal.Status != "modified" || modified.Flow == nil || modified.Flow.Name != "Reviewed companion flow" {
		t.Fatalf("modified=%#v", modified)
	}
	afterModify, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if len(afterModify.Flows) != len(before.Flows) {
		t.Fatal("modifying the draft must not create a flow")
	}
	applied, err := application.DecideCompanionAction(CompanionActionDecision{ProposalID: proposalID, Action: CompanionActionApply})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Proposal.Status != "applied" || applied.Flow == nil || applied.Proposal.AppliedEntityID != applied.Flow.ID {
		t.Fatalf("applied=%#v", applied)
	}
	finalBoot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if len(finalBoot.Flows) != len(before.Flows)+1 || finalBoot.Flows[0].WorkspaceID != firstView.Workspace.ID {
		t.Fatalf("flow was not created in current workspace: %#v", finalBoot.Flows)
	}
	if _, err = application.DecideCompanionAction(CompanionActionDecision{ProposalID: proposalID, Action: CompanionActionApply}); err == nil {
		t.Fatal("applied proposal must not be applied twice")
	}
}

func TestCompanionFlowActionCanBeIgnored(t *testing.T) {
	application := newTestApp(t)
	var err error
	root := t.TempDir()
	if err = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	view, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(view.Workspace.ID, boot.Blueprints[0])); err != nil {
		t.Fatal(err)
	}
	response, err := application.CompanionChat(context.Background(), companion.ChatRequest{Message: "Create a Flow for review"})
	if err != nil || response.ActionProposal == nil {
		t.Fatalf("response=%#v err=%v", response, err)
	}
	ignored, err := application.DecideCompanionAction(CompanionActionDecision{ProposalID: response.ActionProposal.ID, Action: CompanionActionIgnore})
	if err != nil {
		t.Fatal(err)
	}
	after, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if ignored.Proposal.Status != "ignored" || ignored.Flow != nil || len(after.Flows) != len(boot.Flows) {
		t.Fatalf("ignored=%#v flows=%#v", ignored, after.Flows)
	}
}

func TestCompanionCreatesReviewedAgentAndTeamFromChat(t *testing.T) {
	application := newTestApp(t)
	var err error
	root := t.TempDir()
	if err = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	view, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	before, err := application.Bootstrap()
	if err != nil || len(before.Blueprints) == 0 {
		t.Fatalf("bootstrap blueprints=%#v err=%v", before.Blueprints, err)
	}

	agentResponse, err := application.CompanionChat(context.Background(), companion.ChatRequest{Message: "Создай backend агента"})
	if err != nil {
		t.Fatal(err)
	}
	if agentResponse.ActionProposal == nil || agentResponse.ActionProposal.Kind != domain.CompanionActionCreateAgent || agentResponse.ActionProposal.Agent == nil {
		t.Fatalf("agent response=%#v", agentResponse)
	}
	agentProposalID := agentResponse.ActionProposal.ID
	afterAgentDraft, err := application.Bootstrap()
	if err != nil || len(afterAgentDraft.ProjectAgents) != len(before.ProjectAgents) {
		t.Fatalf("agent draft mutated roster: %#v err=%v", afterAgentDraft.ProjectAgents, err)
	}
	modifiedAgent, err := application.DecideCompanionAction(CompanionActionDecision{
		ProposalID: agentProposalID, Action: CompanionActionModify, Name: "Backend Guardian",
		RoleDescription: "Owns backend fixes and API verification", Mission: "Keep the backend stable",
	})
	if err != nil {
		t.Fatal(err)
	}
	if modifiedAgent.Agent == nil || modifiedAgent.Agent.Name != "Backend Guardian" || modifiedAgent.Proposal.Status != "modified" {
		t.Fatalf("modified agent=%#v", modifiedAgent)
	}
	createdAgent, err := application.DecideCompanionAction(CompanionActionDecision{ProposalID: agentProposalID, Action: CompanionActionApply})
	if err != nil {
		t.Fatal(err)
	}
	if createdAgent.Agent == nil || createdAgent.Agent.WorkspaceID != view.Workspace.ID || createdAgent.Proposal.AppliedEntityID != createdAgent.Agent.ID {
		t.Fatalf("created agent=%#v", createdAgent)
	}
	second := domain.ProjectAgentFromBlueprint(view.Workspace.ID, before.Blueprints[min(1, len(before.Blueprints)-1)])
	second.Name = "Reviewer"
	second, err = application.SaveProjectAgent(second)
	if err != nil {
		t.Fatal(err)
	}

	teamResponse, err := application.CompanionChat(context.Background(), companion.ChatRequest{Message: "Создай backend команду агентов"})
	if err != nil {
		t.Fatal(err)
	}
	if teamResponse.ActionProposal == nil || teamResponse.ActionProposal.Kind != domain.CompanionActionCreateTeam || teamResponse.ActionProposal.Team == nil {
		t.Fatalf("team response=%#v", teamResponse)
	}
	teamProposalID := teamResponse.ActionProposal.ID
	if _, err = application.DecideCompanionAction(CompanionActionDecision{
		ProposalID: teamProposalID, Action: CompanionActionModify, AgentIDs: []string{},
	}); err == nil || !strings.Contains(err.Error(), "хотя бы один агент") {
		t.Fatalf("пустой отряд принят или объяснён непонятно: %v", err)
	}
	if _, err = application.DecideCompanionAction(CompanionActionDecision{
		ProposalID: teamProposalID, Action: CompanionActionModify, AgentIDs: []string{"unknown-agent"},
	}); err == nil {
		t.Fatal("team draft accepted an unknown project agent")
	}
	modifiedTeam, err := application.DecideCompanionAction(CompanionActionDecision{
		ProposalID: teamProposalID, Action: CompanionActionModify, Name: "Backend Strike Team",
		Description: "Backend implementation and review", AgentIDs: []string{createdAgent.Agent.ID, second.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if modifiedTeam.Team == nil || len(modifiedTeam.Team.AgentIDs) != 2 || modifiedTeam.Proposal.Status != "modified" {
		t.Fatalf("modified team=%#v", modifiedTeam)
	}
	beforeTeamApply, err := application.Bootstrap()
	if err != nil || len(beforeTeamApply.Teams) != len(before.Teams) {
		t.Fatalf("team draft mutated teams: %#v err=%v", beforeTeamApply.Teams, err)
	}
	createdTeam, err := application.DecideCompanionAction(CompanionActionDecision{ProposalID: teamProposalID, Action: CompanionActionApply})
	if err != nil {
		t.Fatal(err)
	}
	if createdTeam.Team == nil || createdTeam.Team.Name != "Backend Strike Team" || len(createdTeam.Team.AgentIDs) != 2 || createdTeam.Proposal.AppliedEntityID != createdTeam.Team.ID {
		t.Fatalf("created team=%#v", createdTeam)
	}
	finalBoot, err := application.Bootstrap()
	if err != nil || len(finalBoot.ProjectAgents) != len(before.ProjectAgents)+2 || len(finalBoot.Teams) != len(before.Teams)+1 {
		t.Fatalf("final roster=%#v teams=%#v err=%v", finalBoot.ProjectAgents, finalBoot.Teams, err)
	}
}

func TestCompanionCreatesReviewedAndEquippedSkillFromChat(t *testing.T) {
	application := newTestApp(t)
	var err error
	root := t.TempDir()
	if err = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = application.OpenWorkspace(root); err != nil {
		t.Fatal(err)
	}
	before, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	response, err := application.CompanionChat(context.Background(), companion.ChatRequest{Message: "Создай PostgreSQL skill для проверки миграций"})
	if err != nil {
		t.Fatal(err)
	}
	proposal := response.ActionProposal
	if proposal == nil || proposal.Kind != domain.CompanionActionCreateSkill || proposal.Skill == nil || proposal.Status != "pending" {
		t.Fatalf("skill response=%#v", response)
	}
	if len(proposal.Skill.Scripts) != 0 || len(proposal.Skill.References) != 0 || len(proposal.Skill.PermissionDelta) != 0 || len(proposal.Skill.RequiredTools) == 0 {
		t.Fatalf("unsafe or incomplete skill draft=%#v", proposal.Skill)
	}
	hasRunCommand := false
	for _, tool := range proposal.Skill.RequiredTools {
		if tool == "run_command" {
			hasRunCommand = true
			break
		}
	}
	if !hasRunCommand {
		t.Fatalf("postgres/migration skill draft missing run_command: %#v", proposal.Skill.RequiredTools)
	}
	afterDraft, err := application.Bootstrap()
	if err != nil || len(afterDraft.Skills) != len(before.Skills) || len(afterDraft.ProjectSkills) != len(before.ProjectSkills) {
		t.Fatalf("skill draft mutated Hub: skills=%d projectSkills=%d err=%v", len(afterDraft.Skills), len(afterDraft.ProjectSkills), err)
	}
	if _, err = application.DecideCompanionAction(CompanionActionDecision{
		ProposalID: proposal.ID, Action: CompanionActionModify, RequiredTools: []string{"unknown_tool"},
	}); err == nil {
		t.Fatal("skill draft accepted an unknown tool")
	}
	modified, err := application.DecideCompanionAction(CompanionActionDecision{
		ProposalID: proposal.ID, Action: CompanionActionModify, Name: "PostgreSQL Expert",
		Description: "Review and verify database migrations", Instructions: "Inspect schema and migrations, then run the approved verification command.",
		RequiredTools: []string{"read_file", "search_text", "run_command"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if modified.Skill == nil || modified.Skill.Name != "PostgreSQL Expert" || modified.Proposal.Status != "modified" || len(modified.Skill.RequiredTools) != 3 {
		t.Fatalf("modified skill=%#v", modified)
	}
	stillDraft, err := application.Bootstrap()
	if err != nil || len(stillDraft.Skills) != len(before.Skills) || len(stillDraft.ProjectSkills) != len(before.ProjectSkills) {
		t.Fatalf("modified skill draft mutated Hub: skills=%d projectSkills=%d err=%v", len(stillDraft.Skills), len(stillDraft.ProjectSkills), err)
	}
	applied, err := application.DecideCompanionAction(CompanionActionDecision{ProposalID: proposal.ID, Action: CompanionActionApply})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Skill == nil || applied.ProjectSkill == nil || applied.Proposal.AppliedEntityID != applied.Skill.ID || applied.ProjectSkill.SkillID != applied.Skill.ID || !applied.ProjectSkill.Enabled {
		t.Fatalf("applied skill=%#v", applied)
	}
	afterApply, err := application.Bootstrap()
	if err != nil || len(afterApply.Skills) != len(before.Skills)+1 || len(afterApply.ProjectSkills) != len(before.ProjectSkills)+1 {
		t.Fatalf("applied skill absent: skills=%d projectSkills=%d err=%v", len(afterApply.Skills), len(afterApply.ProjectSkills), err)
	}
	if _, err = application.DecideCompanionAction(CompanionActionDecision{ProposalID: proposal.ID, Action: CompanionActionApply}); err == nil {
		t.Fatal("applied skill action was replayed")
	}
	duplicate, err := application.CompanionChat(context.Background(), companion.ChatRequest{Message: "Создай PostgreSQL skill"})
	if err != nil || duplicate.ActionProposal != nil {
		t.Fatalf("duplicate skill response=%#v err=%v", duplicate, err)
	}
}

// Настройки, выбранные человеком в карточке ленты, доезжают до созданного
// агента. Раньше решение несло только имя, роль и миссию: карточка показывала
// модель, умения и пределы, человек их правил — и агент всё равно заводился с
// тем, что придумало ядро.
func TestCompanionAgentActionKeepsHumanRuntimeChoices(t *testing.T) {
	application := newTestApp(t)
	var err error

	root := t.TempDir()
	if err = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = application.OpenWorkspace(root); err != nil {
		t.Fatal(err)
	}
	response, err := application.CompanionChat(context.Background(), companion.ChatRequest{Message: "Создай backend агента"})
	if err != nil {
		t.Fatal(err)
	}
	if response.ActionProposal == nil || response.ActionProposal.Kind != domain.CompanionActionCreateAgent {
		t.Fatalf("ядро не предложило создать агента: %#v", response)
	}
	proposalID := response.ActionProposal.ID

	temperature := 0.0
	modified, err := application.DecideCompanionAction(CompanionActionDecision{
		ProposalID: proposalID, Action: CompanionActionModify,
		Name: "Хранитель бэкенда", RoleDescription: "Ведёт серверную часть",
		AllowedTools:       []string{"read_file", "search_text", "run_command"},
		ToolPolicies:       map[string]string{"run_command": "ASK", "network": "DENY"},
		PrimaryModel:       "qwen3-coder:30b",
		ReasoningEffort:    "low",
		ApprovalMode:       string(domain.ApprovalAlways),
		MaxSteps:           12,
		MaxDurationSeconds: 300,
		MaxOutputTokens:    8192,
		Temperature:        &temperature,
	})
	if err != nil {
		t.Fatal(err)
	}
	if modified.Agent == nil || modified.Agent.PrimaryModel != "qwen3-coder:30b" || modified.Agent.MaxSteps != 12 {
		t.Fatalf("черновик потерял выбор человека: %#v", modified.Agent)
	}

	applied, err := application.DecideCompanionAction(CompanionActionDecision{ProposalID: proposalID, Action: CompanionActionApply})
	if err != nil {
		t.Fatal(err)
	}
	agent := applied.Agent
	if agent == nil {
		t.Fatalf("агент не создан: %#v", applied)
	}
	if agent.PrimaryModel != "qwen3-coder:30b" || agent.ReasoningEffort != "low" || agent.ApprovalMode != domain.ApprovalAlways {
		t.Fatalf("мозг агента собран не по карточке: %#v", agent)
	}
	if agent.MaxSteps != 12 || agent.MaxDurationSeconds != 300 || agent.MaxOutputTokens != 8192 || agent.Temperature != 0 {
		t.Fatalf("пределы агента собраны не по карточке: %#v", agent)
	}
	if strings.Join(agent.AllowedTools, ",") != "read_file,search_text,run_command" {
		t.Fatalf("умения агента собраны не по карточке: %#v", agent.AllowedTools)
	}
	if agent.ToolPolicies["run_command"] != "ASK" || agent.ToolPolicies["network"] != "DENY" {
		t.Fatalf("политики умений собраны не по карточке: %#v", agent.ToolPolicies)
	}
}

// Неизвестное умение отбивается на правке черновика, а не всплывает при
// создании: человек должен узнать об ошибке там, где он её сделал.
func TestCompanionAgentActionRejectsUnknownTool(t *testing.T) {
	application := newTestApp(t)
	var err error

	root := t.TempDir()
	if err = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = application.OpenWorkspace(root); err != nil {
		t.Fatal(err)
	}
	response, err := application.CompanionChat(context.Background(), companion.ChatRequest{Message: "Создай backend агента"})
	if err != nil {
		t.Fatal(err)
	}
	if response.ActionProposal == nil || response.ActionProposal.Kind != domain.CompanionActionCreateAgent {
		t.Fatalf("ядро не предложило создать агента: %#v", response)
	}
	if _, err = application.DecideCompanionAction(CompanionActionDecision{
		ProposalID: response.ActionProposal.ID, Action: CompanionActionModify,
		AllowedTools: []string{"read_file", "launch_missiles"},
	}); err == nil || !strings.Contains(err.Error(), "launch_missiles") {
		t.Fatalf("неизвестное умение принято или объяснено непонятно: %v", err)
	}
}
