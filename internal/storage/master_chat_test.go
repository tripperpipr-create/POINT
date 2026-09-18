package storage

import (
	"context"
	"database/sql"
	"fmt"
	"local-agent-workbench/internal/domain"
	"path/filepath"
	"testing"
	"time"
)

func TestMasterMigrationPreservesLegacySessions(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	_, err = db.Exec(`CREATE TABLE settings(key TEXT PRIMARY KEY,value TEXT);CREATE TABLE companion_messages(id TEXT PRIMARY KEY,workspace_id TEXT,speaker TEXT,content TEXT,created_at TEXT);
 INSERT INTO settings VALUES('master.sessions.ws-a','{"active":"chat-a","mode":"detailed","memory":"Примеры на Go","items":[{"id":"legacy","title":"Старая история"},{"id":"chat-a","title":"Дизайн","pinned":true,"archived":true}]}');
 INSERT INTO companion_messages VALUES('m1','ws-a','master','Прежний ответ','2026-01-01'),('m2','ws-a','master:chat-a','Новый ответ','2026-02-01');`)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = migrationMasterConversationsV1(ctx, tx); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	s := &SQLite{db: db}
	items, err := s.MasterConversations(ctx, "ws-a")
	if err != nil || len(items) != 2 {
		t.Fatalf("conversations: %+v %v", items, err)
	}
	if items[0].ID != "chat-a" || !items[0].Archived || items[0].Mode != "detailed" {
		t.Fatalf("metadata lost: %+v", items)
	}
	var speaker, id, search string
	if err = db.QueryRow(`SELECT speaker,conversation_id,search_text FROM companion_messages WHERE id='m2'`).Scan(&speaker, &id, &search); err != nil {
		t.Fatal(err)
	}
	if speaker != "master" || id != "chat-a" || search != "новый ответ" {
		t.Fatalf("message migration: %s %s %s", speaker, id, search)
	}
	memory, err := s.MasterMemory(ctx, "ws-a")
	if err != nil || len(memory) != 1 || memory[0].Content != "Примеры на Go" {
		t.Fatalf("memory lost: %+v %v", memory, err)
	}
}
func TestMasterPagesTenThousandMessages(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO companion_messages(id,workspace_id,speaker,role,content,level,mode,created_at,conversation_id,search_text) VALUES(?,'w','master','user',?,'','',?,'chat',?)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10000; i++ {
		content := fmt.Sprintf("Сообщение %d", i)
		if _, err = stmt.ExecContext(ctx, fmt.Sprintf("m-%05d", i), content, time.Now().UTC().Format(time.RFC3339Nano), fmt.Sprintf("сообщение %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	stmt.Close()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var before int64
	for {
		page, err := s.MasterMessagePage(ctx, "w", "chat", before, "", 60)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) > 60 {
			t.Fatal("unbounded page")
		}
		for _, m := range page.Items {
			if seen[m.ID] {
				t.Fatal("duplicate page message")
			}
			seen[m.ID] = true
		}
		if !page.HasMore {
			break
		}
		if page.Before >= before && before != 0 {
			t.Fatal("cursor did not advance")
		}
		before = page.Before
	}
	if len(seen) != 10000 {
		t.Fatalf("lost messages: %d", len(seen))
	}
	page, err := s.MasterMessagePage(ctx, "w", "chat", 0, "СООБЩЕНИЕ 9876", 60)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("unicode search: %+v %v", page, err)
	}
	foreign, err := s.MasterMessagePage(ctx, "other", "chat", 0, "", 60)
	if err != nil || len(foreign.Items) > 0 {
		t.Fatal("cross-project leak")
	}
}
func TestMasterMessageAttachmentRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	m := domain.CompanionMessage{ID: "m", WorkspaceID: "w", Speaker: "master", ConversationID: "c", TurnID: "t", Role: "user", Content: "question", CreatedAt: time.Now(), Attachments: []domain.MasterAttachment{{ID: "a", Name: "main.go", Content: "package main", StartLine: 12}}}
	if err = s.SaveCompanionMessage(ctx, m); err != nil {
		t.Fatal(err)
	}
	p, err := s.MasterMessagePage(ctx, "w", "c", 0, "", 60)
	if err != nil || len(p.Items) != 1 || p.Items[0].TurnID != "t" || len(p.Items[0].Attachments) != 1 || p.Items[0].Attachments[0].StartLine != 12 {
		t.Fatalf("snapshot lost: %+v %v", p, err)
	}
}
