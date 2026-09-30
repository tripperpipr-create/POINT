package orchestrator

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

// Отряд из одного агента планируется без модели: модель, если её позвать,
// роняет тест.
func TestSingleLockedAgentIsPlannedWithoutModel(t *testing.T) {
	planner := Planner{NewModel: func(providers.Config) (providers.Model, error) {
		t.Fatal("planner model must not be called for a single agent")
		return nil, nil
	}}
	request := testPlannerRequest()
	request.LockedAgentIDs = []string{request.Agents[0].ID}
	request.Proposal.Brief = &domain.TaskBrief{
		Mode: domain.TaskModeProject, Goal: "Деплой пакета на незащищённые ветки",
		Scope:     []string{"cf-vue-apps/.gitlab-ci.yml"},
		Decisions: []domain.BriefDecision{{Topic: "ветки", Decision: "незащищённые ветки, не default branch", Source: "user"}},
		Criteria: []domain.AcceptanceCriterion{
			{ID: "build", Text: "npm run verify проходит", Kind: "verification"},
			{ID: "health", Text: "сервис отвечает", Kind: "verification"},
		},
	}
	request.Environment = &ExecutionEnvironment{HostVerifiedCriteria: []string{"health"}}
	result, err := planner.Plan(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != SingleAgentPlanModel || len(result.Plan.Stages) != 1 {
		t.Fatalf("result=%#v", result)
	}
	stage := result.Plan.Stages[0]
	if stage.AgentID != request.Agents[0].ID || !slices.Equal(stage.CriterionIDs, []string{"build"}) {
		t.Fatalf("stage=%#v", stage)
	}
	if !strings.Contains(stage.Instruction, "незащищённые ветки, не default branch") || !strings.Contains(stage.Instruction, "проверит Point на хосте") {
		t.Fatalf("instruction lost the brief: %s", stage.Instruction)
	}
}

// Один писатель: Integrate и пустой review не вставляются, приёмка остаётся,
// а писатель получает права на общие манифесты своего вложенного проекта.
func TestSingleWriterPipelineSkipsIntegrateAndReview(t *testing.T) {
	flow := CompileModelFlow(CompileRequest{Title: "T", AgentIDs: []string{"a"}}, ModelPlan{
		AgentIDs: []string{"a"},
		Stages: []PlanStage{{Name: "Сделать", AgentID: "a", Instruction: "do", Phase: 1,
			OwnedPaths: []string{"cf-vue-apps/.gitlab-ci.yml", "cf-vue-apps/scripts"}, ForbiddenPaths: []string{"cf-vue-apps/package-lock.json", "package-lock.json"}}},
	}, "model")
	got := EnsureProjectPipeline(flow, []string{"a"})
	var writer *domain.FlowNode
	acceptAgent := false
	for index, node := range got.Nodes {
		switch domain.FlowNodeStageRole(node) {
		case domain.StageRoleIntegrate, domain.StageRoleImplReview:
			t.Fatalf("single writer got %s stage", node.Name)
		case domain.StageRoleAccept:
			acceptAgent = acceptAgent || node.Kind == domain.FlowNodeAgent
		}
		if domain.FlowNodeWriteFiles(node) {
			writer = &got.Nodes[index]
		}
	}
	if !acceptAgent || writer == nil {
		t.Fatalf("accept=%v writer=%v", acceptAgent, writer)
	}
	raw, _ := json.Marshal(writer.Config["workContract"])
	var contract domain.WorkContract
	_ = json.Unmarshal(raw, &contract)
	if !slices.Contains(contract.OwnedPaths, "cf-vue-apps/package-lock.json") || !slices.Contains(contract.OwnedPaths, "package.json") {
		t.Fatalf("writer did not get integrate ownership: %v", contract.OwnedPaths)
	}
	if len(contract.ForbiddenPaths) != 0 {
		t.Fatalf("shared manifests still forbidden: %v", contract.ForbiddenPaths)
	}
}

func TestTwoWritersKeepIntegrateStage(t *testing.T) {
	flow := CompileModelFlow(CompileRequest{Title: "T", AgentIDs: []string{"a", "b"}}, ModelPlan{
		AgentIDs: []string{"a", "b"},
		Stages: []PlanStage{
			{Name: "API", AgentID: "a", Instruction: "api", Phase: 1, OwnedPaths: []string{"api"}},
			{Name: "UI", AgentID: "b", Instruction: "ui", Phase: 1, OwnedPaths: []string{"ui"}},
		},
	}, "model")
	got := EnsureProjectPipeline(flow, []string{"a", "b"})
	integrate := false
	for _, node := range got.Nodes {
		if domain.FlowNodeStageRole(node) == domain.StageRoleIntegrate {
			integrate = true
		}
	}
	if !integrate {
		t.Fatal("parallel writers must still be integrated")
	}
}
