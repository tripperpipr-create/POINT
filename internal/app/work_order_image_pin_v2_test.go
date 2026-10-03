package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
)

// imagePinSandbox — бэкенд с образами: разрешает образ без песочницы и, как
// ContainerBackend, отказывает в создании под другим закреплённым образом.
type imagePinSandbox struct {
	*recordingSandboxBackend
	mu       sync.Mutex
	digest   string
	resolved int
	release  chan struct{}
}

func (b *imagePinSandbox) Capabilities() sandbox.Capabilities {
	return (&strongWorkOrderSandbox{}).Capabilities()
}

func (b *imagePinSandbox) current() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.digest
}

func (b *imagePinSandbox) ResolveRuntimeImage(ctx context.Context, _ sandbox.RuntimeRequirements) (string, string, error) {
	if b.release != nil {
		select {
		case <-b.release:
		case <-ctx.Done():
			return "", "", ctx.Err()
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.resolved++
	return "point-agent-sandbox:test", b.digest, nil
}

func (b *imagePinSandbox) Create(ctx context.Context, request sandbox.CreateRequest) (domain.SandboxRecord, error) {
	actual := b.current()
	if pinned := request.Runtime.PinnedImageDigest; pinned != "" && pinned != actual {
		return domain.SandboxRecord{}, fmt.Errorf("%w: approved %s, now %s (point-agent-sandbox:test)", sandbox.ErrRuntimeImageChanged, pinned, actual)
	}
	record, err := b.recordingSandboxBackend.Create(ctx, request)
	record.BackendImageDigest = actual
	return record, err
}

func imagePinTestApp(t *testing.T, backend *imagePinSandbox) (*App, domain.Workspace) {
	t.Helper()
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir(), WithSandboxBackend(backend))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	return application, openTestWorld(t, application)
}

func imagePinTestOrder(t *testing.T, application *App, world domain.Workspace) domain.WorkOrder {
	t.Helper()
	order := managedWorkOrderV2()
	order.WorkspaceID = world.ID
	order.Workspace = domain.WorkspacePlan{Mode: "existing", Path: world.Path, Isolation: "snapshot"}
	assignReadyRosterForTest(t, application, &order)
	return order
}

// Q17: готовый наряд получает дайджест образа следующей версией, до
// утверждения. Пока образ не закреплён, утвердить наряд нельзя.
func TestReadyWorkOrderPinsItsImageBeforeApproval(t *testing.T) {
	first := "sha256:" + strings.Repeat("1", 64)
	backend := &imagePinSandbox{recordingSandboxBackend: &recordingSandboxBackend{Manager: &sandbox.Manager{Root: t.TempDir()}}, digest: first, release: make(chan struct{})}
	application, world := imagePinTestApp(t, backend)
	ctx := context.Background()
	saved, err := application.SaveWorkOrderV2(ctx, imagePinTestOrder(t, application, world))
	if err != nil || saved.State != "ready" {
		t.Fatalf("наряд не готов: %s %v", saved.State, err)
	}
	_, err = application.ApproveWorkOrderV2(ctx, saved.ID, ApproveWorkOrderV2Request{Version: saved.Version, Digest: saved.Digest, IdempotencyKey: "early"})
	if !errors.Is(err, errWorkOrderImagePending) {
		t.Fatalf("наряд утверждён без закреплённого образа: %v", err)
	}
	close(backend.release)
	application.waitWorkOrderStaffing()
	pinned, err := application.WorkOrderV2(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pinned.Version != saved.Version+1 || pinned.Sandbox.ImageDigest != first || pinned.Sandbox.ImageBasis == "" || pinned.State != "ready" {
		t.Fatalf("образ не закреплён следующей версией: v%d %+v", pinned.Version, pinned.Sandbox)
	}
	if err = application.requireWorkOrderImagePinnedV2(pinned); err != nil {
		t.Fatalf("закреплённый наряд не утверждается: %v", err)
	}
	if contract := taskBriefSandboxForTest(t, pinned); contract.PinnedImageDigest != first {
		t.Fatalf("закреплённый образ не дошёл до песочницы этапа: %q", contract.PinnedImageDigest)
	}

	// Требования к образу изменились — прежний дайджест недействителен, и
	// следующая запись закрепляет образ заново.
	changed := pinned
	changed.Sandbox.Toolchains = map[string]string{"node": "20"}
	if cleared := withCurrentImagePinV2(changed); cleared.Sandbox.ImageDigest != "" {
		t.Fatalf("дайджест пережил смену требований: %+v", cleared.Sandbox)
	}
	if kept := withCurrentImagePinV2(pinned); kept.Sandbox.ImageDigest != first {
		t.Fatal("дайджест снят без смены требований")
	}
	if diff := domain.DiffWorkOrders(saved, pinned); !strings.Contains(strings.Join(diff.ChangedFields, ","), "sandbox") {
		t.Fatalf("смена образа не видна в отличиях версий: %v", diff.ChangedFields)
	}
}

func taskBriefSandboxForTest(t *testing.T, order domain.WorkOrder) sandbox.RuntimeRequirements {
	t.Helper()
	brief, err := taskBriefFromWorkOrderV2(order)
	if err != nil {
		t.Fatal(err)
	}
	return managedSandboxRuntimeForBrief(&brief)
}

// Q17: образ сменился после утверждения — квест встаёт на паузу с причиной и
// строкой в ленте Мастера, а не исполняется под чужим образом.
func TestLaunchUnderChangedImagePausesTheQuest(t *testing.T) {
	approved := "sha256:" + strings.Repeat("2", 64)
	backend := &imagePinSandbox{recordingSandboxBackend: &recordingSandboxBackend{Manager: &sandbox.Manager{Root: t.TempDir()}}, digest: approved}
	application, world := imagePinTestApp(t, backend)
	ctx := context.Background()
	draft := imagePinTestOrder(t, application, world)
	if err := application.store.SaveOrchestratorConfig(ctx, domain.OrchestratorConfig{
		ID: "master", WorkspaceID: world.ID, Preset: "conductor", ConnectionID: draft.Routing.FixedConnectionID,
		Provider: domain.ProviderOpenAI, ProviderPreset: "openai", Model: "gpt-test",
		PlanningDepth: 70, Parallelism: 50, ApprovalStrictness: 40, TeamPreference: 80,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := application.SaveWorkOrderV2(ctx, draft); err != nil {
		t.Fatal(err)
	}
	application.waitWorkOrderStaffing()
	orders, err := application.store.ListWorkOrdersForWorkspaceV2(ctx, world.ID)
	if err != nil || len(orders) != 1 || orders[0].Sandbox.ImageDigest != approved {
		t.Fatalf("образ не закреплён: %v %+v", err, orders)
	}
	order := orders[0]
	approval, err := application.store.ApproveWorkOrderV2(ctx, order.ID, order.Version, order.Digest, "image-changed")
	if err != nil {
		t.Fatal(err)
	}
	quest, err := application.workOrderQuestV2(ctx, world.ID, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	quest.Status = domain.QuestPreflight
	if err = application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	backend.digest = "sha256:" + strings.Repeat("3", 64)
	backend.mu.Unlock()

	application.runWorkOrderLaunchV2(ctx, approval, quest, "")

	held, err := application.WorkOrderV2(ctx, order.ID)
	if err != nil || held.Runtime == nil {
		t.Fatalf("runtime unreadable: %v", err)
	}
	if held.Runtime.Status != domain.QuestPaused || !strings.Contains(held.Runtime.Message, "Образ песочницы изменился") {
		t.Fatalf("смена образа не остановила квест: %#v", held.Runtime)
	}
	messages, err := application.store.ListChatMessages(ctx, world.ID, "master", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) == 0 || !strings.Contains(messages[len(messages)-1].Content, "новую версию наряда") {
		t.Fatalf("лента Мастера молчит о смене образа: %#v", messages)
	}
	backend.mu.Lock()
	created := len(backend.records)
	backend.mu.Unlock()
	if created != 0 {
		t.Fatalf("песочница создана под чужим образом: %d", created)
	}
}
