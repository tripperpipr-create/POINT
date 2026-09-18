package companion

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"local-agent-workbench/internal/domain"
)

// Столько раз подряд компаньон может сходить в инструменты за одно сообщение.
//
// Потолок низкий нарочно: компаньон отвечает на каждую реплику, и цикл без
// границы превратил бы «Привет» в поход по индексу проекта. Трёх шагов хватает
// на «посмотреть ветки → прочитать файл → ответить»; всё, что длиннее, — уже
// работа агента, и её запускают квестом с подтверждением.
const maxCompanionToolSteps = 3

// Сколько знаков результата инструмента уходит модели. Читающие инструменты
// возвращают и мегабайтные выдачи; целиком такая вытеснила бы из окна и
// историю разговора, и сам вопрос — ровно то переполнение контекста, ради
// ухода от которого инструменты и заводились.
const maxCompanionToolResultChars = 6000

// Что модели говорят про инструменты. Три вещи, и каждая от своей ошибки:
// вызывать по делу — иначе «Привет» уходит в индекс; не выдумывать состояние —
// иначе инструмент заводится ради вида; и напоминание, что читающий набор
// границу «ничего не меняю» не отменяет.
const companionToolInstructions = `You may call the read-only tools provided with this request to check facts about the workspace before answering. They only read: they never change files, run commands, or start anything. Call one when the supplied context cannot answer the question — for example listing branches, reading a specific file, or searching the code. Do not call anything when the context already answers, and never guess workspace state you did not read. After tool results arrive, answer with the single JSON object described above. Having read-only tools does not widen your boundaries: you still cannot execute, mutate, or claim that work was performed. When a tool answers with an error, say plainly in your reply what you could not check and answer from what you already have; never describe the content you failed to read, and never repeat the same failing call - try a different tool or different arguments only if that can actually help.`

// Напоминание о форме ответа после инструментов. Короткое и без повтора самой
// схемы: схема уже стоит в системном сообщении, а второй её экземпляр в конце
// разговора — второй источник правды о формате.
const companionEnvelopeReminder = `Your previous message was not the required JSON object. Send the same answer again as one JSON object with the schema from the system message, and nothing else: no markdown, no fenced block, no text around it.`

// Схема конверта стоит в системном сообщении, а к моменту ответа его отделяют
// от модели результаты инструментов и вся прошлая переписка. Исправляющий круг
// это лечил, но стоил целого лишнего обращения к модели — в живом логе он
// срабатывал почти на каждом ответе, добавляя к нему пять секунд и десятки
// килобайт входа. Дешевле напомнить формат там, где ответ уже ждут; про
// инструменты сказано отдельно, иначе напоминание торопило бы модель отвечать
// вместо нужного вызова.
const companionEnvelopeExpectation = `Reminder about the answer format: when you are ready to answer the person, reply with exactly one JSON object matching the schema from the system message, and nothing else - no markdown, no fenced block, no text around it. If you still need a tool result first, call the tool instead.`

// Что делать, когда прошлый ответ человеку не помог. Отметка приходит из
// интерфейса и действует один круг: повторять тот же ход бессмысленно, а
// менять поведение навсегда из-за одного недовольства — слишком.
const companionRejectedAnswerInstruction = `The person marked your previous answer as unhelpful. Do not repeat the same approach or restate it in other words: check a different part of the project with a tool, propose another path, or ask one precise clarifying question. Say plainly what you are trying differently.`

// Когда какой уровень ответа. Схема перечисляла три значения и молчала о том,
// чем они отличаются: модель ставила уровень наугад. Интерфейс тем временем
// рисует критичному ответу свою рамку — и уровень, который ставят всему подряд
// или не ставят никогда, эту рамку обесценивает.
const companionAnswerLevels = `Choose the answer level deliberately. Use "critical" only when ignoring your answer right now risks losing data, leaking a secret, or breaking production, and say plainly what breaks. Use "warning" for a real defect or risk that can wait until the person finishes what they are doing. Use "suggestion" for everything else, including plans, explanations and ideas. Never raise the level to draw attention: a level that is always high stops meaning anything.`

// Что значит неполный индекс. Признак приходит фактом из сборки контекста, а
// правило живёт здесь: контекст — недоверенные данные, а вывод из него делает
// системное сообщение.
const companionPartialIndexInstruction = `The project index for this workspace is partial: it stopped at a size limit and does not list every file. Never conclude that a file, symbol or feature is missing because you did not find it - say that the index is incomplete and, if it matters, offer to check a specific path with a tool.`

// Предел ответа модель не видит: провайдер обрывает поток молча, посреди
// строки. Знающая о пределе модель укладывается сама, не знающая — пишет
// вступление на всю длину и обрывается перед выводом.
const companionReplyBudgetInstruction = `Your reply is capped at %d output tokens and the cap cuts mid-sentence without warning. Plan the answer to fit and finish your last thought. When the subject needs more room, answer the most important part first and end by offering to continue.`

// Границы для помощника, который читает проект своими руками. Инструментов
// Point он не получает: их исполняет не он. Зато у него есть собственные, и
// сказать ему, где проходит граница, обязан Point — CLI сам по себе умеет и
// править файлы, и запускать команды.
const companionOwnToolsInstruction = `You have two sets of tools. Your own read the project directly - open files, search the code, read git history - and they are for reading only. Point's tools arrive over MCP as mcp__point__*: they carry what your own tools cannot reach, the ranked project index, the symbol map and the skills worn for this project. Prefer mcp__point__search_code over a blind file sweep when you need implementation context.

Never modify a file, never run anything beyond read-only git commands, and never claim that work was performed - Point's companion advises, and every change goes through the person. Anything that changes or risks something belongs to Point's tools, where the permissions of this workspace are enforced, and the companion has no such permission at all. When you read something, say plainly in the reply what you looked at, so the person knows where the answer came from.`

// Точка отсчёта для всего датированного. Модель считает «сегодня» по дате
// обучения, а рядом лежат логи, коммиты и расход «за текущий месяц»: без
// внешней отметки она меряет их возраст от чужого дня.
const companionTimeInstruction = `Current moment: %s. Treat this as now: your own sense of today's date comes from training and is wrong. Timestamps in project context, logs and git history are in this timezone unless they say otherwise, so compute ages and "recent" against this moment, not against what you assume the date to be.`

// Что значит устаревший снимок индекса. Инструменты читают файлы прямо сейчас,
// индекс — то, что было на момент сборки; путать их значит уверенно пересказывать
// вчерашнее содержимое.
const companionStaleIndexInstruction = `The project index snapshot is not fresh: files may have changed since it was built. Do not state the current content of a file from the index alone - read it with a tool when the answer depends on what is there right now, and say when you are describing an older snapshot.`

// Ответ на повторный вызов. Все инструменты помощника читают и ничего не
// меняют, поэтому тот же вызов с теми же аргументами внутри одного ответа
// вернёт то же самое — исполнять его второй раз незачем. Модель об этом
// говорится прямо: молчаливый повтор прежнего результата она читает как новый
// факт и просит ещё раз.
const companionRepeatedCallNote = `{"ok":false,"repeated":true,"error":{"code":"repeated_tool_call","message":"this exact tool call was already made in this answer; its result is above","hint":"answer from what you already have, or call a different tool with different arguments"}}`

// ReadTools — читающие инструменты рабочей папки.
//
// Компаньону дают только их, и «ничего не меняет» держится не уговором в
// системном промпте, а тем, что меняющего инструмента у него просто нет:
// `run_command` и `propose_patch` сюда не попадают, и вызвать их модели неоткуда.
//
// Интерфейс объявлен здесь, а собирается снаружи: рабочая папка и реестр
// инструментов живут слоем приложения, и тянуть их в компаньон значило бы
// связать разговор с файловой системой навсегда.
type ReadTools interface {
	Definitions() []domain.ToolDefinition
	Execute(ctx context.Context, name string, arguments json.RawMessage) domain.ToolResult
}

// Результат инструмента в том виде, в каком он уходит модели: тот же JSON, что
// у агента, — одна форма на продукт.
//
// Обрезается содержимое, а не готовый JSON: разрубленный посередине объект
// модель читает как поломку инструмента, а не как «многовато данных», и идёт
// вызывать его заново.
func companionToolMessage(result domain.ToolResult) string {
	if len(result.Output) > maxCompanionToolResultChars {
		result.Truncated = true
		result.Output = json.RawMessage(strconv.Quote(string(result.Output[:maxCompanionToolResultChars]) + "… (результат обрезан)"))
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return `{"ok":false,"error":{"code":"tool_result_unreadable","message":"результат инструмента не удалось сериализовать"}}`
	}
	return string(payload)
}

// Вызов инструмента от имени компаньона.
//
// Разобранные аргументы проверяет сам инструмент; здесь ловится только то, что
// до него не дойдёт: инструментов нет вовсе или модель прислала аргументы, не
// собравшиеся в JSON. Ответ всегда возвращается моделью читаемой ошибкой, а не
// обрывом потока: оборвать разговор из-за одного неудачного вызова — потерять
// и остальные, уже полученные, результаты.
func (s Service) callReadTool(ctx context.Context, name string, arguments json.RawMessage, argumentError string) domain.ToolResult {
	if s.Tools == nil {
		return domain.ToolResult{OK: false, Error: &domain.ToolError{
			Code: "tools_unavailable", Message: "инструменты для этого проекта недоступны",
			Hint: "ответьте по тем фактам, что уже есть в контексте",
		}}
	}
	if argumentError != "" {
		return domain.ToolResult{OK: false, Error: &domain.ToolError{
			Code: "invalid_arguments", Message: argumentError,
			Hint: "повторите вызов с аргументами по схеме инструмента",
		}}
	}
	return s.Tools.Execute(ctx, name, arguments)
}

// Сколько ждём заголовков ответа. Провайдер, принявший запрос, присылает их
// сразу; молчание дольше этого срока — это молчание, а не долгий ответ, и
// платить за него всем бюджетом значит менять полторы минуты ожидания на
// местный разбор, который был готов сразу. На замере шлюз не прислал заголовков
// и за семьдесят пять секунд.
const companionProviderHeaderTimeoutSeconds = 40

// Общий срок одного обращения к провайдеру. Держится ниже бюджета модели,
// чтобы отказ успел превратиться в местный разбор.
const companionProviderTimeoutSeconds = 75

// Бюджет обращений к модели. Расширение ждёт ответа от ядра 90 секунд, и ядро
// обязано освободиться раньше: остатка должно хватить на местный разбор и на
// его сохранение. Пара чисел сверяется договорённостью в ui/contracts.mjs.
const companionModelBudget = 80 * time.Second

// Сколько даётся откату после того, как модель не ответила. Он читает уже
// собранный контекст и пишет одну запись — секунды хватает, десять взяты с
// запасом на занятую базу.
const companionFallbackBudget = 10 * time.Second
