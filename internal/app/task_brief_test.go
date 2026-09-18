package app

import (
	"context"
	"local-agent-workbench/internal/domain"
	"testing"
	"time"
)

func TestTaskBriefDecisionVersionedApproval(t *testing.T) {
	b := domain.NormalizeTaskBrief(domain.TaskBrief{Mode: domain.TaskModeProject, State: "ready", Goal: "Inspect project", ResultKind: "report", Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Report", Kind: "manual"}}})
	p := domain.QuestProposal{Brief: &b}
	if err := applyTaskBriefDecision(&p, QuestProposalDecision{Action: QuestProposalStart, ExpectedVersion: 1}); err == nil {
		t.Fatal("project launched without approval")
	}
	if err := applyTaskBriefDecision(&p, QuestProposalDecision{Action: QuestProposalStart, ExpectedVersion: 1, ApproveVersion: 1}); err != nil {
		t.Fatal(err)
	}
	changed := *p.Brief
	changed.Goal = "New goal"
	if err := applyTaskBriefDecision(&p, QuestProposalDecision{Action: QuestProposalModify, ExpectedVersion: 1, Brief: &changed}); err != nil {
		t.Fatal(err)
	}
	if p.Brief.Version != 2 || domain.IsTaskBriefApproved(*p.Brief) {
		t.Fatal("edit failed to invalidate approval")
	}
	if err := applyTaskBriefDecision(&p, QuestProposalDecision{Action: QuestProposalStart, ExpectedVersion: 1, ApproveVersion: 1}); err == nil {
		t.Fatal("stale card launched")
	}
	if err := applyTaskBriefDecision(&p, QuestProposalDecision{Action: QuestProposalStart, ExpectedVersion: 2, ApproveVersion: 1}); err == nil {
		t.Fatal("stale approval launched")
	}
	p.Brief.Mode = domain.TaskModePrecise
	p.Brief.Budget.MaxParallel = 1
	if err := applyTaskBriefDecision(&p, QuestProposalDecision{Action: QuestProposalStart, ExpectedVersion: 2}); err != nil {
		t.Fatalf("precise task needs extra approval: %v", err)
	}
}

func TestTaskBriefCannotBeInjectedOrEnlargedThroughQuestCRUD(t *testing.T) {
	a, ws := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	b, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{Mode: domain.TaskModeProject, Goal: "Report", ResultKind: "report", Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Report", Kind: "manual"}}}))
	if err != nil {
		t.Fatal(err)
	}
	q := domain.Quest{ID: "q", WorkspaceID: ws.ID, Title: "Report", Description: b.Goal, Brief: &b, BudgetTokens: b.Budget.Tokens, CreatedAt: now, UpdatedAt: now}
	if _, err = a.SaveQuest(q); err == nil {
		t.Fatal("generic CRUD accepted injected approval")
	}
	if err = a.store.SaveQuest(ctx, q); err != nil {
		t.Fatal(err)
	}
	larger := q
	larger.Brief = nil
	larger.BudgetTokens++
	if _, err = a.SaveQuest(larger); err == nil {
		t.Fatal("legacy update enlarged approved budget")
	}
	if _, err = a.SaveQuest(q); err != nil {
		t.Fatal(err)
	}
}

func TestTaskExecutionCannotReplayOrChangeStage(t *testing.T) {
	b := &domain.TaskBrief{}
	e := domain.ExecutionInstance{ProjectAgentID: "agent", Task: "agreed", Status: domain.RunPending}
	if err := validateTaskExecutionLaunch(&e, b, "agent", "agreed"); err != nil {
		t.Fatal(err)
	}
	for _, status := range []domain.RunStatus{domain.RunInterrupted, domain.RunRunning, domain.RunCompleted, domain.RunFailed} {
		e.Status = status
		if validateTaskExecutionLaunch(&e, b, "agent", "agreed") == nil {
			t.Fatal("replayed " + status)
		}
	}
	e.Status = domain.RunPending
	e.RunID = "previous"
	if validateTaskExecutionLaunch(&e, b, "agent", "agreed") == nil {
		t.Fatal("lost previous run")
	}
	e.RunID = ""
	if validateTaskExecutionLaunch(&e, b, "other", "agreed") == nil || validateTaskExecutionLaunch(&e, b, "agent", "expanded") == nil {
		t.Fatal("stage replaced")
	}
	if validateTaskExecutionLaunch(nil, b, "agent", "agreed") == nil {
		t.Fatal("unplanned stage accepted")
	}
	if err := validateTaskExecutionLaunch(&e, nil, "other", "legacy"); err != nil {
		t.Fatal("legacy behavior changed", err)
	}
}

func TestTaskChildrenCannotBypassApprovalThroughQuestCRUD(t *testing.T) {
	a, ws := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{Mode: domain.TaskModePrecise, Goal: "Return function", ResultKind: "code", Criteria: []domain.AcceptanceCriterion{{ID: "c", Kind: "manual", Text: "Function contract"}}}))
	if err != nil {
		t.Fatal(err)
	}
	root := domain.Quest{ID: "root", WorkspaceID: ws.ID, Title: "Root", Brief: &brief, CreatedAt: now, UpdatedAt: now}
	if err := a.store.SaveQuest(ctx, root); err != nil {
		t.Fatal(err)
	}
	child := domain.Quest{ID: "child", ParentID: "root", WorkspaceID: ws.ID, Title: "Stage", Description: "agreed", CreatedAt: now, UpdatedAt: now}
	if _, err := a.SaveQuest(child); err == nil {
		t.Fatal("unplanned child inherited approval")
	}
	if err := a.store.SaveQuest(ctx, child); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveQuest(child); err != nil {
		t.Fatal("unchanged stage rejected", err)
	}
	changed := child
	changed.Description = "unrelated goal"
	if _, err := a.SaveQuest(changed); err == nil {
		t.Fatal("child expanded")
	}
	changed = child
	changed.ParentID = ""
	if _, err := a.SaveQuest(changed); err == nil {
		t.Fatal("child detached from authority")
	}
}

func TestUnknownToolOutcomeDoesNotRetryAutomatically(t *testing.T) {
	for _, message := range []string{"unknown_outcome: result lost", "tool_journal_integrity: request lost", "workspace mutation audit failed after executable tool started", "persist completion evidence: disk failure"} {
		if retryableFlowFailure(domain.RunFailed, message) {
			t.Fatal("unsafe automatic retry", message)
		}
	}
}

func TestFinishedTaskDoesNotTeachUnverifiedDefinitionOfDone(t *testing.T) {
	a, ws := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{Mode: domain.TaskModePrecise, Goal: "Return function", ResultKind: "code", Criteria: []domain.AcceptanceCriterion{{ID: "c", Kind: "manual", Text: "Function contract"}}}))
	if err != nil {
		t.Fatal(err)
	}
	q := domain.Quest{ID: "unverified", WorkspaceID: ws.ID, Title: "Function", Brief: &brief, DefinitionOfDone: []string{"User accepted contract"}, CreatedAt: now, UpdatedAt: now}
	if err = a.store.SaveQuest(ctx, q); err != nil {
		t.Fatal(err)
	}
	a.finalizeQuestAfterFlow(q.ID, true)
	quests, err := a.store.ListQuests(ctx, ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(quests) != 1 || quests[0].Status != domain.QuestNeedsReview || quests[0].FinishedAt == nil {
		t.Fatalf("manual task status = %#v", quests)
	}
	memories, err := a.store.ListMemories(ctx, ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, memory := range memories {
		if memory.Source == "quest-dod" {
			t.Fatal("unverified criteria entered success memory")
		}
	}
}

func TestFinishedTaskWithoutVerificationIsBlocked(t *testing.T) {
	a, ws := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	exit := 0
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModePrecise, Goal: "Return function", ResultKind: "code",
		Criteria: []domain.AcceptanceCriterion{{ID: "verify", Kind: "verification", Text: "Tests pass", Tool: "run_command", Arguments: []byte(`{"command":"go test ./..."}`), ExpectedExitCode: &exit}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	q := domain.Quest{ID: "unverified-machine", WorkspaceID: ws.ID, Title: "Function", Brief: &brief, CreatedAt: now, UpdatedAt: now}
	if err = a.store.SaveQuest(ctx, q); err != nil {
		t.Fatal(err)
	}
	a.finalizeQuestAfterFlow(q.ID, true)
	quests, err := a.store.ListQuests(ctx, ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(quests) != 1 || quests[0].Status != domain.QuestBlocked || quests[0].FinishedAt == nil {
		t.Fatalf("unverified task status = %#v", quests)
	}
}

func TestStageScopedBriefStripsBootstrapVerification(t *testing.T) {
	exit := 0
	brief := domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModeProject, Goal: "Ship", ResultKind: "workspace_change",
		Criteria: []domain.AcceptanceCriterion{{ID: "verify-1", Kind: "verification", Text: "phpunit", Tool: "run_command",
			Arguments: []byte(`{"command":"php bin/phpunit"}`), ExpectedExitCode: &exit}},
		Permissions: domain.TaskPermissions{WriteFiles: true, ExecuteCommands: true},
		Budget:      domain.TaskBudget{Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxAttempts: 1},
	})
	approved, err := domain.ApproveTaskBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	scoped := stageScopedBrief(&approved, domain.StageRoleBootstrap)
	if scoped == nil || len(scoped.Criteria) != 1 || scoped.Criteria[0].Kind != "manual" {
		t.Fatalf("bootstrap criteria=%#v", scoped)
	}
	if !domain.IsTaskBriefApproved(*scoped) {
		t.Fatal("scoped bootstrap brief must remain approved")
	}
	keep := stageScopedBrief(&approved, domain.StageRoleImplement)
	if keep == nil || len(keep.Criteria) != 1 || keep.Criteria[0].Kind != "manual" {
		t.Fatalf("implement must drop machine verification: %#v", keep)
	}
	accept := stageScopedBrief(&approved, domain.StageRoleAccept)
	if accept == nil || len(accept.Criteria) != 1 || accept.Criteria[0].Kind != "verification" {
		t.Fatalf("accept must keep verification: %#v", accept)
	}
}
