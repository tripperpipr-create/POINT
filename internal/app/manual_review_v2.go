package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
)

// ManualCriterionReviewRequest is the human decision on one manual criterion.
type ManualCriterionReviewRequest struct {
	Decision string `json:"decision"`
	Note     string `json:"note,omitempty"`
}

// ReviewManualCriterionV2 lets the human close a manual criterion. Before it
// existed a quest with such a criterion could end only as "blocked" or
// "completed with limitations": the card asked for a manual check and offered
// no way to record that it was done.
func (a *App) ReviewManualCriterionV2(ctx context.Context, questID, criterionID string, input ManualCriterionReviewRequest) (domain.EvidenceBundle, error) {
	decision := strings.TrimSpace(input.Decision)
	note := strings.TrimSpace(input.Note)
	if utf8.RuneCountInString(note) > 1000 {
		return domain.EvidenceBundle{}, errors.New("заметка к решению длиннее 1000 символов")
	}
	status, err := a.store.ReviewManualCriterionV2(ctx, domain.ManualCriterionReview{
		QuestID: strings.TrimSpace(questID), CriterionID: strings.TrimSpace(criterionID),
		Decision: decision, Note: note, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return domain.EvidenceBundle{}, err
	}
	bundle, err := a.store.GetEvidenceBundle(ctx, questID)
	if err != nil {
		return domain.EvidenceBundle{}, err
	}
	if approval, approvalErr := a.store.WorkOrderApprovalByQuestV2(ctx, questID); approvalErr == nil {
		a.publishManualReviewV2(ctx, approval, criterionID, decision, status, bundle)
	}
	return bundle, nil
}

func (a *App) publishManualReviewV2(ctx context.Context, approval domain.WorkOrderApproval, criterionID, decision string, status domain.QuestStatus, bundle domain.EvidenceBundle) {
	text := criterionID
	for _, criterion := range approval.WorkOrder.Criteria {
		if criterion.ID == criterionID && strings.TrimSpace(criterion.Text) != "" {
			text = strings.TrimSpace(criterion.Text)
		}
	}
	verdict := "не принято"
	if decision == domain.ManualReviewAccepted {
		verdict = "принято"
	}
	title, level := "Заблокировано", "error"
	switch {
	case status == domain.QuestCompleted && bundle.Assurance == domain.WorkOrderAssuranceVerified:
		title, level = "Готово", "success"
	case status == domain.QuestCompleted:
		title, level = "Готово с ограничениями", "warning"
	case status == domain.QuestNeedsReview:
		title, level = "Ждёт ручной приёмки", "warning"
	}
	_, err := a.store.SaveCompanionMessageOnce(ctx, domain.CompanionMessage{
		ID: "master-workorder-review-" + approval.QuestID + "-" + criterionID, WorkspaceID: approval.WorkOrder.WorkspaceID,
		ConversationID: approval.WorkOrder.ConversationID, Speaker: "master", Role: "assistant",
		Content: fmt.Sprintf("Ручная приёмка: «%s» — %s.\nИтог квеста: %s", text, verdict, title),
		Level:   level, Mode: "quest_completion", ProposalID: approval.WorkOrder.ProposalID, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		slog.Warn("manual review outcome not published", "quest_id", approval.QuestID, "error", security.Redact(err.Error()))
	}
}
