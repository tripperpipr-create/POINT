package app

import (
	"local-agent-workbench/internal/domain"
	"testing"
)

func TestMasterAutoRunBoundaries(t *testing.T) {
	agent := domain.ProjectAgent{ID: "reader", AllowedTools: []string{"read_file", "search_code"}}
	base := domain.TaskBrief{Mode: domain.TaskModePrecise, State: "ready", Budget: domain.TaskBudget{Tokens: 20000, ActiveSeconds: 120, MaxParallel: 1, MaxAttempts: 1, MaxReplans: 1}}
	proposal := domain.QuestProposal{Brief: &base, TeamAgentIDs: []string{agent.ID}}
	if err := masterAutoRunAllowed(proposal, []domain.ProjectAgent{agent}); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*domain.TaskBrief){
		"write":      func(b *domain.TaskBrief) { b.Permissions.WriteFiles = true },
		"commands":   func(b *domain.TaskBrief) { b.Permissions.ExecuteCommands = true },
		"network":    func(b *domain.TaskBrief) { b.Permissions.NetworkHosts = []string{"example.com"} },
		"tokens":     func(b *domain.TaskBrief) { b.Budget.Tokens++ },
		"time":       func(b *domain.TaskBrief) { b.Budget.ActiveSeconds++ },
		"attempts":   func(b *domain.TaskBrief) { b.Budget.MaxAttempts++ },
		"replans":    func(b *domain.TaskBrief) { b.Budget.MaxReplans++ },
		"parallel":   func(b *domain.TaskBrief) { b.Budget.MaxParallel++ },
		"unresolved": func(b *domain.TaskBrief) { b.OpenQuestions = []string{"Clarify scope"} },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			b := base
			change(&b)
			proposal.Brief = &b
			if masterAutoRunAllowed(proposal, []domain.ProjectAgent{agent}) == nil {
				t.Fatal("scope escape allowed")
			}
		})
	}
	proposal.Brief = &base
	agent.AllowedTools = append(agent.AllowedTools, "run_command")
	if masterAutoRunAllowed(proposal, []domain.ProjectAgent{agent}) == nil {
		t.Fatal("unsafe agent tools allowed")
	}
}
