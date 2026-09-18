package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/orchestrator"
	"strings"
	"sync"
	"time"
)

var masterSessionsMu sync.Mutex

type MasterSession = domain.MasterConversation
type MasterSessions struct {
	AutoRunReadOnly bool                       `json:"autoRunReadOnly"`
	Model           string                     `json:"model,omitempty"`
	Active          string                     `json:"active"`
	Items           []MasterSession            `json:"items"`
	Memory          string                     `json:"memory"`
	MemoryEntries   []domain.MasterMemoryEntry `json:"memoryEntries"`
	Mode            string                     `json:"mode"`
	WorkMode        string                     `json:"workMode"`
}
type MasterSessionUpdate struct {
	Action   string `json:"action"`
	ID       string `json:"id"`
	Value    string `json:"value"`
	SourceID string `json:"sourceId,omitempty"`
}

func (a *App) MasterSessions(ctx context.Context) (MasterSessions, error) {
	w := a.currentWorldID()
	items, err := a.store.MasterConversations(ctx, w)
	if err != nil {
		return MasterSessions{}, err
	}
	if len(items) == 0 {
		v := MasterSession{ID: "legacy", WorkspaceID: w, Title: "Первый разговор", Mode: "auto", WorkMode: "plan"}
		if err = a.store.SaveMasterConversation(ctx, v); err != nil {
			return MasterSessions{}, err
		}
		items = []MasterSession{v}
	}
	active, err := a.store.Setting(ctx, "master.active."+w)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return MasterSessions{}, err
	}
	result := MasterSessions{Items: items, Active: items[0].ID, Mode: items[0].Mode, WorkMode: items[0].WorkMode}
	autoRun, _ := a.store.Setting(ctx, "master.auto-run.read-only."+w)
	result.AutoRunReadOnly = autoRun == "true"
	for _, v := range items {
		if v.ID == active {
			result.Active = active
			result.Mode = v.Mode
			result.WorkMode = v.WorkMode
		}
	}
	result.MemoryEntries, err = a.store.MasterMemory(ctx, w)
	if err != nil {
		return result, err
	}
	for _, entry := range result.MemoryEntries {
		if entry.Status == "accepted" {
			if result.Memory != "" {
				result.Memory += "\n"
			}
			result.Memory += entry.Content
		}
	}
	return result, nil
}
func (a *App) UpdateMasterSession(ctx context.Context, req MasterSessionUpdate) (MasterSessions, error) {
	masterSessionsMu.Lock()
	defer masterSessionsMu.Unlock()
	value, err := a.MasterSessions(ctx)
	if err != nil {
		return value, err
	}
	w := a.currentWorldID()
	id := req.ID
	if id == "" {
		id = value.Active
	}
	var current *MasterSession
	for i := range value.Items {
		if value.Items[i].ID == id {
			current = &value.Items[i]
			break
		}
	}
	switch req.Action {
	case "memory-replace":
		err = a.store.ReplaceMasterMemory(ctx, w, req.ID, req.Value)
	case "auto-read-only":
		if req.Value != "true" && req.Value != "false" {
			return value, errors.New("неверное значение автозапуска")
		}
		err = a.store.SaveSetting(ctx, "master.auto-run.read-only."+w, req.Value)
	case "new", "temporary":
		id = domain.NewID("chat")
		if req.Action == "temporary" {
			id = domain.NewID("temporary")
		}
		err = a.store.SaveMasterConversation(ctx, MasterSession{ID: id, WorkspaceID: w, Title: "Новый разговор", Mode: "auto", WorkMode: "plan", Temporary: req.Action == "temporary"})
		if err == nil {
			err = a.store.SaveSetting(ctx, "master.active."+w, id)
		}
	case "select":
		if current == nil {
			return value, errors.New("разговор не найден")
		}
		err = a.store.SaveSetting(ctx, "master.active."+w, id)
	case "rename", "archive", "pin", "mode", "workMode", "model":
		if current == nil {
			return value, errors.New("разговор не найден")
		}
		switch req.Action {
		case "model":
			if len(req.Value) > 160 {
				return value, errors.New("слишком длинное имя модели")
			}
			current.Model = strings.TrimSpace(req.Value)
		case "rename":
			v := strings.TrimSpace(req.Value)
			if v == "" || len([]rune(v)) > 100 {
				return value, errors.New("название: от 1 до 100 символов")
			}
			current.Title = v
		case "archive":
			current.Archived = !current.Archived
		case "pin":
			current.Pinned = !current.Pinned
		case "mode":
			switch req.Value {
			case "auto", "brief", "detailed", "plan", "questions":
				current.Mode = req.Value
			default:
				return value, errors.New("неизвестная форма ответа")
			}
		case "workMode":
			switch req.Value {
			case "discuss", "plan", "execute", "agent":
				current.WorkMode = req.Value
			default:
				return value, errors.New("неизвестный режим работы")
			}
		}
		current.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		err = a.store.SaveMasterConversation(ctx, *current)
	case "delete":
		// Разговор уносит свои наряды, поэтому перед удалением спрашиваем, не
		// ведёт ли какой-то из них живой квест: иначе работа осталась бы без
		// договора, по которому её утверждали.
		if err = a.refuseConversationDeleteWithLiveQuest(ctx, w, id); err == nil {
			err = a.store.DeleteMasterConversation(ctx, w, id)
		}
	case "memory", "memory-propose", "memory-save", "memory-accept", "memory-delete":
		if len([]rune(req.Value)) > 4000 {
			return value, errors.New("память: не более 4000 символов")
		}
		if req.Action == "memory-delete" {
			err = a.store.DeleteMasterMemory(ctx, w, req.ID)
			break
		}
		entry := domain.MasterMemoryEntry{ID: req.ID, Content: strings.TrimSpace(req.Value), SourceID: req.SourceID, Status: "accepted"}
		if req.Action == "memory" {
			entry.ID = "legacy-memory"
		}
		if req.Action == "memory-propose" {
			entry.ID = domain.NewID("memory")
			entry.Status = "proposed"
		}
		if req.Action == "memory-accept" {
			found := false
			for _, v := range value.MemoryEntries {
				if v.ID == req.ID {
					entry = v
					entry.Status = "accepted"
					found = true
					break
				}
			}
			if !found {
				return value, errors.New("запись памяти не найдена")
			}
		}
		if entry.ID == "" {
			entry.ID = domain.NewID("memory")
		}
		if entry.Content == "" {
			err = a.store.DeleteMasterMemory(ctx, w, entry.ID)
		} else {
			err = a.store.SaveMasterMemory(ctx, w, entry)
		}
	default:
		return value, errors.New("неизвестное действие")
	}
	if err != nil {
		return value, err
	}
	return a.MasterSessions(ctx)
}

type masterSessionStore struct {
	orchestrator.ChatStore
	id          string
	turnID      string
	attachments []domain.MasterAttachment
	memoryIDs   []string
	page        func(context.Context, string, string, int64, string, int) (domain.MasterMessagePage, error)
}

func (s masterSessionStore) SaveCompanionMessage(ctx context.Context, m domain.CompanionMessage) error {
	if m.Speaker == "master" {
		m.ConversationID = s.id
		if s.turnID != "" {
			m.TurnID = s.turnID
		}
		if m.Role == "user" {
			m.Attachments = s.attachments
		} else {
			m.MemoryIDs = s.memoryIDs
		}
	}
	return s.ChatStore.SaveCompanionMessage(ctx, m)
}
func (s masterSessionStore) ListChatMessages(ctx context.Context, w, speaker string, limit int) ([]domain.CompanionMessage, error) {
	if speaker != "master" {
		return s.ChatStore.ListChatMessages(ctx, w, speaker, limit)
	}
	p, err := s.page(ctx, w, s.id, 0, "", limit)
	return p.Items, err
}
func (a *App) sessionMasterService(ctx context.Context, s orchestrator.ChatService, id string) (orchestrator.ChatService, MasterSessions, error) {
	sessions, err := a.MasterSessions(ctx)
	if err != nil {
		return s, sessions, err
	}
	if id == "" {
		id = sessions.Active
	}
	for _, v := range sessions.Items {
		if v.ID == id {
			s.Store = masterSessionStore{ChatStore: s.Store, id: id, page: a.store.MasterMessagePage}
			sessions.Active = id
			sessions.Mode = v.Mode
			sessions.WorkMode = v.WorkMode
			sessions.Model = v.Model
			return s, sessions, nil
		}
	}
	return s, sessions, errors.New("разговор не найден")
}
func (a *App) MasterPage(ctx context.Context, id string, before int64, query string) (domain.MasterMessagePage, error) {
	_, sessions, err := a.sessionMasterService(ctx, orchestrator.ChatService{Store: a.store}, id)
	if err != nil {
		return domain.MasterMessagePage{}, err
	}
	return a.store.MasterMessagePage(ctx, a.currentWorldID(), sessions.Active, before, query, 60)
}

// Живой квест держит разговор, в котором утвердили его наряд.
func (a *App) refuseConversationDeleteWithLiveQuest(ctx context.Context, workspaceID, conversationID string) error {
	orders, err := a.store.ListWorkOrdersForConversationV2(ctx, conversationID)
	if err != nil {
		return err
	}
	if len(orders) == 0 {
		return nil
	}
	quests, err := a.store.ListQuests(ctx, workspaceID)
	if err != nil {
		return err
	}
	byID := make(map[string]domain.Quest, len(quests))
	for _, quest := range quests {
		byID[quest.ID] = quest
	}
	for _, order := range orders {
		if order.Runtime == nil || order.Runtime.QuestID == "" {
			continue
		}
		quest, ok := byID[order.Runtime.QuestID]
		if !ok {
			continue
		}
		switch quest.Status {
		case domain.QuestCompleted, domain.QuestNeedsReview, domain.QuestBlocked, domain.QuestFailed, domain.QuestCancelled:
			continue
		}
		return fmt.Errorf("разговор ведёт квест %q — закройте или отмените квест", quest.Title)
	}
	return nil
}
