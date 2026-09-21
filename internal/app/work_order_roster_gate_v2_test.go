package app

import (
	"context"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

func TestApproveWorkOrderRefusesSelectorDraft(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	order := rosterTestOrder(t, application, rosterTestProposal(world.ID, "qp-invented", "Собрать backend API с /health", false), "conversation-invented")
	if len(order.Roster.Permanent) != 1 || !order.Roster.Permanent[0].Existing {
		t.Fatalf("selector must persist draft before approval: %#v", order.Roster)
	}
	_, err := application.ApproveWorkOrderV2(context.Background(), order.ID, ApproveWorkOrderV2Request{
		Version: order.Version, Digest: domain.WorkOrderDigest(order), IdempotencyKey: "approve-draft",
	})
	if err == nil || !strings.Contains(err.Error(), "активирован") {
		t.Fatalf("WorkOrder with draft was approved: %v", err)
	}
}

// Готовность существующего исполнителя проверялась везде, кроме пути
// утверждения наряда: карточка агента показывала «не готов», а наряд всё равно
// уходил в работу, и человек узнавал причину из провала квеста.
func TestApproveWorkOrderRefusesBlockedExistingAgent(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	agent := rosterTestAgent(t, application, "Backend", "Backend-разработчик", "Держит серверную часть проекта")
	order := rosterTestOrder(t, application, rosterTestProposal(world.ID, "qp-blocked", "Собрать backend API с /health", false), "conversation-blocked")
	if len(order.Roster.Permanent) != 1 || !order.Roster.Permanent[0].Existing {
		t.Fatalf("тесту нужен наряд с готовым исполнителем: %#v", order.Roster)
	}

	// Лимит ходов снимается тем же путём, каким его правит человек в мастерской:
	// агент остаётся в проекте, но запускаться ему нечем.
	broken := agent
	broken.MaxSteps = 0
	broken.MaxDurationSeconds = 0
	if err := application.store.SaveProjectAgent(context.Background(), broken); err != nil {
		t.Fatal(err)
	}

	_, err := application.ApproveWorkOrderV2(context.Background(), order.ID, ApproveWorkOrderV2Request{
		Version: order.Version, Digest: domain.WorkOrderDigest(order), IdempotencyKey: "approve-blocked",
	})
	if err == nil || !strings.Contains(err.Error(), "не готов") {
		t.Fatalf("наряд с неготовым исполнителем утверждён: %v", err)
	}
}
