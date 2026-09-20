package agent

import (
	"testing"

	"local-agent-workbench/internal/domain"
)

func TestTaskAutoApprovedFastAgentProposePatch(t *testing.T) {
	engine := &Engine{}
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		SourceRequest: "edit file",
		Mode:          domain.TaskModePrecise,
		State:         "ready",
		Goal:          "edit file",
		ResultKind:    "workspace_change",
		Scope:         []string{"one file"},
		Criteria:      []domain.AcceptanceCriterion{{ID: "done", Text: "done", Kind: "manual"}},
		Permissions:   domain.TaskPermissions{WriteFiles: true, ExecuteCommands: true},
		Budget:        domain.TaskBudget{Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxReplans: 1, MaxAttempts: 1},
		FastAgent:     true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	active := &activeRun{taskBrief: &brief}
	profile := domain.DefaultProfile()
	profile.ApprovalMode = domain.ApprovalSafe
	if !engine.taskAutoApproved(active, profile, "propose_patch") {
		t.Fatal("fast agent precise write must auto-approve propose_patch")
	}
	if engine.taskAutoApproved(active, profile, "run_command") {
		t.Fatal("fast agent must still ask for run_command")
	}
	profile.ApprovalMode = domain.ApprovalAlways
	if engine.taskAutoApproved(active, profile, "propose_patch") {
		t.Fatal("ApprovalAlways must still require clicks")
	}
}

func TestTaskAutoApprovedFastAgentWorkOrderKeepsCommandsManual(t *testing.T) {
	engine := &Engine{}
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		SourceRequest: "edit file", Mode: domain.TaskModeProject, State: "ready", Goal: "edit file",
		ResultKind: "workspace_change", Scope: []string{"one file"},
		Criteria:    []domain.AcceptanceCriterion{{ID: "done", Text: "done", Kind: "manual"}},
		Permissions: domain.TaskPermissions{WriteFiles: true, ExecuteCommands: true},
		Budget:      domain.TaskBudget{Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxReplans: 1, MaxAttempts: 1},
		FastAgent:   true, WorkOrder: &domain.WorkOrderExecutionContract{ID: "work-order", Version: 1, Digest: "sha256:test"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	active := &activeRun{taskBrief: &brief}
	profile := domain.DefaultProfile()
	profile.ApprovalMode = domain.ApprovalSafe
	if !engine.taskAutoApproved(active, profile, "propose_patch") {
		t.Fatal("fast-agent WorkOrder must auto-approve file writes")
	}
	if engine.taskAutoApproved(active, profile, "run_command") {
		t.Fatal("fast-agent WorkOrder must keep commands behind approval")
	}
}
