package orchestrator

import (
	"fmt"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/textutil"
)

// Политика Мастера, посчитанная тем же кодом, который её исполняет.
//
// Раньше превью настройки жило в JS и пересказывало правила своими словами:
// «высокая параллельность» там, где движок даёт ровно четырёх агентов. Две
// реализации одного правила неизбежно расходятся, и расходились — человек
// настраивал одно, а получал другое.
//
// Здесь одна функция и один ответ: что показано, то и произойдёт.

type Policy struct {
	PartySize             int  `json:"partySize"`
	MaxSubquestSteps      int  `json:"maxSubquestSteps"`
	MaxConcurrentAgents   int  `json:"maxConcurrentAgents"`
	RequiresExplicitStart bool `json:"requiresExplicitStart"`
	UsesModelPlanner      bool `json:"usesModelPlanner"`
	// Lines — те же числа человеческим языком. Порядок неслучаен: сначала кого
	// назначит, потом сколько работы разом, потом чем планирует.
	Lines []string `json:"lines"`
}

// DescribePolicy переводит конфигурацию в наблюдаемое поведение.
func DescribePolicy(cfg domain.OrchestratorConfig) Policy {
	party := PartySize(cfg)
	steps := MaxSubquestSteps(cfg)
	concurrent := MaxConcurrentAgents(cfg)
	// Строгость применяется как approval-гейты внутри собранного Flow, а не
	// отказом запускать: квест стартует, но останавливается перед выводом.
	strict := cfg.Preset == "conservative" || cfg.ApprovalStrictness >= 80
	model := UsesModelPlanner(cfg)

	lines := []string{
		fmt.Sprintf("Назначит %d %s, если отряд не выбран вручную.",
			party, textutil.Plural(party, "агента", "агентов", "агентов")),
		fmt.Sprintf("Разобьёт квест не более чем на %d %s.",
			steps, textutil.Plural(steps, "подзадачу", "подзадачи", "подзадач")),
		fmt.Sprintf("Одновременно работают до %d %s.",
			concurrent, textutil.Plural(concurrent, "агента", "агентов", "агентов")),
	}
	if strict {
		lines = append(lines, "Перед выводом результата Flow остановится и попросит подтверждение.")
	} else {
		lines = append(lines, "После вашего Start собранный Flow идёт без промежуточных подтверждений.")
	}
	if model {
		lines = append(lines, fmt.Sprintf("Планирует модель %s; при её сбое — детерминированный движок Point.", cfg.Model))
	} else {
		lines = append(lines, "Планирует детерминированный движок Point, без обращения к модели.")
	}

	return Policy{
		PartySize:             party,
		MaxSubquestSteps:      steps,
		MaxConcurrentAgents:   concurrent,
		RequiresExplicitStart: strict,
		UsesModelPlanner:      model,
		Lines:                 lines,
	}
}
