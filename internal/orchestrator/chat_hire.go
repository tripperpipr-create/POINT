// Подбор роли под задачу и объяснение выбора.
//
// Пустой ростер не заканчивает разговор: Мастер предлагает роль и говорит,
// почему именно её. Найм остаётся решением человека.
package orchestrator

import (
	"fmt"
	"sort"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/textutil"
)

// blueprintHaystack — весь текст чертежа, по которому его можно узнать.
func blueprintHaystack(blueprint domain.AgentBlueprint) string {
	return strings.Join([]string{
		blueprint.Name, blueprint.RoleDescription, blueprint.Mission,
		strings.Join(blueprint.Goals, " "), strings.Join(blueprint.SkillIDs, " "),
		strings.Join(blueprint.AllowedTools, " "),
	}, " ")
}

// RankHires выстраивает чертежи по близости к задаче. Наблюдатель ростера
// показывает человеку несколько вариантов, а разговор берёт первый: подбор у
// них один, иначе карточка найма и реплика Мастера начинают советовать разное.
func RankHires(message string, blueprints []domain.AgentBlueprint, limit int) []HireSuggestion {
	if len(blueprints) == 0 || limit <= 0 {
		return nil
	}
	type ranked struct {
		suggestion HireSuggestion
		score      int
		order      int
	}
	candidates := make([]ranked, 0, len(blueprints))
	for index, blueprint := range blueprints {
		matched := matchedTokens(message, blueprintHaystack(blueprint))
		why := "подходит как универсальная отправная точка — уточните задачу, и я предложу точнее"
		if len(matched) > 0 {
			why = "совпало с задачей: " + strings.Join(matched, ", ")
		}
		candidates = append(candidates, ranked{
			suggestion: HireSuggestion{
				BlueprintID: blueprint.ID,
				Name:        blueprint.Name,
				Role:        strings.TrimSpace(blueprint.RoleDescription),
				Why:         why,
				Tools:       blueprint.AllowedTools,
				Matched:     matched,
			},
			score: len(matched),
			order: index,
		})
	}
	// Порядок каталога — запасной ключ: при равном совпадении список не должен
	// перетасовываться от хода к ходу, иначе человек видит каждый раз другой
	// «лучший» чертёж без единой причины.
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].order < candidates[j].order
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	result := make([]HireSuggestion, 0, len(candidates))
	for _, candidate := range candidates {
		result = append(result, candidate.suggestion)
	}
	return result
}

// suggestHire подбирает чертёж под задачу. Точного попадания не требуется:
// человеку нужна отправная точка с объяснением, а не безошибочный выбор.
func suggestHire(message string, blueprints []domain.AgentBlueprint) *HireSuggestion {
	ranked := RankHires(message, blueprints, 1)
	if len(ranked) == 0 {
		return nil
	}
	best := ranked[0]
	return &best
}

// matchedTokens — слова задачи, встретившиеся в тексте. Общий случай того же,
// что matchedTerms делает для агента.
func matchedTokens(goal, haystack string) []string {
	inHaystack := map[string]bool{}
	for _, token := range textutil.Tokens(haystack) {
		inHaystack[token] = true
	}
	seen := map[string]bool{}
	matched := make([]string, 0, 4)
	for _, token := range textutil.Tokens(goal) {
		if len(matched) >= 4 {
			break
		}
		if inHaystack[token] && !seen[token] {
			seen[token] = true
			matched = append(matched, token)
		}
	}
	return matched
}

// matchedTerms возвращает слова задачи, встретившиеся в описании агента. Это и
// есть причина выбора на языке пользователя: не «оценка 36», а «совпало:
// тесты, биллинг».
func matchedTerms(agent domain.ProjectAgent, goal string) []string {
	haystack := textutil.Tokens(strings.Join([]string{
		agent.Name, agent.RoleDescription, agent.Mission,
		strings.Join(agent.Goals, " "), strings.Join(agent.SkillIDs, " "),
	}, " "))
	inHaystack := make(map[string]bool, len(haystack))
	for _, token := range haystack {
		inHaystack[token] = true
	}
	seen := map[string]bool{}
	matched := make([]string, 0, 4)
	for _, token := range textutil.Tokens(goal) {
		if len(matched) >= 4 {
			break
		}
		if inHaystack[token] && !seen[token] {
			seen[token] = true
			matched = append(matched, token)
		}
	}
	return matched
}

// chatPartyWhy — почему отряд такой. В разговоре это не то же, что при запуске.
//
// AssignParty — реализация запасного пути, и при настроенной модели она честно
// помечает себя «модель недоступна, детерминированный выбор»: там, откуда её
// зовут при старте квеста, планировщик действительно пробовал и не смог.
// В разговоре не пробовал никто. Отряд здесь предварительный — его показывает
// движок Point, — а при запуске состав пересоберёт модель, и он может выйти
// другим. Чужая формулировка сообщала бы о поломке там, где всё исправно, и
// молчала бы о пересборке, из-за которой запущенный отряд не совпадёт с
// показанным. Строка эта не только на экране: она же уходит в предложение
// квеста и оттуда — в очередь решений.
func chatPartyWhy(cfg domain.OrchestratorConfig, assignment Assignment) string {
	if !UsesModelPlanner(cfg) {
		return assignment.Reason
	}
	return fmt.Sprintf("пресет %s · отряд %d · предварительно, движком Point · при запуске состав пересоберёт модель %s",
		cfg.Preset, len(assignment.AgentIDs), cfg.Model)
}
