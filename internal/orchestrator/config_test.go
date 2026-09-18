package orchestrator

import (
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

func TestPresetDefaultsRejectUnknown(t *testing.T) {
	if _, ok := PresetDefaults("balanced"); ok {
		t.Fatal("companion preset must not be a valid orchestrator preset")
	}
}

func TestSelectPartyDispatcherIsSolo(t *testing.T) {
	cfg, ok := PresetDefaults("dispatcher")
	if !ok {
		t.Fatal("dispatcher preset missing")
	}
	agents := []domain.ProjectAgent{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	got := SelectParty(cfg, agents, nil)
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("dispatcher party=%v", got)
	}
}

func TestSelectPartyDispatcherShrinksCompanionProposal(t *testing.T) {
	cfg, _ := PresetDefaults("dispatcher")
	got := SelectParty(cfg, []domain.ProjectAgent{{ID: "a"}, {ID: "b"}}, []string{"b", "a"})
	if len(got) != 1 || got[0] != "b" {
		t.Fatalf("dispatcher must take the first recommended agent: %v", got)
	}
}

func TestSelectPartyConductorKeepsRecommendedParty(t *testing.T) {
	cfg, _ := PresetDefaults("conductor")
	got := SelectParty(cfg, []domain.ProjectAgent{{ID: "a"}, {ID: "b"}}, []string{"b", "a"})
	if len(got) != 2 || got[0] != "b" || got[1] != "a" {
		t.Fatalf("conductor party=%v", got)
	}
}

func TestAssignPartyPrefersGoalFitOverStoreOrder(t *testing.T) {
	cfg, _ := PresetDefaults("conductor")
	agents := []domain.ProjectAgent{
		{ID: "coder", Name: "Coder", RoleDescription: "frontend ui engineer"},
		{ID: "sec", Name: "Guard", RoleDescription: "security review and auth audit"},
		{ID: "qa", Name: "QA", RoleDescription: "test verifier qa"},
	}
	got := AssignParty(cfg, agents, nil, "critical oauth security review")
	if len(got.AgentIDs) == 0 || got.AgentIDs[0] != "sec" {
		t.Fatalf("expected security agent first, got %#v", got)
	}
	if !strings.Contains(got.Reason, "движок Point") {
		t.Fatalf("reason=%q", got.Reason)
	}
}

func TestAssignPartyKeepsCompanionPriorThenFills(t *testing.T) {
	cfg, _ := PresetDefaults("conductor")
	agents := []domain.ProjectAgent{
		{ID: "a", Name: "A", RoleDescription: "backend api"},
		{ID: "b", Name: "B", RoleDescription: "frontend"},
		{ID: "c", Name: "C", RoleDescription: "security review"},
	}
	got := AssignParty(cfg, agents, []string{"b"}, "backend api migration")
	if len(got.AgentIDs) != 3 {
		t.Fatalf("party=%v", got.AgentIDs)
	}
	if got.AgentIDs[0] != "b" {
		t.Fatalf("companion prior must stay first when scores allow: %v", got.AgentIDs)
	}
}

func TestAssignPartyModelConfiguredUsesFallbackMode(t *testing.T) {
	cfg, _ := PresetDefaults("conductor")
	cfg.Provider = domain.ProviderOllama
	cfg.Model = "llama3"
	got := AssignParty(cfg, []domain.ProjectAgent{{ID: "a"}}, nil, "")
	if got.Mode != "model-fallback" {
		t.Fatalf("mode=%s", got.Mode)
	}
}

func TestAssignPartyUsesConfirmedEvidenceAndCurrentLoadWithBreakdown(t *testing.T) {
	cfg, _ := PresetDefaults("balanced")
	cfg.TeamPreference = 0
	cfg.Parallelism = 0
	agents := []domain.ProjectAgent{
		{ID: "busy", Name: "Busy", RoleDescription: "backend API", AllowedTools: []string{"read_file", "run_command"}},
		{ID: "proven", Name: "Proven", RoleDescription: "backend API", AllowedTools: []string{"read_file", "run_command"}},
	}
	signals := map[string]CandidateSignal{
		"busy":   {Attempts: 10, ConfirmedSuccesses: 10, ActiveExecutions: 3},
		"proven": {Attempts: 8, ConfirmedSuccesses: 7},
	}
	got := AssignPartyWithSignals(cfg, agents, nil, "backend API", signals)
	if len(got.AgentIDs) == 0 || got.AgentIDs[0] != "proven" {
		t.Fatalf("active load was not reflected: %#v", got)
	}
	if len(got.Breakdown) == 0 || got.Breakdown[0].Evidence == 0 || got.Breakdown[0].Total == 0 {
		t.Fatalf("selection is not reproducible: %#v", got.Breakdown)
	}
}

func TestShouldAutoStartFlowConservativeRequiresRequest(t *testing.T) {
	cfg, _ := PresetDefaults("conservative")
	if ShouldAutoStartFlow(cfg, false) {
		t.Fatal("conservative orchestrator must not auto-start Flow without Start")
	}
	if !ShouldAutoStartFlow(cfg, true) {
		t.Fatal("explicit Start must start Flow")
	}
	conductor, _ := PresetDefaults("conductor")
	if !ShouldAutoStartFlow(conductor, false) {
		t.Fatal("conductor keeps current Start default")
	}
}

func TestCompileFlowBindsFullPartyAndApproval(t *testing.T) {
	cfg, _ := PresetDefaults("conservative")
	flow := CompileFlow(CompileRequest{
		Title: "Auth", Importance: domain.QuestImportant,
		AgentIDs: []string{"p", "r", "s"}, PlanningDepth: cfg.PlanningDepth,
		Parallelism: cfg.Parallelism, ApprovalStrictness: cfg.ApprovalStrictness, Preset: cfg.Preset,
	})
	agents := map[string]bool{}
	hasApproval := false
	for _, node := range flow.Nodes {
		if node.Kind == domain.FlowNodeAgent && node.AgentID != "" {
			agents[node.AgentID] = true
		}
		if node.Kind == domain.FlowNodeApproval {
			hasApproval = true
		}
	}
	if !agents["p"] || !agents["r"] || !agents["s"] {
		t.Fatalf("expected full party in flow nodes, got %#v", agents)
	}
	if !hasApproval {
		t.Fatal("conservative must compile an approval gate")
	}
}

func TestCompileFlowParallelismEscalatesShape(t *testing.T) {
	flow := CompileFlow(CompileRequest{
		Title: "Ship", Importance: domain.QuestImportant,
		AgentIDs: []string{"a", "b"}, PlanningDepth: 50, Parallelism: 80, Preset: "dispatcher",
	})
	hasParallel := false
	for _, node := range flow.Nodes {
		if node.Kind == domain.FlowNodeParallel {
			hasParallel = true
		}
	}
	if !hasParallel {
		t.Fatal("high parallelism must prefer independent branches")
	}
}

func TestMaxSubquestSteps(t *testing.T) {
	shallow := domain.OrchestratorConfig{PlanningDepth: 20}
	if MaxSubquestSteps(shallow) != 1 {
		t.Fatalf("shallow=%d", MaxSubquestSteps(shallow))
	}
	deep, _ := PresetDefaults("conductor")
	if MaxSubquestSteps(deep) != 6 {
		t.Fatalf("deep=%d", MaxSubquestSteps(deep))
	}
}

func TestMaxConcurrentAgentsFromParallelism(t *testing.T) {
	if MaxConcurrentAgents(domain.OrchestratorConfig{Parallelism: 10}) != 1 {
		t.Fatal("low parallelism must serialize agent starts")
	}
	if MaxConcurrentAgents(domain.OrchestratorConfig{Parallelism: 50}) != 2 {
		t.Fatal("mid parallelism must allow two concurrent agents")
	}
	dispatcher, _ := PresetDefaults("dispatcher")
	if MaxConcurrentAgents(dispatcher) != 4 {
		t.Fatalf("dispatcher concurrency=%d", MaxConcurrentAgents(dispatcher))
	}
}
