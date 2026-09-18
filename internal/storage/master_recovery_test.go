package storage

import (
	"context"
	"local-agent-workbench/internal/domain"
	"path/filepath"
	"testing"
)

func TestMasterTemporaryConversationsExpire(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "temporary.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []domain.MasterConversation{{WorkspaceID: "w", ID: "durable", Title: "Keep"}, {WorkspaceID: "w", ID: "temporary-1", Title: "Temporary", Temporary: true}} {
		if err = s.SaveMasterConversation(ctx, v); err != nil {
			t.Fatal(err)
		}
		if err = s.SaveCompanionMessage(ctx, domain.CompanionMessage{ID: v.ID, WorkspaceID: "w", ConversationID: v.ID, Speaker: "master", Role: "user", Content: "Message"}); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	conversations, err := s.MasterConversations(ctx, "w")
	if err != nil || len(conversations) != 1 || conversations[0].ID != "durable" {
		t.Fatalf("temporary retention: %+v %v", conversations, err)
	}
	page, err := s.MasterMessagePage(ctx, "w", "temporary-1", 0, "", 60)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("temporary messages survived: %+v %v", page, err)
	}
}

func TestMasterTurnPersistsWorkOrderLinkV2(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "master-work-order.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	turn := domain.MasterTurn{
		ID: "turn-work-order", ConversationID: "conversation", WorkspaceID: "workspace",
		WorkOrderID: "workorder-proposal", Status: "completed", Reply: "Готово",
		RequestHash: "hash",
	}
	if err := store.SaveMasterTurn(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	stored, err := store.MasterTurn(context.Background(), turn.WorkspaceID, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.WorkOrderID != turn.WorkOrderID {
		t.Fatalf("work order link was not persisted: %#v", stored)
	}
}

func TestMasterInterruptedTurnRecoveryAndReplay(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "recovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	turn := domain.MasterTurn{ID: "turn", WorkspaceID: "project", ConversationID: "chat", Status: "streaming", Reply: "Частичный ответ", RequestHash: "hash"}
	if err = s.SaveMasterTurn(ctx, turn); err != nil {
		t.Fatal(err)
	}
	first, err := s.AppendMasterEvent(ctx, "project", domain.MasterTurnEvent{TurnID: turn.ID, ConversationID: turn.ConversationID, Type: "reply", Text: turn.Reply})
	if err != nil {
		t.Fatal(err)
	}
	// Подробность события переживает запись и переигровку: без неё лента
	// показала бы, что Мастер к чему-то обратился, но не к чему именно.
	detailed, err := s.AppendMasterEvent(ctx, "project", domain.MasterTurnEvent{TurnID: turn.ID, ConversationID: turn.ConversationID, Type: "tools", Text: "read_file", Detail: `{"tool":"read_file","argument":"internal/app/app.go"}`})
	if err != nil {
		t.Fatal(err)
	}
	if replayed, e := s.MasterEvents(ctx, "project", "turn", first.Sequence); e != nil || len(replayed) != 1 || replayed[0].Detail != detailed.Detail {
		t.Fatalf("подробность события потеряна: %+v %v", replayed, e)
	}
	if err = s.InterruptMasterTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.InterruptMasterTurns(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := s.MasterMessagePage(ctx, "project", "chat", 0, "", 60)
	if err != nil || len(page.Items) != 1 || page.Items[0].Content != turn.Reply || page.Items[0].Mode != "interrupted" {
		t.Fatalf("recovery: %+v %v", page, err)
	}
	events, err := s.MasterEvents(ctx, "project", "turn", detailed.Sequence)
	if err != nil || len(events) != 1 || events[0].Text != "interrupted" {
		t.Fatalf("replay: %+v %v", events, err)
	}
	other, err := s.MasterEvents(ctx, "another-project", "turn", 0)
	if err != nil || len(other) != 0 {
		t.Fatalf("workspace isolation: %+v %v", other, err)
	}
	turn.ID = "next"
	if err = s.SaveMasterTurn(ctx, turn); err != nil {
		t.Fatalf("interrupted turn blocks next: %v", err)
	}
	turn.ID = "duplicate-active"
	if err = s.SaveMasterTurn(ctx, turn); err == nil {
		t.Fatal("allowed two active turns in one conversation")
	}
	turn.ConversationID = "independent-chat"
	if err = s.SaveMasterTurn(ctx, turn); err != nil {
		t.Fatalf("independent conversation blocked: %v", err)
	}
}
