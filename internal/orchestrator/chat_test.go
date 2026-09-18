package orchestrator

import (
	"fmt"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/textutil"
)

// Причина выбора должна быть причиной.
//
// Состав отряда показывается человеку вместе с обоснованием — «совпало: тесты,
// биллинг», — и весь смысл этой строки в том, что с ней можно спорить. Пока в
// неё попадали предлоги, спорить было не с чем: «совпало: на» одинаково верно
// для любого агента, потому что «на» есть в любом описании.
func TestMatchedTermsDoNotExplainWithFunctionWords(t *testing.T) {
	agent := domain.ProjectAgent{
		Name:            "Reviewer",
		RoleDescription: "Старший ревьюер кода, ориентированный на минимальные изменения",
	}
	for _, term := range matchedTerms(agent, "Добавь тесты на биллинг") {
		if textutil.IsStopWord(term) {
			t.Fatalf("выбор объясняется служебным словом %q", term)
		}
	}
}

// Совпадение по служебным словам не должно поднимать агента в отряде.
func TestGoalScoreIgnoresFunctionWords(t *testing.T) {
	agent := domain.ProjectAgent{
		Name:            "Designer",
		RoleDescription: "Оформляет интерфейс и следит за тем, как он выглядит",
	}
	// Ни одного общего значимого слова — только предлоги и союзы.
	if score := scoreAgentForGoal(agent, "Почини и проверь это за тем как"); score != 0 {
		t.Fatalf("служебные слова дали оценку %d, а совпадений по существу нет", score)
	}
	// Настоящее совпадение по-прежнему считается.
	if score := scoreAgentForGoal(agent, "Почини интерфейс"); score == 0 {
		t.Fatal("совпадение по существу перестало считаться")
	}
}

// Обрезанное название обязано выглядеть обрезанным.
//
// Название переживает разговор: оно уходит в очередь решений, в квест и в
// хронику, где исходной задачи рядом уже нет. Обрубок посреди слова читается там
// как законченное имя, и понять, что конец потерян, неоткуда.
func TestQuestTitleCutsByWordAndSaysSo(t *testing.T) {
	long := "Почини флаки-тест оплаты подписки в биллинге, разобрав повторные вебхуки провайдера " +
		"и добавив идемпотентность обработчику, чтобы дубли не создавали лишних списаний"
	title := questTitle(long)

	if runes := []rune(title); len(runes) > maxQuestTitle+1 {
		t.Fatalf("название длиннее предела: %d знаков", len(runes))
	}
	if !strings.HasSuffix(title, "…") {
		t.Fatalf("обрезка не помечена — название читается как законченное: %q", title)
	}
	// Резать по слову: последнее слово либо целое, либо его нет.
	body := strings.TrimSuffix(title, "…")
	if strings.HasSuffix(body, " ") {
		t.Fatalf("перед многоточием остался пробел: %q", title)
	}
	if !strings.Contains(long, body) {
		t.Fatalf("название не является началом задачи: %q", title)
	}
	tail := body[strings.LastIndex(body, " ")+1:]
	if !strings.Contains(long, tail+" ") && !strings.HasSuffix(long, tail) {
		t.Fatalf("последнее слово обрублено: %q", tail)
	}

	// Короткое остаётся нетронутым и с заглавной буквы.
	if got := questTitle("почини тесты."); got != "Почини тесты" {
		t.Fatalf("короткое название испорчено: %q", got)
	}
	for input, want := range map[string]string{
		"Создай квест: почини тесты оплаты":                                           "Почини тесты оплаты",
		"Создай квест на анализ проекта. Посмотри на агентов и скажи, нужны ли новые": "Анализ проекта",
		"подготовь квест — обновить API":                                              "Обновить API",
		"поставь квест для ревью миграции":                                            "Для ревью миграции",
	} {
		if got := questTitle(input); got != want {
			t.Fatalf("команда быстрого старта просочилась в название: %q вместо %q", got, want)
		}
	}
	// Одно слово длиннее предела резать по пробелу нечем — но пометить нужно.
	solid := strings.Repeat("щ", maxQuestTitle+40)
	if got := questTitle(solid); !strings.HasSuffix(got, "…") || len([]rune(got)) > maxQuestTitle+1 {
		t.Fatalf("сплошное слово обработано неверно: %d знаков, %q", len([]rune(got)), got[:20])
	}
}

// Подробности задачи доходят до квеста.
//
// Названием квеста становится первая строка, описанием — объяснение подбора
// отряда. Всё, что человек написал дальше, не попадало никуда: агент, который
// потом берётся за работу, видел один заголовок. А в целях стоял перечень
// отряда — не работа, а состав, показанный карточкой рядом.
func TestObjectivesCarryTheTaskNotTheParty(t *testing.T) {
	task := strings.Join([]string{
		"Почини флаки-тест оплаты подписки",
		"",
		"Падает раз в двадцать прогонов, подозреваю гонку в вебхуке провайдера.",
		"Нужно воспроизвести локально и закрыть.",
	}, "\n")

	objectives := objectivesFor(task)

	for _, detail := range []string{"гонку в вебхуке", "воспроизвести локально"} {
		found := false
		for _, objective := range objectives {
			if strings.Contains(objective, detail) {
				found = true
			}
		}
		if !found {
			t.Fatalf("подробность «%s» не дошла до квеста: %v", detail, objectives)
		}
	}
	// Первая строка уже стала названием — второй раз ей в целях делать нечего.
	for _, objective := range objectives[1 : len(objectives)-1] {
		if strings.Contains(objective, "Почини флаки-тест оплаты подписки") {
			t.Fatalf("название повторено в целях: %v", objectives)
		}
	}
	if len(objectives) > maxObjectives {
		t.Fatalf("целей больше предела: %d", len(objectives))
	}

	// Однострочная задача оставляет рамку, в которую агенту и работать.
	short := objectivesFor("Почини тесты")
	if len(short) != 2 {
		t.Fatalf("у короткой задачи ожидались две рамочные цели, получено %v", short)
	}

	// Длинный список подробностей обрезается по пределу, но рамка сохраняется.
	long := make([]string, 0, 12)
	long = append(long, "Почини всё")
	for index := 0; index < 10; index++ {
		long = append(long, fmt.Sprintf("Пункт %d", index))
	}
	capped := objectivesFor(strings.Join(long, "\n"))
	if len(capped) > maxObjectives {
		t.Fatalf("предел целей не соблюдён: %d", len(capped))
	}
	if capped[len(capped)-1] != "Подтвердить результат проверкой, а не словами" {
		t.Fatalf("рамка потерялась при обрезке: %v", capped)
	}
}

// Задача по-английски — тоже задача.
//
// В разговоре о коде английская просьба обычна, а глаголы Мастер знал только
// русские: «fix flaky billing test» уходило в общий ответ «опишите задачу».
// Человек её описал — и получил просьбу описать, без единого намёка, что дело в
// языке. Та же потеря вслепую, что когда-то устраивало слово «статус».
//
// И та же ловушка рядом: искать по вхождению нельзя. «fix» живёт внутри
// «prefix», и подстрочный поиск превратил бы разговор о ветках в задачу.
func TestLooksLikeWorkHearsEnglishButNotSubstrings(t *testing.T) {
	for _, message := range []string{
		"fix flaky billing test",
		"add metrics to the billing webhook",
		"refactor the payment handler",
		"Update the migration script",
		"revert the last change",
	} {
		if !looksLikeWork(message) {
			t.Fatalf("задача не распознана: %q", message)
		}
	}

	for _, message := range []string{
		"our branch prefix is release",
		"the suffix here is odd",
		"address of the service",
		"как дела",
		"fix?",
	} {
		if looksLikeWork(message) {
			t.Fatalf("не задача принята за задачу: %q", message)
		}
	}

	// Русские глаголы по-прежнему ловятся вхождением: они склоняются.
	for _, message := range []string{"Почини тесты", "почините сборку", "Добавь метрику"} {
		if !looksLikeWork(message) {
			t.Fatalf("русская задача перестала распознаваться: %q", message)
		}
	}
}

// Разговор не жалуется на исправную модель.
//
// AssignParty — запасной путь, и при настроенной модели она подписывается
// «модель недоступна». При старте квеста это правда: планировщик пробовал.
// В разговоре не пробовал никто, и та же подпись сообщала бы о поломке там, где
// всё исправно, — а заодно молчала бы о том, что при запуске состав пересоберут.
func TestChatPartyWhyDoesNotBlameTheModelItNeverCalled(t *testing.T) {
	cfg := domain.OrchestratorConfig{Preset: "balanced", Provider: domain.ProviderOllama, Model: "qwen"}
	assignment := Assignment{AgentIDs: []string{"a", "b"}, Reason: "пресет balanced · отряд 2 · модель недоступна, детерминированный выбор"}

	why := chatPartyWhy(cfg, assignment)
	if strings.Contains(why, "недоступна") {
		t.Fatalf("разговор жалуется на модель, которую не звал: %q", why)
	}
	if !strings.Contains(why, "qwen") {
		t.Fatalf("о пересборке отряда моделью при запуске не сказано: %q", why)
	}

	// Без модели пересобирать нечем: показанный отряд и есть окончательный,
	// и объяснение остаётся тем же, что даёт сам подбор.
	plain := domain.OrchestratorConfig{Preset: "balanced"}
	if why := chatPartyWhy(plain, assignment); why != assignment.Reason {
		t.Fatalf("без модели объяснение подменено: %q", why)
	}

	// Запасной путь при запуске своей подписи не теряет — там она заслужена.
	started := AssignParty(cfg, []domain.ProjectAgent{{ID: "a", Name: "A"}}, nil, "почини тесты")
	if !strings.Contains(started.Reason, "недоступна") {
		t.Fatalf("подбор при запуске перестал признаваться запасным: %q", started.Reason)
	}
}

// Повторённое слово — одно совпадение.
func TestGoalScoreDoesNotRewardRepetition(t *testing.T) {
	agent := domain.ProjectAgent{Name: "QA", RoleDescription: "Пишет тесты"}
	once := scoreAgentForGoal(agent, "тесты")
	thrice := scoreAgentForGoal(agent, "тесты тесты тесты")
	if once == 0 {
		t.Fatal("совпадение по существу не засчитано — проверять нечего")
	}
	if thrice != once {
		t.Fatalf("повтор слова поднял оценку: %d против %d", thrice, once)
	}
}

// Вопрос про ростер узнаётся не только в одной формулировке.
//
// Найдено на живом экране: «Привет» и «Сколько есть агентов?» получали ответ,
// совпадающий слово в слово, — при том что «кто есть в ростере?» отвечало
// составом. Список точных фраз узнавал одну формулировку из пяти, остальные
// падали в общую строку, неотличимую от приветствия: понял Мастер вопрос или
// нет, по ответу узнать было нельзя.
func TestRosterQuestionsAreRecognisedInPlainWording(t *testing.T) {
	for _, message := range []string{
		"кто есть в ростере?",
		"Сколько есть агентов?",
		"сколько у меня агентов",
		"покажи агентов",
		"агенты?",
		"какие персонажи есть",
		"кто в отряде",
	} {
		if got := intentOf(message); got != "roster" {
			t.Fatalf("«%s» разобрано как %q, а это вопрос о составе", message, got)
		}
	}

	// Приветствие — не непонимание: на него уместен приглашающий ответ.
	for _, message := range []string{"Привет", "здравствуйте", "добрый день", "спасибо"} {
		if got := intentOf(message); got != "greeting" {
			t.Fatalf("«%s» разобрано как %q, а это приветствие", message, got)
		}
	}

	// Непонятое обязано остаться непонятым, а не выдаваться за приветствие.
	for _, message := range []string{"а погода какая", "ну ладно"} {
		if got := intentOf(message); got != "talk" {
			t.Fatalf("«%s» разобрано как %q, ожидалось непонятое", message, got)
		}
	}

	// Сущности Hub создаются как проверяемые карточки, а не маскируются под
	// coding-квест. Обычная работа при этом остаётся работой.
	for message, want := range map[string]string{
		"добавь агента для тестов":            "agent",
		"создай отряд под миграции":           "team",
		"сформируй команду для релиза":        "team",
		"мне нужен отряд для backend API":     "team",
		"создай квест: обновить документацию": "work",
		"обнови статус заказа в API":          "work",
		"почини вебхук биллинга":              "work",
	} {
		if got := intentOf(message); got != want {
			t.Fatalf("«%s» разобрано как %q, ожидалось %q", message, got, want)
		}
	}

	// Справка по-прежнему своя.
	if got := intentOf("что ты умеешь"); got != "help" {
		t.Fatalf("справка разобрана как %q", got)
	}
}
