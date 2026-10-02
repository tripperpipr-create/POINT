package app

import (
	"context"
	"errors"
	"local-agent-workbench/internal/domain"
	"strings"
	"time"
)

// Delete a chat's history only after its workers stop. Never undo host files.
func (a *App) deletePointConversation(ctx context.Context, w, id string) (MasterSessions, error) {
	turns, err := a.store.MasterActiveTurns(ctx, w)
	if err != nil {
		return MasterSessions{}, err
	}
	for _, turn := range turns {
		if turn.ConversationID != id {
			continue
		}
		key := w + "/" + turn.ID
		a.masterTurnsMu.Lock()
		cancel := a.masterTurnCancels[key]
		var signal chan struct{}
		if cancel != nil {
			cancel()
			if a.masterTurnSignals == nil {
				a.masterTurnSignals = map[string]chan struct{}{}
			}
			signal = a.masterTurnSignals[key]
			if signal == nil {
				signal = make(chan struct{})
				a.masterTurnSignals[key] = signal
			}
		}
		a.masterTurnsMu.Unlock()
		if signal != nil {
			select {
			case <-signal:
			case <-ctx.Done():
				return MasterSessions{}, ctx.Err()
			case <-time.After(10 * time.Second):
				return MasterSessions{}, errors.New("master worker has not stopped yet")
			}
		} else {
			unlock, e := scopeFileLock(ctx.Value(masterScopeKey{}).(masterScope).Workspace, "master-turns")
			if e != nil {
				return MasterSessions{}, errors.New("another core is still answering in this chat")
			}
			e = a.store.RecoverPointMasterTurns(ctx, w)
			unlock()
			if e != nil {
				return MasterSessions{}, e
			}
		}
	}
	orders, err := a.store.ListWorkOrdersForConversationV2(ctx, w, id)
	if err != nil {
		return MasterSessions{}, err
	}
	executions, err := a.store.ListExecutions(ctx, w, 10000)
	if err != nil {
		return MasterSessions{}, err
	}
	for _, order := range orders {
		if order.Runtime == nil {
			continue
		}
		quest := order.Runtime.QuestID
		if _, err = a.stopQuestWork(ctx, w, quest); err != nil {
			return MasterSessions{}, err
		}
		for _, execution := range executions {
			if execution.QuestID == quest && execution.RunID != "" && a.engine.IsActiveRun(execution.RunID) {
				a.engine.WaitFinalized(execution.RunID, 10*time.Second)
				if a.engine.IsActiveRun(execution.RunID) {
					return MasterSessions{}, errors.New("file worker has not stopped yet")
				}
			}
		}
	}
	if err = a.store.DeleteMasterConversation(ctx, w, id); err != nil {
		return MasterSessions{}, err
	}
	// This scope belongs to one folder, so the next conversation gets a new one.
	if !strings.HasPrefix(w, "point-chat-") {
		return MasterSessions{}, errors.New("POINT scope required")
	}
	next, err := a.newPointChatScope(ctx, domain.NewID("chat"), false)
	if err != nil {
		return MasterSessions{}, err
	}
	return a.MasterSessions(next)
}
