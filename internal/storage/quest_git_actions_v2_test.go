package storage

import (
	"context"
	"path/filepath"
	"testing"

	"local-agent-workbench/internal/domain"
)

func TestQuestGitActionsAreAppendOnly(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	saved, err := store.AppendQuestGitAction(ctx, domain.QuestGitAction{QuestID: "q", Repo: ".", Action: "commit", Phase: "succeeded", CommitID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.ExecContext(ctx, `UPDATE quest_git_actions_v2 SET commit_id='x' WHERE id=?`, saved.ID); err == nil {
		t.Fatal("git action journal must be immutable")
	}
	if _, err = store.AppendQuestGitAction(ctx, domain.QuestGitAction{QuestID: "q", Repo: ".", Action: "force_push", Phase: "succeeded"}); err == nil {
		t.Fatal("unknown action accepted")
	}
	items, err := store.ListQuestGitActions(ctx, "q")
	if err != nil || len(items) != 1 || items[0].CommitID != "c1" {
		t.Fatalf("items = %+v %v", items, err)
	}
	proposal := domain.CompanionActionProposal{ID: "p", WorkspaceID: "ws", Kind: domain.CompanionActionGit, Title: "Отправить?", Status: "pending",
		Git: &domain.GitActionRequest{QuestID: "q", Action: "push"}}
	if err = store.SaveCompanionActionProposal(ctx, proposal); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.ListCompanionActionProposals(ctx, "ws")
	if err != nil || len(loaded) != 1 || loaded[0].Git == nil || loaded[0].Git.Action != "push" {
		t.Fatalf("git proposal round trip = %+v %v", loaded, err)
	}
}
