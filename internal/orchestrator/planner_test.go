package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/flowruntime"
	"local-agent-workbench/internal/providers"
)

type plannerModel struct {
	events []providers.ModelEvent
	err    error
}

func (m plannerModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	if len(request.Tools) != 0 {
		return &plannerTestError{"planner request exposed tools"}
	}
	for _, event := range m.events {
		if err := emit(event); err != nil {
			return err
		}
	}
	return m.err
}

type plannerTestError struct{ message string }

func (e *plannerTestError) Error() string { return e.message }

func testPlannerRequest() PlanRequest {
	return PlanRequest{
		Config: domain.OrchestratorConfig{
			Preset: "conductor", Provider: domain.ProviderOpenAI, ProviderPreset: "openai",
			BaseURL: "https://example.test/v1", Model: "planner", Temperature: 0.1,
			MaxOutputTokens: 1000, PlanningDepth: 70, Parallelism: 70, ApprovalStrictness: 40, TeamPreference: 85,
		},
		Proposal: domain.QuestProposal{
			Title: "Secure OAuth callback", Importance: domain.QuestImportant,
			Objectives:       []string{"Implement callback", "Verify tests"},
			Constraints:      []string{"Do not write to live workspace"},
			DefinitionOfDone: []string{"go test ./... exits 0"},
		},
		Agents: []domain.ProjectAgent{
			{ID: "backend", Name: "Backend", RoleDescription: "OAuth implementation", AllowedTools: []string{"read_file", "run_command"}},
			{ID: "reviewer", Name: "Reviewer", RoleDescription: "Security review", AllowedTools: []string{"read_file", "search_code"}},
			{ID: "frontend", Name: "Frontend", RoleDescription: "UI"},
		},
		APIKey: "secret",
	}
}

func TestPlannerProducesValidatedNoToolsPlan(t *testing.T) {
	raw := `{"agentIds":["backend","reviewer"],"rationale":"Backend implements and reviewer verifies independently.","stages":[{"name":"Implement callback","agentId":"backend","instruction":"Implement the callback and run focused tests.","phase":1},{"name":"Security review","agentId":"reviewer","instruction":"Review the implementation and verification evidence.","phase":2}],"requiresApproval":false}`
	planner := Planner{NewModel: func(config providers.Config) (providers.Model, error) {
		if config.APIKey != "secret" || config.Kind != domain.ProviderOpenAI {
			t.Fatalf("provider config=%#v", config)
		}
		return plannerModel{events: []providers.ModelEvent{
			{Kind: providers.EventTextDelta, Delta: raw[:80]},
			{Kind: providers.EventTextDelta, Delta: raw[80:]},
			{Kind: providers.EventUsage, InputTokens: 120, OutputTokens: 40},
		}}, nil
	}}
	result, err := planner.Plan(context.Background(), testPlannerRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "planner" || result.InputTokens != 120 || result.OutputTokens != 40 {
		t.Fatalf("result=%#v", result)
	}
	if strings.Join(result.Plan.AgentIDs, ",") != "backend,reviewer" || len(result.Plan.Stages) != 2 {
		t.Fatalf("plan=%#v", result.Plan)
	}
}

func TestPlannerRejectsToolCallsAndUnknownShape(t *testing.T) {
	request := testPlannerRequest()
	planner := Planner{NewModel: func(providers.Config) (providers.Model, error) {
		return plannerModel{events: []providers.ModelEvent{{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{Name: "run_command", Arguments: json.RawMessage(`{}`)}}}}, nil
	}}
	if _, err := planner.Plan(context.Background(), request); err == nil || !strings.Contains(err.Error(), "tool call") {
		t.Fatalf("expected tool-call rejection, got %v", err)
	}

	invalid := `{"agentIds":["invented"],"rationale":"Looks useful","stages":[{"name":"Do it","agentId":"invented","instruction":"Act","phase":1}],"requiresApproval":false}`
	planner.NewModel = func(providers.Config) (providers.Model, error) {
		return plannerModel{events: []providers.ModelEvent{{Kind: providers.EventTextDelta, Delta: invalid}}}, nil
	}
	if _, err := planner.Plan(context.Background(), request); err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("expected unknown-agent rejection, got %v", err)
	}
}

func TestPlannerRespectsLockedPartyAndRejectsParallelAgentReuse(t *testing.T) {
	request := testPlannerRequest()
	request.LockedAgentIDs = []string{"backend", "reviewer"}
	changed := `{"agentIds":["backend"],"rationale":"Solo","stages":[{"name":"Do it","agentId":"backend","instruction":"Implement and verify.","phase":1}],"requiresApproval":false}`
	planner := Planner{NewModel: func(providers.Config) (providers.Model, error) {
		return plannerModel{events: []providers.ModelEvent{{Kind: providers.EventTextDelta, Delta: changed}}}, nil
	}}
	if _, err := planner.Plan(context.Background(), request); err == nil || !strings.Contains(err.Error(), "user-selected party") {
		t.Fatalf("expected locked-party rejection, got %v", err)
	}

	reused := `{"agentIds":["backend","reviewer"],"rationale":"Parallel","stages":[{"name":"One","agentId":"backend","instruction":"First task.","phase":1},{"name":"Two","agentId":"backend","instruction":"Second task.","phase":1},{"name":"Review","agentId":"reviewer","instruction":"Review both.","phase":2}],"requiresApproval":false}`
	planner.NewModel = func(providers.Config) (providers.Model, error) {
		return plannerModel{events: []providers.ModelEvent{{Kind: providers.EventTextDelta, Delta: reused}}}, nil
	}
	if _, err := planner.Plan(context.Background(), request); err == nil || !strings.Contains(err.Error(), "twice in phase") {
		t.Fatalf("expected parallel-reuse rejection, got %v", err)
	}
}

func TestCompileModelFlowOwnsParallelJoinVerificationAndApproval(t *testing.T) {
	plan := ModelPlan{
		AgentIDs: []string{"backend", "reviewer", "qa"}, Rationale: "Implement in parallel, then verify.", RequiresApproval: false,
		Stages: []PlanStage{
			{Name: "Backend", AgentID: "backend", Instruction: "Implement backend.", Phase: 1},
			{Name: "Review", AgentID: "reviewer", Instruction: "Inspect design.", Phase: 1},
			{Name: "Test", AgentID: "qa", Instruction: "Run verification.", Phase: 2},
		},
	}
	flow := CompileModelFlow(CompileRequest{Title: "OAuth", ApprovalStrictness: 85, Preset: "conservative"}, plan, "planner")
	if err := flowruntime.ValidateGraph(flow); err != nil {
		t.Fatalf("compiled model flow invalid: %v\n%#v", err, flow)
	}
	kinds := map[domain.FlowNodeKind]int{}
	for _, node := range flow.Nodes {
		kinds[node.Kind]++
	}
	if kinds[domain.FlowNodeAgent] != 3 || kinds[domain.FlowNodeParallel] != 1 || kinds[domain.FlowNodeJoin] != 1 || kinds[domain.FlowNodeVerifier] != 1 || kinds[domain.FlowNodeApproval] != 1 {
		t.Fatalf("unexpected model flow kinds=%#v", kinds)
	}
}

func TestValidateStageModelRejectsUnknownZeroPriceAndIncompleteBinding(t *testing.T) {
	candidates := []domain.ModelCandidate{{
		ConnectionID: "conn-1", Model: "qwen", Runtime: "point", Healthy: true, PricingKnown: false,
		Capabilities: []string{"coding"},
	}}
	err := validateStageModel(PlanStage{ConnectionID: "conn-1", Model: "qwen", Runtime: "point"}, candidates)
	if err == nil || !strings.Contains(err.Error(), "not free") {
		t.Fatalf("unknown zero price: %v", err)
	}
	if err = validateStageModel(PlanStage{Model: "qwen", Runtime: "point"}, candidates); err == nil || !strings.Contains(err.Error(), "connectionId") {
		t.Fatalf("incomplete binding: %v", err)
	}
	if err = validateStageModel(PlanStage{ConnectionID: "conn-1", Model: "qwen", Runtime: "point", EstimatedCostCents: 25}, candidates); err != nil {
		t.Fatal(err)
	}
	priced := candidates
	priced[0].PricingKnown = true
	if err = validateStageModel(PlanStage{ConnectionID: "conn-1", Model: "qwen", Runtime: "point"}, priced); err != nil {
		t.Fatal(err)
	}
}

// plannerBudgetModel запоминает предел вывода каждой попытки и первым ходом
// повторяет отказ, с которого всё началось: размышление съело бюджет целиком.
type plannerBudgetModel struct {
	raw       string
	budgets   *[]int
	failFirst bool
}

func (m *plannerBudgetModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	*m.budgets = append(*m.budgets, request.MaxOutputTokens)
	if m.failFirst && len(*m.budgets) == 1 {
		return &plannerTestError{"model returned no answer: the entire output budget of 4096 tokens went to reasoning (finish_reason=length)"}
	}
	if err := emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: m.raw}); err != nil {
		return err
	}
	return emit(providers.ModelEvent{Kind: providers.EventUsage, InputTokens: 10, OutputTokens: 5})
}

// Размышляющей модели планировщик даёт место, а упёршейся — добавляет.
//
// 21 сентября 2026 план собрал движок, потому что потолок планировщика резал
// бюджет до 4096: модель Qwen3.8-27B потратила его на размышление и не ответила
// ничего. Повтора не было вовсе — планировщик не знал лестницы Мастера.
func TestPlannerGivesThinkingModelRoomAndGrowsAfterTruncation(t *testing.T) {
	raw := `{"agentIds":["backend","reviewer"],"rationale":"Backend implements and reviewer verifies independently.","stages":[{"name":"Implement callback","agentId":"backend","instruction":"Implement the callback and run focused tests.","phase":1},{"name":"Security review","agentId":"reviewer","instruction":"Review the implementation and verification evidence.","phase":2}],"requiresApproval":false}`
	budgets := []int{}
	planner := Planner{NewModel: func(providers.Config) (providers.Model, error) {
		return &plannerBudgetModel{raw: raw, budgets: &budgets, failFirst: true}, nil
	}}
	request := testPlannerRequest()
	request.Config.Model = "qwen3-27b"
	result, err := planner.Plan(context.Background(), request)
	if err != nil {
		t.Fatalf("план обязан собраться со второй попытки: %v", err)
	}
	if len(result.Plan.Stages) != 2 {
		t.Fatalf("plan=%#v", result.Plan)
	}
	if len(budgets) != 2 {
		t.Fatalf("попыток должно быть две, а было %d: %v", len(budgets), budgets)
	}
	if budgets[0] < domain.MinThinkingOutputTokens {
		t.Fatalf("размышляющей модели дали %d — меньше пола %d", budgets[0], domain.MinThinkingOutputTokens)
	}
	if budgets[1] <= budgets[0] {
		t.Fatalf("повтор пошёл под тем же потолком: %v", budgets)
	}
}

// Предел вывода планировщика держится в своих рамках, а не в чужих.
//
// Валидатор принимает план на восемь стадий с инструкцией до 2000 рун каждая —
// около одиннадцати тысяч токенов, — поэтому скупой конфиг поднимается до пола.
// Щедрый, наоборот, режется потолком: план не должен резервировать бюджет
// разговора целиком.
func TestPlannerBudgetStaysWithinItsOwnBounds(t *testing.T) {
	raw := `{"agentIds":["backend"],"rationale":"Single owner is enough.","stages":[{"name":"Implement callback","agentId":"backend","instruction":"Implement the callback and run focused tests.","phase":1}],"requiresApproval":false}`
	budgets := []int{}
	planner := Planner{NewModel: func(providers.Config) (providers.Model, error) {
		return &plannerBudgetModel{raw: raw, budgets: &budgets}, nil
	}}
	request := testPlannerRequest()
	request.Config.MaxOutputTokens = 1000
	if _, err := planner.Plan(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(budgets) != 1 || budgets[0] != plannerMinOutputTokens {
		t.Fatalf("скупой конфиг не поднят до пола: %v", budgets)
	}

	budgets = nil
	request.Config.MaxOutputTokens = 40000
	if _, err := planner.Plan(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(budgets) != 1 || budgets[0] != plannerMaxOutputTokens {
		t.Fatalf("щедрый конфиг не срезан потолком: %v", budgets)
	}

	// Семейство со своим низким пределом вывода пол не переступает: у gpt-4 он
	// 4096, и запрос на 8192 провайдер отверг бы целиком.
	budgets = nil
	request.Config.Model = "gpt-4-turbo"
	request.Config.MaxOutputTokens = 1000
	if _, err := planner.Plan(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(budgets) != 1 || budgets[0] != 4096 {
		t.Fatalf("пол переступил потолок семейства: %v", budgets)
	}
}
