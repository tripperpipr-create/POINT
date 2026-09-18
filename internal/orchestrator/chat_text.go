// Текстовые эвристики разговора: заголовок квеста, цели, уточняющие вопросы.
package orchestrator

import (
	"context"
	"strings"
	"unicode"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/textutil"
)

// unansweredTask — задача, к которой Мастер обещал вернуться.
//
// Пустой ростер и неработоспособный отряд — два случая, когда он отвечает
// «сделайте вот это, и я предложу квест»: «наймите его, и я сразу предложу
// квест», «поправьте их настройку, и я предложу квест». Человек уходит, делает,
// что просили, и возвращается со словом, которое само по себе не задача:
// «нанял», «поправил», «готово». Просить описать задачу заново — значит не
// сдержать обещание, данное двумя репликами выше, где эта задача и записана.
//
// Берём её оттуда же. Признак того, что обещание осталось невыполненным, —
// прошлый ответ Мастера не создал предложения, а вопрос перед ним был задачей.
// Условие срабатывает только когда препятствие уже снято: пустой ростер
// перехватывается раньше, до разбора намерения, а неработоспособный отряд
// проверяется заново на общем пути.
func (s ChatService) unansweredTask(ctx context.Context, workspaceID string) string {
	history, err := s.History(ctx, workspaceID, 12)
	if err != nil {
		return ""
	}
	// Идём назад от реплики перед текущей: она уже сохранена и рассматривать её
	// саму незачем. Ищем не строго предыдущий ход, а последнюю задачу, которая
	// так и не получила предложения: между ней и сегодняшним днём человек мог
	// спросить что-то ещё — «какой агент нужен», «спасибо», — и обещание от
	// этого не перестаёт быть невыполненным.
	for index := len(history) - 2; index >= 0; index-- {
		message := history[index]
		// Дошли до хода, который предложением закончился: всё, что было до него,
		// уже отвечено, и звать назад некуда.
		if message.Role == "assistant" && strings.TrimSpace(message.ProposalID) != "" {
			return ""
		}
		if message.Role == "user" && looksLikeWork(message.Content) {
			return strings.TrimSpace(message.Content)
		}
	}
	return ""
}

// clarifyingQuestions — то, чего Мастеру не хватает, чтобы отряд был осмысленным.
// Спрашивать всегда — навязчиво; не спрашивать никогда — значит собирать отряд
// наугад и списывать промах на пользователя.
func clarifyingQuestions(message string, party []domain.ProjectAgent) []string {
	questions := make([]string, 0, 2)
	if len([]rune(strings.TrimSpace(message))) < 24 {
		questions = append(questions, "Что считать готовым результатом?")
	}
	if len(party) == 0 {
		questions = append(questions, "Кто из ростера ближе всего к этой задаче?")
	}
	return questions
}

// looksLikeWork отличает задачу от вопроса. Мастер предлагает квест только на
// задачу: на «что сейчас происходит» отвечать созданием работы — грубость,
// которая быстро приучает не разговаривать с диспетчером вовсе.
func looksLikeWork(message string) bool {
	lower := strings.ToLower(message)
	if strings.HasSuffix(strings.TrimSpace(lower), "?") {
		return false
	}
	for _, marker := range []string{
		"сделай", "почини", "исправь", "добавь", "убери", "удали", "перепиши",
		"разбери", "собери", "напиши", "обнови", "прогони", "проверь", "запусти",
		"реализуй", "внедри", "оптимизируй", "отрефактори", "покрой",
		// «создай» отсутствовало, и «создай отряд под миграции» уходило в общий
		// ответ — при том что «собери» рядом работало. Такие пропуски человек
		// объяснить не может: одна просьба стала квестом, соседняя нет.
		"создай", "настрой", "перенеси", "замени", "вынеси", "ускорь", "почисти",
		"дополни", "доработай", "поправь", "верни", "отключи", "включи",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	// Задача, написанная по-английски, — всё ещё задача.
	//
	// «fix flaky billing test» не содержит ни одного русского глагола и уходило
	// в общий ответ «опишите задачу»: человек её описал, а Мастер просил описать.
	// Терялась она при этом необъяснимо — про язык в ответе не было ни слова,
	// ровно как когда-то про слово «статус».
	//
	// Сверяем по целым словам, а не по вхождению: «fix» внутри «prefix» — не
	// просьба чинить, и подстрочный поиск повторил бы ту же ловушку.
	for _, token := range textutil.Tokens(lower) {
		if englishWorkVerbs[token] {
			return true
		}
	}
	return false
}

// Список короткий и намеренно однозначный: только повелительные глаголы, которые
// в разговоре о коде не бывают существительным. «test» и «check» сюда не входят
// по этой самой причине.
var englishWorkVerbs = map[string]bool{
	"fix": true, "add": true, "remove": true, "delete": true, "update": true,
	"refactor": true, "implement": true, "rewrite": true, "migrate": true,
	"rename": true, "optimize": true, "upgrade": true, "bump": true, "revert": true,
}

// questTitle делает из фразы название квеста: первая строка, без хвостовой
// пунктуации, с заглавной буквы и в разумной длине.
//
// Длинное режется по слову и с многоточием. Обрубок посреди слова читается как
// законченное название — а название живёт дольше разговора: оно уходит в
// очередь решений, в сам квест и в хронику, где рядом уже нет исходной задачи,
// по которой можно было бы догадаться, что конец потерян.
func questTitle(message string) string {
	title := strings.TrimSpace(strings.SplitN(message, "\n", 2)[0])
	lower := strings.ToLower(title)
	// Быстрый старт уже подставляет разговорную команду. В названии квеста
	// остаётся сама работа, а не интерфейсное «Создай квест: …».
	for _, prefix := range []string{"создай квест", "создать квест", "подготовь квест", "поставь квест"} {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		if rest := strings.TrimSpace(strings.TrimLeft(title[len(prefix):], ":—- ")); rest != "" {
			if strings.HasPrefix(strings.ToLower(rest), "на ") {
				rest = strings.TrimSpace(rest[len("на "):])
			}
			title = rest
		}
		break
	}
	// Вторая фраза остаётся в задаче и целях, но не превращает название в
	// абзац. Это особенно важно для естественного «создай квест на X. Затем Y».
	titleRunes := []rune(title)
	for index, char := range titleRunes {
		if (char == '.' || char == '!' || char == '?') && index+1 < len(titleRunes) && unicode.IsSpace(titleRunes[index+1]) {
			title = string(titleRunes[:index])
			break
		}
	}
	title = strings.TrimRight(title, ".!,; ")
	if title == "" {
		return "Задача без названия"
	}
	runes := []rune(title)
	if len(runes) > maxQuestTitle {
		cut := runes[:maxQuestTitle]
		// Ищем последний пробел в отрезанном. Не нашли во второй половине —
		// значит слово длиннее полуназвания, и резать по нему нечего.
		for index := len(cut) - 1; index >= maxQuestTitle/2; index-- {
			if unicode.IsSpace(cut[index]) {
				cut = cut[:index]
				break
			}
		}
		title = strings.TrimRight(strings.TrimSpace(string(cut)), ".,;:!-— ") + "…"
		runes = []rune(title)
	}
	return strings.ToUpper(string(runes[0])) + string(runes[1:])
}

// NormalizeQuestTitle даёт всем путям создания квеста то же короткое имя,
// которое использует Мастер. Сырые предложения модели не должны обходить
// правила интерфейса при фактическом старте квеста.
func NormalizeQuestTitle(message string) string { return questTitle(message) }

// objectivesFor — что предстоит сделать, по словам самого человека.
//
// Раньше в цели попадал перечень отряда: строки вида «ИМЯ: роль агента». Это не
// работа, а состав, и он же показан карточкой рядом — цели дублировали соседа и
// не говорили о задаче ничего.
//
// Хуже другое: подробности задачи не доходили до квеста вовсе. Названием
// становится первая строка, описанием квеста — объяснение подбора отряда, и всё
// написанное дальше оставалось только репликой в переписке. Агент, который
// потом берётся за работу, не видел ни частоты падения, ни подозрений, ни
// требования воспроизвести локально — один заголовок. Теперь эти строки и есть
// цели: человек уже написал, что нужно сделать.
func objectivesFor(message string) []string {
	objectives := []string{"Разобраться в текущем состоянии и воспроизвести задачу"}
	// Первая строка стала названием, поэтому берём написанное после неё.
	for index, line := range strings.Split(message, "\n") {
		if index == 0 || len(objectives) >= maxObjectives-1 {
			continue
		}
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			objectives = append(objectives, trimmed)
		}
	}
	return append(objectives, "Подтвердить результат проверкой, а не словами")
}
