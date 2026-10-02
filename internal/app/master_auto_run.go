package app

import (
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
