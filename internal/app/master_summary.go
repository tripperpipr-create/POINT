package app

import (
	"context"
	"log/slog"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/orchestrator"
)

// Резюме длинного разговора пересчитывается фоном после хода, а не внутри
// него: человек ждёт ответа, а не пересказа всей беседы.
//
// Пересчёт раз в несколько ходов: одна свежая реплика почти не меняет
// пересказ ранней части, а каждый пересчёт — полный запрос к модели. Счётчик
// живёт в памяти ядра; после перезапуска резюме в худшем случае пересчитается
// на один раз раньше.
const (
	masterSummaryEvery   = 4
	masterSummaryTimeout = 3 * time.Minute
	masterSummaryHistory = 48
)

func (a *App) refreshMasterSummary(workspaceID, conversationID string, cfg domain.OrchestratorConfig, apiKey string) {
	// Отмена регистрируется рядом с ходами: остановка ядра не должна ждать,
	// пока модель допишет пересказ.
	ctx, cancel := context.WithTimeout(context.Background(), masterSummaryTimeout)
	key := workspaceID + "/summary:" + conversationID
	a.masterTurnsMu.Lock()
	if a.masterTurnsStopping {
		a.masterTurnsMu.Unlock()
		cancel()
		return
	}
	if _, running := a.masterTurnCancels[key]; running {
		a.masterTurnsMu.Unlock()
		cancel()
		return
	}
	if a.masterTurnCancels == nil {
		a.masterTurnCancels = map[string]context.CancelFunc{}
	}
	a.masterTurnCancels[key] = cancel
	a.masterTurnsWG.Add(1)
	a.masterTurnsMu.Unlock()
	go func() {
		defer a.masterTurnsWG.Done()
		defer func() {
			cancel()
			a.masterTurnsMu.Lock()
			delete(a.masterTurnCancels, key)
			a.masterTurnsMu.Unlock()
		}()
		service, sessions, err := a.sessionMasterService(ctx, a.masterChatService(ctx, orchestrator.ProjectFacts{}), conversationID)
		if err != nil || a.currentWorldID() != workspaceID {
			return
		}
		history, err := service.History(ctx, workspaceID, masterSummaryHistory)
		if err != nil || len(history) <= orchestrator.MasterVerbatimHistory {
			return
		}
		var conversation domain.MasterConversation
		for _, item := range sessions.Items {
			if item.ID == conversationID {
				conversation = item
			}
		}
		if conversation.ID == "" || !a.masterSummaryDue(workspaceID+"/"+conversationID, conversation.Summary == "") {
			return
		}
		summary, err := service.SummarizeConversation(ctx, cfg, apiKey, history, conversation.Summary)
		if err != nil {
			slog.Warn("master conversation summary not refreshed", "conversation_id", conversationID, "error", err)
			return
		}
		if summary == conversation.Summary {
			return
		}
		// Пишется одно резюме: за время пересказа человек мог переименовать или
		// закрепить беседу, а время обновления поднимало бы её в списке, как
		// новая реплика.
		if err = a.store.SaveMasterConversationSummary(ctx, workspaceID, conversationID, summary); err != nil {
			slog.Warn("master conversation summary not saved", "conversation_id", conversationID, "error", err)
		}
	}()
}

// masterSummaryDue считает ходы длинного разговора и отвечает, пора ли
// пересчитать резюме. Разговор без резюме получает его сразу.
func (a *App) masterSummaryDue(key string, missing bool) bool {
	a.masterSummaryMu.Lock()
	defer a.masterSummaryMu.Unlock()
	if a.masterSummaryTurns == nil {
		a.masterSummaryTurns = map[string]int{}
	}
	a.masterSummaryTurns[key]++
	if missing || a.masterSummaryTurns[key] >= masterSummaryEvery {
		a.masterSummaryTurns[key] = 0
		return true
	}
	return false
}
