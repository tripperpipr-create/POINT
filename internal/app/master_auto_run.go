package app

import (
	"context"
	"errors"
	"local-agent-workbench/internal/domain"
)

// The first auto-run policy is deliberately explicit: project-local read tools,
// one agent, no commands or network, and a bounded task. Other work is reviewed.
func masterAutoRunAllowed(proposal domain.QuestProposal, agents []domain.ProjectAgent) error {
	b := proposal.Brief
	if b == nil || b.State != "ready" || b.Mode != domain.TaskModePrecise || len(b.OpenQuestions) > 0 {
		return errors.New("нужно готовое точное задание")
	}
	if b.Permissions.WriteFiles || b.Permissions.ExecuteCommands || len(b.Permissions.NetworkHosts) > 0 {
		return errors.New("автозапуск разрешает только чтение проекта")
	}
	if b.Budget.Tokens <= 0 || b.Budget.Tokens > 20000 || b.Budget.ActiveSeconds <= 0 || b.Budget.ActiveSeconds > 120 || b.Budget.MaxParallel != 1 || b.Budget.MaxAttempts != 1 || b.Budget.MaxReplans != 1 {
		return errors.New("задание выходит за лимиты автозапуска")
	}
	if len(proposal.TeamAgentIDs) != 1 {
		return errors.New("автозапуск разрешает одного исполнителя")
	}
	allowed := map[string]bool{"read_file": true, "list_files": true, "search_text": true, "project_map": true, "search_code": true}
	for _, agent := range agents {
		if agent.ID != proposal.TeamAgentIDs[0] {
			continue
		}
		if domain.IsAgentCLIProvider(agent.Provider) {
			return errors.New("CLI-провайдеры сняты — используйте HTTP API")
		}
		if len(agent.AllowedTools) == 0 {
			return errors.New("у исполнителя не заданы инструменты чтения")
		}
		for _, tool := range agent.AllowedTools {
			if !allowed[tool] {
				return errors.New("инструменты исполнителя выходят за разрешённый список")
			}
		}
		return nil
	}
	return errors.New("исполнитель не найден")
}

func (a *App) tryMasterAutoRun(ctx context.Context, w string, proposal *domain.QuestProposal, key string) (bool, error) {
	enabled, err := a.store.Setting(ctx, "master.auto-run.read-only."+w)
	if err != nil || enabled != "true" || proposal == nil {
		return false, nil
	}
	agents, err := a.store.ListProjectAgents(ctx, w)
	if err != nil {
		return false, err
	}
	if err = masterAutoRunAllowed(*proposal, agents); err != nil {
		return false, err
	}
	// Existing orchestration still owns approvals, validation and execution.
	if a.currentWorldID() != w {
		return false, errors.New("проект переключён; подтвердите запуск в исходном проекте")
	}
	result, err := a.DecideQuestProposalContext(ctx, QuestProposalDecision{ProposalID: proposal.ID, Action: QuestProposalStart, ExpectedVersion: proposal.Brief.Version, ApproveVersion: proposal.Brief.Version, StartFlow: true, OrchestratorAPIKey: key})
	if err != nil {
		return false, err
	}
	*proposal = result.Proposal
	return true, nil
}
