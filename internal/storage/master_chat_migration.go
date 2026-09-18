package storage

import (
	"context"
	"database/sql"
	"strings"
)

func migrationMasterConversationsV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE master_conversations (
 workspace_id TEXT NOT NULL, id TEXT NOT NULL, title TEXT NOT NULL,
 archived INTEGER NOT NULL DEFAULT 0, pinned INTEGER NOT NULL DEFAULT 0,
 temporary INTEGER NOT NULL DEFAULT 0, mode TEXT NOT NULL DEFAULT 'auto', work_mode TEXT NOT NULL DEFAULT 'plan',
 model TEXT NOT NULL DEFAULT '', summary TEXT NOT NULL DEFAULT '', parent_id TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL,
 PRIMARY KEY(workspace_id,id));
ALTER TABLE companion_messages ADD COLUMN conversation_id TEXT NOT NULL DEFAULT '';
ALTER TABLE companion_messages ADD COLUMN turn_id TEXT NOT NULL DEFAULT '';
ALTER TABLE companion_messages ADD COLUMN attachments_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE companion_messages ADD COLUMN memory_ids_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE companion_messages ADD COLUMN search_text TEXT NOT NULL DEFAULT '';
ALTER TABLE companion_messages ADD COLUMN clarifications_json TEXT NOT NULL DEFAULT '[]';
UPDATE companion_messages SET conversation_id=CASE WHEN speaker='master' THEN 'legacy' ELSE substr(speaker,8) END,
 turn_id='legacy-'||id, speaker='master' WHERE speaker='master' OR speaker LIKE 'master:%';
INSERT INTO master_conversations(workspace_id,id,title,updated_at)
 SELECT workspace_id,conversation_id,CASE WHEN conversation_id='legacy' THEN 'Первый разговор' ELSE 'Разговор' END,MAX(created_at)
 FROM companion_messages WHERE speaker='master' GROUP BY workspace_id,conversation_id;
INSERT INTO master_conversations(workspace_id,id,title,archived,pinned,mode,updated_at)
 SELECT substr(s.key,17),json_extract(j.value,'$.id'),json_extract(j.value,'$.title'),
 coalesce(json_extract(j.value,'$.archived'),0),coalesce(json_extract(j.value,'$.pinned'),0),coalesce(json_extract(s.value,'$.mode'),'auto'),strftime('%Y-%m-%dT%H:%M:%fZ','now')
 FROM settings s, json_each(s.value,'$.items') j WHERE s.key LIKE 'master.sessions.%' AND json_valid(s.value)
 ON CONFLICT(workspace_id,id) DO UPDATE SET title=excluded.title,archived=excluded.archived,pinned=excluded.pinned,mode=excluded.mode;
CREATE INDEX master_message_page ON companion_messages(workspace_id,conversation_id,created_at,id);
CREATE TABLE master_memory(workspace_id TEXT NOT NULL,id TEXT NOT NULL,content TEXT NOT NULL,source_id TEXT NOT NULL DEFAULT '',status TEXT NOT NULL,updated_at TEXT NOT NULL,PRIMARY KEY(workspace_id,id));
INSERT INTO master_memory(workspace_id,id,content,status,updated_at)
 SELECT substr(key,17),'legacy-memory',json_extract(value,'$.memory'),'accepted',strftime('%Y-%m-%dT%H:%M:%fZ','now')
 FROM settings WHERE key LIKE 'master.sessions.%' AND json_valid(value) AND coalesce(json_extract(value,'$.memory'),'')<>'';
CREATE TABLE master_turns(workspace_id TEXT NOT NULL,id TEXT NOT NULL,conversation_id TEXT NOT NULL,status TEXT NOT NULL,reply TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '',request_hash TEXT NOT NULL,updated_at TEXT NOT NULL,PRIMARY KEY(workspace_id,id));
CREATE UNIQUE INDEX master_one_active_turn ON master_turns(workspace_id,conversation_id) WHERE status IN ('preparing','waiting','streaming','tools');
CREATE TABLE master_turn_events(sequence INTEGER PRIMARY KEY AUTOINCREMENT,workspace_id TEXT NOT NULL,turn_id TEXT NOT NULL,conversation_id TEXT NOT NULL,type TEXT NOT NULL,text TEXT NOT NULL DEFAULT '');
CREATE INDEX master_event_replay ON master_turn_events(workspace_id,turn_id,sequence);
`)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,content FROM companion_messages")
	if err != nil {
		return err
	}
	type item struct{ id, content string }
	items := []item{}
	for rows.Next() {
		var v item
		if err = rows.Scan(&v.id, &v.content); err != nil {
			rows.Close()
			return err
		}
		items = append(items, v)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, v := range items {
		if _, err = tx.ExecContext(ctx, "UPDATE companion_messages SET search_text=? WHERE id=?", strings.ToLower(v.content), v.id); err != nil {
			return err
		}
	}
	return nil
}

// Подробность события живого хода.
//
// Лента Мастера показывала одно слово на всё ожидание: «Изучаю проект…». Что
// именно читалось и чем кончилось, ход рассказывал только задним числом, уже
// готовой репликой. Колонка держит JSON события — аргумент инструмента, его
// ответ, накопленное рассуждение, — а text по-прежнему остаётся короткой
// строкой для сборок, которые о подробностях не знают.
func migrationMasterTurnEventDetailV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `ALTER TABLE master_turn_events ADD COLUMN detail TEXT NOT NULL DEFAULT ''`)
	return err
}
