package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

// Наряды, утверждённые до git-агента, не должны поменять дайджест.
func TestWorkOrderWithoutGitPlanKeepsItsJSON(t *testing.T) {
	raw, _ := json.Marshal(WorkOrder{})
	if strings.Contains(string(raw), `"git"`) {
		t.Fatalf("empty git plan leaked into JSON: %s", raw)
	}
	raw, _ = json.Marshal(RepositoryCommit{Repo: ".", CommitID: "abc"})
	if strings.Contains(string(raw), "branch") {
		t.Fatalf("empty branch leaked into commit JSON: %s", raw)
	}
}

func TestGitPlanValidationAndApproval(t *testing.T) {
	repo := GitRepoPlan{Path: ".", Current: "main", HeadCommit: "a", CurrentBaseCommit: "a", DefaultBaseCommit: "b"}
	plan := &GitPlan{Version: "1", Choice: GitChoiceRequired, Repositories: []GitRepoPlan{repo}}
	if err := ValidateGitPlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := GitPlanApprovalError(plan); err == nil || !strings.Contains(err.Error(), "выберите ветку") {
		t.Fatalf("required choice must block approval: %v", err)
	}
	plan.Choice, plan.Mode, plan.BaseKind, plan.Branch = GitChoiceChosen, GitModeNew, "default", "feat/x"
	if err := GitPlanApprovalError(plan); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"feat/../x", "feat//x", "-x", "x.lock", "фича/x", "feat/x/"} {
		plan.Branch = bad
		if ValidateGitPlan(plan) == nil {
			t.Fatalf("branch %q accepted", bad)
		}
	}
	plan.Branch = "feat/x"
	plan.Repositories = append(plan.Repositories, GitRepoPlan{Path: "../escape"})
	if ValidateGitPlan(plan) == nil {
		t.Fatal("repository outside the project accepted")
	}
	if !ValidCommitMode(CommitOnRequest) || ValidCommitMode("push") {
		t.Fatal("commit mode validation wrong")
	}
}

func TestQuestGitViewHidesMergeRequestForProtectedBranch(t *testing.T) {
	order := WorkOrder{Delivery: DeliveryPolicy{CommitMode: CommitOnCompletion}, Git: &GitPlan{Mode: GitModeCurrent, Repositories: []GitRepoPlan{
		{Path: ".", Current: "main", DefaultBranch: "develop", Protected: true, GitLab: true},
	}}}
	actions := []QuestGitAction{{Repo: ".", Action: "commit", Phase: "succeeded", CommitID: "c1", Message: "feat: x\n\nbody"}}
	view := BuildQuestGitView(order, QuestCompleted, WorkOrderAssuranceVerified, []string{"a.go"}, actions)
	item := view.Repositories[0]
	if item.CommitSubject != "feat: x" || len(item.Actions) != 1 || item.Actions[0] != "push" {
		t.Fatalf("protected branch view = %+v", item)
	}
	actions = append(actions, QuestGitAction{Repo: ".", Action: "push", Phase: "failed", Error: "auth"})
	if view = BuildQuestGitView(order, QuestCompleted, WorkOrderAssuranceVerified, []string{"a.go"}, actions); view.Repositories[0].LastError != "auth" {
		t.Fatalf("failed push not shown: %+v", view.Repositories[0])
	}
}
