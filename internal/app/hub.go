// Общая почва сценариев Гильдии.
//
// Сами сценарии живут по семействам рядом: hub_bootstrap, agent_blueprints,
// hub_skills, hub_quests, hub_flows, hub_changesets. Здесь остаётся то, чем
// пользуются все они и что не принадлежит ни одному.
package app

import (
	"errors"
	"maps"

	"local-agent-workbench/internal/domain"
)

func (a *App) requireWorkspace() (domain.Workspace, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.currentWorkspace == nil {
		return domain.Workspace{}, errors.New("workspace is not open")
	}
	return *a.currentWorkspace, nil
}

// Пустая карта возвращается как nil: вызывающие отличают «нет данных» от
// «есть пустая карта», и maps.Clone сам по себе этого различия не делает.
func cloneMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	return maps.Clone(values)
}
