package app

import (
	"context"
	"os"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

// Утверждение заводит агентов внутри своей транзакции, мимо SaveProjectAgent:
// ни лимитов, ни проверки определения там нет. Единственное место, где это ещё
// можно поймать, — предполётная проверка наряда.
func TestApproveWorkOrderRefusesInventedTools(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	order := rosterTestOrder(t, application, rosterTestProposal(world.ID, "qp-invented", "Собрать backend API с /health", false), "conversation-invented")
	if len(order.Roster.Permanent) != 1 || order.Roster.Permanent[0].Existing {
		t.Fatalf("тесту нужен наряд с черновиком нового агента: %#v", order.Roster)
	}

	order.Roster.Permanent[0].RequiredTools = []string{"read_file", "symfony_console"}
	order.State = "ready"
	order.Version++
	saved, err := application.SaveWorkOrderV2(context.Background(), order)
	if err != nil {
		t.Fatal(err)
	}
	_, err = application.ApproveWorkOrderV2(context.Background(), saved.ID, ApproveWorkOrderV2Request{
		Version: saved.Version, Digest: domain.WorkOrderDigest(saved), IdempotencyKey: "approve-invented",
		RosterConsent: []string{saved.Roster.Permanent[0].ID},
	})
	if err == nil || !strings.Contains(err.Error(), "symfony_console") {
		t.Fatalf("наряд с выдуманным инструментом утверждён: %v", err)
	}
	if saved.Workspace.Mode == "managed" {
		if _, statErr := os.Stat(saved.Workspace.Path); statErr == nil {
			t.Fatal("отказ обязан случиться до создания рабочей папки")
		}
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
