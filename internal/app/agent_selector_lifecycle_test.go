package app

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestRuntimeSubagentRequiresActiveParentPermissionAndToolIntersection(t *testing.T) {
	application := newTestApp(t)
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Developer", RoleFamily: "developer", RoleDescription: "General developer",
		Provider: domain.ProviderOllama, BaseURL: "http://127.0.0.1:11434", PrimaryModel: "model",
		AllowedTools: []string{"read_file", "search_code"}, ToolPolicies: map[string]string{"read_file": "ALLOW", "search_code": "ASK"},
		MaxSteps: 10, MaxDurationSeconds: 600, MaxOutputTokens: 1024, ContextWindowTokens: 8192,
	})
	if err != nil {
		t.Fatal(err)
	}
	brief := domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModePrecise, State: "ready", Goal: "Implement Symfony route", ResultKind: "code",
		Criteria:    []domain.AcceptanceCriterion{{ID: "c1", Text: "route works", Kind: "manual"}},
		Permissions: domain.TaskPermissions{ProvisionProjectAgents: true},
		Budget:      domain.TaskBudget{Tokens: 10000, ActiveSeconds: 600, MaxParallel: 1, MaxReplans: 1, MaxAttempts: 1, MaxProjectAgents: 1},
	})
	brief, err = domain.ApproveTaskBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	quest := domain.Quest{ID: "quest-runtime-subagent", WorkspaceID: view.Workspace.ID, Title: brief.Goal, Status: domain.QuestRunning, Brief: &brief, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err = application.store.SaveQuest(context.Background(), quest); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveExecution(context.Background(), domain.ExecutionInstance{
		ID: "execution-parent", WorkspaceID: view.Workspace.ID, ProjectAgentID: parent.ID, QuestID: quest.ID,
		Task: brief.Goal, Status: domain.RunRunning, StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, deniedErr := application.RequestTemporarySubagent(context.Background(), domain.SubagentRequest{
		WorkspaceID: view.Workspace.ID, QuestID: quest.ID, ParentAgentID: parent.ID,
		Role: "Shell specialist", Mission: "Run deployment", RequiredTools: []string{"run_command"},
	}); deniedErr == nil || !strings.Contains(deniedErr.Error(), "not allowed") {
		t.Fatalf("parent tool ceiling was not enforced: %v", deniedErr)
	}
	child, err := application.RequestTemporarySubagent(context.Background(), domain.SubagentRequest{
		WorkspaceID: view.Workspace.ID, QuestID: quest.ID, ParentAgentID: parent.ID,
		Role: "Symfony-разработчик", Mission: "Implement route", RequiredTools: []string{"read_file", "run_command"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !child.Temporary || child.ParentAgentID != parent.ID || child.OwnerQuestID != quest.ID || child.BlueprintID != "" || child.Status != domain.ProjectAgentActive {
		t.Fatalf("invalid temporary child: %#v", child)
	}
	if strings.Join(child.AllowedTools, ",") != "read_file" || child.ConnectionID != parent.ConnectionID || child.PrimaryModel != parent.PrimaryModel {
		t.Fatalf("child exceeded or failed to inherit parent boundary: %#v", child)
	}
	if _, err = application.RequestTemporarySubagent(context.Background(), domain.SubagentRequest{
		WorkspaceID: view.Workspace.ID, QuestID: quest.ID, ParentAgentID: parent.ID,
		Role: "Другой специалист", Mission: "Second child", RequiredTools: []string{"read_file"},
	}); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("subagent budget was not enforced: %v", err)
	}
}

func TestAgentSelectorUsesGeneralDeveloperDraftForSymfony(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	proposal := rosterTestProposal(world.ID, "selector-symfony", "Развернуть Symfony API", true)
	order := rosterTestOrder(t, application, proposal, "conversation-selector-symfony")
	if len(order.Roster.AgentIDs) != 1 {
		t.Fatalf("selector agent IDs = %#v", order.Roster.AgentIDs)
	}
	agent, err := application.store.GetProjectAgent(context.Background(), order.Roster.AgentIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if agent.Status != domain.ProjectAgentDraft || agent.RoleFamily != "developer" || agent.BlueprintID != "" || agent.Temporary {
		t.Fatalf("Symfony became a top-level specialization: %#v", agent)
	}
	if strings.Contains(strings.ToLower(agent.RoleDescription), "symfony") || strings.Contains(strings.ToLower(agent.Name), "symfony") {
		t.Fatalf("technology leaked into root role: %#v", agent)
	}
	if _, err = application.ApproveWorkOrderV2(context.Background(), order.ID, ApproveWorkOrderV2Request{
		Version: order.Version, Digest: domain.WorkOrderDigest(order), IdempotencyKey: "draft-must-block",
	}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "активирован") {
		t.Fatalf("draft did not block WorkOrder approval: %v", err)
	}

	agent.Mission = "Общий разработчик после правки карточки"
	saved, err := application.SaveProjectAgent(agent)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != domain.ProjectAgentDraft {
		t.Fatalf("ordinary save activated draft: %#v", saved)
	}
	active, err := application.ActivateProjectAgentDraft(agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if active.Status != domain.ProjectAgentActive {
		t.Fatalf("explicit activation did not transition status: %#v", active)
	}
}

func TestRejectDraftCascadesAndExcludesFamilyForSameSelection(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	order := rosterTestOrder(t, application, rosterTestProposal(world.ID, "selector-reject", "Развернуть Symfony", true), "conversation-selector-reject")
	if len(order.Roster.AgentIDs) != 1 {
		t.Fatalf("selector result = %#v", order.Roster)
	}
	root, err := application.store.GetProjectAgent(context.Background(), order.Roster.AgentIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	child := root
	child.ID, child.Status, child.ParentAgentID, child.OwnerQuestID, child.Temporary = "draft-child", domain.ProjectAgentActive, root.ID, "quest-x", true
	child.Name = "Symfony child"
	if err = application.store.SaveProjectAgent(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	result, err := application.RejectProjectAgentDraft(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ReplacementAgentIDs) != 0 {
		t.Fatalf("rejected family was proposed again: %#v", result)
	}
	for _, id := range []string{root.ID, child.ID} {
		if _, getErr := application.store.GetProjectAgent(context.Background(), id); !errors.Is(getErr, sql.ErrNoRows) {
			t.Fatalf("rejected agent %s survived: %v", id, getErr)
		}
	}
	rejected, err := application.store.RejectedRoleFamiliesForWorkOrder(context.Background(), order.ID)
	if err != nil || len(rejected) != 1 || rejected[0] != "developer" {
		t.Fatalf("rejection exclusion = %#v err=%v", rejected, err)
	}
}

func TestExactSelectionDigestReusesPersistedDraft(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	proposal := rosterTestProposal(world.ID, "selector-stable-1", "Собрать API", false)
	first := rosterTestOrder(t, application, proposal, "conversation-selector-stable")
	proposal.ID = "selector-stable-2"
	second := rosterTestOrder(t, application, proposal, "conversation-selector-stable")
	if len(first.Roster.AgentIDs) != 1 || len(second.Roster.AgentIDs) != 1 || first.Roster.AgentIDs[0] != second.Roster.AgentIDs[0] {
		t.Fatalf("same digest did not preserve selection: %#v / %#v", first.Roster.AgentIDs, second.Roster.AgentIDs)
	}
}
