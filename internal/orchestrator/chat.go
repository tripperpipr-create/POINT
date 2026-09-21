package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/textutil"
)

// Chat — один ход разговора с Мастером.
func (s ChatService) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if req.WorkMode == "discuss" {
		return s.DiscussTask(ctx, req)
	}
	// Разговор Мастера целиком принадлежит разбору задания.
	//
	// Здесь стояла лазейка по ключевым словам: если intentOf считал реплику
	// просьбой про агента или отряд, ход уходил в старый маршрут ниже, и тот
	// сразу клал в очередь CompanionActionProposal{create_agent}. Разбор этот
	// грубый и намеренно грубый — isTeamCreationRequest ловит подстроку
	// «команд», — так что «добавь команды в CLI» посреди обсуждения задачи
	// становилось подбором отряда, и человек получал карточку найма вместо
	// разбора работы. Исполнителя заводят на последнем этапе квеста: когда
	// задание утверждено, нехватку считает assessAgentGap, а карточку готовит
	// proposeRoleGapHire (internal/app/agent_provisioning.go) — по нажатию
	// человека, а не по слову в реплике.
	//
	// Старый маршрут остаётся живым для компаньона: он TaskIntake не шлёт, и
	// создание сущностей Хаба у него на своём месте.
	if req.TaskIntake {
		return s.DiscussTask(ctx, req)
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		return ChatResponse{}, errors.New("сообщение мастеру не может быть пустым")
	}
	if len(req.Message) > maxChatMessage {
		return ChatResponse{}, errors.New("сообщение мастеру длиннее 32 КиБ")
	}

	agents, err := s.Store.ListProjectAgents(ctx, req.WorkspaceID)
	if err != nil {
		return ChatResponse{}, err
	}
	quests, err := s.Store.ListQuests(ctx, req.WorkspaceID)
	if err != nil {
		return ChatResponse{}, err
	}

	active := 0
	for _, quest := range quests {
		if quest.Status == "active" || quest.Status == "running" {
			active++
		}
	}
	// «Размер отряда» — это предел из политики, а не состав, который вышел.
	//
	// Числом он спорил сам с собой в одной строке: «агентов в ростере: 2 · размер
	// отряда: 3» — отряда из трёх при ростере из двух не бывает. И с экраном:
	// рядом стоял состав из двух. Подбор берёт из политики верхнюю границу и
	// упирается в то, сколько агентов есть, — так это и называется.
	facts := []string{
		fmt.Sprintf("агентов в ростере: %d", len(agents)),
		fmt.Sprintf("активных квестов: %d", active),
		fmt.Sprintf("отряд по политике: до %d", PartySize(req.Config)),
	}

	response := ChatResponse{Mode: "deterministic", Facts: facts}

	// Снимок мира берётся один раз за ход и переиспользуется. Два снимка берутся
	// в разные мгновения, и один ответ мог сказать «ждут решения: 3» и подписать
	// кнопку «разобрать очередь (2)». Модельная ветка спрашивает его для
	// обоснования, детерминированная — для сводки и кнопок; спрашивать дважды
	// незачем и вредно.
	var (
		cachedSituation Situation
		cachedKnown     bool
		situationTaken  bool
	)
	situation := func() (Situation, bool) {
		if !situationTaken {
			cachedSituation, cachedKnown = s.situationOf(ctx, req.WorkspaceID)
			situationTaken = true
		}
		return cachedSituation, cachedKnown
	}

	if err = s.persist(ctx, req.WorkspaceID, "user", req.Message, "", req.Config); err != nil {
		return ChatResponse{}, err
	}

	intent := intentOf(req.Message)
	if intent == "work" && isBareQuestCreationRequest(req.Message) {
		response.Reply = "Какую задачу превратить в квест? Опишите результат и, если важно, ограничения — я подготовлю карточку и подберу отряд."
		return response, s.persistReply(ctx, req, response, "")
	}
	// Do not spend a planner request when the hard readiness filter has already
	// proved that nobody can execute the task. Show the rejected candidates and
	// their exact blockers so the user can repair the roster.
	if intent == "work" && len(agents) > 0 && len(s.runnableAgents(agents)) == 0 {
		response.Party = s.rosterMembers(agents)
		response.Reply = "Ни один из подходящих агентов не сможет довести работу до конца — поправьте их настройку, и я предложу квест."
		response.Actions = []ChatAction{{Label: "Открыть гильдию", Tab: "agents", Hint: "поправить настройку агента — там же, где его создавали"}}
		return response, s.persistReply(ctx, req, response, "")
	}

	// Разговор ведёт модель Мастера; работа остаётся за детерминированным путём.
	//
	// Предложение квеста собирает отряд по проверке годности и кладёт его в
	// очередь решений — это права, а не беседа, и отдавать их модели незачем.
	// Всё остальное — вопросы, объяснения, уточнения — как раз то, где список
	// ключевых слов проигрывал живой речи.
	// Модель Мастера участвует в любом ходе: в разговоре она отвечает сама, в
	// работе — формулирует квест и называет, кого бы взяла. Решает при этом не
	// она: состав идёт через AssignParty, годность считает тот же код, что и
	// карточка агента, а запуск остаётся за человеком.
	var modelSaid *masterEnvelope
	if UsesModelPlanner(req.Config) && intent != "agent" && intent != "team" {
		// Fetch enough turns to summarize the early conversation while keeping a
		// small verbatim tail for pronouns and immediate follow-ups.
		history, historyErr := s.Store.ListChatMessages(ctx, req.WorkspaceID, "master", 48)
		if historyErr != nil {
			return ChatResponse{}, historyErr
		}
		blockers := make(map[string][]string, len(agents))
		for _, agent := range agents {
			blockers[agent.ID] = s.blockersFor(agent)
		}
		snapshot, _ := situation()
		// Предложения нужны модели, чтобы помнить своё же предложение: «а можно
		// без него?» задают сразу после него, и отвечать на это счётчиком
		// «ждут решения: 1» — не разговор.
		proposals, proposalsErr := s.Store.ListQuestProposals(ctx, req.WorkspaceID)
		if proposalsErr != nil {
			return ChatResponse{}, proposalsErr
		}
		world := masterWorldPrompt(agents, blockers, quests, proposals, active, snapshot)
		envelope, modelErr := s.chatWithModel(ctx, req, world, history)
		if modelErr == nil {
			modelSaid = &envelope
			response.Mode = "model"
			response.Model = req.Config.Model
			// Разговор модель заканчивает сама. Работу заканчивает движок:
			// предложение квеста — это запись в очереди решений, и собирать её
			// должен проверенный путь.
			if intent != "work" {
				response.Reply = envelope.Reply
				response.Questions = envelope.Questions
				response.Actions = s.actionsFor(snapshot)
				return response, s.persistReply(ctx, req, response, "")
			}
		}
		// Молчаливый откат неотличим от исправной работы: человек настроил
		// модель и вправе знать, что отвечала не она. Успех сюда доходит только
		// на задаче — её заканчивает движок, и откатываться не с чего.
		if modelErr != nil {
			response.FallbackReason = modelErr.Error()
		}
	}

	// Создание агента и отряда — отдельные проверяемые действия Hub. Мастер
	// готовит карточку, а человек применяет, правит или отклоняет её прямо в
	// разговоре. Coding-квест из фразы «создай агента» здесь не появляется.
	if intent == "agent" {
		action, hire, actionErr := s.prepareAgentAction(ctx, req, agents, req.Message, "", "")
		if actionErr != nil {
			return ChatResponse{}, actionErr
		}
		response.ActionProposal = action
		response.Hire = hire
		if action == nil {
			response.Reply = "Готовых чертежей агента пока нет. Откройте Гильдию и создайте первый чертёж — после этого я подготовлю карточку по одной просьбе."
			response.Actions = []ChatAction{{Label: "Открыть гильдию", Tab: "agents"}}
		} else if action.Status == "modified" || hire == nil {
			response.Reply = fmt.Sprintf("Черновик «%s» уже ждёт вашего решения. Его можно создать, изменить или отклонить прямо здесь.", action.Agent.Name)
		} else {
			response.Reply = fmt.Sprintf("Подготовил агента «%s» на основе подходящего чертежа. Проверьте роль и модель, затем создайте или измените карточку.", action.Agent.Name)
		}
		return response, s.persistReply(ctx, req, response, "")
	}

	if intent == "team" {
		if len(agents) == 0 {
			action, hire, actionErr := s.prepareAgentAction(ctx, req, agents, req.Message, req.Message, "ПРОДОЛЖИТЬ ПОДБОР ОТРЯДА")
			if actionErr != nil {
				return ChatResponse{}, actionErr
			}
			response.ActionProposal, response.Hire = action, hire
			if action == nil {
				response.Reply = "Собирать отряд пока не из кого, и готовых чертежей нет. Сначала создайте агента в Гильдии."
				response.Actions = []ChatAction{{Label: "Открыть гильдию", Tab: "agents"}}
			} else {
				response.Reply = fmt.Sprintf("Собирать отряд пока не из кого. Подготовил первого агента «%s» — создайте его, затем повторите подбор.", action.Agent.Name)
			}
			return response, s.persistReply(ctx, req, response, "")
		}
		action, members, why, actionErr := s.prepareTeamAction(ctx, req, agents)
		if actionErr != nil {
			return ChatResponse{}, actionErr
		}
		response.ActionProposal, response.Party, response.PartyWhy = action, members, why
		if action == nil {
			response.Reply = "Не смог подобрать ни одного исполнителя. Уточните задачу или проверьте готовность агентов в Гильдии."
			response.Actions = []ChatAction{{Label: "Открыть гильдию", Tab: "agents"}}
		} else {
			response.Reply = fmt.Sprintf("Подобрал %s. Состав можно изменить перед созданием отряда — решение остаётся за вами.", textutil.Count(len(members), "исполнитель", "исполнителя", "исполнителей"))
		}
		return response, s.persistReply(ctx, req, response, "")
	}

	// Пустой ростер — не тупик, но и не единственная тема разговора.
	//
	// Нанимать некого — значит первый шаг работы это найм, и Мастер обязан его
	// предложить, а не отправить человека разбираться самому. Но предлагал он это
	// на всё подряд: ветка стояла до разбора намерения, и «что ты умеешь», «что
	// сейчас», «кто есть» и даже «привет» получали одно и то же предложение
	// нанять. Пока ростер пуст, Мастер переставал уметь что-либо ещё.
	//
	// Хуже того, чертёж подбирался по тексту самого сообщения: на «привет» он
	// выбирался по слову «привет», а ответ уверял, что подобран «под то, что вы
	// описали», — хотя не описали ничего.
	//
	// Найм предлагается там, где он и есть ответ: на задачу. Про справку, сводку
	// и состав Мастер рассказывает по-прежнему — ростер для этого не нужен.
	if len(agents) == 0 && intent == "work" {
		action, hire, actionErr := s.prepareAgentAction(ctx, req, agents, req.Message, req.Message, "ПРОДОЛЖИТЬ ЗАДАЧУ")
		if actionErr != nil {
			return ChatResponse{}, actionErr
		}
		response.ActionProposal, response.Hire = action, hire
		if action == nil {
			response.Reply = "В ростере пусто, и готовых чертежей тоже нет — соберите первого агента вручную в Гильдии, и я подключусь."
			response.Actions = []ChatAction{{Label: "Открыть гильдию", Tab: "agents"}}
		} else {
			// Подсказок здесь нет: нанимают кнопкой на карточке, а отправленная
			// Мастеру строка «Нанять „X"?» ничего не нанимает.
			response.Reply = fmt.Sprintf(
				"Задачу понял, но выполнять её пока некому. Под неё подходит «%s» — %s. Наймите его, и я сразу предложу квест.",
				action.Agent.Name, action.Rationale)
		}
		return response, s.persistReply(ctx, req, response, "")
	}

	// Задача, к которой вернулись по обещанию Мастера. Пустая строка означает,
	// что человек описал работу сам, здесь и сейчас.
	resumed := ""

	// Разговор — не только «задача или не задача».
	//
	// Раньше всё, что не похоже на задачу, получало одну и ту же строку про
	// размер ростера. Диспетчер, который умеет ровно одно, и выглядит как
	// умеющий ровно одно: спросить у него о состоянии дел было нельзя.
	switch intent {
	// Снимок мира берётся один раз на ход.
	//
	// Сводка и кнопки перехода спрашивали его каждая для себя, а снимок — это
	// вся очередь решений целиком: наборы, прогоны, узлы Flow, предложения.
	// Двойная работа здесь не главное: два снимка берутся в разные мгновения, и
	// один ответ мог сказать «ждут решения: 3» и подписать кнопку «разобрать
	// очередь (2)». Числа в одном ответе обязаны сходиться.
	case "help":
		snapshot, _ := situation()
		response.Reply = strings.Join([]string{
			"Я диспетчер: со мной можно говорить о проекте обычными словами.",
			"Спросите «что сейчас» — расскажу, что ждёт решения и что выполняется.",
			"Спросите «кто есть» — перечислю отряд и кто из них готов к работе.",
			"Скажите «создай агента» или «подбери отряд» — подготовлю редактируемую карточку и подожду подтверждения.",
			"Скажите «создай квест» или просто опишите работу — подберу отряд и предложу квест.",
			"Квест не стартует сам: запуск всегда за вами.",
		}, "\n")
		response.Actions = s.actionsFor(snapshot)
		return response, s.persistReply(ctx, req, response, "")

	case "status":
		snapshot, known := situation()
		response.Reply = s.statusReply(snapshot, known, len(agents), active)
		response.Actions = s.actionsFor(snapshot)
		return response, s.persistReply(ctx, req, response, "")

	case "roster":
		response.Reply = s.rosterReply(agents)
		response.Party = s.rosterMembers(agents)
		response.Actions = []ChatAction{{Label: "Открыть гильдию", Tab: "agents"}}
		return response, s.persistReply(ctx, req, response, "")

	case "greeting", "talk":
		snapshot, _ := situation()
		// Реплика, которая не команда и не вопрос, чаще всего продолжает
		// предыдущий ход. Пока ответ на неё был один на все случаи, «давай»,
		// «ок» и «а можно без него?» сразу после предложенного квеста получали
		// приглашение описать задачу — разговор обрывался ровно там, где человек
		// отвечал Мастеру. Предложение при этом никуда не девалось: оно висело
		// в очереди решений и в переписке выше, но в ответе о нём не было ни слова.
		if waiting := s.pendingOwnProposal(ctx, req.WorkspaceID); waiting != nil {
			response.Reply = fmt.Sprintf(
				"Предложение «%s» ещё ждёт вашего решения — запустить или отклонить его можно прямо в переписке выше. Если задача другая, опишите её, и я соберу отряд под неё.",
				waiting.Title)
			// Подсказки здесь нет намеренно.
			//
			// Чип с вопросом подставляется в поле ввода человека и уходит в
			// следующей реплике — то есть Мастер получает собственный вопрос и
			// отвечает на него тем же текстом. «Что поменять в предложении?»
			// вдобавок обещало то, чего Мастер не умеет: править предложение из
			// разговора нечем, его правят карточкой. Вопрос, на который нельзя
			// ответить, хуже отсутствия вопроса.
			response.Actions = s.actionsFor(snapshot)
			return response, s.persistReply(ctx, req, response, "")
		}
		// Обещание, которое надо сдержать: препятствие снято, задача известна —
		// возвращаемся к ней, а не просим описать её заново.
		if task := s.unansweredTask(ctx, req.WorkspaceID); task != "" && len(agents) > 0 {
			resumed, req.Message = task, task
			break
		}
		if len(agents) == 0 {
			// Ростер пуст, а задача уже названа: разговор идёт о ней, и на
			// «какой агент нужен» отвечать надо ролью под ту самую задачу.
			// Подбирать под текст вопроса — как раз то, из-за чего на «привет»
			// приходил чертёж, выбранный по слову «привет».
			if task := s.unansweredTask(ctx, req.WorkspaceID); task != "" {
				blueprints, bpErr := s.Store.ListBlueprints(ctx)
				if bpErr != nil {
					return ChatResponse{}, bpErr
				}
				if hire := suggestHire(task, blueprints); hire != nil {
					response.Hire = hire
					response.Reply = fmt.Sprintf(
						"Под задачу «%s» я бы взял «%s» — %s. Наймите его, и я предложу квест.",
						questTitle(task), hire.Name, hire.Why)
					return response, s.persistReply(ctx, req, response, "")
				}
			}
			// «В ростере 0 агентов» — счётная строка, а не разговор.
			response.Reply = "В ростере пока никого. Опишите задачу — подберу, кого под неё нанять."
		} else if intent == "greeting" {
			response.Reply = fmt.Sprintf(
				"Сейчас в ростере %s, активных квестов %d. Опишите задачу — подберу отряд и предложу квест, или спросите «что сейчас».",
				textutil.Count(len(agents), "агент", "агента", "агентов"), active)
		} else if asksWhichAgent(req.Message) {
			// Вопрос понят, не хватает не понимания, а задачи.
			response.Reply = "Под какую задачу? Опишите её — подберу исполнителя из ростера и предложу квест."
		} else {
			// Непонятое и приветствие звучали одинаково, и по ответу нельзя было
			// понять, разобрал ли Мастер вопрос: «привет» и «сколько есть
			// агентов» получали слово в слово одну строку. Признать непонимание
			// честнее и полезнее, чем выдать его за приглашение.
			response.Reply = fmt.Sprintf(
				"Не понял вопроса. Скажу, что сейчас в работе, или перечислю ростер (сейчас в нём %s) — или опишите задачу, и я предложу квест.",
				textutil.Count(len(agents), "агент", "агента", "агентов"))
		}
		// Подсказки — то, что можно сказать Мастеру, а не то, о чём он спрашивает.
		//
		// Чип уходит в его же адрес следующей репликой. «Что нужно сделать в
		// проекте?» возвращалось ему вопросом, на который он отвечал этой же
		// строкой, — разговор ходил по кругу. Эти две реплики работают: на них
		// он отвечает сводкой и составом.
		response.Questions = []string{"что сейчас?", "кто есть в ростере?"}
		response.Actions = s.actionsFor(snapshot)
		return response, s.persistReply(ctx, req, response, "")
	}

	// Состав, названный моделью, — предложение, а не назначение: AssignParty
	// отбросит несуществующие идентификаторы и обрежет по политике отряда.
	var proposedIDs []string
	if modelSaid != nil && modelSaid.Proposal != nil {
		proposedIDs = modelSaid.Proposal.AgentIDs
	}
	assignment := s.assignParty(ctx, req, agents, proposedIDs)
	byID := make(map[string]domain.ProjectAgent, len(agents))
	for _, agent := range agents {
		byID[agent.ID] = agent
	}
	party := make([]domain.ProjectAgent, 0, len(assignment.AgentIDs))
	names := make([]string, 0, len(assignment.AgentIDs))
	for _, id := range assignment.AgentIDs {
		if agent, ok := byID[id]; ok {
			party = append(party, agent)
			names = append(names, agent.Name)
		}
	}

	members := make([]PartyMember, 0, len(party))
	blocked := 0
	for _, agent := range party {
		blockers := s.blockersFor(agent)
		if len(blockers) > 0 {
			blocked++
		}
		score := scoreAgentForGoal(agent, req.Message)
		members = append(members, PartyMember{
			AgentID:  agent.ID,
			Name:     agent.Name,
			Role:     strings.TrimSpace(agent.RoleDescription),
			Score:    &score,
			Matched:  matchedTerms(agent, req.Message),
			Blocking: blockers,
		})
	}

	// Весь отряд неработоспособен — предлагать квест бессмысленно: он упрётся
	// в первую же правку. Честнее назвать причину, чем создать заведомо
	// провальное предложение и ждать, пока человек сам догадается.
	if len(members) > 0 && blocked == len(members) {
		response.Party = members
		response.Reply = "Ни один из подходящих агентов не сможет довести работу до конца — поправьте их настройку, и я предложу квест."
		// Кнопкой, а не подсказкой: подсказка уходит Мастеру следующей репликой,
		// а «открыть карточку агента» ему не адресуется — это переход по разделам.
		// Отправленная ему, она возвращала тот же отказ, потому что в мире ничего
		// не поменялось. Чинят агента в гильдии, туда и ведём.
		response.Actions = []ChatAction{{
			Label: "Открыть гильдию", Tab: "agents",
			Hint: "поправить настройку агента — там же, где его создавали",
		}}
		return response, s.persistReply(ctx, req, response, "")
	}

	// Прежнее предложение ищется до того, как появится новое, иначе поиск нашёл
	// бы его же. Молчать о нём нельзя: очередь решений растёт от каждой задачи,
	// и человек, описывающий вторую, не видит, что первая всё ещё ждёт. Ветки,
	// которые до предложения не доходят, за этот поиск не платят.
	previous := s.pendingOwnProposal(ctx, req.WorkspaceID)

	// Та же задача второй раз — то же предложение, а не второе такое же.
	//
	// Повторяют задачу по-разному: не заметили ответа, вернулись к разговору,
	// нажали не туда. Раньше каждый повтор клал в очередь ещё одно решение с тем
	// же названием, и Мастер сообщал о «предыдущем предложении» с заголовком,
	// который только что назвал сам, — читалось как сбой. Отличить уточнение
	// задачи от новой он не умеет, но дословный повтор — случай без сомнений.
	if previous != nil && previous.Title == questTitle(req.Message) {
		response.Proposal = previous
		response.Reply = fmt.Sprintf(
			"Это та же задача: предложение «%s» уже ждёт вашего решения — запустить или отклонить его можно прямо здесь.",
			previous.Title)
		return response, s.persistReply(ctx, req, response, previous.ID)
	}

	// Просьба поправить предложенный квест — правка, а не второй квест.
	//
	// «Убери Разведчика из отряда» начинается рабочим глаголом, и разбор считал
	// это новой работой: в очередь ложилось второе предложение с названием
	// «Убери Разведчика из отряда», а первое продолжало ждать. Человек получал
	// две записи вместо одной поправленной и разбирался руками.
	//
	// Правит модель — она читала и просьбу, и снимок мира с идентификаторами
	// ждущих предложений. Права при этом те же: состав проходит через
	// AssignParty, годность считает тот же код, запуск остаётся за человеком.
	if amended, handled, amendErr := s.amendPendingProposal(ctx, req, modelSaid, members, names, blocked, assignment, response); handled {
		return amended, amendErr
	}

	partyWhy := chatPartyWhy(req.Config, assignment)
	// Задача — то, что человек написал; при возврате по обещанию — та задача, к
	// которой вернулись, а не слово «нанял», которым он об этом сообщил.
	taskText := req.Message
	if resumed != "" {
		taskText = resumed
	}
	// Формулировку берём у модели, когда она её дала: она читала и задачу, и
	// снимок мира. Пустые поля добирает движок — предложение не должно зависеть
	// от того, насколько словоохотлива модель.
	title := questTitle(req.Message)
	objectives := objectivesFor(req.Message)
	definitionOfDone := []string{"Изменения приняты", "Проверка завершилась успешно после последней правки"}
	var constraints []string
	if modelSaid != nil && modelSaid.Proposal != nil {
		if modelSaid.Proposal.Title != "" {
			title = questTitle(modelSaid.Proposal.Title)
		}
		if len(modelSaid.Proposal.Objectives) > 0 {
			objectives = modelSaid.Proposal.Objectives
		}
		if len(modelSaid.Proposal.DefinitionOfDone) > 0 {
			definitionOfDone = modelSaid.Proposal.DefinitionOfDone
		}
		constraints = modelSaid.Proposal.Constraints
	}
	proposal := domain.QuestProposal{
		ID:          s.newID("qp"),
		WorkspaceID: req.WorkspaceID,
		Title:       title,
		// Задача словами человека — она и есть содержание квеста. Без неё в
		// описание квеста уходило объяснение выбора отряда, а оттуда в контекст
		// исполняющего агента: он читал, как его выбирали, вместо задачи.
		Task:               taskText,
		Rationale:          partyWhy,
		Objectives:         objectives,
		Constraints:        constraints,
		DefinitionOfDone:   definitionOfDone,
		TeamAgentIDs:       assignment.AgentIDs,
		SelectionBreakdown: assignment.Breakdown,
		Importance:         domain.QuestNormal,
		Status:             "pending",
		CreatedAt:          s.now(),
	}
	// Потолок расхода. Без него квест уходит в работу без всякого предела:
	// проверка бюджета пропускает квест без бюджета целиком. Оценку ставил
	// только компаньон, а Мастер — основной путь создания квестов — не ставил.
	proposal.EstimateTokens = domain.EstimateQuestTokens(proposal)
	if err = s.Store.SaveQuestProposal(ctx, proposal); err != nil {
		return ChatResponse{}, err
	}

	response.Proposal = &proposal
	response.Party = members
	response.PartyWhy = partyWhy
	response.Questions = clarifyingQuestions(req.Message, party)
	switch {
	case len(names) == 0:
		response.Reply = "Подобрать отряд не удалось — уточните задачу или проверьте ростер."
	case blocked > 0:
		response.Reply = fmt.Sprintf("Собрал отряд: %s. Из них %d не сможет завершить работу — причина указана в составе. Квест предложен, но лучше сначала поправить настройку.",
			strings.Join(names, ", "), blocked)
	default:
		response.Reply = fmt.Sprintf("Собрал отряд: %s. Квест предложен и ждёт вашего решения — сам он не стартует.",
			strings.Join(names, ", "))
		// Модель объясняет выбор своими словами — это и есть разница между
		// диспетчером и справочником. Но только здесь: в ветках отказа и
		// оговорок говорит движок, потому что там речь о том, чего модель не
		// знает — о годности агентов и о прежнем предложении в очереди.
		if modelSaid != nil && strings.TrimSpace(modelSaid.Reply) != "" {
			response.Reply = strings.TrimSpace(modelSaid.Reply) + "\n\n" + response.Reply
			if len(modelSaid.Questions) > 0 {
				response.Questions = modelSaid.Questions
			}
		}
	}
	// Вернулись к задаче по обещанию — говорим об этом прямо: реплика человека
	// была «нанял», и без оговорки ответ выглядел бы так, будто Мастер собрал
	// отряд под слово «нанял».
	if resumed != "" {
		response.Reply = fmt.Sprintf("Возвращаюсь к задаче «%s». ", questTitle(resumed)) + response.Reply
	}
	// Второе предложение не отменяет первое: отличить уточнение задачи от новой
	// задачи Мастер не умеет, а угадывать и молча снимать чужое решение — хуже,
	// чем назвать его. Иначе очередь растёт от каждой реплики, а замечает это
	// человек уже по счётчику в шапке, не понимая, откуда там столько.
	if previous != nil {
		response.Reply += fmt.Sprintf(" Предыдущее предложение «%s» всё ещё ждёт решения — если оно больше не нужно, отклоните его.", previous.Title)
	}
	return response, s.persistReply(ctx, req, response, proposal.ID)
}
