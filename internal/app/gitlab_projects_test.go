package app

import (
	"context"
	"testing"

	"local-agent-workbench/internal/integrations/gitlab"
)

func connectFakeGitLab(t *testing.T, application *App) {
	t.Helper()
	view, err := application.SaveGitLabPlugin(GitLabPluginUpsert{URL: "https://gitlab.example.test", Token: "glpat-abcdefghijklmnopqrstuvwx"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.TrustMCPServer(view.ID, view.PendingDigest); err != nil {
		t.Fatal(err)
	}
}

func TestGitLabProjectScreens(t *testing.T) {
	application := newTestApp(t)
	useFakeGitLab(t, application)
	connectFakeGitLab(t, application)
	ctx := context.Background()

	list := decodeData[GitLabProjectsView](t, application.GitLabProjects(ctx, "", ""))
	// Папка связана с billing/payments по origin — список отмечает её.
	if list.Scope != gitlab.ProjectsMember || list.Current != "billing/payments" || len(list.Items) != 4 || list.Items[2].SSHURL != "" {
		t.Fatalf("projects: %+v", list)
	}
	for _, bad := range []GitLabResponse{
		application.GitLabProjects(ctx, "", "starred"),
		application.GitLabProjects(ctx, "a\nb", ""),
		application.GitLabProjectDetail(ctx, "../../etc"),
		application.GitLabCommits(ctx, "billing/payments", "-oBad ref", 1),
		application.GitLabCommits(ctx, "billing/payments", "main", 5000),
		application.GitLabCommit(ctx, "billing/payments", "HEAD~1"),
		application.GitLabTree(ctx, "billing/payments", "docs/../../secret", "main"),
		application.GitLabTree(ctx, "billing/payments", "a\x00b", "main"),
	} {
		if bad.Reason != GitLabBadRequest {
			t.Fatalf("bad request accepted: %+v", bad)
		}
	}

	card := decodeData[GitLabProjectCardView](t, application.GitLabProjectDetail(ctx, "billing/payments"))
	if !card.Current || card.Project.HTTPURL == "" || card.Project.DefaultBranch != "main" {
		t.Fatalf("project card: %+v", card)
	}
	commits := decodeData[GitLabCommitsView](t, application.GitLabCommits(ctx, "billing/payments", "main", 0))
	if commits.Page != 1 || commits.More || len(commits.Items) != 3 || commits.Items[0].Stats == nil {
		t.Fatalf("commits: %+v", commits)
	}
	commit := decodeData[struct {
		Commit gitlab.CommitDetail `json:"commit"`
	}](t, application.GitLabCommit(ctx, "billing/payments", fakeGitLabHead))
	if len(commit.Commit.Files) != 3 || commit.Commit.ParentIDs[0] == "" {
		t.Fatalf("commit: %+v", commit)
	}
	branches := decodeData[struct {
		Items []gitlab.Branch `json:"items"`
	}](t, application.GitLabBranches(ctx, "billing/payments", ""))
	if len(branches.Items) != 3 || !branches.Items[0].Default {
		t.Fatalf("branches: %+v", branches)
	}
	tree := decodeData[struct {
		Tree gitlab.Tree `json:"tree"`
	}](t, application.GitLabTree(ctx, "billing/payments", "/", "main"))
	if tree.Tree.Path != "" || len(tree.Tree.Entries) != 6 || tree.Tree.Entries[0].Type != "tree" {
		t.Fatalf("tree: %+v", tree)
	}
}
