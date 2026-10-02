package orchestrator

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"local-agent-workbench/internal/domain"
	workbenchtools "local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/verification"
)

// Инструменты разговора Мастера — то, чем он оформляет структуру хода.
//
// Раньше вся структура ехала JSON-конвертом в тексте ответа: даже на «что
// делает этот файл?» модель обязана была вернуть intent, reply и brief. Слабая
// модель теряла на этом целые ходы — две подсказки формата, отдельный круг
// починки задания и в конце «модель не вернула структуру» при разборчивом
// ответе. Теперь ответ — обычный текст, а задание, уточнения и память
// оформляются вызовом, схему которого держит протокол, а не разбор строки.
//
// Инструменты ничего не исполняют и в каталог проекта не входят: они только
// копят черновик хода. Сохраняет его DiscussTask тем же кодом, что и раньше, —
// версия, digest и права остаются за сервером.
const (
	masterActionProposeBrief      = "propose_brief"
	masterActionAskClarifications = "ask_clarifications"
	masterActionSuggestMemory     = "suggest_memory"
	masterActionFastTask          = "dispatch_fast_task"
	// masterActionGitRequest — просьба человека закоммитить, отправить или
	// создать MR. Мастер git не трогает: он передаёт просьбу git-агенту, тот
	// показывает карточку, и действие исполняется только по нажатию.
	masterActionGitRequest = "request_git_action"
)

// IsMasterActionTool — вызов разговора, а не обращение к проекту. Реплей
// обучения сохраняет только такие определения: читающие инструменты уже
// отработали, и их результаты едут в реплее записанным свидетельством.
func IsMasterActionTool(name string) bool {
	switch name {
	case masterActionProposeBrief, masterActionAskClarifications, masterActionSuggestMemory, masterActionFastTask, masterActionGitRequest:
		return true
	}
	return false
}

// masterActions — черновик хода, собранный из вызовов. Последний принятый
// вызов побеждает: модель уточняет задание по замечаниям сервера, и в карточку
// должна попасть исправленная версия, а не первая.
type masterActions struct {
	fastTask       string
	gitRequest     *MasterGitRequest
	title          string
	proposalID     string
	brief          *domain.TaskBrief
	rejectedBriefs int
	// rejectReasons — почему сервер отверг бриф; уходят в дефекты операции.
	rejectReasons  []string
	clarifications []domain.MasterQuestion
	memory         []string
	undecodable    bool
	// questionsPrompted — модели уже дан круг, чтобы задать открытые вопросы
	// карточкой. Второй раз ход её не ждёт.
	questionsPrompted bool
}

func (a *masterActions) execute(name string, arguments json.RawMessage) domain.ToolResult {
	switch name {
	case masterActionFastTask:
		var input struct {
			Task string `json:"task"`
		}
		if failure := workbenchtools.Decode(arguments, &input); failure != nil {
			return *failure
		}
		input.Task = strings.TrimSpace(input.Task)
		if input.Task == "" || len(input.Task) > 32768 || len(a.clarifications) > 0 || a.brief != nil {
			return workbenchtools.Fail("invalid_fast_task", "Fast task requires clear bounded work without unresolved questions or a proposed plan")
		}
		a.fastTask = input.Task
		return workbenchtools.OK(map[string]any{"route": "fast", "note": "The server will start the system Fast Agent in the pinned workspace."})
	case masterActionGitRequest:
		var input MasterGitRequest
		if failure := workbenchtools.Decode(arguments, &input); failure != nil {
			return *failure
		}
		input.Action, input.QuestID = strings.TrimSpace(input.Action), strings.TrimSpace(input.QuestID)
		if input.Action != "commit" && input.Action != "push" && input.Action != "merge_request" {
			return workbenchtools.FailWithHint("invalid_git_request", "action должен быть commit, push или merge_request", "назови одно действие; несколько — по очереди после подтверждения")
		}
		a.gitRequest = &input
		return workbenchtools.OK(map[string]any{"route": "git", "note": "Git-агент покажет человеку карточку с подтверждением; сам ничего не делай и не обещай, что уже сделано."})
	case masterActionProposeBrief:
		return a.proposeBrief(arguments)
	case masterActionAskClarifications:
		return a.askClarifications(arguments)
	case masterActionSuggestMemory:
		return a.suggestMemory(arguments)
	}
	return workbenchtools.Fail("tool_not_allowed", "неизвестный инструмент разговора")
}

func (a *masterActions) proposeBrief(arguments json.RawMessage) domain.ToolResult {
	var input struct {
		ProposalID string          `json:"proposalId"`
		Title      string          `json:"title"`
		Brief      json.RawMessage `json:"brief"`
	}
	if failure := workbenchtools.Decode(arguments, &input); failure != nil {
		a.undecodable = true
		return *failure
	}
	var brief domain.TaskBrief
	if err := json.Unmarshal(unwrapJSONString(input.Brief), &brief); err != nil || len(input.Brief) == 0 {
		a.rejectedBriefs++
		a.rejectReasons = append(a.rejectReasons, "brief is not a task object")
		return workbenchtools.FailWithHint("invalid_brief", "brief должен быть объектом задания по схеме инструмента", "передай brief объектом, а не строкой, и вызови propose_brief снова")
	}
	brief = domain.NormalizeTaskBrief(brief)
	// Версию и состояние назначает сервер; здесь они нужны только затем, чтобы
	// проверка судила задание так же, как при сохранении.
	brief.Version = 1
	brief.State = "discussion"
	if len(brief.OpenQuestions) == 0 && brief.Mode != domain.TaskModeUndecided {
		brief.State = "ready"
	}
	if issues := domain.ValidateTaskBriefIssues(brief); len(issues) > 0 {
		a.rejectedBriefs++
		lines := make([]string, 0, len(issues))
		defer func() { a.rejectReasons = append(a.rejectReasons, strings.Join(lines, "; ")) }()
		hints := []string{"исправь перечисленные поля и вызови propose_brief снова с полным заданием"}
		for _, issue := range issues {
			lines = append(lines, issue.Path+": "+issue.Message)
			if hint := briefIssueHints[issue.Code]; hint != "" && !slices.Contains(hints, hint) {
				hints = append(hints, hint)
			}
		}
		return workbenchtools.FailWithHint("invalid_brief", "задание не прошло проверку сервера: "+strings.Join(lines, "; "), strings.Join(hints, "; "))
	}
	for _, check := range []func(domain.TaskBrief) *domain.ToolResult{maskedCriterionCommands, manualCriteriaInChange} {
		if failure := check(brief); failure != nil {
			a.rejectedBriefs++
			reason := "criterion rejected"
			if failure.Error != nil {
				reason = failure.Error.Message
			}
			a.rejectReasons = append(a.rejectReasons, reason)
			return *failure
		}
	}
	a.brief = &brief
	a.title = input.Title
	a.proposalID = strings.TrimSpace(input.ProposalID)
	note := "Задание принято черновиком; карточку человек увидит под ответом. Не пересказывай его в тексте."
	if a.awaitsQuestionCard() {
		note = "Задание принято в обсуждении. Открытые вопросы задай вызовом ask_clarifications с вариантами, первым поставь свой; в тексте их не повторяй."
	}
	return workbenchtools.OK(map[string]any{"accepted": true, "state": brief.State, "note": note})
}

// manualCriteriaInChange отказывает ручному критерию в задании, которое меняет
// проект. Ручной приёмки у квеста больше нет: проверку выполняет сам квест —
// тестом, e2e или командой, которая поднимает сервис, обращается к нему и
// останавливает; если у квеста работает, он выполнен. Ручной критерий держал
// готовый результат в «ждёт приёмки» и коммит до неё (02.10). Правило здесь, а
// не в домене: сохранённые задания с manual не должны уйти в карантин.
func manualCriteriaInChange(brief domain.TaskBrief) *domain.ToolResult {
	if brief.ResultKind != "workspace_change" && brief.ResultKind != "hub_tool" {
		return nil
	}
	var ids []string
	for _, criterion := range brief.Criteria {
		if criterion.Kind == "manual" {
			ids = append(ids, criterion.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	result := workbenchtools.FailWithHint("manual_criterion",
		"ручная приёмка отключена: критерии "+strings.Join(ids, ", ")+" должен проверить сам квест",
		"перепиши их в verification с run_command, который исполнится в песочнице: тест или e2e проекта, либо одна команда, которая поднимает сервис, обращается к нему и останавливает его; если машиной проверить нельзя — спроси человека, как проверить, через ask_clarifications")
	return &result
}

// maskedCriterionCommands отказывает критерию, чья команда проглатывает код
// выхода: `npm run verify || true` проходит и при упавшей сборке, и итог
// сказал бы «проверено» там, где проверка провалилась (Q08). Правило живёт
// здесь, а не в domain.ValidateTaskBrief: та решает и об утверждённости уже
// сохранённых заданий, и новое правило отправило бы их в карантин.
func maskedCriterionCommands(brief domain.TaskBrief) *domain.ToolResult {
	var lines []string
	for index, criterion := range brief.Criteria {
		if criterion.Kind == "manual" || criterion.Tool != "run_command" {
			continue
		}
		var arguments struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(criterion.Arguments, &arguments)
		if fragment, masked := verification.MasksExitCode(arguments.Command); masked {
			lines = append(lines, fmt.Sprintf("/criteria/%d: команда критерия %q скрывает код выхода (%s)", index, criterion.ID, fragment))
		}
	}
	if len(lines) == 0 {
		return nil
	}
	failure := workbenchtools.FailWithHint("invalid_brief", "задание не прошло проверку сервера: "+strings.Join(lines, "; "), "убери из команды «|| true», «; exit 0», «; echo …» и «set +e»: критерий должен падать вместе с проверкой; пайп вроде «| tail» допустим — в песочнице и в оболочке хоста включён pipefail")
	return &failure
}

// awaitsQuestionCard — задание осталось в обсуждении, а карточки вопросов нет.
// Открытые вопросы без вариантов человек видит голым списком и отвечает на них
// текстом; ход даёт модели один круг, чтобы задать их карточкой.
func (a *masterActions) awaitsQuestionCard() bool {
	return a.brief != nil && a.brief.State == "discussion" && len(a.clarifications) == 0 && !a.questionsPrompted
}

// Замечание домена говорит, что не так, но не как исправить. «only a
// workspace_change task may authorize file changes» модель прочла как запрет
// писать файлы и гадала, какое из полей уступить. Для частых замечаний
// подсказка называет само исправление.
var briefIssueHints = map[string]string{
	"write_files_result_mismatch": "если результат — созданные или изменённые файлы проекта, поставь resultKind=workspace_change; если код нужен только текстом в ответе, оставь writeFiles=false",
}

func (a *masterActions) askClarifications(arguments json.RawMessage) domain.ToolResult {
	var input struct {
		Items []domain.MasterQuestion `json:"items"`
	}
	if failure := workbenchtools.Decode(arguments, &input); failure != nil {
		a.undecodable = true
		return *failure
	}
	var accepted []domain.MasterQuestion
	for _, item := range input.Items {
		item.Text = strings.TrimSpace(item.Text)
		if item.Text == "" {
			continue
		}
		// Тот же предел держит DiscussTask: длинный вопрос он отбросит, и под
		// ответом «вопросы ниже» не окажется ничего. Модель должна узнать об
		// этом сейчас и сократить вопрос.
		if len([]rune(item.Text)) > 1000 {
			return workbenchtools.FailWithHint("invalid_input", "вопрос длиннее 1000 знаков", "сократи вопрос до одной-двух фраз и вызови ask_clarifications снова")
		}
		accepted = append(accepted, item)
		if len(accepted) == 2 {
			break
		}
	}
	if len(accepted) == 0 {
		return workbenchtools.FailWithHint("invalid_input", "нет ни одного вопроса с текстом", "передай items с полем text")
	}
	a.clarifications = accepted
	return workbenchtools.OK(map[string]any{"shown": len(accepted), "note": "Вопросы покажутся карточкой под ответом; не дублируй их в тексте."})
}

func (a *masterActions) suggestMemory(arguments json.RawMessage) domain.ToolResult {
	var input struct {
		Entries []string `json:"entries"`
	}
	if failure := workbenchtools.Decode(arguments, &input); failure != nil {
		a.undecodable = true
		return *failure
	}
	entries := cleanList(input.Entries, 3)
	if len(entries) == 0 {
		return workbenchtools.FailWithHint("invalid_input", "нечего предложить", "передай entries с текстом записей")
	}
	a.memory = entries
	return workbenchtools.OK(map[string]any{"proposed": len(entries), "note": "Человек решит, запоминать ли это."})
}

// envelope переводит собранные вызовы в прежнюю форму результата хода: всё,
// что ниже по течению сохраняет карточку, вопросы и память, остаётся прежним.
func (a *masterActions) envelope(reply string) taskIntakeEnvelope {
	envelope := taskIntakeEnvelope{Intent: "chat", Reply: reply, Clarifications: a.clarifications, MemorySuggestions: a.memory}
	envelope.FastTask = a.fastTask
	envelope.GitRequest = a.gitRequest
	if a.brief != nil {
		envelope.Intent = "task"
		envelope.Brief = a.brief
		envelope.Title = a.title
		envelope.ProposalID = a.proposalID
	}
	// Задание пытались оформить, но ни одна попытка не прошла проверку: ответ
	// есть, карточки не будет, и об этом надо сказать, а не промолчать.
	envelope.degraded = a.brief == nil && a.rejectedBriefs > 0
	return envelope
}

// silentReply — ответ за модель, которая оформила вызов и промолчала.
// Размышляющие модели часто так и делают: думают, зовут инструмент и не пишут
// ни слова. Лишний круг ради вежливой фразы стоил бы полминуты ожидания, а
// карточка под ответом и так говорит сама за себя.
func (a *masterActions) silentReply() string {
	switch {
	case a.fastTask != "":
		return "Передаю задачу Fast Agent для локального выполнения."
	case a.gitRequest != nil:
		return "Передаю git-агенту — подтвердите действие карточкой ниже."
	case a.brief != nil && len(a.clarifications) > 0:
		return "Набросал задание; прежде чем его утверждать, нужно уточнить — вопросы ниже."
	case a.brief != nil:
		return "Оформил задание — карточка ниже."
	case len(a.clarifications) > 0:
		return "Прежде чем продолжить, нужно уточнить — вопросы ниже."
	case len(a.memory) > 0:
		return "Предлагаю запомнить это — решение за вами."
	case a.rejectedBriefs > 0:
		// Задание не прошло проверку ни разу, а модель промолчала. Это не
		// сбой модели, и «проверьте модель» здесь было бы неправдой.
		return "Не смог оформить задание: оно не прошло проверку сервера. Уточните, что нужно получить, — обсуждение сохранено."
	}
	return ""
}

// concludes — вызов, после которого ходу нечего добавить: карточка задания
// или вопросы уже и есть ответ. Предложение запомнить — нет: модель зовёт его
// попутно и ответ на сам вопрос ещё впереди.
func masterActionConcludes(name string) bool {
	return name == masterActionProposeBrief || name == masterActionAskClarifications || name == masterActionFastTask || name == masterActionGitRequest
}

// MasterGitRequest — просьба человека к git-агенту. QuestID пуст — последний
// квест разговора.
type MasterGitRequest struct {
	Action  string `json:"action"`
	QuestID string `json:"questId,omitempty"`
}

// Некоторые модели сериализуют вложенный объект строкой: "brief": "{...}".
// Это та же структура, и отказывать из-за кавычек значит терять ход.
func unwrapJSONString(raw json.RawMessage) json.RawMessage {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return json.RawMessage(text)
	}
	return raw
}
