package domain

import "time"

// CloseUnfinishedNodes закрывает этапы прогона Flow, которые уже никто не
// исполнит: квест закрыт, а узлы `waiting_*`, `ready` или `running` карточка
// и полоса «Нужно ваше решение» показывали живыми (TODO Q14). Узел
// становится `skipped` с причиной. Возвращает, изменилось ли что-то.
func (run *FlowRun) CloseUnfinishedNodes(reason string, now time.Time) bool {
	changed := false
	for id, state := range run.NodeStates {
		switch state.Status {
		case "blocked", "ready", "running", "waiting_agent", "waiting_approval", "waiting_join":
			state.Status = "skipped"
			state.Error = reason
			if state.Output == nil {
				state.Output = map[string]any{}
			}
			state.Output["closedWithQuest"] = true
			finished := now
			state.FinishedAt = &finished
			run.NodeStates[id] = state
			changed = true
		}
	}
	return changed
}
