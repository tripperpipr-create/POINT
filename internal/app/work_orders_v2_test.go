package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
)

type strongWorkOrderSandbox struct{ *recordingSandboxBackend }

func (*strongWorkOrderSandbox) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{Backend: "test-container", ProcessIsolation: true, NetworkIsolation: true, SecretEnvironmentSanitization: true, SymlinkIsolation: true, StrongOSBoundary: true}
}

func managedWorkOrderV2() domain.WorkOrder {
	return domain.WorkOrder{
		State: "ready", Goal: "Новый API", Scope: []string{"health"},
		Criteria:  []domain.AcceptanceCriterion{{ID: "health", Kind: "manual", Text: "healthy"}},
		Workspace: domain.WorkspacePlan{Mode: "managed"},
		Stack:     domain.StackPresetRef{ID: "recommended-web", Version: "1", Category: "web", Source: "benchmark"},
		Routing:   domain.ModelRoutingPolicy{Mode: "fixed", FixedConnectionID: "c", FixedModel: "m"},
		Budget:    domain.BudgetEnvelope{Preset: "small", Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxAttempts: 1},
		Delivery:  domain.DeliveryPolicy{ApplyMode: "automatic", KeepPartialDays: 30},
	}
}

func TestMasterProposalBecomesSingleApprovableWorkOrderV2(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	backend := &strongWorkOrderSandbox{recordingSandboxBackend: &recordingSandboxBackend{Manager: &sandbox.Manager{Root: t.TempDir()}}}
	application, err := New(t.TempDir(), WithSandboxBackend(backend))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	world := openTestWorld(t, application)
	connection := saveTestConnection(t, application, "master-v2", "openai", "Master v2")
	if err = application.store.SaveOrchestratorConfig(context.Background(), domain.OrchestratorConfig{
		ID: "master", WorkspaceID: world.ID, Preset: "conductor", ConnectionID: connection.ID,
		Provider: domain.ProviderOpenAI, ProviderPreset: "openai", Model: "gpt-test",
		PlanningDepth: 70, Parallelism: 50, ApprovalStrictness: 40, TeamPreference: 80,
	}); err != nil {
		t.Fatal(err)
	}
	proposal := domain.QuestProposal{
		ID: "proposal-v2", WorkspaceID: world.ID, TeamAgentIDs: []string{},
		Brief: &domain.TaskBrief{
			State: "ready", Goal: "Развернуть Symfony API", ResultKind: "web",
			Scope:       []string{"Docker Compose", "PostgreSQL", "health endpoint"},
			Criteria:    []domain.AcceptanceCriterion{{ID: "health", Kind: "verification", Text: "Health endpoint отвечает 200", Tool: "run_command", Arguments: json.RawMessage(`{"command":"go version"}`)}},
			Permissions: domain.TaskPermissions{ExecuteCommands: true, NetworkHosts: []string{"repo.packagist.org"}},
			Budget:      domain.TaskBudget{Tokens: 120000, ActiveSeconds: 1800, MaxParallel: 2, MaxReplans: 4, MaxAttempts: 2, MaxProjectAgents: 2},
		},
	}
	id, err := application.saveMasterWorkOrderV2(context.Background(), &proposal, nil, "conversation-v2", nil)
	if err != nil {
		t.Fatal(err)
	}
	order, err := application.WorkOrderV2(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if order.State != "ready" || order.Workspace.Mode != "existing" || order.Workspace.Path != world.Path {
		t.Fatalf("master work order lost trusted workspace/state: %#v", order)
	}
	if order.ConversationID != "conversation-v2" || order.Digest == "" {
		t.Fatalf("master conversation/digest link is missing: %#v", order)
	}
	if order.Routing.FixedConnectionID != connection.ID || order.Routing.FixedModel != "gpt-test" {
		t.Fatalf("master work order did not bind exact routing: %#v", order.Routing)
	}
	if len(order.Roster.Permanent) != 1 || !order.Roster.Permanent[0].RequiresConsent || order.Roster.Permanent[0].Existing {
		t.Fatalf("missing agent must remain an explicit approval-card draft: %#v", order.Roster)
	}
	if len(order.Network) != 1 || order.Network[0].Host != "repo.packagist.org:443" {
		t.Fatalf("network authority was not normalized to exact TLS host: %#v", order.Network)
	}
	// Черновик агента требует согласия: без списка подтверждённых черновиков
	// утверждение обязано отказать, а не создать исполнителя молча.
	if _, consentErr := application.ApproveWorkOrderV2(context.Background(), order.ID, ApproveWorkOrderV2Request{
		Version: order.Version, Digest: domain.WorkOrderDigest(order), IdempotencyKey: "master-v2-approval-no-consent",
	}); consentErr == nil || !strings.Contains(consentErr.Error(), "consent") {
		t.Fatalf("approval without roster consent must be refused: %v", consentErr)
	}
	approval, err := application.ApproveWorkOrderV2(context.Background(), order.ID, ApproveWorkOrderV2Request{
		Version: order.Version, Digest: domain.WorkOrderDigest(order), IdempotencyKey: "master-v2-approval",
		RosterConsent: []string{order.Roster.Permanent[0].ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Утверждение только начинает запуск: планировщик milestone идёт к модели и
	// переживает свой HTTP-запрос. Исход читается после фоновой работы.
	application.waitWorkOrderLaunches()
	approval, err = application.store.WorkOrderApprovalByQuestV2(context.Background(), approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	quest, err := application.WorkOrderQuestV2(context.Background(), approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	approval.Status, approval.FlowID, approval.FlowRunID = string(quest.Status), quest.FlowID, quest.FlowRunID
	if approval.Status != string(domain.QuestAwaitingUser) || len(approval.AgentIDs) != 1 || approval.FlowID == "" || approval.FlowRunID == "" {
		t.Fatalf("approval did not reuse the v2 quest and enter executable Flow: %#v", approval)
	}
	flow, err := application.store.GetFlow(context.Background(), approval.FlowID)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range flow.Nodes {
		if node.Kind != domain.FlowNodeAgent {
			continue
		}
		binding, bindingErr := modelBindingFromNode(node)
		if bindingErr != nil {
			t.Fatal(bindingErr)
		}
		if binding == nil || binding.ConnectionID != connection.ID || binding.Model != "gpt-test" || binding.Source != "work_order_v2" {
			t.Fatalf("approved fixed routing was not compiled into node %q: %#v", node.Name, binding)
		}
	}
	reloaded, err := application.WorkOrderV2(context.Background(), order.ID)
	if err != nil || reloaded.Runtime == nil || reloaded.Runtime.QuestID != approval.QuestID || reloaded.Runtime.Status != domain.QuestAwaitingUser {
		t.Fatalf("work order lost its live quest state after reload: %#v err=%v", reloaded.Runtime, err)
	}
}

func TestTaskBriefFromWorkOrderV2PreservesExecutionContract(t *testing.T) {
	order := managedWorkOrderV2()
	order.ID, order.Version = "workorder-contract", 3
	order.WorkspaceID = "workspace-contract"
	order.Workspace = domain.WorkspacePlan{Mode: "existing", Path: `C:\Point\project`, Isolation: "snapshot"}
	order.Stack = domain.StackPresetRef{ID: "web", Version: "7", Category: "web", Source: "benchmark"}
	order.Sources = []domain.SourceSnapshotRef{{ID: "source-1", Kind: "url", Digest: "sha256:source", Locator: "https://example.test/spec"}}
	order.Routing = domain.ModelRoutingPolicy{Mode: "fixed", FixedConnectionID: "connection-1", FixedModel: "model-1", FallbackMode: "transient_only", FallbackModels: []string{"model-2"}}
	order.Network = []domain.NetworkGrant{{Host: "api.example.test:443", Purpose: "fixture"}}
	order.Secrets = []domain.SecretRequirement{{Name: "API_TOKEN", Required: true}}
	order.Delivery = domain.DeliveryPolicy{ApplyMode: "automatic", CommitMode: "squash", KeepPartialDays: 30, KeepServicesRunning: true, ApplicationURL: "http://localhost:8080"}

	brief, err := taskBriefFromWorkOrderV2(order)
	if err != nil {
		t.Fatal(err)
	}
	if brief.WorkOrder == nil {
		t.Fatal("approved task brief discarded the WorkOrder execution contract")
	}
	contract := brief.WorkOrder
	if contract.ID != order.ID || contract.Version != order.Version || contract.Digest != domain.WorkOrderDigest(order) || contract.SourceDigest != domain.WorkOrderSourceDigest(order) {
		t.Fatalf("work order identity/digests were collapsed: %#v", contract)
	}
	if contract.Workspace.Path != order.Workspace.Path || contract.Stack.Version != "7" || contract.Routing.FixedConnectionID != "connection-1" || contract.Delivery.CommitMode != "squash" {
		t.Fatalf("runtime contract lost approved execution fields: %#v", contract)
	}
	if len(contract.Sources) != 1 || len(contract.Network) != 1 || len(contract.Secrets) != 1 || len(contract.Routing.FallbackModels) != 1 {
		t.Fatalf("runtime contract lost approved collections: %#v", contract)
	}
}

func TestWorkOrderRevisionRequiresDigestAndReplaysOnlyExactPayload(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	openTestWorld(t, application)
	order, err := application.SaveWorkOrderV2(context.Background(), managedWorkOrderV2())
	if err != nil {
		t.Fatal(err)
	}
	revision := order
	revision.State = "ready"
	revision.Goal = "Revised goal"
	request := ReviseWorkOrderV2Request{
		ExpectedVersion: order.Version, ExpectedDigest: domain.WorkOrderDigest(order), IdempotencyKey: "revision-exact-once", WorkOrder: revision,
	}
	first, err := application.ReviseWorkOrderV2(context.Background(), order.ID, request)
	if err != nil || first.Version != order.Version+1 {
		t.Fatalf("first revision=%#v err=%v", first, err)
	}
	replay, err := application.ReviseWorkOrderV2(context.Background(), order.ID, request)
	if err != nil || replay.Version != first.Version || domain.WorkOrderDigest(replay) != domain.WorkOrderDigest(first) {
		t.Fatalf("idempotent replay=%#v err=%v", replay, err)
	}
	request.WorkOrder.Goal = "Different payload"
	if _, err = application.ReviseWorkOrderV2(context.Background(), order.ID, request); err == nil {
		t.Fatal("idempotency key accepted a different revision payload")
	}
	request.IdempotencyKey = "revision-stale"
	if _, err = application.ReviseWorkOrderV2(context.Background(), order.ID, request); err == nil {
		t.Fatal("stale version/digest revision was accepted")
	}
}

func TestMilestoneBriefContainsOnlyCurrentScopeAndCriteria(t *testing.T) {
	order := managedWorkOrderV2()
	order.Criteria = []domain.AcceptanceCriterion{
		{ID: "database", Kind: "manual", Text: "database ready"},
		{ID: "frontend", Kind: "manual", Text: "frontend ready"},
	}
	order.Milestones = []domain.MilestonePlan{
		{ID: "backend", Goal: "Build backend", Scope: []string{"database"}, CriterionIDs: []string{"database"}},
		{ID: "ui", Goal: "Build UI", Scope: []string{"frontend"}, CriterionIDs: []string{"frontend"}, DependsOn: []string{"backend"}},
	}
	brief, err := taskBriefForMilestoneV2(order, order.Milestones[0])
	if err != nil {
		t.Fatal(err)
	}
	if brief.Goal != "Build backend" || len(brief.Scope) != 1 || brief.Scope[0] != "database" || len(brief.Criteria) != 1 || brief.Criteria[0].ID != "database" {
		t.Fatalf("future milestone leaked into current Flow brief: %#v", brief)
	}
	if brief.WorkOrder == nil || len(brief.WorkOrder.Milestones) != 2 || brief.WorkOrder.Digest != domain.WorkOrderDigest(order) {
		t.Fatalf("approved root contract was not preserved: %#v", brief.WorkOrder)
	}
	runtimes := []domain.MilestoneRuntime{{MilestoneID: "backend", Status: domain.QuestCompleted}, {MilestoneID: "ui", Status: domain.QuestDraft}}
	next, _, ok := nextWorkOrderMilestoneV2(order, runtimes)
	if !ok || next.ID != "ui" {
		t.Fatalf("dependency-ready milestone was not selected: %#v", next)
	}
}

func TestWorkOrderDeliveryUsesApprovedWorkspaceInsteadOfActiveUIWorkspace(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })

	approvedPath := t.TempDir()
	activeUIPath := t.TempDir()
	approved, err := application.OpenWorkspace(approvedPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.OpenWorkspace(activeUIPath); err != nil {
		t.Fatal(err)
	}
	content := "delivered from approved work order\n"
	sum := sha256.Sum256([]byte(content))
	now := time.Now().UTC()
	set := domain.ChangeSet{
		ID: "changeset-approved-path", WorkspaceID: approved.Workspace.ID, ExecutionID: "execution-v2",
		Title: "v2 delivery", Status: domain.ChangeSetPending, CreatedAt: now, UpdatedAt: now,
		Items: []domain.ChangeItem{{
			ID: "item-v2", Path: "result.txt", Kind: "create", ProposedContent: content,
			ProposedHash: hex.EncodeToString(sum[:]),
		}},
	}
	if err = application.store.SaveChangeSet(context.Background(), set); err != nil {
		t.Fatal(err)
	}
	result, err := application.applyChangeSetAtPathV2(context.Background(), approved.Workspace.ID, approvedPath, set.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Applied) != 1 || result.Applied[0] != "result.txt" {
		t.Fatalf("unexpected delivery result: %#v", result)
	}
	data, err := os.ReadFile(filepath.Join(approvedPath, "result.txt"))
	if err != nil || string(data) != content {
		t.Fatalf("result was not delivered to approved workspace: %q err=%v", data, err)
	}
	if _, err = os.Stat(filepath.Join(activeUIPath, "result.txt")); !os.IsNotExist(err) {
		t.Fatalf("delivery leaked into currently active UI workspace: %v", err)
	}
}

func TestManagedWorkOrdersReserveUniquePathsWithoutCreatingProjects(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	first, err := application.SaveWorkOrderV2(context.Background(), managedWorkOrderV2())
	if err != nil {
		t.Fatal(err)
	}
	second, err := application.SaveWorkOrderV2(context.Background(), managedWorkOrderV2())
	if err != nil {
		t.Fatal(err)
	}
	if first.Workspace.Path == second.Workspace.Path || filepath.Dir(first.Workspace.Path) != filepath.Join(home, "Point", "Projects") {
		t.Fatalf("managed paths were not safely allocated: first=%q second=%q", first.Workspace.Path, second.Workspace.Path)
	}
	if _, statErr := os.Stat(first.Workspace.Path); !os.IsNotExist(statErr) {
		t.Fatalf("draft unexpectedly created managed project: %v", statErr)
	}
	if _, err = filepath.Abs(first.Workspace.Path); err != nil {
		t.Fatal(err)
	}
}

func TestManagedApprovalNeverAdoptsFolderCreatedAfterReview(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	t.Setenv("USERPROFILE", t.TempDir())
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	order, err := application.SaveWorkOrderV2(context.Background(), managedWorkOrderV2())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(order.Workspace.Path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = application.ApproveWorkOrderV2(context.Background(), order.ID, ApproveWorkOrderV2Request{Version: order.Version, Digest: domain.WorkOrderDigest(order), IdempotencyKey: "collision"}); err == nil {
		t.Fatal("approval adopted an existing folder")
	}
	if _, err = os.Stat(order.Workspace.Path); err != nil {
		t.Fatalf("foreign folder was removed: %v", err)
	}
}

func TestWorkOrderFlowFailureCanOnlyFinishThroughEvidenceGate(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	world := openTestWorld(t, application)
	order := managedWorkOrderV2()
	order.WorkspaceID = world.ID
	order.Workspace = domain.WorkspacePlan{Mode: "existing", Path: world.Path, Isolation: "snapshot"}
	order.Criteria = []domain.AcceptanceCriterion{{ID: "review", Kind: "manual", Text: "Проверить результат"}}
	order.Routing.FixedConnectionID = "unused-for-storage-approval"
	order, err = application.SaveWorkOrderV2(context.Background(), order)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := application.store.ApproveWorkOrderV2(context.Background(), order.ID, order.Version, domain.WorkOrderDigest(order), "failure-gate")
	if err != nil {
		t.Fatal(err)
	}
	quest, err := application.workOrderQuestV2(context.Background(), world.ID, approval.QuestID)
	if err != nil {
		t.Fatal(err)
	}
	quest.Status = domain.QuestRunning
	if err = application.store.SaveQuest(context.Background(), quest); err != nil {
		t.Fatal(err)
	}
	application.finalizeQuestAfterFlow(quest.ID, false)
	quest, err = application.workOrderQuestV2(context.Background(), world.ID, quest.ID)
	if err != nil || quest.Status != domain.QuestBlocked {
		t.Fatalf("failed v2 flow escaped evidence gate: quest=%#v err=%v", quest, err)
	}
	bundle, err := application.EvidenceBundle(context.Background(), quest.ID)
	if err != nil || bundle.BriefDigest != domain.WorkOrderDigest(order) || len(bundle.Criteria) != 1 {
		t.Fatalf("failed v2 flow did not persist bound evidence: %#v err=%v", bundle, err)
	}
}

// TestMasterKeepsOneWorkOrderPerConversationV2 закрепляет единственность
// карточки запуска в разговоре. Идентификатор наряда шёл от предложения, а
// предложение у каждого хода своё: уточнение задачи приходило в ленту второй
// карточкой — та же работа, другой идентификатор, — и человек выбирал между
// черновиком и его же уточнением. Открытый наряд беседы продолжается новой
// версией, а наряды чужой беседы остаются нетронутыми.
func TestMasterKeepsOneWorkOrderPerConversationV2(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	world := openTestWorld(t, application)
	connection := saveTestConnection(t, application, "master-one-card", "openai", "Master one card")
	if err = application.store.SaveOrchestratorConfig(context.Background(), domain.OrchestratorConfig{
		ID: "master", WorkspaceID: world.ID, Preset: "conductor", ConnectionID: connection.ID,
		Provider: domain.ProviderOpenAI, ProviderPreset: "openai", Model: "gpt-test",
		PlanningDepth: 70, Parallelism: 50, ApprovalStrictness: 40, TeamPreference: 80,
	}); err != nil {
		t.Fatal(err)
	}
	proposal := func(id, goal, state string, questions []string) domain.QuestProposal {
		return domain.QuestProposal{
			ID: id, WorkspaceID: world.ID, TeamAgentIDs: []string{},
			Brief: &domain.TaskBrief{
				State: state, Goal: goal, ResultKind: "web",
				Scope:         []string{"composer create-project"},
				OpenQuestions: questions,
				Criteria:      []domain.AcceptanceCriterion{{ID: "health", Kind: "manual", Text: "GET /health отвечает 200"}},
				Budget:        domain.TaskBudget{Tokens: 120000, ActiveSeconds: 1800, MaxParallel: 2, MaxReplans: 4, MaxAttempts: 2, MaxProjectAgents: 2},
			},
		}
	}
	first := proposal("qp-first", "Развернуть Symfony-проект", "discussion", []string{"Какой Symfony развернуть?"})
	firstID, err := application.saveMasterWorkOrderV2(context.Background(), &first, nil, "conversation-one", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Ход после ответа человека: модель не назвала proposalId, поэтому
	// предложение у него новое — карточка обязана остаться прежней.
	second := proposal("qp-second", "Каркас Symfony 7 с работающим /health", "ready", nil)
	secondID, err := application.saveMasterWorkOrderV2(context.Background(), &second, nil, "conversation-one", nil)
	if err != nil {
		t.Fatal(err)
	}
	if secondID != firstID {
		t.Fatalf("уточнение создало второй наряд: %q после %q", secondID, firstID)
	}
	orders, err := application.store.ListWorkOrdersForConversationV2(context.Background(), "conversation-one")
	if err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 {
		t.Fatalf("в ленте разговора %d карточек, ожидалась одна: %#v", len(orders), orders)
	}
	if orders[0].Version != 2 || orders[0].State != "ready" || orders[0].Goal != "Каркас Symfony 7 с работающим /health" {
		t.Fatalf("наряд не принял уточнение новой версией: %#v", orders[0])
	}
	// Другой разговор — другая работа: его карточку уборка не трогает.
	other := proposal("qp-other", "Собрать отчёт", "ready", nil)
	otherID, err := application.saveMasterWorkOrderV2(context.Background(), &other, nil, "conversation-two", nil)
	if err != nil {
		t.Fatal(err)
	}
	if otherID == firstID {
		t.Fatalf("чужой разговор продолжил наряд первого: %q", otherID)
	}
	if _, err = application.WorkOrderV2(context.Background(), firstID); err != nil {
		t.Fatalf("наряд первого разговора пропал: %v", err)
	}
	others, err := application.store.ListWorkOrdersForConversationV2(context.Background(), "conversation-two")
	if err != nil {
		t.Fatal(err)
	}
	if len(others) != 1 || others[0].ID != otherID {
		t.Fatalf("лента второго разговора неверна: %#v", others)
	}
}
