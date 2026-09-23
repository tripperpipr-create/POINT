package app

import (
	"context"
	"testing"

	"local-agent-workbench/internal/domain"
)

// Карточка найма считается на чтении и приезжает вместе с разговором: прежнее
// предложение найма жило в ответе последнего хода и исчезало при перезагрузке.
func TestLegacyHiringCardDoesNotReplaceSelectorDraft(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	order := rosterTestOrder(t, application, rosterTestProposal(world.ID, "qp-card", "Собрать backend API с /health", false), "conversation-card")

	cards := application.hiringCardsForConversation(context.Background(), []domain.WorkOrder{order})
	if len(cards) != 0 {
		t.Fatalf("legacy hiring card appeared beside selector-bound draft: %#v", cards)
	}
}

// Утверждённый наряд карточки не получает: состав зафиксирован согласием, и
// звать человека менять работающее незачем.
func TestHiringCardSkipsApprovedOrder(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	order := rosterTestOrder(t, application, rosterTestProposal(world.ID, "qp-approved-card", "Собрать backend API", false), "conversation-approved-card")
	order.State = "approved"
	order.ApprovedDigest = "digest"

	if cards := application.hiringCardsForConversation(context.Background(), []domain.WorkOrder{order}); len(cards) != 0 {
		t.Fatalf("утверждённый наряд получил карточку найма: %#v", cards)
	}
}

// Уточняющие вопросы ещё не являются согласованным заданием. Состав на этом
// этапе нестабилен, поэтому карточка «Кем делать» не должна опережать ответ
// человека и предлагать исполнителей для черновика.
func TestHiringCardSkipsDiscussionOrder(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	order := rosterTestOrder(t, application, rosterTestProposal(world.ID, "qp-discussion-card", "Собрать backend API и проверить", false), "conversation-discussion-card")
	order.State = "discussion"
	order.Roster = domain.AgentRosterPlan{}

	if cards := application.hiringCardsForConversation(context.Background(), []domain.WorkOrder{order}); len(cards) != 0 {
		t.Fatalf("несогласованное задание получило карточку подбора: %#v", cards)
	}
}

// Когда решать нечего — состав собран, замены нет, пробелов нет, — карточки
// быть не должно: она повторяла бы блок «Агенты» карточки запуска.
func TestHiringCardStaysQuietWhenNothingToDecide(t *testing.T) {
	card := RosterCardView{WorkOrderID: "workorder-1", State: "ready", Selected: []RosterCandidate{{AgentID: "agent-1", Readiness: "READY"}}}
	if rosterCardWorthShowing(card) {
		t.Fatal("карточка найма показалась там, где решать нечего")
	}
	card.Considered = []RosterCandidate{{AgentID: "agent-2", Readiness: "READY"}}
	if !rosterCardWorthShowing(card) {
		t.Fatal("возможная замена исполнителя обязана показываться")
	}
	card.Considered = nil
	card.Blueprints = []RosterBlueprintMatch{{BlueprintID: "tester", Name: "Тестировщик"}}
	if rosterCardWorthShowing(card) {
		t.Fatal("невидимый чертёж без действия не должен показывать пустую карточку")
	}
}
