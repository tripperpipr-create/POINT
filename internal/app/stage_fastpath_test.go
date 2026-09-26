package app

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/masterskills"
)

func TestStageAllowsLLMBypassOnlyForInheritedSerialStages(t *testing.T) {
	if !stageAllowsLLMBypass(domain.StageRoleIntegrate, "inherited", "php-symfony-7") {
		t.Fatal("Symfony integrate with prepared shared files should bypass LLM")
	}
	if stageAllowsLLMBypass(domain.StageRoleIntegrate, "inherited", "recommended-web") {
		t.Fatal("generic integrate still needs to create missing shared manifests and deployment files")
	}
	if !stageAllowsLLMBypass(domain.StageRoleImplReview, "inherited", "recommended-web") {
		t.Fatal("serial impl_review should bypass LLM")
	}
	if stageAllowsLLMBypass(domain.StageRoleIntegrate, "merged_parallel_join", "php-symfony-7") {
		t.Fatal("parallel merge integrate must keep LLM/merge agent")
	}
	if stageAllowsLLMBypass(domain.StageRoleImplement, "inherited", "recommended-web") {
		t.Fatal("implement must never bypass LLM")
	}
	if stageAllowsLLMBypass(domain.StageRoleAccept, "inherited", "recommended-web") {
		t.Fatal("accept verification must never bypass LLM via inheritance shortcut")
	}
}

func TestCriteriaSupportDeterministicAccept(t *testing.T) {
	exit := 0
	ok := &domain.TaskBrief{Criteria: []domain.AcceptanceCriterion{{
		ID: "v1", Kind: "verification", Tool: "run_command",
		Arguments: json.RawMessage(`{"command":"php bin/phpunit"}`), ExpectedExitCode: &exit,
	}}}
	if !criteriaSupportDeterministicAccept(ok) {
		t.Fatal("declared run_command criteria should be deterministic")
	}
	manual := &domain.TaskBrief{Criteria: []domain.AcceptanceCriterion{{ID: "m1", Kind: "manual", Text: "looks good"}}}
	if criteriaSupportDeterministicAccept(manual) {
		t.Fatal("manual criteria must keep LLM accept")
	}
}

func TestApprovedWorkOrderAcceptKeepsManualCriterionPending(t *testing.T) {
	brief := &domain.TaskBrief{Criteria: []domain.AcceptanceCriterion{
		{ID: "build", Kind: "verification", Tool: "run_command", Arguments: json.RawMessage(`{"command":"docker compose build"}`)},
		{ID: "health-down", Kind: "manual", Text: "Check outage and recovery"},
	}}
	if criteriaSupportDeterministicAccept(brief) || !criteriaSupportWorkOrderAcceptV2(brief) {
		t.Fatal("approved mixed criteria must use deterministic accept and leave manual work pending")
	}
	brief.Criteria[0].Tool = "custom_tool"
	if criteriaSupportWorkOrderAcceptV2(brief) {
		t.Fatal("unsupported machine criteria must not be silently accepted")
	}
}

func TestOnlyApprovedComposeCriteriaAreDeferred(t *testing.T) {
	criterion := domain.AcceptanceCriterion{
		ID: "health-200", Kind: "verification", Tool: "run_command",
		Arguments: json.RawMessage(`{"command":"docker compose up -d && curl -fsS http://localhost:8080/health"}`),
	}
	order := domain.WorkOrder{Criteria: []domain.AcceptanceCriterion{criterion}}
	if !deferredComposeCriterionV2(order, criterion) {
		t.Fatal("approved Compose verification must run after delivery")
	}
	formatted := criterion
	formatted.Arguments = json.RawMessage(`{ "command": "docker compose up -d && curl -fsS http://localhost:8080/health" }`)
	if !deferredComposeCriterionV2(order, formatted) {
		t.Fatal("equivalent JSON formatting must retain approval")
	}
	modified := criterion
	modified.Arguments = json.RawMessage(`{"command":"docker compose down"}`)
	if deferredComposeCriterionV2(order, modified) {
		t.Fatal("a command not frozen in the WorkOrder must not run on the host")
	}
	ordinary := criterion
	ordinary.Arguments = json.RawMessage(`{"command":"go test ./..."}`)
	if deferredComposeCriterionV2(domain.WorkOrder{Criteria: []domain.AcceptanceCriterion{ordinary}}, ordinary) {
		t.Fatal("ordinary verification belongs in the isolated Accept stage")
	}
}

func TestOnlyComposeVerificationNodeIsDeferred(t *testing.T) {
	criterion := domain.AcceptanceCriterion{
		ID: "health-200", Kind: "verification", Tool: "run_command",
		Arguments: json.RawMessage(`{"command":"docker compose up -d && curl -fsS http://localhost:8080/health"}`),
	}
	order := domain.WorkOrder{Criteria: []domain.AcceptanceCriterion{criterion}}
	verify := domain.FlowNode{Kind: domain.FlowNodeAgent, Name: "Verify compose health", Config: map[string]any{
		"criterionIds": []any{criterion.ID},
	}}
	if !hostComposeVerificationNodeV2(verify, order) {
		t.Fatal("model-planned Compose verification must be deferred to delivery")
	}
	implement := verify
	implement.Name = "Implement Go health service"
	if hostComposeVerificationNodeV2(implement, order) {
		t.Fatal("implementation node must still write the service")
	}
	verify.Config = map[string]any{"criterionIds": []any{criterion.ID, "unknown"}}
	if hostComposeVerificationNodeV2(verify, order) {
		t.Fatal("a node with unaccounted criteria must not be skipped")
	}
}

func TestModelPlannedManualDockerStageIsDeferred(t *testing.T) {
	order := domain.WorkOrder{Criteria: []domain.AcceptanceCriterion{
		{ID: "c3", Kind: "manual", Text: "docker compose up and GET /healthz returns 200"},
		{ID: "c4", Kind: "manual", Text: "PostgreSQL outage returns 503"},
	}}
	node := domain.FlowNode{Kind: domain.FlowNodeAgent, Name: "Проверка Docker и PostgreSQL", Config: map[string]any{
		"planner": "model", "criterionIds": []any{"c3", "c4"},
		"instruction": "Подними docker compose и проверь ответ сервиса",
	}}
	if !hostManualComposeNodeV2(node, order) {
		t.Fatal("manual host-only Docker stage must not start inside the sandbox")
	}
	node.Config["criterionIds"] = []any{"c3", "unknown"}
	if hostManualComposeNodeV2(node, order) {
		t.Fatal("unknown criterion prevents stage bypass")
	}
	node.Config["criterionIds"] = []any{"c3", "c4"}
	node.Config["planner"] = "human"
	if hostManualComposeNodeV2(node, order) {
		t.Fatal("human-authored stage must keep its execution")
	}
	node.Config["planner"] = "model"
	order.Criteria[0].Kind = "verification"
	if hostManualComposeNodeV2(node, order) {
		t.Fatal("machine verification cannot be skipped as manual")
	}
}

func TestManagedComposeCriteriaUseApprovedLocalTestURL(t *testing.T) {
	base := "http://localhost:8080"
	start := "docker compose up -d && timeout 90 bash -c 'until curl -sf " + base + "/health >/dev/null; do sleep 2; done'"
	outage := "docker compose stop postgres && sleep 3 && [ \"$(curl -s -o /dev/null -w '%{http_code}' " + base + "/health)\" = \"503\" ] && [ \"$(curl -s -o /dev/null -w '%{http_code}' " + base + "/live)\" = \"200\" ]"
	order := domain.WorkOrder{}
	if kind, url := managedComposeCriterionKindV2(order, start); kind != "health-200" || url != base {
		t.Fatalf("start criterion classified as %q at %q", kind, url)
	}
	if kind, url := managedComposeCriterionKindV2(order, outage); kind != "health-503-live-200" || url != base {
		t.Fatalf("outage criterion classified as %q at %q", kind, url)
	}
	order.Delivery.ApplicationURL = "http://localhost:9999"
	if kind, _ := managedComposeCriterionKindV2(order, start); kind != "" {
		t.Fatal("criterion URL must match the approved delivery URL when one is set")
	}
}

// Навык критериев называет Мастеру команды, которые Point проверит на хосте
// после доставки. Разойдись он с классификатором — Мастер снова писал бы
// проверки, которые никто не исполнит: «docker compose up -d --build» не
// имеет управляемой формы, а цикл с curl уходит в песочницу без Docker.
func TestMasterCriteriaSkillNamesOnlyManagedComposeChecks(t *testing.T) {
	var instructions string
	for _, skill := range masterskills.Builtins() {
		if skill.ID == masterskills.Criteria {
			instructions = skill.Instructions
		}
	}
	commands := regexp.MustCompile("`((?:docker compose|curl -sf) [^`]+)`").FindAllStringSubmatch(instructions, -1)
	structured := regexp.MustCompile("`(\\{[^`]+\\})`").FindAllStringSubmatch(instructions, -1)
	if len(commands) < 2 || len(structured) < 2 {
		t.Fatalf("навык критериев перестал называть управляемые проверки Compose: команд %d, структурных форм %d", len(commands), len(structured))
	}
	forms := []json.RawMessage{}
	for _, match := range commands {
		arguments, _ := json.Marshal(map[string]string{"command": strings.ReplaceAll(match[1], "ПОРТ", "8080")})
		forms = append(forms, arguments)
	}
	for _, match := range structured {
		form := strings.NewReplacer("ПОРТ", "8080", "СЕРВИС", "postgres").Replace(match[1])
		if !json.Valid([]byte(form)) {
			t.Fatalf("структурная форма навыка — не JSON: %s", form)
		}
		forms = append(forms, json.RawMessage(form))
	}
	for _, arguments := range forms {
		criterion := domain.AcceptanceCriterion{ID: "c", Kind: "verification", Tool: "run_command", Arguments: arguments}
		order := domain.WorkOrder{Criteria: []domain.AcceptanceCriterion{criterion}}
		if !deferredHostCriterionV2(order, criterion) {
			t.Fatalf("%s не будет отложена до проверки на хосте", arguments)
		}
		if check := managedHostCheckFromArgsV2(order, arguments); check.Kind == "" {
			t.Fatalf("%s не имеет управляемой проверки на хосте", arguments)
		}
	}
}

func TestModelPlannedWorkOrderNodeUsesImplementationBrief(t *testing.T) {
	node := domain.FlowNode{Kind: domain.FlowNodeAgent, Name: "Implement Go service"}
	if got := workOrderExecutionStageRoleV2(node, true); got != domain.StageRoleImplement {
		t.Fatalf("approved work node role=%q", got)
	}
	if got := workOrderExecutionStageRoleV2(node, false); got != "" {
		t.Fatalf("legacy flow role changed to %q", got)
	}
	node.Config = map[string]any{"stageRole": domain.StageRoleAccept}
	if got := workOrderExecutionStageRoleV2(node, true); got != domain.StageRoleAccept {
		t.Fatalf("explicit Accept role changed to %q", got)
	}
}

func TestDeterministicAcceptFailureDetailShowsMissingTool(t *testing.T) {
	result := domain.ToolResult{Output: json.RawMessage(`{"exitCode":127,"stderr":"/bin/sh: docker: not found\nsecond line"}`)}
	if got := deterministicAcceptFailureDetail(result); got != "/bin/sh: docker: not found" {
		t.Fatalf("failure detail=%q", got)
	}
}

func TestContinueAfterDeterministicStageTerminalStatuses(t *testing.T) {
	failed := domain.FlowRun{ID: "fr-fail", Status: domain.RunFailed}
	completed := domain.FlowRun{ID: "fr-ok", Status: domain.RunCompleted}
	// No quest id → finalize is a no-op; ensures the switch does not panic.
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	if err = application.continueAfterDeterministicStage(failed); err != nil {
		t.Fatal(err)
	}
	if err = application.continueAfterDeterministicStage(completed); err != nil {
		t.Fatal(err)
	}
}

func TestApplyStageExecutionBudgetPreservesImplementCeiling(t *testing.T) {
	profile := domain.DefaultProfile()
	profile.MaxOutputTokens = 65536
	profile.MaxSteps = 100
	profile.ReasoningEffort = "medium"
	applyStageExecutionBudget(&profile, domain.StageRoleImplement)
	if profile.MaxOutputTokens != 16384 {
		t.Fatalf("implement output cap=%d", profile.MaxOutputTokens)
	}
	if profile.MaxSteps != 100 {
		t.Fatalf("implement max steps must stay %d, got %d", 100, profile.MaxSteps)
	}
	if profile.ReasoningEffort != "low" {
		t.Fatalf("reasoning=%q", profile.ReasoningEffort)
	}

	review := domain.DefaultProfile()
	review.MaxOutputTokens = 65536
	review.MaxSteps = 100
	applyStageExecutionBudget(&review, domain.StageRoleImplReview)
	if review.MaxSteps != 16 || review.MaxOutputTokens != 4096 {
		t.Fatalf("review budget steps=%d output=%d", review.MaxSteps, review.MaxOutputTokens)
	}
}
