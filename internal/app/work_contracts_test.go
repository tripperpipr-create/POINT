package app

import (
	"context"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestWorkContractRejectsChangesOutsideOwnership(t *testing.T) {
	contract := domain.WorkContract{OwnedPaths: []string{"internal/api"}, ForbiddenPaths: []string{"internal/api/secrets"}}
	if err := validateWorkContractChanges(contract, domain.ChangeSet{Items: []domain.ChangeItem{{Path: "internal/api/server.go"}}}); err != nil {
		t.Fatal(err)
	}
	if err := validateWorkContractChanges(contract, domain.ChangeSet{Items: []domain.ChangeItem{{Path: "internal/ui/app.js"}}}); err == nil {
		t.Fatal("out-of-scope change was accepted")
	}
	if err := validateWorkContractChanges(contract, domain.ChangeSet{Items: []domain.ChangeItem{{Path: "internal/api/secrets/key.go"}}}); err == nil {
		t.Fatal("forbidden change was accepted")
	}
}

func TestWorkContractMidRunForbiddenPathsSkipsSharedManifests(t *testing.T) {
	contract := domain.WorkContract{
		ForbiddenPaths: append(append([]string{}, domain.SharedProjectPaths()...), "bin", "tests", "phpunit.xml"),
	}
	got := workContractMidRunForbiddenPaths(&contract)
	if len(got) != 0 {
		t.Fatalf("mid-run forbids must stay empty to avoid workspace-wide gate deadlock, got %v", got)
	}
}

func TestDetectRoleGapFindsMissingFrontend(t *testing.T) {
	gap := detectRoleGap("Create a React UI and API", []domain.ProjectAgent{{Name: "Backend", RoleDescription: "backend engineer"}})
	if gap == nil || gap.Role != "frontend" {
		t.Fatalf("gap=%#v", gap)
	}
}

func TestAgentPrepCreatesTemporarySubagentWithoutBlueprintMutation(t *testing.T) {
	application, world := outcomeWorld(t)
	ctx := context.Background()
	before, err := application.store.ListBlueprints(ctx)
	if err != nil || len(before) == 0 {
		t.Fatalf("blueprints=%d err=%v", len(before), err)
	}
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, Goal: "Build UI", ResultKind: "workspace_change",
		Criteria:    []domain.AcceptanceCriterion{{ID: "c1", Text: "UI ships", Kind: "manual"}},
		Permissions: domain.TaskPermissions{WriteFiles: true, ProvisionProjectAgents: true},
		Budget:      domain.TaskBudget{Tokens: 10000, ActiveSeconds: 600, MaxParallel: 1, MaxReplans: 2, MaxAttempts: 2, MaxProjectAgents: 2},
	}))
	if err != nil {
		t.Fatal(err)
	}
	primary := domain.ProjectAgentFromBlueprint(world.ID, before[0])
	primary, err = application.SaveProjectAgent(primary)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	parent := domain.Quest{
		ID: "quest-parent", WorkspaceID: world.ID, Title: "App", Kind: "project", Brief: &brief,
		Status: domain.QuestActive, CreatedAt: now, UpdatedAt: now,
	}
	if err = application.store.SaveQuest(ctx, parent); err != nil {
		t.Fatal(err)
	}
	chain, err := application.createAgentPrepChain(ctx, &parent, domain.RoleRequirement{
		Role: "frontend", Responsibility: "Own the UI", PreparationKind: "subagent", ParentAgentID: primary.ID,
		RequiredTools: []string{"read_file", "propose_patch"}, Verification: "READY",
	})
	if err != nil {
		t.Fatal(err)
	}
	if chain.CandidateAgentID == "" {
		t.Fatalf("chain=%#v", chain)
	}
	after, err := application.store.ListBlueprints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("global blueprint count changed %d -> %d", len(before), len(after))
	}
	agent, err := application.store.GetProjectAgent(ctx, chain.CandidateAgentID)
	if err != nil {
		t.Fatal(err)
	}
	if agent.WorkspaceID != world.ID || !agent.Temporary || agent.ParentAgentID != primary.ID {
		t.Fatalf("candidate escaped workspace: %#v", agent)
	}
}

func TestAgentGapDistinguishesUserCreatedPrimaryAndTemporarySubagent(t *testing.T) {
	application, world := outcomeWorld(t)
	brief := domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModePrecise, Goal: "Deploy Symfony with PostgreSQL", ResultKind: "workspace_change",
		Criteria:    []domain.AcceptanceCriterion{{ID: "c1", Text: "Tests pass", Kind: "manual"}},
		Permissions: domain.TaskPermissions{WriteFiles: true, ExecuteCommands: true},
	})
	if gap := application.assessAgentGap(context.Background(), &brief, nil, nil); gap == nil || gap.Kind != "missing_primary" || gap.Requirement.PreparationKind != "create_agent" {
		t.Fatalf("empty roster gap=%#v", gap)
	}
	blueprints, err := application.store.ListBlueprints(context.Background())
	if err != nil || len(blueprints) == 0 {
		t.Fatal(err)
	}
	primary := domain.ProjectAgentFromBlueprint(world.ID, blueprints[0])
	primary.Name, primary.RoleDescription = "Backend", "backend developer"
	primary, err = application.SaveProjectAgent(primary)
	if err != nil {
		t.Fatal(err)
	}
	if gap := application.assessAgentGap(context.Background(), &brief, []domain.ProjectAgent{primary}, []domain.ProjectAgent{primary}); gap == nil || gap.Kind != "missing_subagent" || gap.Requirement.ParentAgentID != primary.ID {
		t.Fatalf("specialist gap=%#v", gap)
	}
}

func TestMissingPrimaryCreatesUserGuidedPrerequisiteWithoutAutonomousAgent(t *testing.T) {
	application, world := outcomeWorld(t)
	ctx := context.Background()
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModePrecise, Goal: "Deploy Symfony", ResultKind: "workspace_change",
		Criteria:    []domain.AcceptanceCriterion{{ID: "c1", Text: "Symfony runs", Kind: "manual"}},
		Permissions: domain.TaskPermissions{WriteFiles: true},
	}))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	parent := domain.Quest{ID: "quest-user-primary", WorkspaceID: world.ID, Title: "Symfony", Brief: &brief, Status: domain.QuestDraft, CreatedAt: now, UpdatedAt: now}
	if err = application.store.SaveQuest(ctx, parent); err != nil {
		t.Fatal(err)
	}
	before, _ := application.store.ListProjectAgents(ctx, world.ID)
	chain, err := application.createAgentPrepChain(ctx, &parent, domain.RoleRequirement{
		Role: "developer", Responsibility: "Build Symfony", PreparationKind: "create_agent", Verification: "READY",
	})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := application.store.ListProjectAgents(ctx, world.ID)
	if chain.State != "user_decision" || parent.Status != domain.QuestPaused || len(parent.PrerequisiteIDs) != 1 {
		t.Fatalf("user prerequisite not retained: chain=%#v parent=%#v", chain, parent)
	}
	if len(after) != len(before) {
		t.Fatalf("full agent was created autonomously: %d -> %d", len(before), len(after))
	}
	actions, err := application.store.ListCompanionActionProposals(ctx, world.ID)
	if err != nil || len(actions) == 0 || actions[0].Kind != domain.CompanionActionCreateAgent {
		t.Fatalf("user create-agent action missing: %#v err=%v", actions, err)
	}
}

func TestUserReconfigurationCompletesPrerequisiteAndKeepsParent(t *testing.T) {
	application, world := outcomeWorld(t)
	ctx := context.Background()
	blueprints, err := application.store.ListBlueprints(ctx)
	if err != nil || len(blueprints) == 0 {
		t.Fatal(err)
	}
	blocked := domain.ProjectAgentFromBlueprint(world.ID, blueprints[0])
	blocked.Provider, blocked.PrimaryModel, blocked.ConnectionID = "", "", ""
	if err = application.store.SaveProjectAgent(ctx, blocked); err != nil {
		t.Fatal(err)
	}
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModePrecise, Goal: "Implement endpoint", ResultKind: "workspace_change",
		Criteria:    []domain.AcceptanceCriterion{{ID: "c1", Text: "Done", Kind: "manual"}},
		Permissions: domain.TaskPermissions{WriteFiles: true},
	}))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	parent := domain.Quest{ID: "quest-reconfigure", WorkspaceID: world.ID, Title: "Endpoint", Brief: &brief, Status: domain.QuestDraft, CreatedAt: now, UpdatedAt: now}
	if err = application.store.SaveQuest(ctx, parent); err != nil {
		t.Fatal(err)
	}
	chain, err := application.createAgentPrepChain(ctx, &parent, domain.RoleRequirement{
		Role: "developer", Responsibility: "Implement endpoint", PreparationKind: "reconfigure_agent",
		ParentAgentID: blocked.ID, Verification: "READY",
	})
	if err != nil || chain.State != "user_decision" {
		t.Fatalf("chain=%#v err=%v", chain, err)
	}
	ready := domain.ProjectAgentFromBlueprint(world.ID, blueprints[0])
	ready.ID, ready.CreatedAt = blocked.ID, blocked.CreatedAt
	if _, err = application.SaveProjectAgent(ready); err != nil {
		t.Fatal(err)
	}
	chains, err := application.store.ListAgentPrepChains(ctx, world.ID)
	if err != nil || len(chains) == 0 || chains[0].State != "ready" {
		t.Fatalf("reconfiguration did not complete: %#v err=%v", chains, err)
	}
	quests, err := application.store.ListQuests(ctx, world.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundCompleted := false
	for _, quest := range quests {
		if quest.ID == chain.PrepQuestID && quest.Status == domain.QuestCompleted {
			foundCompleted = true
		}
	}
	if !foundCompleted {
		t.Fatalf("preparation quest did not complete: %#v", quests)
	}
}

func TestRoleGapWithoutProvisioningStopsOnHireCard(t *testing.T) {
	application, world := outcomeWorld(t)
	ctx := context.Background()
	before, err := application.store.ListCompanionActionProposals(ctx, world.ID)
	if err != nil {
		t.Fatal(err)
	}
	gap := detectRoleGap("Create a React UI", []domain.ProjectAgent{{Name: "Backend", RoleDescription: "backend engineer"}})
	if gap == nil {
		t.Fatal("expected frontend gap")
	}
	if err = application.proposeRoleGapHire(ctx, world.ID, "Create a React UI", *gap); err != nil {
		t.Fatal(err)
	}
	after, err := application.store.ListCompanionActionProposals(ctx, world.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("hire card not created: before=%d after=%d", len(before), len(after))
	}
	found := false
	for _, item := range after {
		if item.Kind == domain.CompanionActionCreateAgent && item.Status == "pending" && item.Agent != nil {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing pending create_agent card: %#v", after)
	}
}

func TestRequireIsolatedProjectWritersFailClosedWithoutIsolation(t *testing.T) {
	application, _ := outcomeWorld(t)
	flow := domain.FlowGraph{Nodes: []domain.FlowNode{
		{Kind: domain.FlowNodeAgent, Name: "A"},
		{Kind: domain.FlowNodeAgent, Name: "B"},
	}}
	if err := application.requireIsolatedProjectWriters(flow); err != nil {
		t.Fatal(err)
	}
}

func TestParallelWritersRequireDistinctExecutionRoots(t *testing.T) {
	application, _ := outcomeWorld(t)
	flow := domain.FlowGraph{
		Nodes: []domain.FlowNode{
			{ID: "p", Kind: domain.FlowNodeParallel, Name: "wave"},
			{ID: "a", Kind: domain.FlowNodeAgent, Name: "A", Config: map[string]any{"writeFiles": true}},
			{ID: "b", Kind: domain.FlowNodeAgent, Name: "B", Config: map[string]any{"writeFiles": true}},
		},
		Edges: []domain.FlowEdge{
			{From: "p", To: "a"}, {From: "p", To: "b"},
		},
	}
	if err := application.requireIsolatedProjectWriters(flow); err == nil {
		t.Fatal("expected parallel writers without executionRoot to fail")
	}
	flow.Nodes[1].Config["executionRoot"] = "root:a"
	flow.Nodes[2].Config["executionRoot"] = "root:b"
	if err := application.requireIsolatedProjectWriters(flow); err != nil {
		t.Fatal(err)
	}
}

func TestWriterRootBusySerializesUnlabeledWriters(t *testing.T) {
	flow := domain.FlowGraph{Nodes: []domain.FlowNode{
		{ID: "a", Kind: domain.FlowNodeAgent, Config: map[string]any{"writeFiles": true}},
		{ID: "b", Kind: domain.FlowNodeAgent, Config: map[string]any{"writeFiles": true}},
	}}
	run := domain.FlowRun{NodeStates: map[string]domain.FlowNodeState{
		"a": {Status: "running", Output: map[string]any{"executionId": "exec-a"}},
	}}
	if !writerRootBusy(flow, run, flow.Nodes[1]) {
		t.Fatal("second unlabeled writer must wait")
	}
}

func TestReviewReadsIntegratedRevision(t *testing.T) {
	quest := domain.Quest{Controller: map[string]any{}}
	appendStageArtifact(&quest, domain.StageArtifact{Kind: domain.StageRoleImplement, StageID: "implement", Status: domain.ArtifactValid})
	review := domain.FlowNode{Kind: domain.FlowNodeAgent, Config: map[string]any{"stageRole": domain.StageRoleImplReview, "writeFiles": false}}
	if reviewReadsIntegratedRevision(quest, review) {
		t.Fatal("review must wait for integrate artifact")
	}
	appendStageArtifact(&quest, domain.StageArtifact{Kind: domain.StageRoleIntegrate, StageID: "integrate", Status: domain.ArtifactValid})
	if !reviewReadsIntegratedRevision(quest, review) {
		t.Fatal("review should proceed after integrate")
	}
}

func TestSerialWritersDoNotRequireIsolation(t *testing.T) {
	application, _ := outcomeWorld(t)
	flow := domain.FlowGraph{
		Nodes: []domain.FlowNode{
			{ID: "a", Kind: domain.FlowNodeAgent, Name: "Bootstrap", Config: map[string]any{"stageRole": domain.StageRoleBootstrap, "writeFiles": true}},
			{ID: "b", Kind: domain.FlowNodeAgent, Name: "Implement", Config: map[string]any{"stageRole": domain.StageRoleImplement, "writeFiles": true}},
			{ID: "r", Kind: domain.FlowNodeAgent, Name: "Review", Config: map[string]any{"stageRole": domain.StageRoleImplReview, "writeFiles": false}},
		},
		Edges: []domain.FlowEdge{{From: "a", To: "b"}, {From: "b", To: "r"}},
	}
	if err := application.requireIsolatedProjectWriters(flow); err != nil {
		t.Fatal(err)
	}
}
