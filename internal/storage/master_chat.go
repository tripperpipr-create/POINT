package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"local-agent-workbench/internal/domain"
	"strings"
	"time"
)

func (s *SQLite) MasterConversations(ctx context.Context, w string) ([]domain.MasterConversation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,title,archived,pinned,temporary,mode,work_mode,summary,parent_id,updated_at,model FROM master_conversations WHERE workspace_id=? ORDER BY pinned DESC,updated_at DESC,id`, w)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.MasterConversation{}
	for rows.Next() {
		v := domain.MasterConversation{WorkspaceID: w}
		if err = rows.Scan(&v.ID, &v.Title, &v.Archived, &v.Pinned, &v.Temporary, &v.Mode, &v.WorkMode, &v.Summary, &v.ParentID, &v.UpdatedAt, &v.Model); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *SQLite) SaveMasterConversation(ctx context.Context, v domain.MasterConversation) error {
	if v.UpdatedAt == "" {
		v.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if v.Mode == "" {
		v.Mode = "auto"
	}
	if v.WorkMode == "" {
		v.WorkMode = "plan"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO master_conversations(workspace_id,id,title,archived,pinned,temporary,mode,work_mode,summary,parent_id,updated_at,model) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(workspace_id,id) DO UPDATE SET title=excluded.title,archived=excluded.archived,pinned=excluded.pinned,mode=excluded.mode,work_mode=excluded.work_mode,summary=excluded.summary,updated_at=excluded.updated_at,model=excluded.model`, v.WorkspaceID, v.ID, v.Title, v.Archived, v.Pinned, v.Temporary, v.Mode, v.WorkMode, v.Summary, v.ParentID, v.UpdatedAt, v.Model)
	return err
}
func (s *SQLite) DeleteMasterConversation(ctx context.Context, w, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var active int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM master_turns WHERE workspace_id=? AND conversation_id=? AND status IN ('preparing','waiting','streaming','tools')`, w, id).Scan(&active)
	if err != nil {
		return err
	}
	if active > 0 {
		return errors.New("сначала остановите ответ")
	}
	for _, table := range []string{"master_turn_events", "master_turns", "companion_messages"} {
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE workspace_id=? AND conversation_id=?`, w, id); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM master_conversations WHERE workspace_id=? AND id=?`, w, id); err != nil {
		return err
	}
	// Наряды разговора уходят вместе с ним.
	//
	// Карточка запуска принадлежит переписке, в которой её собрали: лента
	// показывает наряды активного разговора. Пока удаление чата их не трогало,
	// оставались двое: наряд удалённого чата — сиротой, которого не видно
	// нигде, и наряд разговора `legacy` — всплывающим в пустом чате, куда
	// человек попадает после удаления всех остальных.
	if err = deleteWorkOrdersOfConversationTx(ctx, tx, w, id); err != nil {
		return err
	}
	return tx.Commit()
}

// Наряд разложен по шести таблицам, и conversationId живёт внутри payload, а не
// колонкой: выбираем кандидатов и разбираем JSON здесь, тем же способом, что и
// чтение ленты — json_extract потребовал бы гарантий сборки SQLite с json1.
func deleteWorkOrdersOfConversationTx(ctx context.Context, tx *sql.Tx, workspaceID, conversationID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, payload_json FROM work_order_current_v2 WHERE workspace_id=?`, workspaceID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id, payload string
		if err = rows.Scan(&id, &payload); err != nil {
			rows.Close()
			return err
		}
		var order domain.WorkOrder
		if json.Unmarshal([]byte(payload), &order) != nil || order.ConversationID != conversationID {
			continue
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err = deleteWorkOrderRowsTx(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

// Удаляется действующая карточка наряда — и только она.
//
// Версии наряда, их diff-ы, расписки утверждения и события управления квестом
// неизменяемы по договору v2: на трёх из этих таблиц стоят триггеры, которые
// отменяют любое DELETE. Так и надо: договор, который человек утвердил, —
// доказательство сделанного выбора, и стирать его вместе с карточкой значило бы
// переписывать историю проекта ради уборки ленты. Из ленты наряд читается по
// work_order_current_v2, поэтому убирается он оттуда.
//
// Список один на оба пути удаления — чата целиком и одного наряда, — чтобы
// новая таблица не осталась только в одном из них.
func deleteWorkOrderRowsTx(ctx context.Context, tx *sql.Tx, workOrderID string) error {
	for _, statement := range []string{
		`DELETE FROM work_order_current_v2 WHERE id=?`,
		`DELETE FROM work_order_revision_idempotency_v2 WHERE work_order_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, workOrderID); err != nil {
			return err
		}
	}
	return nil
}
func (s *SQLite) MasterMemory(ctx context.Context, w string) ([]domain.MasterMemoryEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,content,source_id,status,updated_at FROM master_memory WHERE workspace_id=? ORDER BY updated_at,id`, w)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.MasterMemoryEntry{}
	for rows.Next() {
		var v domain.MasterMemoryEntry
		if err = rows.Scan(&v.ID, &v.Content, &v.SourceID, &v.Status, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *SQLite) SaveMasterMemory(ctx context.Context, w string, v domain.MasterMemoryEntry) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO master_memory(workspace_id,id,content,source_id,status,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(workspace_id,id) DO UPDATE SET content=excluded.content,status=excluded.status,updated_at=excluded.updated_at`, w, v.ID, v.Content, v.SourceID, v.Status, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (s *SQLite) DeleteMasterMemory(ctx context.Context, w, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM master_memory WHERE workspace_id=? AND id=?`, w, id)
	return err
}

const masterMessageColumns = `rowid,id,workspace_id,speaker,role,content,level,mode,provider,model,facts_used_json,questions_json,usage_record_id,proposal_id,action_proposal_id,fallback_reason,input_tokens,output_tokens,total_tokens,latency_ms,feedback,reasoning,steps_json,created_at,conversation_id,turn_id,attachments_json,memory_ids_json,clarifications_json`

func (s *SQLite) MasterMessagePage(ctx context.Context, w, id string, before int64, query string, limit int) (domain.MasterMessagePage, error) {
	if limit < 1 || limit > 200 {
		limit = 60
	}
	if before <= 0 {
		before = 9223372036854775807
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+masterMessageColumns+` FROM companion_messages WHERE workspace_id=? AND conversation_id=? AND rowid<? AND (?='' OR instr(search_text,?)>0) ORDER BY rowid DESC LIMIT ?`, w, id, before, query, strings.ToLower(query), limit+1)
	if err != nil {
		return domain.MasterMessagePage{}, err
	}
	defer rows.Close()
	page := domain.MasterMessagePage{Items: []domain.CompanionMessage{}}
	sequences := []int64{}
	for rows.Next() {
		var m domain.CompanionMessage
		var seq int64
		var facts, questions, steps, created, attachments, memories, clarifications string
		if err = rows.Scan(&seq, &m.ID, &m.WorkspaceID, &m.Speaker, &m.Role, &m.Content, &m.Level, &m.Mode, &m.Provider, &m.Model, &facts, &questions, &m.UsageRecordID, &m.ProposalID, &m.ActionProposalID, &m.FallbackReason, &m.InputTokens, &m.OutputTokens, &m.TotalTokens, &m.LatencyMs, &m.Feedback, &m.Reasoning, &steps, &created, &m.ConversationID, &m.TurnID, &attachments, &memories, &clarifications); err != nil {
			return page, err
		}
		unmarshalJSON(facts, &m.FactsUsed)
		unmarshalJSON(questions, &m.Questions)
		unmarshalJSON(steps, &m.Steps)
		unmarshalJSON(attachments, &m.Attachments)
		unmarshalJSON(memories, &m.MemoryIDs)
		unmarshalJSON(clarifications, &m.Clarifications)
		m.CreatedAt = parseTime(created)
		page.Items = append(page.Items, m)
		sequences = append(sequences, seq)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > limit {
		page.HasMore = true
		page.Items = page.Items[:limit]
	}
	if len(page.Items) > 0 {
		page.Before = sequences[len(page.Items)-1]
	}
	for i, j := 0, len(page.Items)-1; i < j; i, j = i+1, j-1 {
		page.Items[i], page.Items[j] = page.Items[j], page.Items[i]
	}
	return page, nil
}
func (s *SQLite) SaveMasterTurn(ctx context.Context, t domain.MasterTurn) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO master_turns(workspace_id,id,conversation_id,work_order_id,status,reply,error,request_hash,updated_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(workspace_id,id) DO UPDATE SET work_order_id=excluded.work_order_id,status=excluded.status,reply=excluded.reply,error=excluded.error,updated_at=excluded.updated_at`, t.WorkspaceID, t.ID, t.ConversationID, t.WorkOrderID, t.Status, t.Reply, t.Error, t.RequestHash, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (s *SQLite) MasterTurn(ctx context.Context, w, id string) (domain.MasterTurn, error) {
	v := domain.MasterTurn{ID: id, WorkspaceID: w}
	err := s.db.QueryRowContext(ctx, `SELECT conversation_id,work_order_id,status,reply,error,request_hash,updated_at FROM master_turns WHERE workspace_id=? AND id=?`, w, id).Scan(&v.ConversationID, &v.WorkOrderID, &v.Status, &v.Reply, &v.Error, &v.RequestHash, &v.UpdatedAt)
	return v, err
}
func (s *SQLite) MasterActiveTurns(ctx context.Context, w string) ([]domain.MasterTurn, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,conversation_id,work_order_id,status,reply,error,updated_at FROM master_turns WHERE workspace_id=? AND status IN ('preparing','waiting','streaming','tools')`, w)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.MasterTurn{}
	for rows.Next() {
		v := domain.MasterTurn{WorkspaceID: w}
		if err = rows.Scan(&v.ID, &v.ConversationID, &v.WorkOrderID, &v.Status, &v.Reply, &v.Error, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *SQLite) AppendMasterEvent(ctx context.Context, w string, e domain.MasterTurnEvent) (domain.MasterTurnEvent, error) {
	r, err := s.db.ExecContext(ctx, `INSERT INTO master_turn_events(workspace_id,turn_id,conversation_id,type,text,detail) VALUES(?,?,?,?,?,?)`, w, e.TurnID, e.ConversationID, e.Type, e.Text, e.Detail)
	if err == nil {
		e.Sequence, err = r.LastInsertId()
	}
	return e, err
}
func (s *SQLite) MasterEvents(ctx context.Context, w, id string, after int64) ([]domain.MasterTurnEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT sequence,turn_id,conversation_id,type,text,detail FROM master_turn_events WHERE workspace_id=? AND turn_id=? AND sequence>? ORDER BY sequence LIMIT 256`, w, id, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.MasterTurnEvent{}
	for rows.Next() {
		var e domain.MasterTurnEvent
		if err = rows.Scan(&e.Sequence, &e.TurnID, &e.ConversationID, &e.Type, &e.Text, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *SQLite) InterruptMasterTurns(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// A crash can happen after a fragment was committed but before the final message.
	_, err = tx.ExecContext(ctx, `INSERT INTO companion_messages(id,workspace_id,speaker,role,content,mode,fallback_reason,created_at,conversation_id,turn_id,search_text)
 SELECT 'recovered-'||t.workspace_id||'-'||t.id,t.workspace_id,'master','assistant',t.reply,'interrupted','Ядро было перезапущено. Частичный ответ сохранён.',t.updated_at,t.conversation_id,t.id,lower(t.reply)
 FROM master_turns t WHERE t.status IN ('preparing','waiting','streaming','tools') AND NOT EXISTS
 (SELECT 1 FROM companion_messages m WHERE m.workspace_id=t.workspace_id AND m.turn_id=t.id AND m.role='assistant')`)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO master_turn_events(workspace_id,turn_id,conversation_id,type,text) SELECT workspace_id,id,conversation_id,'done','interrupted' FROM master_turns WHERE status IN ('preparing','waiting','streaming','tools')`)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE master_turns SET status='interrupted',error='Ядро было перезапущено. Частичный ответ сохранён.' WHERE status IN ('preparing','waiting','streaming','tools')`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
