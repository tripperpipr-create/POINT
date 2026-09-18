// Сохранение хода разговора и его история.
package orchestrator

import (
	"context"
	"fmt"
	"time"

	"local-agent-workbench/internal/domain"
)

func (s ChatService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s ChatService) newID(prefix string) string {
	if s.NewID != nil {
		return s.NewID(prefix)
	}
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

func (s ChatService) persist(ctx context.Context, workspaceID, role, content, proposalID string, cfg domain.OrchestratorConfig) error {
	return s.Store.SaveCompanionMessage(ctx, domain.CompanionMessage{
		ID:          s.newID("mm"),
		WorkspaceID: workspaceID,
		Speaker:     "master",
		Role:        role,
		Content:     content,
		Mode:        "deterministic",
		Provider:    string(cfg.Provider),
		Model:       cfg.Model,
		ProposalID:  proposalID,
		CreatedAt:   s.now(),
	})
}

// persistReply сохраняет ответ целиком, а не только его текст.
//
// Основания и уточняющие вопросы складывались в ответ на текущий ход и жили до
// первой перезагрузки панели: разговор поднимался из истории голыми репликами,
// чипы вопросов пропадали, а «на чём это основано» приходилось выспрашивать
// заново. Колонки под то и другое в хронике есть с самого начала — Мастер их
// просто не заполнял.
func (s ChatService) persistReply(ctx context.Context, req ChatRequest, response ChatResponse, proposalID string) error {
	actionProposalID := ""
	if response.ActionProposal != nil {
		actionProposalID = response.ActionProposal.ID
	}
	return s.Store.SaveCompanionMessage(ctx, domain.CompanionMessage{
		ID:               s.newID("mm"),
		WorkspaceID:      req.WorkspaceID,
		Speaker:          "master",
		Role:             "assistant",
		Content:          response.Reply,
		Mode:             response.Mode,
		Provider:         string(req.Config.Provider),
		Model:            req.Config.Model,
		FactsUsed:        response.Facts,
		Questions:        response.Questions,
		Clarifications:   response.Clarifications,
		ProposalID:       proposalID,
		ActionProposalID: actionProposalID,
		FallbackReason:   response.FallbackReason,
		Reasoning:        response.Reasoning,
		Steps:            response.Steps,
		InputTokens:      response.Usage.InputTokens,
		OutputTokens:     response.Usage.OutputTokens,
		TotalTokens:      response.Usage.TotalTokens,
		LatencyMs:        response.Usage.LatencyMs,
		CreatedAt:        s.now(),
	})
}

// History возвращает разговор Мастера, не смешивая его с компаньоном.
func (s ChatService) History(ctx context.Context, workspaceID string, limit int) ([]domain.CompanionMessage, error) {
	return s.Store.ListChatMessages(ctx, workspaceID, "master", limit)
}
