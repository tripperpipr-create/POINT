package companion

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/textutil"
)

func personalityInstructions(cfg domain.CompanionConfig) string {
	questionPolicy := "ask only when a missing decision materially changes the plan"
	if cfg.QuestionStrictness >= 75 {
		questionPolicy = "ask focused questions for material ambiguity, but never ask for facts already present in project context"
	} else if cfg.QuestionStrictness <= 30 {
		questionPolicy = "prefer explicit, labeled assumptions over questions unless proceeding would be unsafe"
	}
	verbosity := "keep the reply compact"
	if cfg.Verbosity >= 70 {
		verbosity = "give a detailed but structured rationale"
	} else if cfg.Verbosity <= 30 {
		verbosity = "use a very short reply"
	}
	initiative := "recommend a quest when the user asks for project work"
	if cfg.Initiative >= 70 {
		initiative = "proactively point out one relevant next step or risk, without starting anything"
	} else if cfg.Initiative <= 30 {
		initiative = "answer the request directly and avoid unsolicited proposals"
	}
	return fmt.Sprintf("Companion personality preset=%s. Settings (0-100): criticality=%d, creativity=%d, verbosity=%d, initiative=%d, questionStrictness=%d, riskTolerance=%d. Behavior: %s; %s; %s. Higher criticality lowers the threshold for warnings. Higher creativity may compare alternatives. Lower risk tolerance requires stronger verification and rollback constraints. AutoAct=%t never overrides the explicit user-approval boundary.",
		cfg.Preset, cfg.Criticality, cfg.Creativity, cfg.Verbosity, cfg.Initiative, cfg.QuestionStrictness, cfg.RiskTolerance,
		questionPolicy, verbosity, initiative, cfg.AutoAct)
}

type companionIntent string

const (
	companionIntentStatus   companionIntent = "status"
	companionIntentUsage    companionIntent = "usage"
	companionIntentPlanning companionIntent = "planning"
)

func classifyCompanionIntent(message string) companionIntent {
	message = strings.ToLower(strings.TrimSpace(message))
	if isUsageRequest(message) && !isUsageChangeRequest(message) {
		return companionIntentUsage
	}
	if isPlanningRequest(message) && !strings.Contains(message, "?") {
		return companionIntentPlanning
	}
	if isInformationalRequest(message) {
		return companionIntentStatus
	}
	if isPlanningRequest(message) {
		return companionIntentPlanning
	}
	return companionIntentStatus
}

func isFlowCreationRequest(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	if strings.Contains(message, "квест") || strings.Contains(message, "quest") {
		return false
	}
	if !strings.Contains(message, "flow") && !strings.Contains(message, "workflow") && !strings.Contains(message, "воркфлоу") {
		return false
	}
	for _, marker := range []string{"создай", "сделай", "сгенерир", "построй", "create", "build", "generate"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func isSkillCreationRequest(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	if strings.Contains(message, "квест") || strings.Contains(message, "quest") || strings.Contains(message, "flow") || strings.Contains(message, "воркфлоу") {
		return false
	}
	if !strings.Contains(message, "skill") && !strings.Contains(message, "скилл") && !strings.Contains(message, "навык") {
		return false
	}
	return hasCreationMarker(message)
}

// isToolCreationRequest — просьба завести самодельный инструмент.
//
// Навыки и квесты отсекаются раньше: «создай навык, использующий инструмент X»
// — просьба про навык, и путать их нельзя.
func isToolCreationRequest(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	if strings.Contains(message, "квест") || strings.Contains(message, "quest") || strings.Contains(message, "flow") || strings.Contains(message, "воркфлоу") {
		return false
	}
	if strings.Contains(message, "skill") || strings.Contains(message, "скилл") || strings.Contains(message, "навык") {
		return false
	}
	if !strings.Contains(message, "инструмент") && !strings.Contains(message, "tool") {
		return false
	}
	return hasCreationMarker(message)
}

func isAgentCreationRequest(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	if strings.Contains(message, "квест") || strings.Contains(message, "quest") || strings.Contains(message, "flow") || strings.Contains(message, "воркфлоу") || strings.Contains(message, "команд") || strings.Contains(message, "team") || strings.Contains(message, "отряд") || strings.Contains(message, "skill") || strings.Contains(message, "навык") {
		return false
	}
	if !strings.Contains(message, "агент") && !strings.Contains(message, "agent") {
		return false
	}
	return hasCreationMarker(message)
}

func isTeamCreationRequest(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	if strings.Contains(message, "квест") || strings.Contains(message, "quest") {
		return false
	}
	if !strings.Contains(message, "команд") && !strings.Contains(message, "team") && !strings.Contains(message, "отряд") {
		return false
	}
	return hasCreationMarker(message)
}

func hasCreationMarker(message string) bool {
	for _, marker := range []string{"создай", "сделай", "собери", "сгенерир", "create", "build", "generate", "assemble"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func isUsageRequest(message string) bool {
	markers := []string{
		"токен", "расход", "стоимост", "бюджет", "лимит", "usage", "tokens", "token ", "cost", "spend", "budget",
	}
	for _, marker := range markers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func isHereRequest(message string) bool {
	markers := []string{
		"здесь", "сюда", "этот файл", "эта строка", "этот код", "эта ошибка", "этот фрагмент",
		"текущий файл", "текущая строка", "посмотри на", "посмотри этот", "разбери это", "разбери файл",
		"this file", "this line", "this code", "current file", "current line", "what is wrong", "look at this",
	}
	for _, marker := range markers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func isIDEProblemRequest(message string) bool {
	markers := []string{
		"ошиб", "проблем", "диагност", "не собира", "не компили", "упал", "падает", "терминал", "консол", "команд",
		"error", "errors", "problem", "diagnostic", "compile", "build failed", "test failed", "terminal", "console", "command failed",
	}
	for _, marker := range markers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func isUsageChangeRequest(message string) bool {
	markers := []string{
		"сократ", "уменьш", "оптимизир", "установи бюджет", "задай бюджет", "настрой бюджет", "ограничь",
		"reduce", "optimize", "set budget", "lower cost", "limit usage",
	}
	for _, marker := range markers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func lastCompanionByRole(history []domain.CompanionMessage, role string) domain.CompanionMessage {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == role {
			return history[i]
		}
	}
	return domain.CompanionMessage{}
}

func isCompanionAffirmative(message string) bool {
	message = strings.TrimSpace(strings.ToLower(message))
	message = strings.Trim(message, ".!?")
	switch message {
	case "да", "ок", "okay", "ok", "yes", "ага", "угу", "хорошо", "ясно", "понял", "подтверждаю", "запускай", "start":
		return true
	}
	return false
}

func isCompanionFollowUp(message string) bool {
	if isCompanionAffirmative(message) {
		return true
	}
	markers := []string{
		"подробнее", "продолж", "дальше", "ещё раз", "еще раз", "уточни", "разверни",
		"continue", "go on", "keep going", "more detail", "tell me more",
	}
	for _, marker := range markers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return strings.HasPrefix(strings.TrimSpace(message), "а ")
}

func compactCompanionSnippet(snippet string) string {
	compacted := strings.Join(strings.Fields(snippet), " ")
	return trim(compacted, 160)
}

func continueDeterministicReply(last domain.CompanionMessage, projectContext gatheredContext) string {
	parts := []string{"Продолжаю предыдущий ответ.", trim(last.Content, 420)}
	if last.ProposalID != "" {
		parts = append(parts, "Квест остаётся карточкой на ревью: Start / Modify / Ignore.")
	}
	if label := projectContext.Focus.Label(); label != "" {
		parts = append(parts, "Сейчас в фокусе "+label+".")
	}
	if snippet := compactCompanionSnippet(projectContext.Focus.Snippet); snippet != "" {
		parts = append(parts, "Фрагмент: "+snippet+".")
	}
	return strings.Join(parts, " ")
}

func isInformationalRequest(message string) bool {
	if strings.Contains(message, "?") {
		return true
	}
	markers := []string{
		"почему", "объясни", "покажи", "расскажи", "сколько", "какие ", "какой ", "как ", "кто ", "где ", "что ",
		"оцени", "проанализ", "состояние", "статус", "сводка", "риски", "what ", "why ", "how ", "show ",
		"explain", "analyze", "analyse", "status", "summary", "risks",
	}
	for _, marker := range markers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func shouldAskQuestion(message string, strictness int) bool {
	if strings.TrimSpace(message) == "" {
		return true
	}
	if strictness < 35 {
		return false
	}
	if looksAmbiguous(message) {
		return true
	}
	return strictness >= 85 && len(textutil.Tokens(message)) < 3
}

func looksAmbiguous(message string) bool {
	markers := []string{"может", "либо", " или ", "не уверен", "наверное", "maybe", "either", "unsure", "??"}
	for _, marker := range markers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func isPlanningRequest(message string) bool {
	markers := []string{
		"добав", "сдел", "реализ", "исправ", "почин", "созда", "настрой", "перепиш", "мигрир", "проверь", "заплан", "квест",
		"сократ", "уменьш", "оптимизир", "ограничь", "implement", "add ", "fix", "create", "build", "configure", "refactor", "migrate", "review", "plan", "quest", "reduce", "optimize", "limit usage",
	}
	for _, marker := range markers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func interventionLevel(message string, cfg domain.CompanionConfig) string {
	score := (cfg.Criticality - cfg.RiskTolerance) / 3
	warningMarkers := []string{"security", "безопас", "секрет", "credential", "prod", "production", "payment", "платеж", "permission", "удал"}
	criticalMarkers := []string{"drop database", "delete database", "rm -rf", "утеч", "leak", "rotate secret", "продакшен", "production database"}
	for _, marker := range warningMarkers {
		if strings.Contains(message, marker) {
			score += 45
		}
	}
	for _, marker := range criticalMarkers {
		if strings.Contains(message, marker) {
			score += 45
		}
	}
	if score >= 75 {
		return "critical"
	}
	if score >= 35 {
		return "warning"
	}
	return "suggestion"
}

func deterministicStatusReply(facts []string, focus ChatFocus, verbosity int) string {
	parts := make([]string, 0, 6)
	if label := focus.Label(); label != "" {
		part := "Сейчас открыт " + label
		if focus.Language != "" {
			part += " (" + focus.Language + ")"
		}
		if focus.Dirty {
			part += ", буфер не сохранён"
		}
		if focus.Selection {
			part += ", есть выделение"
		}
		if focus.Diagnostics > 0 {
			part += fmt.Sprintf(", в файле %d замечаний Problems", focus.Diagnostics)
		}
		parts = append(parts, part+".")
	} else if strings.TrimSpace(focus.Snippet) != "" {
		parts = append(parts, "Сейчас в фокусе фрагмент кода из редактора.")
	}
	if focus.Run != "" {
		parts = append(parts, "Цель запуска: "+focus.Run+".")
	}
	if focus.Debug != "" {
		parts = append(parts, "Отладка: "+focus.Debug+".")
	}
	if focus.Failure != "" {
		parts = append(parts, "Последний сбой IDE: "+focus.Failure+".")
	}
	if snippet := compactCompanionSnippet(focus.Snippet); snippet != "" {
		parts = append(parts, "Фрагмент под курсором: "+snippet+".")
	}
	if briefing := humanizeCompanionFacts(facts, verbosity); briefing != "" {
		parts = append(parts, briefing)
	}
	if len(parts) == 0 {
		return "Контекст проекта пока недоступен. Я могу подготовить квест, когда вы сформулируете требуемое изменение."
	}
	parts = append(parts, "Для изменения сформулируйте ожидаемый результат — я подготовлю квест и не запущу его без подтверждения.")
	return strings.Join(parts, " ")
}

func humanizeCompanionFacts(facts []string, verbosity int) string {
	lookup := map[string]string{}
	for _, fact := range facts {
		key, value, ok := strings.Cut(fact, "=")
		if ok {
			lookup[key] = value
		}
	}
	bits := make([]string, 0, 6)
	appendIf := func(key, label string) {
		value := lookup[key]
		if value == "" || value == "0" {
			return
		}
		bits = append(bits, label+" "+value)
	}
	appendIf("ideDiagnosticErrors", "ошибок Problems")
	appendIf("ideFailedCommands", "упавших команд")
	appendIf("activeQuests", "активных квестов")
	appendIf("activeExecutions", "исполнений")
	appendIf("pendingChangeSets", "ожидающих Change Set")
	if verbosity >= 70 {
		appendIf("projectAgents", "агентов")
	}
	if len(bits) == 0 {
		if agents := lookup["projectAgents"]; agents != "" {
			return "В мире " + agents + " агентов, срочных сигналов нет."
		}
		return ""
	}
	return "В проекте: " + strings.Join(bits, ", ") + "."
}

func deterministicIDEReply(observations []domain.IDEObservation, focus ChatFocus, verbosity int) (string, string) {
	diagnostics := make([]domain.IDEObservation, 0, 8)
	failed := make([]domain.IDEObservation, 0, 4)
	seenCommands := map[string]bool{}
	for _, item := range observations {
		if item.Kind == "diagnostic" && (item.Level == "error" || item.Level == "warning") {
			diagnostics = append(diagnostics, item)
			continue
		}
		if item.Kind != "terminal" && item.Kind != "task" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(item.Command))
		if key == "" {
			key = strings.ToLower(item.Source + "\x00" + item.Summary)
		}
		if seenCommands[key] {
			continue
		}
		seenCommands[key] = true
		if item.ExitCode != nil && *item.ExitCode != 0 {
			failed = append(failed, item)
		}
	}
	diagnostics = prioritizeIDEObservations(diagnostics, focus)
	if len(diagnostics) == 0 && len(failed) == 0 {
		bits := make([]string, 0, 4)
		if label := focus.Label(); label != "" {
			bits = append(bits, "В фокусе "+label+".")
		}
		if focus.Selection {
			bits = append(bits, "Опираюсь на текущее выделение.")
		} else if strings.TrimSpace(focus.Snippet) != "" {
			bits = append(bits, "Вижу фрагмент вокруг курсора.")
		}
		if focus.Run != "" {
			bits = append(bits, "Цель запуска: "+focus.Run+".")
		}
		if focus.Failure != "" {
			bits = append(bits, "Последний сбой: "+focus.Failure+".")
		}
		if len(bits) > 0 {
			bits = append(bits, "Активных ошибок редактора или упавших команд нет. Если проблема только в этом фрагменте — опишите ожидаемый результат, и я подготовлю квест.")
			return "suggestion", strings.Join(bits, " ")
		}
		return "suggestion", "IDE не сообщает активных ошибок редактора или последних упавших команд. Если ошибка всё ещё видна, повторите команду или дождитесь обновления Problems."
	}
	parts := make([]string, 0, 6)
	if label := focus.Label(); label != "" {
		parts = append(parts, "Фокус IDE: "+label+".")
		matched := 0
		for _, item := range diagnostics {
			if observationMatchesFocus(item, focus) {
				matched++
			}
		}
		if matched == 0 && len(diagnostics) > 0 {
			parts = append(parts, "В открытом файле отдельной диагностики нет.")
		}
	}
	if focus.Selection {
		parts = append(parts, "Есть выделение в редакторе.")
	}
	if len(diagnostics) > 0 {
		errorsCount := 0
		for _, item := range diagnostics {
			if item.Level == "error" {
				errorsCount++
			}
		}
		parts = append(parts, fmt.Sprintf("Problems: %d активных сообщений, из них ошибок %d.", len(diagnostics), errorsCount))
		limit := 2
		if verbosity >= 70 {
			limit = 5
		}
		for _, item := range diagnostics[:min(limit, len(diagnostics))] {
			location := item.Path
			if item.Line > 0 {
				location += fmt.Sprintf(":%d", item.Line)
			}
			parts = append(parts, strings.TrimSpace(location+" — "+item.Summary)+".")
		}
	}
	if len(failed) > 0 {
		item := failed[0]
		parts = append(parts, fmt.Sprintf("Последняя неуспешная команда: %s, код %d. %s", trim(item.Command, 220), *item.ExitCode, trim(item.Detail, 500)))
	}
	parts = append(parts, "Чтобы запустить исправление, напишите «исправь текущие ошибки» — я подготовлю квест с проверкой результата.")
	return "warning", strings.Join(parts, " ")
}

func shouldRetitleFromFocus(title, file string) bool {
	title = strings.ToLower(strings.TrimSpace(title))
	base := strings.ToLower(filepath.Base(strings.TrimSpace(file)))
	if base != "" && strings.Contains(title, base) {
		return false
	}
	for _, marker := range []string{"новый квест", "исправь этот файл", "исправь текущие ошибки", "этот файл", "fix this"} {
		if strings.Contains(title, marker) {
			return true
		}
	}
	return title == "" || len([]rune(title)) < 8
}

func groundQuestProposalInIDE(proposal *domain.QuestProposal, observations []domain.IDEObservation, focus ChatFocus) {
	objectives := make([]string, 0, 8)
	diagnosticErrors, failedCommands := 0, 0
	seenCommands := map[string]bool{}
	observations = prioritizeIDEObservations(observations, focus)
	for _, item := range observations {
		if item.Kind == "diagnostic" && item.Level == "error" {
			diagnosticErrors++
			if len(objectives) < 5 {
				location := item.Path
				if item.Line > 0 {
					location += fmt.Sprintf(":%d", item.Line)
				}
				objectives = append(objectives, "Исправить "+strings.TrimSpace(location+" — "+item.Summary))
			}
			continue
		}
		if item.Kind != "terminal" && item.Kind != "task" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(item.Command))
		if key == "" || seenCommands[key] {
			continue
		}
		seenCommands[key] = true
		if item.ExitCode != nil && *item.ExitCode != 0 {
			failedCommands++
			if len(objectives) < 7 {
				objectives = append(objectives, "Восстановить успешное выполнение: "+trim(item.Command, 300))
			}
		}
	}
	if len(objectives) == 0 {
		if label := focus.Label(); label != "" {
			objectives = append(objectives, "Разобрать и исправить "+label)
		}
	}
	if focus.Failure != "" && !textutil.ContainsFold(objectives, focus.Failure) {
		objectives = append(objectives, "Разобрать сбой: "+focus.Failure)
	}
	if focus.Run != "" && focus.Failure == "" && !textutil.ContainsFold(objectives, focus.Run) {
		objectives = append(objectives, "Проверить цель запуска: "+focus.Run)
	}
	if len(objectives) == 0 {
		return
	}
	proposal.Objectives = objectives
	if label := focus.Label(); label != "" && shouldRetitleFromFocus(proposal.Title, focus.File) {
		proposal.Title = trim("Исправить "+label, 80)
	}
	proposal.Rationale = fmt.Sprintf("Квест основан на текущих сигналах IDE: ошибок редактора %d, последних неуспешных команд %d.", diagnosticErrors, failedCommands)
	if diagnosticErrors > 0 && !textutil.ContainsFold(proposal.DefinitionOfDone, "Problems") {
		proposal.DefinitionOfDone = append(proposal.DefinitionOfDone, "Problems не содержит ошибок по затронутым файлам")
	}
	if failedCommands > 0 && !textutil.ContainsFold(proposal.DefinitionOfDone, "кодом 0") {
		proposal.DefinitionOfDone = append(proposal.DefinitionOfDone, "Неуспешная команда повторно выполняется с кодом 0")
	}
	if focus.Run != "" && !textutil.ContainsFold(proposal.DefinitionOfDone, focus.Run) {
		proposal.DefinitionOfDone = append(proposal.DefinitionOfDone, "Цель запуска проходит: "+focus.Run)
	}
	if proposal.Importance == domain.QuestNormal {
		proposal.Importance = domain.QuestImportant
	}
	proposal.EstimateTokens = domain.EstimateQuestTokens(*proposal)
}

func deterministicUsageReply(usage usageAnalysis, verbosity int) string {
	if usage.Records == 0 {
		return "В текущем проекте пока нет Usage Records. Я не могу надёжно объяснить расход токенов или стоимость без фактических записей провайдера."
	}
	current := usage.CurrentMonth
	previous := usage.PreviousMonth
	parts := []string{fmt.Sprintf("За текущий месяц сохранено %d Usage Records и %d токенов.", current.Records, current.Tokens)}
	if previous.Records > 0 {
		delta := int64(0)
		if previous.Tokens > 0 {
			delta = (current.Tokens - previous.Tokens) * 100 / previous.Tokens
		}
		parts = append(parts, fmt.Sprintf("В прошлом месяце было %d записей и %d токенов; изменение %+d%%.", previous.Records, previous.Tokens, delta))
	} else {
		parts = append(parts, "За прошлый месяц данных нет, поэтому достоверное сравнение роста недоступно.")
	}
	if len(usage.TopModels) > 0 {
		top := usage.TopModels[0]
		share := int64(0)
		if current.Tokens > 0 {
			share = top.Tokens * 100 / current.Tokens
		}
		parts = append(parts, fmt.Sprintf("Основной вклад даёт %s: %d токенов (%d%% месяца).", top.Name, top.Tokens, share))
	}
	if current.UnknownCostRecords > 0 {
		parts = append(parts, fmt.Sprintf("Известная стоимость — %d центов, но у %d записей стоимость не сообщена провайдером; полную сумму Point не выдумывает.", current.KnownCostCents, current.UnknownCostRecords))
	} else {
		parts = append(parts, fmt.Sprintf("Подтверждённая стоимость месяца — %d центов.", current.KnownCostCents))
	}
	if verbosity >= 60 && len(usage.TopAgents) > 0 {
		top := usage.TopAgents[0]
		parts = append(parts, fmt.Sprintf("Больше всего токенов связано с «%s»: %d по %d обращениям.", top.Name, top.Tokens, top.Records))
	}
	if current.FailedOutcomes > 0 {
		parts = append(parts, fmt.Sprintf("Неуспешных outcomes в этом месяце: %d из %d.", current.FailedOutcomes, current.Records))
	}
	if usage.Bounded {
		parts = append(parts, fmt.Sprintf("Анализ ограничен %d последними Usage Records проекта.", companionUsageRecordLimit))
	}
	return strings.Join(parts, " ")
}

func usageAnalysisLevel(usage usageAnalysis) string {
	current := usage.CurrentMonth
	if current.Records > 0 && current.FailedOutcomes*100/current.Records >= 50 {
		return "warning"
	}
	if usage.PreviousMonth.Tokens > 0 && current.Tokens >= usage.PreviousMonth.Tokens*2 {
		return "warning"
	}
	return "suggestion"
}

func selectTeam(agents []domain.ProjectAgent, goal string, limit int) []string {
	if limit <= 0 || limit > 5 {
		limit = 3
	}
	type candidate struct {
		agent domain.ProjectAgent
		score int
		index int
	}
	goalLower := strings.ToLower(goal)
	goalTokens := textutil.Tokens(goal)
	roleHints := map[string][]string{
		"security": {"security", "review", "backend", "безопас", "аудит"},
		"auth":     {"security", "backend", "identity", "auth", "review"},
		"frontend": {"frontend", "ui", "ux", "design"},
		"backend":  {"backend", "api", "database", "server"},
		"test":     {"test", "qa", "review", "verifier"},
		"review":   {"review", "qa", "architect", "verifier"},
		"архитект": {"architect", "architecture", "review"},
		"мигр":     {"database", "backend", "architect", "review"},
	}
	candidates := make([]candidate, 0, len(agents))
	for index, agent := range agents {
		haystack := strings.Join([]string{agent.Name, agent.RoleDescription, agent.Mission, strings.Join(agent.Goals, " "), strings.Join(agent.SkillIDs, " "), strings.Join(agent.AllowedTools, " ")}, " ")
		score := textutil.Overlap(goalTokens, textutil.Tokens(haystack)) * 12
		haystackLower := strings.ToLower(haystack)
		for trigger, hints := range roleHints {
			if !strings.Contains(goalLower, trigger) {
				continue
			}
			for _, hint := range hints {
				if strings.Contains(haystackLower, hint) {
					score += 18
				}
			}
		}
		if agent.TasksCompleted > 0 {
			score += min(8, agent.SuccessCount*8/agent.TasksCompleted)
		}
		candidates = append(candidates, candidate{agent: agent, score: score, index: index})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].index < candidates[j].index
	})
	result := make([]string, 0, min(limit, len(candidates)))
	for _, candidate := range candidates {
		if len(result) >= limit {
			break
		}
		result = append(result, candidate.agent.ID)
	}
	return result
}

func selectBlueprint(blueprints []domain.AgentBlueprint, request string) domain.AgentBlueprint {
	tokens := textutil.Tokens(request)
	lower := strings.ToLower(request)
	bestIndex, bestScore := 0, -1
	for index, blueprint := range blueprints {
		haystack := strings.Join([]string{
			blueprint.Name, blueprint.RoleDescription, blueprint.Mission,
			strings.Join(blueprint.Goals, " "), strings.Join(blueprint.SkillIDs, " "), strings.Join(blueprint.AllowedTools, " "),
		}, " ")
		score := textutil.Overlap(tokens, textutil.Tokens(haystack)) * 12
		haystack = strings.ToLower(haystack)
		for trigger, hints := range map[string][]string{
			"backend": {"backend", "api", "server"}, "бэкенд": {"backend", "api", "server"},
			"frontend": {"frontend", "ui", "ux"}, "фронтенд": {"frontend", "ui", "ux"},
			"security": {"security", "review", "audit"}, "безопас": {"security", "review", "audit"},
			"test": {"test", "qa", "verifier"}, "тест": {"test", "qa", "verifier"},
			"architect": {"architect", "design"}, "архитект": {"architect", "design"},
		} {
			if !strings.Contains(lower, trigger) {
				continue
			}
			for _, hint := range hints {
				if strings.Contains(haystack, hint) {
					score += 20
				}
			}
		}
		if score > bestScore {
			bestIndex, bestScore = index, score
		}
	}
	return blueprints[bestIndex]
}

func selectExistingFlow(flows []domain.FlowGraph, goal string) string {
	goalTokens := textutil.Tokens(goal)
	bestID, bestScore := "", 0
	for _, flow := range flows {
		parts := []string{flow.Name, flow.Description}
		for _, node := range flow.Nodes {
			parts = append(parts, node.Name, string(node.Kind))
		}
		score := textutil.Overlap(goalTokens, textutil.Tokens(strings.Join(parts, " ")))
		if score > bestScore {
			bestID, bestScore = flow.ID, score
		}
	}
	return bestID
}

func trim(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}

func decomposeObjectives(goal string, creativity int) []string {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return []string{"Уточнить цель квеста"}
	}
	lower := strings.ToLower(goal)
	large := len([]rune(goal)) > 48 ||
		strings.Contains(lower, "implement") || strings.Contains(lower, "oauth") ||
		strings.Contains(lower, "payment") || strings.Contains(lower, "архитект") ||
		strings.Contains(lower, "migrate") || strings.Contains(lower, "перепиш") ||
		strings.Contains(lower, "реализ") || strings.Contains(lower, "интеграц")
	if !large {
		return []string{goal}
	}
	if creativity <= 30 {
		return []string{"Основная реализация", "Тесты и диагностика", "Review и Definition of Done"}
	}
	result := []string{"Архитектура и границы изменения", "Основная реализация", "Тесты и диагностика", "Review и Definition of Done"}
	if creativity >= 75 {
		result = append([]string{"Сравнить допустимые варианты и зафиксировать выбранный подход"}, result...)
	}
	return result
}
