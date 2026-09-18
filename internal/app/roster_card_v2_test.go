package app

import (
	"context"
	"testing"

	"local-agent-workbench/internal/domain"
)

// Карточка найма считается на чтении и приезжает вместе с разговором: прежнее
// предложение найма жило в ответе последнего хода и исчезало при перезагрузке.
func TestHiringCardOffersDraftForOpenOrder(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	order := rosterTestOrder(t, application, rosterTestProposal(world.ID, "qp-card", "Собрать backend API с /health", false), "conversation-card")

	cards := application.hiringCardsForConversation(context.Background(), []domain.WorkOrder{order})
	if len(cards) != 1 {
		t.Fatalf("открытый наряд без исполнителя обязан дать карточку найма: %#v", cards)
	}
	card := cards[0]
	if card.WorkOrderID != order.ID || card.State != "create" || card.Draft == nil {
		t.Fatalf("карточка не предлагает завести исполнителя: %#v", card)
	}
	if card.Draft.ID != order.Roster.Permanent[0].ID {
		t.Fatalf("карточка показывает не тот черновик, что уйдёт на утверждение: %q против %q", card.Draft.ID, order.Roster.Permanent[0].ID)
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
}
