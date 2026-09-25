package app

import (
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/workspace"
)

// masterProjectRules — правила репозитория для хода: AGENTS.md и CLAUDE.md из
// корня открытого проекта, очищенные от секретов. Файлы читаются на каждый
// ход: их правят руками, и Мастер не должен отвечать по вчерашним правилам.
func (a *App) masterProjectRules() workspace.ProjectRules {
	a.mu.RLock()
	fs := a.currentFS
	a.mu.RUnlock()
	if fs == nil {
		return workspace.ProjectRules{}
	}
	rules := fs.ProjectRules()
	rules.Text = security.Redact(rules.Text)
	return rules
}
