package storage

import (
	"context"
	"local-agent-workbench/internal/domain"
	"path/filepath"
	"testing"
	"time"
)

func TestTaskBriefHistoryAndLegacyUpdates(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "brief.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	b := domain.NormalizeTaskBrief(domain.TaskBrief{Mode: domain.TaskModePrecise, State: "ready", Goal: "Return function", ResultKind: "code", Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Contract", Kind: "manual"}}})
	p := domain.QuestProposal{ID: "p", WorkspaceID: "ws", Title: "Function", Status: "pending", CreatedAt: time.Now().UTC(), Brief: &b}
	if err = store.SaveQuestProposal(ctx, p); err != nil {
		t.Fatal(err)
	}
	b.Goal = "Revised function"
	if err = store.SaveQuestProposal(ctx, p); err == nil {
		t.Fatal("same-version mutation accepted")
	}
	b.Version++
	if err = store.SaveQuestProposal(ctx, p); err != nil {
		t.Fatal(err)
	}
	approved, err := domain.ApproveTaskBrief(b)
	if err != nil {
		t.Fatal(err)
	}
	p.Brief = &approved
	if err = store.SaveQuestProposal(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Brief = nil
	p.Status = "modified"
	if err = store.SaveQuestProposal(ctx, p); err != nil {
		t.Fatal(err)
	}
	revisions, err := store.ListTaskBriefRevisions(ctx, "ws", "p")
	if err != nil || len(revisions) != 2 {
		t.Fatalf("history=%v err=%v", revisions, err)
	}
	if revisions[0].Brief.Goal != "Return function" || revisions[1].Brief.Goal != "Revised function" {
		t.Fatal("history rewritten")
	}
	foreign, err := store.ListTaskBriefRevisions(ctx, "other", "p")
	if err != nil || len(foreign) != 0 {
		t.Fatal("history leaks across workspaces")
	}
	p.WorkspaceID = "other"
	if err = store.SaveQuestProposal(ctx, p); err == nil {
		t.Fatal("foreign ownership accepted")
	}
	if _, err = store.db.ExecContext(ctx, "UPDATE task_brief_revisions SET digest='changed' WHERE proposal_id='p'"); err == nil {
		t.Fatal("immutable history updated")
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	loaded, err := store.ListQuestProposals(ctx, "ws")
	if err != nil || len(loaded) != 1 {
		t.Fatalf("load: %v %v", loaded, err)
	}
	if loaded[0].Brief == nil || !domain.IsTaskBriefApproved(*loaded[0].Brief) {
		t.Fatal("legacy save or restart lost approval")
	}
}
