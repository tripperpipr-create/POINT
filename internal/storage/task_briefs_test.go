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

// An approval digest computed before the brief schema changed used to make
// ListQuests fail, and /api/bootstrap took the whole UI down with it. Such a
// record is now readable and can change status, but stays quarantined.
func TestStoredBriefThatNoLongerValidatesIsQuarantinedNotFatal(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "quarantine.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	approved, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModePrecise, State: "ready", Goal: "Return function", ResultKind: "code",
		Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Contract", Kind: "manual"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	quest := domain.Quest{ID: "q", WorkspaceID: "ws", Title: "Quest", Status: domain.QuestRunning, Brief: &approved, CreatedAt: now, UpdatedAt: now}
	if err = store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.ExecContext(ctx, `UPDATE quests SET brief_json=json_set(brief_json,'$.approvedDigest','digest-from-an-older-schema') WHERE id='q'`); err != nil {
		t.Fatal(err)
	}
	quests, err := store.ListQuests(ctx, "ws")
	if err != nil || len(quests) != 1 {
		t.Fatalf("one stale approval must not fail the list: quests=%d err=%v", len(quests), err)
	}
	stored := quests[0]
	if stored.Brief == nil || stored.Brief.Quarantine == "" {
		t.Fatalf("stale approval was not quarantined: %#v", stored.Brief)
	}
	stored.Status = domain.QuestPaused
	if err = store.SaveQuest(ctx, stored); err != nil {
		t.Fatalf("an untouched quarantined brief must not block a status change: %v", err)
	}
	stored.Brief.Goal = "Smuggled goal"
	if err = store.SaveQuest(ctx, stored); err == nil {
		t.Fatal("a changed quarantined brief was written without validation")
	}
}
