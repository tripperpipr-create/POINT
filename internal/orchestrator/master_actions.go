package orchestrator

import (
	"encoding/json"
	"strings"

	"local-agent-workbench/internal/domain"
	workbenchtools "local-agent-workbench/internal/tools"
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
)

// IsMasterActionTool — вызов разговора, а не обращение к проекту. Реплей
// обучения сохраняет только такие определения: читающие инструменты уже
// отработали, и их результаты едут в реплее записанным свидетельством.
func IsMasterActionTool(name string) bool {
	switch name {
	case masterActionProposeBrief, masterActionAskClarifications, masterActionSuggestMemory:
		return true
	}
	return false
}

// masterActions — черновик хода, собранный из вызовов. Последний принятый
// вызов побеждает: модель уточняет задание по замечаниям сервера, и в карточку
// должна попасть исправленная версия, а не первая.
type masterActions struct {
	title          string
	proposalID     string
	brief          *domain.TaskBrief
	rejectedBriefs int
	clarifications []domain.MasterQuestion
	memory         []string
	undecodable    bool
}

func (a *masterActions) execute(name string, arguments json.RawMessage) domain.ToolResult {
	switch name {
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
		for _, issue := range issues {
			lines = append(lines, issue.Path+": "+issue.Message)
		}
		return workbenchtools.FailWithHint("invalid_brief", "задание не прошло проверку сервера: "+strings.Join(lines, "; "), "исправь перечисленные поля и вызови propose_brief снова с полным заданием")
	}
	a.brief = &brief
	a.title = input.Title
	a.proposalID = strings.TrimSpace(input.ProposalID)
	return workbenchtools.OK(map[string]any{
		"accepted": true, "state": brief.State,
		"note": "Задание принято черновиком; карточку человек увидит под ответом. Не пересказывай его в тексте.",
	})
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
	return name == masterActionProposeBrief || name == masterActionAskClarifications
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
