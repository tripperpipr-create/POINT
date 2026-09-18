package companion

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/diagnostics"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/textutil"
)

type BudgetSnapshot struct {
	DailyLimitCents   int64
	DailyUsedCents    int64
	MonthlyLimitCents int64
	MonthlyUsedCents  int64
	HardStop          bool
}

func (s Service) EnsureConfig(ctx context.Context, workspaceID string) (domain.CompanionConfig, error) {
	cfg, err := s.Store.GetCompanionConfig(ctx, workspaceID)
	if err == nil {
		return cfg, nil
	}
	now := time.Now().UTC()
	cfg, _ = PresetDefaults("balanced")
	cfg.ID, cfg.WorkspaceID = domain.NewID("companion"), workspaceID
	cfg.Temperature, cfg.MaxOutputTokens = 0.2, 2048
	cfg.CreatedAt, cfg.UpdatedAt = now, now
	if saveErr := s.Store.SaveCompanionConfig(ctx, cfg); saveErr != nil {
		return domain.CompanionConfig{}, saveErr
	}
	return cfg, nil
}

func PresetDefaults(preset string) (domain.CompanionConfig, bool) {
	cfg := domain.CompanionConfig{Preset: preset}
	switch preset {
	case "technical-lead":
		cfg.Criticality, cfg.Creativity, cfg.Verbosity = 65, 45, 55
		cfg.Initiative, cfg.QuestionStrictness, cfg.RiskTolerance = 65, 60, 25
	case "critical-architect":
		cfg.Criticality, cfg.Creativity, cfg.Verbosity = 90, 40, 65
		cfg.Initiative, cfg.QuestionStrictness, cfg.RiskTolerance = 55, 80, 10
	case "mentor":
		cfg.Criticality, cfg.Creativity, cfg.Verbosity = 55, 55, 80
		cfg.Initiative, cfg.QuestionStrictness, cfg.RiskTolerance = 60, 55, 20
	case "product-engineer":
		cfg.Criticality, cfg.Creativity, cfg.Verbosity = 55, 75, 50
		cfg.Initiative, cfg.QuestionStrictness, cfg.RiskTolerance = 85, 40, 40
	case "balanced":
		cfg.Criticality, cfg.Creativity, cfg.Verbosity = 50, 50, 50
		cfg.Initiative, cfg.QuestionStrictness, cfg.RiskTolerance = 50, 70, 30
	case "cautious":
		cfg.Criticality, cfg.Creativity, cfg.Verbosity = 80, 30, 60
		cfg.Initiative, cfg.QuestionStrictness, cfg.RiskTolerance = 35, 85, 15
	case "proactive":
		cfg.Criticality, cfg.Creativity, cfg.Verbosity = 60, 70, 60
		cfg.Initiative, cfg.QuestionStrictness, cfg.RiskTolerance = 85, 45, 40
	case "minimal":
		cfg.Criticality, cfg.Creativity, cfg.Verbosity = 45, 20, 20
		cfg.Initiative, cfg.QuestionStrictness, cfg.RiskTolerance = 20, 30, 30
	case "custom":
		cfg.Criticality, cfg.Creativity, cfg.Verbosity = 50, 50, 50
		cfg.Initiative, cfg.QuestionStrictness, cfg.RiskTolerance = 50, 70, 30
	default:
		return domain.CompanionConfig{}, false
	}
	return cfg, true
}

func BuildInterventions(cfg domain.CompanionConfig, agents []domain.ProjectAgent, executions []domain.ExecutionInstance, changeSets []domain.ChangeSet, usage []domain.UsageRecord, connections []domain.Connection) []domain.CompanionIntervention {
	result := make([]domain.CompanionIntervention, 0, 8)
	add := func(id, level, title, detail, tab, relatedID string) {
		result = append(result, domain.CompanionIntervention{ID: id, Level: level, Title: title, Detail: detail, ActionTab: tab, RelatedID: relatedID})
	}
	fileWarningThreshold := 24 - cfg.Criticality/5 + cfg.RiskTolerance/5
	if fileWarningThreshold < 8 {
		fileWarningThreshold = 8
	}
	for _, set := range changeSets {
		if set.Status == domain.ChangeSetConflict {
			add("changeset-conflict-"+set.ID, "critical", "Конфликт набора изменений", fmt.Sprintf("%s требует ручного разрешения перед Apply.", set.Title), "changesets", set.ID)
			continue
		}
		if (set.Status == domain.ChangeSetPending || set.Status == domain.ChangeSetApproved) && len(set.Items) >= fileWarningThreshold {
			level := "warning"
			if len(set.Items) >= fileWarningThreshold*2 {
				level = "critical"
			}
			add("changeset-size-"+set.ID, level, "Объём изменений выше ожидаемого", fmt.Sprintf("В наборе «%s» изменяется %d файлов; проверьте границы квеста.", set.Title, len(set.Items)), "changesets", set.ID)
		}
	}
	running, waiting, failed := 0, 0, 0
	for index, execution := range executions {
		switch execution.Status {
		case domain.RunRunning, domain.RunPending:
			running++
		case domain.RunWaiting:
			waiting++
		case domain.RunFailed, domain.RunInterrupted:
			if index < 12 {
				failed++
			}
		}
	}
	if waiting > 0 {
		add("executions-waiting", "suggestion", "Есть запросы на подтверждение", textutil.Count(waiting, "исполнение ждёт", "исполнения ждут", "исполнений ждут")+" вашего решения.", "quests", "")
	}
	if failed >= 3 {
		add("executions-failing", "warning", "Повторяющиеся сбои исполнений", fmt.Sprintf("Среди последних запусков обнаружено %d сбоев; стоит проверить модель, контекст и ограничения.", failed), "quests", "")
	}
	if running >= 6 {
		add("executions-concurrency", "warning", "Высокая параллельность", fmt.Sprintf("Одновременно активно %d исполнений; проверьте конфликты файлов и бюджет.", running), "quests", "")
	}
	// Usage records arrive newest first. A bounded rolling window prevents an
	// old provider outage from poisoning the warning forever. The newest three
	// calls are also a recovery hysteresis: three consecutive successes resolve
	// the incident even while the wider ratio is still catching up.
	recentUsage := usage
	if len(recentUsage) > 20 {
		recentUsage = recentUsage[:20]
	}
	failedUsage := 0
	for _, record := range recentUsage {
		outcome := strings.ToLower(record.Outcome)
		if strings.Contains(outcome, "error") || strings.Contains(outcome, "failed") || strings.Contains(outcome, "invalid") {
			failedUsage++
		}
	}
	recoveryWindow := recentUsage
	if len(recoveryWindow) > 3 {
		recoveryWindow = recoveryWindow[:3]
	}
	recentFailure := false
	for _, record := range recoveryWindow {
		outcome := strings.ToLower(record.Outcome)
		if strings.Contains(outcome, "error") || strings.Contains(outcome, "failed") || strings.Contains(outcome, "invalid") {
			recentFailure = true
			break
		}
	}
	if len(recentUsage) >= 5 && recentFailure && failedUsage*100/len(recentUsage) >= 30 {
		item := domain.CompanionIntervention{
			ID: "usage-failure-rate", Level: "warning",
			Title:       "Высокая доля неуспешных обращений к моделям",
			Detail:      fmt.Sprintf("%d из %d последних обращений к моделям завершились ошибкой.", failedUsage, len(recentUsage)),
			ActionTab:   "connections",
			ActionLabel: "Открыть подключения",
		}
		if connectionID := companionConnectionID(cfg, connections); connectionID != "" {
			item.RelatedID = connectionID
			item.ActionKind = domain.CompanionInterventionProbeConnection
			item.ActionLabel = "Проверить связь"
		}
		result = append(result, item)
	}
	if len(agents) == 0 {
		add("agents-empty", "suggestion", "Для квеста нужен агент", "Создайте хотя бы одного project agent, чтобы Companion мог собрать отряд.", "agents", "")
	}
	connected := 0
	for _, connection := range connections {
		if connection.Status == domain.ConnectionConnected {
			connected++
		}
	}
	if connected == 0 {
		add("connections-empty", "suggestion", "Нет подтверждённых подключений моделей", "Deterministic Companion доступен, но исполнениям агентов потребуется рабочее подключение.", "connections", "")
	}
	return MergeInterventions(result)
}

// companionConnectionID resolves the saved, secret-backed connection actually
// selected by Companion. Provider alone is insufficient: one workspace may
// contain several OpenAI-compatible gateways.
func companionConnectionID(cfg domain.CompanionConfig, connections []domain.Connection) string {
	normalizeURL := func(value string) string {
		return strings.ToLower(strings.TrimRight(strings.TrimSpace(value), "/"))
	}
	bestID, bestScore := "", 0
	for _, connection := range connections {
		score := 0
		strongMatch := false
		if cfg.ProviderPreset != "" && connection.PresetID == cfg.ProviderPreset {
			score += 8
			strongMatch = true
		}
		if cfg.BaseURL != "" && normalizeURL(connection.BaseURL) == normalizeURL(cfg.BaseURL) {
			score += 6
			strongMatch = true
		}
		if cfg.Provider != "" && connection.Provider != cfg.Provider {
			continue
		}
		if cfg.Provider != "" {
			score += 3
		}
		// A preset or explicit endpoint identifies one concrete OpenAI-compatible
		// gateway. Falling back to provider-only could probe a different account.
		if (cfg.ProviderPreset != "" || cfg.BaseURL != "") && !strongMatch {
			continue
		}
		if cfg.Provider == "" && !strongMatch {
			continue
		}
		if connection.Status == domain.ConnectionConnected {
			score++
		}
		if score > bestScore {
			bestID, bestScore = connection.ID, score
		}
	}
	return bestID
}

func BuildBudgetInterventions(snapshot BudgetSnapshot) []domain.CompanionIntervention {
	result := make([]domain.CompanionIntervention, 0, 2)
	add := func(id, period string, used, limit int64) {
		if limit <= 0 {
			return
		}
		percent := used * 100 / limit
		if percent < 75 {
			return
		}
		level := "warning"
		if percent >= 100 {
			level = "critical"
		}
		enforcement := "Companion только предупреждает; лимит исполняется политикой бюджета."
		if snapshot.HardStop {
			enforcement = "Hard stop включён и исполняется политикой бюджета, а не Companion."
		}
		result = append(result, domain.CompanionIntervention{
			ID: "budget-" + id, Level: level, Title: fmt.Sprintf("%s бюджет использован на %d%%", period, percent),
			Detail: fmt.Sprintf("Известная стоимость: %d из %d центов. %s", used, limit, enforcement), ActionTab: "overview",
		})
	}
	add("daily", "Дневной", snapshot.DailyUsedCents, snapshot.DailyLimitCents)
	add("monthly", "Месячный", snapshot.MonthlyUsedCents, snapshot.MonthlyLimitCents)
	return result
}

func BuildExecutionInterventions(cfg domain.CompanionConfig, executions []domain.ExecutionInstance, runs []domain.Run, now time.Time) []domain.CompanionIntervention {
	result := make([]domain.CompanionIntervention, 0, 6)
	executionByRun := make(map[string]domain.ExecutionInstance, len(executions))
	for _, execution := range executions {
		if execution.RunID != "" {
			executionByRun[execution.RunID] = execution
		}
	}
	fileThreshold := 18 - cfg.Criticality/10 + cfg.RiskTolerance/10
	if fileThreshold < 8 {
		fileThreshold = 8
	}
	for _, run := range runs {
		execution, linked := executionByRun[run.ID]
		if !linked {
			continue
		}
		if len(run.ChangedFiles) >= fileThreshold {
			level := "warning"
			if len(run.ChangedFiles) >= fileThreshold*2 {
				level = "critical"
			}
			result = append(result, domain.CompanionIntervention{
				ID: "run-files-" + run.ID, Level: level, Title: "Исполнение вышло за ориентир по файлам",
				Detail: fmt.Sprintf("Агент изменил %d файлов при ориентире Companion %d; проверьте границы квеста до Apply.", len(run.ChangedFiles), fileThreshold), ActionTab: "quests", RelatedID: execution.ID,
				ActionKind: domain.CompanionInterventionOpenRun, ActionLabel: "Открыть execution",
			})
		}
		contextTokens := 0
		for _, item := range run.ContextItems {
			if item.TokenEstimate > 0 {
				contextTokens += item.TokenEstimate
			} else {
				contextTokens += int(item.Size / 4)
			}
		}
		window := run.ConfigurationSnapshot.Profile.ContextWindowTokens
		if window > 0 && contextTokens*100/window >= 80 {
			result = append(result, domain.CompanionIntervention{
				ID: "run-context-" + run.ID, Level: "warning", Title: "Контекст исполнения близок к пределу",
				Detail: fmt.Sprintf("Вложения занимают около %d токенов из окна %d; reviewer или следующий шаг может получить слишком много контекста.", contextTokens, window), ActionTab: "quests", RelatedID: execution.ID,
				ActionKind: domain.CompanionInterventionOpenRun, ActionLabel: "Проверить контекст",
			})
		}
		if run.Status == domain.RunRunning || run.Status == domain.RunPaused || run.Status == domain.RunWaiting {
			maxSeconds := run.ConfigurationSnapshot.Profile.MaxDurationSeconds
			elapsed := now.Sub(run.StartedAt)
			if maxSeconds > 0 && elapsed >= time.Duration(maxSeconds)*time.Second*3/4 {
				result = append(result, domain.CompanionIntervention{
					ID: "run-duration-" + run.ID, Level: "warning", Title: "Возможное узкое место Flow",
					Detail: fmt.Sprintf("Исполнение длится %s при лимите %s; проверьте шаг, подтверждение и объём контекста. Изменения Flow повлияют только на следующий запуск.", elapsed.Round(time.Second), (time.Duration(maxSeconds) * time.Second).String()), ActionTab: "quests", RelatedID: execution.ID,
					ActionKind: domain.CompanionInterventionOpenRun, ActionLabel: "Открыть execution",
				})
			}
		}
	}
	return result
}

// BuildRunDiagnosticInterventions turns persisted run evidence into live,
// non-blocking Companion guidance. Suggested messages still require an
// explicit user click and are delivered through the ordinary run-control API.
func BuildRunDiagnosticInterventions(cfg domain.CompanionConfig, executions []domain.ExecutionInstance, runDiagnostics []diagnostics.RunDiagnostics) []domain.CompanionIntervention {
	result := make([]domain.CompanionIntervention, 0, 8)
	executionByRun := make(map[string]domain.ExecutionInstance, len(executions))
	for _, execution := range executions {
		if execution.RunID != "" {
			executionByRun[execution.RunID] = execution
		}
	}
	failureThreshold := 2
	if cfg.Criticality >= 75 {
		failureThreshold = 1
	}
	for _, diag := range runDiagnostics {
		execution, linked := executionByRun[diag.RunID]
		if !linked {
			continue
		}
		active := execution.Status == domain.RunPending || execution.Status == domain.RunRunning || execution.Status == domain.RunWaiting || execution.Status == domain.RunPaused
		if active && diag.Approvals.Pending > 0 {
			detail := fmt.Sprintf("Execution ожидает %d решения пользователя.", diag.Approvals.Pending)
			if diag.Approvals.MaxWaitMs > 0 {
				detail += fmt.Sprintf(" Максимальное ожидание — %s.", (time.Duration(diag.Approvals.MaxWaitMs) * time.Millisecond).Round(time.Second))
			}
			result = append(result, domain.CompanionIntervention{
				ID: "run-approval-" + diag.RunID, Level: "suggestion", Title: "Execution ждёт подтверждения", Detail: detail,
				ActionTab: "quests", RelatedID: execution.ID, ActionKind: domain.CompanionInterventionOpenRun, ActionLabel: "Открыть решение",
			})
		}
		if diag.Tools.Failed >= failureThreshold {
			failedTools := make([]string, 0, 3)
			for _, item := range diag.Tools.Items {
				if item.Failed > 0 && len(failedTools) < 3 {
					failedTools = append(failedTools, fmt.Sprintf("%s (%d)", item.Name, item.Failed))
				}
			}
			detail := fmt.Sprintf("Зафиксировано %d ошибок tools", diag.Tools.Failed)
			if len(failedTools) > 0 {
				detail += ": " + strings.Join(failedTools, ", ")
			}
			detail += ". Повторение того же вызова без изменения подхода увеличит стоимость и риск зацикливания."
			item := domain.CompanionIntervention{
				ID: "run-tool-failures-" + diag.RunID, Level: "warning", Title: "Агент сталкивается с ошибками tools", Detail: detail,
				ActionTab: "quests", RelatedID: execution.ID, ActionKind: domain.CompanionInterventionOpenRun, ActionLabel: "Открыть хронику",
			}
			if active {
				item.ActionKind = domain.CompanionInterventionMessageRun
				item.ActionLabel = "Направить агента"
				item.ActionMessage = "Проанализируй последние ошибки инструментов и измени подход или аргументы. Не повторяй тот же неуспешный вызов. Если не хватает разрешения, контекста или подходящего tool — остановись и явно сообщи, что именно требуется."
			}
			result = append(result, item)
		}
		pressure := 0
		if diag.Context.InputBudgetTokens > 0 {
			pressure = diag.Context.PeakInputTokens * 100 / diag.Context.InputBudgetTokens
		}
		if active && (pressure >= 80 || diag.Context.Compactions >= 2) {
			level := "suggestion"
			if pressure >= 95 || diag.Context.Compactions >= 3 {
				level = "warning"
			}
			result = append(result, domain.CompanionIntervention{
				ID: "run-live-context-" + diag.RunID, Level: level, Title: "Контекст execution требует фокусировки",
				Detail:    fmt.Sprintf("Пиковая загрузка входного бюджета — %d%%, сжатий — %d. Лучше сузить поиск и сохранить ключевые выводы перед следующим шагом.", pressure, diag.Context.Compactions),
				ActionTab: "quests", RelatedID: execution.ID, ActionKind: domain.CompanionInterventionMessageRun, ActionLabel: "Сфокусировать агента",
				ActionMessage: "Сфокусируй дальнейшую работу на минимальном наборе релевантных файлов. Зафиксируй уже подтверждённые выводы кратко, не перечитывай нерелевантный контекст и перед следующим изменением назови конкретную проверяемую гипотезу.",
			})
		}
		if active && diag.Retrieval.TruncatedSearches > 0 {
			result = append(result, domain.CompanionIntervention{
				ID: "run-retrieval-truncated-" + diag.RunID, Level: "suggestion", Title: "Поиск вернул неполный контекст",
				Detail:    fmt.Sprintf("Обрезано поисков: %d. Нельзя считать отсутствие результата доказательством без более узкого запроса.", diag.Retrieval.TruncatedSearches),
				ActionTab: "quests", RelatedID: execution.ID, ActionKind: domain.CompanionInterventionMessageRun, ActionLabel: "Уточнить поиск",
				ActionMessage: "Последний поиск был усечён. Уточни запрос по символу, файлу или зависимости и не делай вывод об отсутствии кода только по неполному результату.",
			})
		}
		if active && diag.Completion.RevisionRequests > 0 {
			result = append(result, domain.CompanionIntervention{
				ID: "run-completion-revision-" + diag.RunID, Level: "warning", Title: "Доказательств завершения пока недостаточно",
				Detail:    fmt.Sprintf("Локальная проверка уже запросила доработку %d раз. Нужна фактическая верификация актуальной ревизии.", diag.Completion.RevisionRequests),
				ActionTab: "quests", RelatedID: execution.ID, ActionKind: domain.CompanionInterventionMessageRun, ActionLabel: "Напомнить о проверке",
				ActionMessage: "Не завершай квест текстовым утверждением. Выполни разрешённую релевантную проверку на текущей ревизии и опирайся на её фактический exit code; если verifier недоступен, явно сообщи об этом.",
			})
		}
		if active && diag.Model.Retries >= 2 {
			result = append(result, domain.CompanionIntervention{
				ID: "run-provider-retries-" + diag.RunID, Level: "warning", Title: "Провайдер нестабилен",
				Detail:    fmt.Sprintf("Запрос к модели повторялся %d раз. Проверьте подключение или fallback-модель, прежде чем расход продолжит расти.", diag.Model.Retries),
				ActionTab: "quests", RelatedID: execution.ID, ActionKind: domain.CompanionInterventionOpenRun, ActionLabel: "Открыть execution",
			})
		}
	}
	return result
}

func BuildIDEInterventions(observations []domain.IDEObservation) []domain.CompanionIntervention {
	return BuildIDEInterventionsGated(observations, domain.CompanionConfig{Initiative: 50, QuestionStrictness: 70}, InterveneContext{})
}

func BuildIDEInterventionsGated(observations []domain.IDEObservation, cfg domain.CompanionConfig, ctx InterveneContext) []domain.CompanionIntervention {
	result := make([]domain.CompanionIntervention, 0, 4)
	diagnosticErrors, diagnosticWarnings := 0, 0
	diagnosticExamples := make([]string, 0, 3)
	firstDiagnosticID, firstPath, firstLine := "", "", 0
	focusDiagnosticID, focusPath, focusLine := "", "", 0
	focus := strings.ReplaceAll(strings.TrimSpace(ctx.FocusPath), "\\", "/")
	for _, item := range observations {
		if item.Kind != "diagnostic" {
			continue
		}
		switch item.Level {
		case "error":
			diagnosticErrors++
		case "warning":
			diagnosticWarnings++
		}
		if len(diagnosticExamples) < 3 && (item.Level == "error" || item.Level == "warning") {
			if firstDiagnosticID == "" {
				firstDiagnosticID = item.ID
				firstPath, firstLine = item.Path, item.Line
			}
			pathNorm := strings.ReplaceAll(item.Path, "\\", "/")
			if focus != "" && focusDiagnosticID == "" && pathNorm != "" &&
				(strings.EqualFold(pathNorm, focus) || strings.HasSuffix(focus, "/"+pathNorm) || strings.HasSuffix(pathNorm, "/"+focus)) {
				focusDiagnosticID, focusPath, focusLine = item.ID, item.Path, item.Line
			}
			location := item.Path
			if item.Line > 0 {
				location += fmt.Sprintf(":%d", item.Line)
			}
			diagnosticExamples = append(diagnosticExamples, strings.TrimSpace(location+" · "+item.Summary))
		}
	}
	if diagnosticErrors > 0 || diagnosticWarnings > 0 {
		level := diagnosticInterventionLevel(observations, ctx, diagnosticErrors)
		detail := fmt.Sprintf("Problems сообщает: ошибок %d, предупреждений %d.", diagnosticErrors, diagnosticWarnings)
		if len(diagnosticExamples) > 0 {
			detail += " " + strings.Join(diagnosticExamples, "; ")
		}
		relatedID, relatedPath, relatedLine := firstDiagnosticID, firstPath, firstLine
		if focusDiagnosticID != "" {
			relatedID, relatedPath, relatedLine = focusDiagnosticID, focusPath, focusLine
		}
		result = append(result, domain.CompanionIntervention{
			ID: "ide-diagnostics", Level: level, Title: "В проекте есть диагностика редактора",
			Detail: detail, ActionTab: "overview", RelatedID: relatedID,
			RelatedPath: relatedPath, RelatedLine: relatedLine,
			ActionKind: domain.CompanionInterventionPrompt, ActionLabel: "Подготовить исправление",
			ActionMessage: "Исправь текущие ошибки IDE. Подготовь проверяемый квест на основе текущих Problems и последних неуспешных команд; не запускай его без моего подтверждения.",
		})
	}
	seenCommands := map[string]bool{}
	for _, item := range observations {
		if item.Kind != "terminal" && item.Kind != "task" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(item.Command))
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(item.Source + "\x00" + item.Summary))
		}
		if seenCommands[key] {
			continue
		}
		seenCommands[key] = true
		if item.ExitCode == nil || *item.ExitCode == 0 {
			continue
		}
		failedTarget := trim(strings.TrimSpace(item.Command), 180)
		if failedTarget == "" {
			failedTarget = trim(strings.TrimSpace(item.Summary), 180)
		}
		result = append(result, domain.CompanionIntervention{
			ID: "ide-command-failed-" + item.ID, Level: "warning", Title: "Команда IDE завершилась с ошибкой",
			Detail:    fmt.Sprintf("%s завершилась с кодом %d. %s", trim(item.Command, 180), *item.ExitCode, trim(item.Detail, 500)),
			ActionTab: "overview", RelatedID: item.ID,
			ActionKind: domain.CompanionInterventionPrompt, ActionLabel: "Разобрать сбой",
			ActionMessage: fmt.Sprintf("Исправь ошибку команды IDE «%s». Подготовь проверяемый квест по текущим Problems и выводу команды; не запускай его без моего подтверждения.", failedTarget),
		})
		if len(result) >= 4 {
			break
		}
	}
	for _, item := range observations {
		if item.Kind != "debug" && item.Kind != "run" {
			continue
		}
		if item.Level == "info" && cfg.Initiative < 70 {
			continue
		}
		id := "ide-run-active"
		title := "Активна цель запуска"
		label := "Разбери цель запуска"
		msg := fmt.Sprintf("Разбери цель запуска «%s». Предложи проверяемый следующий шаг, не запуская ничего без подтверждения.", trim(item.Summary, 180))
		if item.Kind == "debug" {
			id = "ide-debug-active"
			title = "Идёт отладка"
			label = "Разбери отладку"
			msg = fmt.Sprintf("Разбери отладку «%s». Предложи проверяемый следующий шаг, не запуская ничего без подтверждения.", trim(item.Summary, 180))
		}
		level := "suggestion"
		if item.Level == "error" {
			level = "warning"
		}
		result = append(result, domain.CompanionIntervention{
			ID: id, Level: level, Title: title, Detail: trim(item.Detail, 500),
			ActionTab: "overview", RelatedID: item.ID, RelatedPath: item.Path,
			ActionKind: domain.CompanionInterventionPrompt, ActionLabel: label, ActionMessage: msg,
		})
		break
	}
	for _, item := range observations {
		if item.Kind != "scm" {
			continue
		}
		if item.Level == "info" && cfg.Initiative < 60 {
			continue
		}
		snap := parseSCMObservation(item)
		if snap.GitDirty() == 0 && snap.Unsaved == 0 {
			continue
		}
		level := "suggestion"
		if item.Level == "warning" || item.Level == "error" || snap.GitDirty() >= 8 || (snap.Behind > 0 && snap.GitDirty() > 0) {
			level = "warning"
		}
		title, detail, message := scmInterventionCopy(snap, item)
		result = append(result, domain.CompanionIntervention{
			ID: "ide-scm-dirty", Level: level, Title: title,
			Detail: detail, ActionTab: "overview", RelatedID: item.ID,
			RelatedPath: strings.TrimSpace(item.Path),
			ActionKind:  domain.CompanionInterventionPrompt, ActionLabel: "Разобрать diff",
			ActionMessage: message,
		})
		break
	}
	for i := range result {
		result[i].OccurrenceKey = InterventionOccurrenceKey(result[i], observations)
	}
	return GateIDEInterventions(cfg, result, observations, ctx)
}

type scmSnapshot struct {
	Changed   int
	Staged    int
	Untracked int
	Unsaved   int
	Ahead     int
	Behind    int
	Branch    string
}

func (s scmSnapshot) GitDirty() int {
	return s.Changed + s.Staged + s.Untracked
}

func parseSCMObservation(item domain.IDEObservation) scmSnapshot {
	snap := scmSnapshot{Branch: ""}
	raw := item.Detail
	if raw == "" {
		raw = item.Summary
	}
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "changed":
			fmt.Sscanf(value, "%d", &snap.Changed)
		case "staged":
			fmt.Sscanf(value, "%d", &snap.Staged)
		case "untracked":
			fmt.Sscanf(value, "%d", &snap.Untracked)
		case "unsaved", "dirty":
			fmt.Sscanf(value, "%d", &snap.Unsaved)
		case "ahead":
			fmt.Sscanf(value, "%d", &snap.Ahead)
		case "behind":
			fmt.Sscanf(value, "%d", &snap.Behind)
		case "branch":
			if value != "" && value != "—" && value != "-" {
				snap.Branch = value
			}
		}
	}
	return snap
}

func scmInterventionCopy(snap scmSnapshot, item domain.IDEObservation) (title, detail, message string) {
	parts := make([]string, 0, 6)
	if snap.Changed > 0 {
		parts = append(parts, fmt.Sprintf("изменено %d", snap.Changed))
	}
	if snap.Staged > 0 {
		parts = append(parts, fmt.Sprintf("в индексе %d", snap.Staged))
	}
	if snap.Untracked > 0 {
		parts = append(parts, fmt.Sprintf("untracked %d", snap.Untracked))
	}
	if snap.Unsaved > 0 {
		parts = append(parts, fmt.Sprintf("несохранённых буферов %d", snap.Unsaved))
	}
	if snap.Behind > 0 {
		parts = append(parts, fmt.Sprintf("behind upstream %d", snap.Behind))
	}
	if snap.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("ahead %d", snap.Ahead))
	}
	if snap.Branch != "" {
		parts = append(parts, "ветка "+snap.Branch)
	}
	detail = strings.Join(parts, "; ")
	if detail == "" {
		detail = trim(item.Detail, 500)
		if detail == "" {
			detail = trim(item.Summary, 500)
		}
	}
	switch {
	case snap.GitDirty() > 0 && snap.Behind > 0:
		title = "Локальные правки и отставание от upstream"
		message = "Разбери незакоммиченные изменения и отставание от upstream. Предложи проверяемый следующий шаг; не запускай квест без подтверждения."
	case snap.GitDirty() > 0 && snap.Unsaved > 0:
		title = "Есть незакоммиченные и несохранённые правки"
		message = "Разбери незакоммиченные изменения и несохранённые буферы. Предложи проверяемый следующий шаг; не запускай квест без подтверждения."
	case snap.GitDirty() > 0:
		title = "Есть незакоммиченные изменения"
		message = "Разбери текущие незакоммиченные изменения и риски. Предложи проверяемый следующий шаг; не запускай квест без подтверждения."
	case snap.Unsaved > 0:
		title = "Есть несохранённые буферы"
		message = "Разбери несохранённые правки в редакторе и риски потери. Предложи проверяемый следующий шаг; не запускай квест без подтверждения."
	default:
		title = "Есть незавершённые изменения"
		message = "Разбери текущие незакоммиченные изменения и риски. Предложи проверяемый следующий шаг; не запускай квест без подтверждения."
	}
	return title, detail, message
}

// diagnosticInterventionLevel keeps Problems soft by default; critical only for focus / fresh novelty / rising errors.
func diagnosticInterventionLevel(observations []domain.IDEObservation, ctx InterveneContext, errorCount int) string {
	if errorCount <= 0 {
		return "warning"
	}
	now := ctx.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	focus := strings.ReplaceAll(strings.TrimSpace(ctx.FocusPath), "\\", "/")
	focused, freshNovel, rising := false, false, false
	errorPaths := 0
	for _, obs := range observations {
		if obs.Kind != "diagnostic" || obs.Level != "error" {
			continue
		}
		errorPaths++
		path := strings.ReplaceAll(obs.Path, "\\", "/")
		if focus != "" && path != "" && (strings.EqualFold(path, focus) || strings.HasSuffix(focus, "/"+path) || strings.HasSuffix(path, "/"+focus)) {
			focused = true
		}
		first := obs.FirstSeen
		if first.IsZero() {
			first = obs.ObservedAt
		}
		last := obs.LastSeen
		if last.IsZero() {
			last = obs.ObservedAt
		}
		count := obs.Count
		if count <= 0 {
			count = 1
		}
		if now.Sub(first) <= 5*time.Minute {
			freshNovel = true
		}
		if count <= 2 && now.Sub(last) <= 2*time.Minute {
			freshNovel = true
		}
		if count >= 3 && now.Sub(last) <= 3*time.Minute && now.Sub(first) <= 45*time.Minute {
			rising = true
		}
	}
	if errorCount >= 5 {
		rising = true
	}
	if focused || freshNovel || rising {
		return "critical"
	}
	return "warning"
}

func MergeInterventions(groups ...[]domain.CompanionIntervention) []domain.CompanionIntervention {
	return MergeInterventionsWithObservations(nil, groups...)
}

func MergeInterventionsWithObservations(observations []domain.IDEObservation, groups ...[]domain.CompanionIntervention) []domain.CompanionIntervention {
	result := make([]domain.CompanionIntervention, 0, 8)
	seen := map[string]bool{}
	for _, group := range groups {
		for _, item := range group {
			if item.ID == "" || seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			if item.OccurrenceKey == "" {
				item.OccurrenceKey = InterventionOccurrenceKey(item, observations)
			}
			result = append(result, item)
		}
	}
	priority := map[string]int{"critical": 0, "warning": 1, "suggestion": 2}
	sort.SliceStable(result, func(i, j int) bool { return priority[result[i].Level] < priority[result[j].Level] })
	return result
}

func VisibleInterventions(items []domain.CompanionIntervention, dismissed map[string]bool, limit int) ([]domain.CompanionIntervention, int) {
	if limit <= 0 {
		limit = 8
	}
	visible := make([]domain.CompanionIntervention, 0, min(limit, len(items)))
	hidden := 0
	for _, item := range items {
		if dismissed[item.OccurrenceKey] {
			hidden++
			continue
		}
		if len(visible) < limit {
			visible = append(visible, item)
		}
	}
	return visible, hidden
}
