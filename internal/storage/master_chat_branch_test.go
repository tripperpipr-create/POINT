package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestMoveMasterChatToWorktreeKeepsConversationAndConfiguration(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "hub-v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	source := domain.Workspace{ID: "ws-source", Path: filepath.Join(t.TempDir(), "source"), Name: "source", OpenedAt: time.Now().UTC()}
	if err = store.SaveWorkspace(ctx, source); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveMasterConversation(ctx, domain.MasterConversation{ID: "chat-plan", WorkspaceID: source.ID, Title: "Plan", Mode: "auto", WorkMode: "plan", BranchOffer: "pending"}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.ExecContext(ctx, `INSERT INTO companion_messages(id,workspace_id,speaker,role,content,created_at,conversation_id) VALUES('msg-one',?,'user','user','Build',?,'chat-plan')`, source.ID, formatTime(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.ExecContext(ctx, `INSERT INTO orchestrator_config(id,workspace_id,preset,connection_id,provider,provider_preset,base_url,api_version,model,temperature,max_output_tokens,planning_depth,parallelism,approval_strictness,team_preference,created_at,updated_at) VALUES('orchestrator-source',?,'','', 'ollama','','','','model',0,4096,1,1,50,50,?,?)`, source.ID, formatTime(time.Now().UTC()), formatTime(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	plan := storageWorkOrder()
	plan.WorkspaceID = source.ID
	plan.ConversationID = "chat-plan"
	plan, err = store.SaveWorkOrderV2(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.MoveMasterChatToWorktree(ctx, source.ID, "chat-plan", filepath.Join(t.TempDir(), "worktree"), "point/plan", "master", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	items, err := store.MasterConversations(ctx, target.ID)
	if err != nil || len(items) != 1 || items[0].ID != "chat-plan" || items[0].BranchOffer != "bound" || items[0].BranchName != "point/plan" {
		t.Fatalf("moved chat: %#v %v", items, err)
	}
	var count int
	if err = store.db.QueryRowContext(ctx, `SELECT count(*) FROM companion_messages WHERE workspace_id=? AND conversation_id='chat-plan'`, target.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("messages: %d %v", count, err)
	}
	if _, err = store.GetOrchestratorConfig(ctx, target.ID); err != nil {
		t.Fatalf("new world lost master config: %v", err)
	}
	movedPlan, err := store.GetWorkOrderV2(ctx, plan.ID)
	if err != nil || movedPlan.WorkspaceID != target.ID || movedPlan.Version != plan.Version+1 {
		t.Fatalf("plan not moved: %#v %v", movedPlan, err)
	}
	old, err := store.MasterConversations(ctx, source.ID)
	if err != nil || len(old) != 0 {
		t.Fatalf("old chat remained: %#v %v", old, err)
	}
}
