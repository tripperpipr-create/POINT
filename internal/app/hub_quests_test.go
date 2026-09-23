package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// Квест можно убрать из списка проекта — и нельзя убрать тот, за которым стоит
// незакрытая работа.
//
// Кнопки удаления у квеста не было вовсе: черновики и отменённые квесты копились
// в разделе навсегда. Отказ обязан называть, что именно держит квест, иначе
// человек видит только «нельзя» и идёт гадать.
func TestDeleteQuestRefusesWhileWorkIsOpen(t *testing.T) {
	application := newTestApp(t)
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	newQuest := func(title string) domain.Quest {
		saved, saveErr := application.SaveQuest(domain.Quest{Title: title, WorkspaceID: view.Workspace.ID})
		if saveErr != nil {
			t.Fatal(saveErr)
		}
		return saved
	}

	free := newQuest("Черновик")
	if err := application.DeleteQuest(free.ID); err != nil {
		t.Fatalf("квест без работы обязаны удалить: %v", err)
	}
	quests, err := application.store.ListQuests(ctx, view.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, quest := range quests {
		if quest.ID == free.ID {
			t.Fatal("квест остался в списке после удаления")
		}
	}

	// Живой запуск по квесту.
	running := newQuest("С запуском")
	if err := application.store.SaveExecution(ctx, domain.ExecutionInstance{
		ID: "exec-quest-1", WorkspaceID: view.Workspace.ID, QuestID: running.ID, Status: domain.RunRunning,
	}); err != nil {
		t.Fatal(err)
	}
	if err := application.DeleteQuest(running.ID); err == nil {
		t.Fatal("квест с живым запуском удалили — запуск остался бы без карточки")
	}

	// Завершённый запуск удалению не мешает: хроника переживает свой квест.
	finished := newQuest("С хроникой")
	if err := application.store.SaveExecution(ctx, domain.ExecutionInstance{
		ID: "exec-quest-2", WorkspaceID: view.Workspace.ID, QuestID: finished.ID, Status: domain.RunCompleted,
	}); err != nil {
		t.Fatal(err)
	}
	if err := application.DeleteQuest(finished.ID); err != nil {
		t.Fatalf("завершённая хроника не должна держать квест: %v", err)
	}

	// Подквест.
	parent := newQuest("Родитель")
	if _, err := application.SaveQuest(domain.Quest{
		Title: "Подзадача разбора", WorkspaceID: view.Workspace.ID, ParentID: parent.ID,
	}); err != nil {
		t.Fatal(err)
	}
	err = application.DeleteQuest(parent.ID)
	if err == nil {
		t.Fatal("квест с подквестом удалили молча")
	}
	if !strings.Contains(err.Error(), "Подзадача разбора") {
		t.Fatalf("отказ не назвал подквест: %v", err)
	}

	// Незакрытый набор правок: решение по нему живёт у квеста.
	pending := newQuest("С правками")
	if err := application.store.SaveChangeSet(ctx, domain.ChangeSet{
		ID: "set-1", WorkspaceID: view.Workspace.ID, ExecutionID: "exec-quest-3", QuestID: pending.ID,
		Title: "Правка обработчика", Status: domain.ChangeSetPending,
	}); err != nil {
		t.Fatal(err)
	}
	err = application.DeleteQuest(pending.ID)
	if err == nil {
		t.Fatal("квест с ждущим набором правок удалили — правки остались бы без кнопок решения")
	}
	if !strings.Contains(err.Error(), "Правка обработчика") {
		t.Fatalf("отказ не назвал набор правок: %v", err)
	}
}

// workspaceId приходит из HTTP-тела и не является полномочием писать в другой
// мир. Все остальные сущности Хаба сверяют его с открытым проектом; квест до
// этой проверки доверял любому непустому значению и исчезал из текущего UI.
func TestSaveQuestRefusesForeignWorkspace(t *testing.T) {
	application := newTestApp(t)
	firstPath := t.TempDir()
	first, err := application.OpenWorkspace(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.OpenWorkspace(firstPath); err != nil {
		t.Fatal(err)
	}

	if _, err = application.SaveQuest(domain.Quest{Title: "Чужой квест", WorkspaceID: second.Workspace.ID}); err == nil {
		t.Fatal("квест записан в мир, который сейчас не открыт")
	} else if !strings.Contains(err.Error(), "another workspace") {
		t.Fatalf("отказ не называет межпроектную границу: %v", err)
	}
	quests, err := application.store.ListQuests(context.Background(), second.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(quests) != 0 {
		t.Fatalf("отказ оставил квест в соседнем мире: %#v", quests)
	}
	quests, err = application.store.ListQuests(context.Background(), first.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(quests) != 0 {
		t.Fatalf("отказ оставил квест в открытом мире: %#v", quests)
	}
}

// Отряд, оставшийся от закрытого квеста, можно распустить — и нельзя распустить
// тот, что ведёт работу.
//
// Отряд под квест ядро собирает само и не убирает ни при завершении квеста, ни
// при его удалении. Пока маршрута роспуска не было, такой отряд навсегда держал
// своих участников: роспуск персонажа отказывал, ссылаясь на отряд, до которого
// человеку было не дотянуться.
// Схема Мастера переживает свой квест и держит исполнителя узлами. Пока её
// нельзя было удалить, персонаж не уходил из ростера никогда: отказ роспуска
// отправлял «заменить его в схеме», а редактор схем в Чертоге скрыт.
func TestDeleteFlowFreesTheAgentItHeld(t *testing.T) {
	application := newTestApp(t)
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Разработчик проекта", WorkspaceID: view.Workspace.ID, Provider: domain.ProviderOllama,
		BaseURL: "http://127.0.0.1:11434", PrimaryModel: "qwen", MaxSteps: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	flow, err := application.SaveFlow(domain.FlowGraph{
		Name: "pipeline · Symfony REST API",
		Nodes: []domain.FlowNode{
			{ID: "input", Kind: domain.FlowNodeInput},
			{ID: "implement", Kind: domain.FlowNodeAgent, Name: "Implement", AgentID: agent.ID},
			{ID: "output", Kind: domain.FlowNodeOutput},
		},
		Edges: []domain.FlowEdge{{ID: "a", From: "input", To: "implement"}, {ID: "b", From: "implement", To: "output"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Схему ведёт открытый квест — удалять нельзя, иначе он останется без плана.
	quest, err := application.SaveQuest(domain.Quest{
		Title: "Собрать REST API", WorkspaceID: view.Workspace.ID, FlowID: flow.ID, Status: domain.QuestActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = application.DeleteFlow(flow.ID); err == nil {
		t.Fatal("схему открытого квеста удалили — квест остался бы без плана")
	} else if !strings.Contains(err.Error(), "Собрать REST API") {
		t.Fatalf("отказ не назвал квест: %v", err)
	}

	// Пока схема на месте, персонаж из ростера не уходит — и отказ называет узел.
	quest.Status = domain.QuestCancelled
	if _, err = application.SaveQuest(quest); err != nil {
		t.Fatal(err)
	}
	err = application.DeleteProjectAgent(agent.ID)
	if err == nil {
		t.Fatal("персонаж ушёл, оставив узел схемы ссылаться в пустоту")
	}
	if !strings.Contains(err.Error(), "Implement") {
		t.Fatalf("отказ не назвал узел: %v", err)
	}

	if err = application.DeleteFlow(flow.ID); err != nil {
		t.Fatalf("схему закрытого квеста обязаны удалять: %v", err)
	}
	if err = application.DeleteProjectAgent(agent.ID); err != nil {
		t.Fatalf("персонажа без схем и отрядов обязаны распустить: %v", err)
	}
	flows, err := application.store.ListFlows(ctx, view.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != 0 {
		t.Fatalf("схема осталась в проекте: %#v", flows)
	}
}

func TestDeleteTeamRefusesWhileQuestIsOpen(t *testing.T) {
	application := newTestApp(t)
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Разработчик", WorkspaceID: view.Workspace.ID, Provider: domain.ProviderOllama,
		BaseURL: "http://127.0.0.1:11434", PrimaryModel: "qwen", MaxSteps: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	newTeam := func(name string) domain.Team {
		saved, saveErr := application.SaveTeam(domain.Team{Name: name, AgentIDs: []string{agent.ID}})
		if saveErr != nil {
			t.Fatal(saveErr)
		}
		return saved
	}

	// Отряд занят живым квестом.
	busy := newTeam("Отряд · Развернуть Symfony")
	if _, err := application.SaveQuest(domain.Quest{
		Title: "Развернуть Symfony", WorkspaceID: view.Workspace.ID, TeamID: busy.ID, Status: domain.QuestActive,
	}); err != nil {
		t.Fatal(err)
	}
	err = application.DeleteTeam(busy.ID)
	if err == nil {
		t.Fatal("отряд с живым квестом распустили — квест остался бы без состава")
	}
	if !strings.Contains(err.Error(), "Развернуть Symfony") {
		t.Fatalf("отказ не назвал квест: %v", err)
	}

	// Закрытый квест отряд не держит: его состав — уже история.
	closed := newTeam("Отряд · Починить импорт")
	finished := time.Now().UTC()
	if _, err := application.SaveQuest(domain.Quest{
		Title: "Починить импорт", WorkspaceID: view.Workspace.ID, TeamID: closed.ID,
		Status: domain.QuestCompleted, FinishedAt: &finished,
	}); err != nil {
		t.Fatal(err)
	}
	if err := application.DeleteTeam(closed.ID); err != nil {
		t.Fatalf("отряд закрытого квеста обязаны распустить: %v", err)
	}

	// И теперь персонаж уходит из ростера: держал его только этот отряд.
	if err := application.DeleteProjectAgent(agent.ID); err == nil {
		t.Fatal("персонаж ушёл, хотя второй отряд ещё держит его — проверка потеряла смысл")
	}
	if err := application.DeleteTeam(busy.ID); err == nil {
		t.Fatal("отряд живого квеста распустили со второй попытки")
	}
	quests, err := application.store.ListQuests(ctx, view.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, quest := range quests {
		if quest.TeamID != busy.ID {
			continue
		}
		quest.Status = domain.QuestCancelled
		if err := application.store.SaveQuest(ctx, quest); err != nil {
			t.Fatal(err)
		}
	}
	if err := application.DeleteTeam(busy.ID); err != nil {
		t.Fatalf("после отмены квеста отряд обязан распускаться: %v", err)
	}
	if err := application.DeleteProjectAgent(agent.ID); err != nil {
		t.Fatalf("персонажа без отрядов обязаны распустить: %v", err)
	}
}

// Сценарии продолжают находить своих исполнителей после снятия моста.
//
// step.ProfileID хранит идентификатор, который раньше указывал на строку в
// таблице профилей. BlueprintFromProfile сохраняет тот же id, поэтому после
// перевода на чертежи существующие сценарии резолвятся без миграции данных.
// Проверка держит именно это: id, записанный как профиль, находится как чертёж.
func TestWorkflowStepIDsStillResolveAfterBridgeRemoval(t *testing.T) {
	application := newTestApp(t)
	if _, err := application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}

	saved, err := application.SaveProfile(domain.AgentProfile{
		Name: "Старый мастеровой", RoleDescription: "правит бэкенд", SystemPrompt: "минимальные правки",
		Provider: domain.ProviderOllama, BaseURL: "http://127.0.0.1:11434", Model: "qwen",
		AllowedTools: []string{"read_file"}, MaxSteps: 25, MaxDurationSeconds: 500,
		ApprovalMode: domain.ApprovalSafe,
	})
	if err != nil {
		t.Fatal(err)
	}

	profiles, err := application.storedProfiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var found *domain.AgentProfile
	for index := range profiles {
		if profiles[index].ID == saved.ID {
			found = &profiles[index]
		}
	}
	if found == nil {
		t.Fatalf("сценарий не найдёт исполнителя: id %q пропал из выдачи профилей", saved.ID)
	}
	if found.Name != "Старый мастеровой" || found.Model != "qwen" {
		t.Fatalf("производный профиль потерял поля: %#v", *found)
	}
	// Отдаётся исходная инструкция, а не собранный из неё промпт: иначе
	// сохранение намотало бы компиляцию на компиляцию.
	if found.SystemPrompt != "минимальные правки" {
		t.Fatalf("производный профиль отдал неожиданный промпт: %q", found.SystemPrompt)
	}
}
