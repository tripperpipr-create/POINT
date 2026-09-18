package companion

import (
	"fmt"
	"strings"
	"unicode"
)

func companionFlowName(message string) string {
	name := strings.TrimSpace(message)
	lower := strings.ToLower(name)
	for _, prefix := range []string{"создай мне ", "создай ", "сделай мне ", "сделай ", "create ", "build "} {
		if strings.HasPrefix(lower, prefix) {
			name = strings.TrimSpace(name[len(prefix):])
			break
		}
	}
	name = strings.Trim(name, " .:;—-")
	if name == "" || strings.EqualFold(name, "flow") || strings.EqualFold(name, "workflow") || strings.EqualFold(name, "воркфлоу") {
		name = "Рабочий Flow проекта"
	}
	return trim(name, 120)
}

func companionAgentName(message, fallback string) string {
	if explicit := explicitCompanionAgentName(message); explicit != "" {
		return explicit
	}
	lower := strings.ToLower(message)
	for _, item := range []struct{ marker, name string }{
		{"security", "Ревьюер безопасности"}, {"безопас", "Ревьюер безопасности"},
		{"backend", "Разработчик бэкенда"}, {"бэкенд", "Разработчик бэкенда"},
		{"frontend", "Разработчик интерфейса"}, {"фронтенд", "Разработчик интерфейса"},
		{"architect", "Архитектор"}, {"архитект", "Архитектор"},
		{"test", "Инженер по тестам"}, {"qa", "Инженер по тестам"}, {"тест", "Инженер по тестам"},
		{"documentation", "Технический писатель"}, {"документ", "Технический писатель"},
	} {
		if strings.Contains(lower, item.marker) {
			return item.name
		}
	}
	if strings.TrimSpace(fallback) == "" {
		return "Проектный агент"
	}
	return trim(strings.TrimSpace(fallback)+" · проектный", 120)
}

// Явное имя важнее нашей догадки о роли. Фраза «с именем QA-страж» раньше
// содержала маркер qa и превращалась в безликое «QA Engineer»: помощник
// подтверждал не то, что попросил человек.
func explicitCompanionAgentName(message string) string {
	raw := strings.TrimSpace(message)
	lower := strings.ToLower(raw)
	for _, marker := range []string{"с именем ", "по имени ", "named ", "name: ", "имя: "} {
		index := strings.Index(lower, marker)
		if index < 0 {
			continue
		}
		tail := strings.TrimSpace(raw[index+len(marker):])
		if tail == "" {
			return ""
		}
		for _, pair := range [][2]string{{"«", "»"}, {"\"", "\""}, {"'", "'"}} {
			if strings.HasPrefix(tail, pair[0]) {
				unquoted := strings.TrimPrefix(tail, pair[0])
				if end := strings.Index(unquoted, pair[1]); end >= 0 {
					return trim(strings.TrimSpace(unquoted[:end]), 120)
				}
			}
		}
		lowerTail := strings.ToLower(tail)
		stop := len(tail)
		for _, separator := range []string{" для ", " чтобы ", " который ", " которая ", " которое ", " под ", " for ", " who ", " to ", ",", ".", ";"} {
			if found := strings.Index(lowerTail, separator); found >= 0 && found < stop {
				stop = found
			}
		}
		name := strings.Trim(strings.TrimSpace(tail[:stop]), " .:;—-«»\"'")
		return trim(name, 120)
	}
	return ""
}

func companionTeamName(message string) string {
	lower := strings.ToLower(message)
	for _, item := range []struct{ marker, name string }{
		{"security", "Security Team"}, {"безопас", "Security Team"},
		{"backend", "Backend Team"}, {"бэкенд", "Backend Team"},
		{"frontend", "Frontend Team"}, {"фронтенд", "Frontend Team"},
		{"review", "Review Team"}, {"ревью", "Review Team"},
		{"test", "QA Team"}, {"qa", "QA Team"}, {"тест", "QA Team"},
	} {
		if strings.Contains(lower, item.marker) {
			return item.name
		}
	}
	return "Project Team"
}

func companionSkillName(message string) string {
	lower := strings.ToLower(message)
	for _, item := range []struct{ marker, name string }{
		{"postgres", "PostgreSQL Expert"}, {"postgresql", "PostgreSQL Expert"}, {"sql", "SQL Engineering"},
		{"security", "Security Review"}, {"безопас", "Security Review"},
		{"backend", "Backend Development"}, {"бэкенд", "Backend Development"},
		{"frontend", "Frontend Development"}, {"фронтенд", "Frontend Development"},
		{"test", "Test Engineering"}, {"qa", "Test Engineering"}, {"тест", "Test Engineering"},
		{"document", "Documentation"}, {"документ", "Documentation"},
		{"refactor", "Safe Refactoring"}, {"рефактор", "Safe Refactoring"},
		{"api", "API Design"},
	} {
		if strings.Contains(lower, item.marker) {
			return item.name
		}
	}
	name := strings.TrimSpace(message)
	for _, prefix := range []string{"создай мне ", "создай ", "сделай мне ", "сделай ", "create ", "build "} {
		if strings.HasPrefix(strings.ToLower(name), prefix) {
			name = strings.TrimSpace(name[len(prefix):])
			break
		}
	}
	for _, marker := range []string{"skill", "скилл", "навык"} {
		for {
			idx := strings.Index(strings.ToLower(name), marker)
			if idx < 0 {
				break
			}
			name = strings.TrimSpace(name[:idx] + name[idx+len(marker):])
		}
	}
	name = strings.Trim(name, " .:;—-")
	if name == "" {
		return "Project Practice"
	}
	runes := []rune(name)
	runes[0] = unicode.ToUpper(runes[0])
	return trim(string(runes), 120)
}

func companionSkillDescription(message, name string) string {
	return trim(fmt.Sprintf("%s — проверяемая практика для текущего проекта, подготовленная по запросу: %s", name, strings.TrimSpace(message)), 1000)
}

func companionSkillInstructions(message string) string {
	lower := strings.ToLower(message)
	base := "Сначала изучи релевантный контекст через search_code или read_file. Выполняй только работу в границах квеста, делай минимальные проверяемые изменения через propose_patch и не расширяй tools или permissions. В завершение укажи фактические проверки и оставшиеся риски."
	specific := "Применяй профильные практики к задаче и предпочитай уже используемые в проекте решения."
	switch {
	case strings.Contains(lower, "postgres") || strings.Contains(lower, "sql") || strings.Contains(lower, "баз"):
		specific = "Проверяй схему, миграции, совместимость запросов, транзакционные границы и планы выполнения. Не выполняй разрушающие операции с данными без отдельного подтверждения."
	case strings.Contains(lower, "security") || strings.Contains(lower, "безопас"):
		specific = "Проверяй границы доверия, валидацию входов, управление секретами, авторизацию и возможность обхода политик. Отделяй подтверждённые проблемы от предположений."
	case strings.Contains(lower, "test") || strings.Contains(lower, "qa") || strings.Contains(lower, "тест"):
		specific = "Выбирай минимальный релевантный набор тестов, воспроизводи сбой до исправления и подтверждай результат успешным exit code. Не скрывай нестабильные или пропущенные проверки."
	case strings.Contains(lower, "backend") || strings.Contains(lower, "бэкенд") || strings.Contains(lower, "api"):
		specific = "Сохраняй контракты API, обработку ошибок и обратную совместимость; проверяй тесты и статический анализ, относящиеся к изменению."
	case strings.Contains(lower, "frontend") || strings.Contains(lower, "фронтенд"):
		specific = "Сохраняй пользовательский сценарий, доступность, состояния загрузки/ошибки и визуальные соглашения проекта; проверяй сборку и релевантные тесты."
	case strings.Contains(lower, "document") || strings.Contains(lower, "документ"):
		specific = "Сверяй документацию с фактическим поведением и примерами проекта; не утверждай неподтверждённые возможности."
	case strings.Contains(lower, "refactor") || strings.Contains(lower, "рефактор"):
		specific = "Сохраняй внешнее поведение, разделяй механическое изменение и изменение логики, подтверждай результат тестами и компактным diff."
	}
	return specific + "\n\n" + base
}

func companionSkillTools(message string) []string {
	lower := strings.ToLower(message)
	tools := []string{"read_file", "search_text"}
	if strings.Contains(lower, "security") || strings.Contains(lower, "безопас") || strings.Contains(lower, "review") || strings.Contains(lower, "ревью") {
		tools = append(tools, "git_diff")
	}
	if strings.Contains(lower, "backend") || strings.Contains(lower, "бэкенд") || strings.Contains(lower, "frontend") || strings.Contains(lower, "фронтенд") || strings.Contains(lower, "api") || strings.Contains(lower, "refactor") || strings.Contains(lower, "рефактор") || strings.Contains(lower, "document") || strings.Contains(lower, "документ") {
		tools = append(tools, "propose_patch")
	}
	if strings.Contains(lower, "test") || strings.Contains(lower, "qa") || strings.Contains(lower, "тест") || strings.Contains(lower, "backend") || strings.Contains(lower, "бэкенд") || strings.Contains(lower, "frontend") || strings.Contains(lower, "фронтенд") || strings.Contains(lower, "refactor") || strings.Contains(lower, "рефактор") || strings.Contains(lower, "postgres") || strings.Contains(lower, "sql") || strings.Contains(lower, "миграц") || strings.Contains(lower, "migration") {
		tools = append(tools, "run_command")
	}
	return tools
}
