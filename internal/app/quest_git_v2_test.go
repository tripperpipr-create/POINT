package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/gitflow"
)

func gitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitflow.ExecRunner{}.Run(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// questGitRepo — проект на main с origin: как lk-backend, но в TempDir.
func questGitRepo(t *testing.T) string {
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	gitTest(t, root, "init", "--bare", "-q", "-b", "main", origin)
	work := filepath.Join(root, "work")
	gitTest(t, root, "clone", "-q", origin, work)
	gitTest(t, work, "config", "user.name", "Анна Тестова")
	gitTest(t, work, "config", "user.email", "anna@example.test")
	gitTest(t, work, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(work, "app.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTest(t, work, "add", "-A")
	gitTest(t, work, "commit", "-q", "-m", "fix: init")
	gitTest(t, work, "push", "-q", "-u", "origin", "main")
	return work
}

// Инцидент 02.10 в обратную сторону: ветка выбрана до работы, файлы квеста
// лежат без коммита до вердикта, коммит — от имени человека и только файлов
// квеста, отправка — явным действием, откат возвращает проект.
func TestQuestGitBranchCommitPushAndRevert(t *testing.T) {
	ctx := context.Background()
	application := newTestApp(t)
	work := questGitRepo(t)
	view, err := application.OpenWorkspace(work)
	if err != nil {
		t.Fatal(err)
	}
	order := domain.WorkOrder{ID: "workorder-git", WorkspaceID: view.Workspace.ID, Goal: "Новый эндпоинт GET /flag/get-flag", State: "ready",
		Workspace: domain.WorkspacePlan{Mode: "existing", Path: work}, Delivery: domain.DeliveryPolicy{CommitMode: "none"}}
	plan, mode := application.masterWorkOrderGitV2(ctx, order, nil, domain.OrchestratorConfig{}, "")
	if plan == nil || mode != domain.CommitOnCompletion || plan.Choice != domain.GitChoiceProposed || plan.Mode != domain.GitModeNew || plan.Branch != "feat/novyy-endpoint-get-flag-get-flag" {
		t.Fatalf("protected main must get a proposed branch by the template: %+v mode=%s", plan, mode)
	}
	order.Git, order.Delivery.CommitMode = plan, mode
	if again, _ := application.masterWorkOrderGitV2(ctx, order, &order, domain.OrchestratorConfig{}, ""); again == nil || again.Branch != plan.Branch {
		t.Fatalf("unchanged repository must keep the plan: %+v", again)
	}
	checkouts, err := application.checkoutWorkOrderGitV2(ctx, order)
	if err != nil || len(checkouts) != 1 || !checkouts[0].Created {
		t.Fatalf("checkout: %+v %v", checkouts, err)
	}
	if branch := gitTest(t, work, "branch", "--show-current"); branch != plan.Branch {
		t.Fatalf("approval left branch %s", branch)
	}
	if err = application.verifyWorkOrderBranchV2(ctx, order); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	root := domain.Quest{ID: "quest-git", WorkspaceID: view.Workspace.ID, Title: "Flag", Kind: "project", Status: domain.QuestApplying, CreatedAt: now, UpdatedAt: now}
	if err = application.store.SaveQuest(ctx, root); err != nil {
		t.Fatal(err)
	}
	content := "export const flag = true\n"
	set := domain.ChangeSet{ID: "set-git", WorkspaceID: view.Workspace.ID, QuestID: root.ID, Title: "flag", Status: domain.ChangeSetPending, CreatedAt: now, UpdatedAt: now,
		Items: []domain.ChangeItem{{ID: "flag", Path: "flag.ts", Kind: "create", ProposedContent: content, ProposedHash: deliveryHashV2(content)}}}
	if err = application.store.SaveChangeSet(ctx, set); err != nil {
		t.Fatal(err)
	}
	changed, commits, err := application.applyWorkOrderChangeSetsV2(ctx, order, root, "evidence-git")
	if err != nil || len(commits) != 0 || len(changed) != 1 {
		t.Fatalf("delivery must not commit by itself: changed=%v commits=%v err=%v", changed, commits, err)
	}
	if count := gitTest(t, work, "rev-list", "--count", "HEAD"); count != "1" {
		t.Fatalf("commit appeared before the verdict: %s", count)
	}
	if err = os.WriteFile(filepath.Join(work, "mine.txt"), []byte("human draft\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	state := questGitContext{
		approval: domain.WorkOrderApproval{QuestID: root.ID, WorkOrder: order}, order: order, status: domain.QuestCompleted,
		bundle: domain.EvidenceBundle{ID: "evidence-git", Assurance: domain.WorkOrderAssuranceVerified, ChangedFiles: changed},
		dirs:   map[string]string{".": work},
	}
	state.view = domain.BuildQuestGitView(order, domain.QuestCompleted, domain.WorkOrderAssuranceVerified, changed, nil)
	item := state.view.Repositories[0]
	if !containsString(item.Actions, "commit") || !containsString(item.Actions, "revert") || containsString(item.Actions, "push") {
		t.Fatalf("uncommitted result actions = %v", item.Actions)
	}
	if _, err = application.commitQuestRepoV2(ctx, state, item, "auto", ""); err != nil {
		t.Fatal(err)
	}
	if author := gitTest(t, work, "log", "-1", "--format=%an <%ae>"); author != "Анна Тестова <anna@example.test>" {
		t.Fatalf("commit author = %s", author)
	}
	message := gitTest(t, work, "log", "-1", "--format=%B")
	if !strings.HasPrefix(message, "feat: новый эндпоинт GET /flag/get-flag") || !strings.Contains(message, "Point-Quest: quest-git") {
		t.Fatalf("commit message = %q", message)
	}
	if files := gitTest(t, work, "show", "--name-only", "--format=", "HEAD"); files != "flag.ts" {
		t.Fatalf("commit took foreign files: %q", files)
	}
	actions, _ := application.store.ListQuestGitActions(ctx, root.ID)
	state.view = domain.BuildQuestGitView(order, domain.QuestCompleted, domain.WorkOrderAssuranceVerified, changed, actions)
	item = state.view.Repositories[0]
	if !containsString(item.Actions, "push") || containsString(item.Actions, "commit") || containsString(item.Actions, "revert") || item.Target != "main" {
		t.Fatalf("committed result actions = %+v", item)
	}
	if _, err = application.pushQuestRepoV2(ctx, state, item, "button", false); err != nil {
		t.Fatal(err)
	}
	if remote := gitTest(t, work, "ls-remote", "origin", "refs/heads/"+plan.Branch); !strings.HasPrefix(remote, item.CommitID) {
		t.Fatalf("branch not on origin: %q", remote)
	}
	actions, _ = application.store.ListQuestGitActions(ctx, root.ID)
	if view := domain.BuildQuestGitView(order, domain.QuestCompleted, domain.WorkOrderAssuranceVerified, changed, actions); !view.Repositories[0].Pushed || len(view.Repositories[0].Actions) != 0 {
		t.Fatalf("pushed result = %+v", view.Repositories[0])
	}

	gitTest(t, work, "switch", "-q", "main")
	if err = application.verifyWorkOrderBranchV2(ctx, order); err == nil || !strings.Contains(err.Error(), "переключитесь обратно") {
		t.Fatalf("launch guard must notice a foreign branch: %v", err)
	}
}

func TestQuestGitRevertReturnsProjectBeforeQuest(t *testing.T) {
	ctx := context.Background()
	application := newTestApp(t)
	work := questGitRepo(t)
	view, err := application.OpenWorkspace(work)
	if err != nil {
		t.Fatal(err)
	}
	order := domain.WorkOrder{ID: "workorder-revert", WorkspaceID: view.Workspace.ID, Goal: "Исправить отчёт", State: "ready",
		Workspace: domain.WorkspacePlan{Mode: "existing", Path: work}}
	order.Git, order.Delivery.CommitMode = application.masterWorkOrderGitV2(ctx, order, nil, domain.OrchestratorConfig{}, "")
	if order.Git.Branch != "fix/ispravit-otchet" {
		t.Fatalf("fix branch = %s", order.Git.Branch)
	}
	now := time.Now().UTC()
	root := domain.Quest{ID: "quest-revert", WorkspaceID: view.Workspace.ID, Title: "Revert", Kind: "project", Status: domain.QuestApplying, CreatedAt: now, UpdatedAt: now}
	if err = application.store.SaveQuest(ctx, root); err != nil {
		t.Fatal(err)
	}
	proposed := "v2\n"
	set := domain.ChangeSet{ID: "set-revert", WorkspaceID: view.Workspace.ID, QuestID: root.ID, Title: "edit", Status: domain.ChangeSetPending, CreatedAt: now, UpdatedAt: now,
		Items: []domain.ChangeItem{{ID: "edit", Path: "app.txt", Kind: "modify", OriginalContent: "v1\n", OriginalHash: deliveryHashV2("v1\n"), ProposedContent: proposed, ProposedHash: deliveryHashV2(proposed)}}}
	if err = application.store.SaveChangeSet(ctx, set); err != nil {
		t.Fatal(err)
	}
	if _, _, err = application.applyWorkOrderChangeSetsV2(ctx, order, root, "evidence"); err != nil {
		t.Fatal(err)
	}
	state := questGitContext{approval: domain.WorkOrderApproval{QuestID: root.ID, WorkOrder: order}, order: order, status: domain.QuestBlocked,
		bundle: domain.EvidenceBundle{ID: "evidence", ChangedFiles: []string{"app.txt"}}, dirs: map[string]string{".": work}}
	state.view = domain.BuildQuestGitView(order, domain.QuestBlocked, "", state.bundle.ChangedFiles, nil)
	if actions := state.view.Repositories[0].Actions; len(actions) != 1 || actions[0] != "revert" {
		t.Fatalf("blocked quest offers %v", actions)
	}
	if err = os.WriteFile(filepath.Join(work, "app.txt"), []byte("human edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = application.revertQuestDeliveryV2(ctx, state, "button"); err == nil || !strings.Contains(err.Error(), "изменён после квеста") {
		t.Fatalf("revert must stop before touching human edits: %v", err)
	}
	if err = os.WriteFile(filepath.Join(work, "app.txt"), []byte(proposed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = application.revertQuestDeliveryV2(ctx, state, "button"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(work, "app.txt")); string(data) != "v1\n" {
		t.Fatalf("revert left %q", data)
	}
}

func TestDirtyProjectGetsNoBranchSwitchAndNoCommit(t *testing.T) {
	application := newTestApp(t)
	work := questGitRepo(t)
	if err := os.WriteFile(filepath.Join(work, "draft.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	order := domain.WorkOrder{Goal: "x", State: "ready", Workspace: domain.WorkspacePlan{Mode: "existing", Path: work}}
	plan, mode := application.masterWorkOrderGitV2(context.Background(), order, nil, domain.OrchestratorConfig{}, "")
	if plan == nil || plan.Mode != domain.GitModeNone || mode != "none" {
		t.Fatalf("dirty plan = %+v mode=%s", plan, mode)
	}
	if _, err := application.SaveQuestGitPolicy(context.Background(), QuestGitPolicy{CommitPolicy: "onRequest"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(work, "draft.txt")); err != nil {
		t.Fatal(err)
	}
	if _, mode = application.masterWorkOrderGitV2(context.Background(), order, nil, domain.OrchestratorConfig{}, ""); mode != domain.CommitOnRequest {
		t.Fatalf("on-request policy mode = %s", mode)
	}
}
