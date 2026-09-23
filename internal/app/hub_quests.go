// Отряды и квесты: сохранение и удаление с проверкой зависимостей.
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

func (a *App) SaveTeam(team domain.Team) (domain.Team, error) {
	now := time.Now().UTC()
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.Team{}, err
	}
	if team.WorkspaceID != "" && team.WorkspaceID != ws.ID {
		return domain.Team{}, errors.New("team belongs to another workspace")
	}
	team.WorkspaceID = ws.ID
	if strings.TrimSpace(team.Name) == "" || len([]rune(team.Name)) > 120 {
		return domain.Team{}, errors.New("team name is required and must not exceed 120 characters")
	}
	if len([]rune(team.Description)) > 4096 {
		return domain.Team{}, errors.New("team description exceeds 4096 characters")
	}
	if err = a.validateCompanionTeamDraft(team); err != nil {
		return domain.Team{}, err
	}
	if team.ID == "" {
		team.ID = domain.NewID("team")
		team.CreatedAt = now
	}
	if team.CreatedAt.IsZero() {
		team.CreatedAt = now
	}
	team.UpdatedAt = now
	if err := a.store.SaveTeam(context.Background(), team); err != nil {
		return domain.Team{}, err
	}
	return team, nil
}

// DeleteTeam распускает отряд проекта.
//
// Отряд под квест собирает сам оркестратор, и переживает он и завершение квеста,
// и его удаление. Убрать отряд было негде: роспуск персонажа отказывал словами
// «сначала уберите его оттуда», а «оттуда» не открывалось ни одной кнопкой —
// отказ звал сделать невозможное.
//
// Держит отряд только незакрытый квест: у завершённого, проваленного и
// отменённого отряд — уже история, и она остаётся в статистике по идентификатору
// даже после роспуска.
func (a *App) DeleteTeam(teamID string) error {
	teamID = strings.TrimSpace(teamID)
	if teamID == "" {
		return errors.New("не указан отряд")
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return err
	}
	ctx := context.Background()
	quests, err := a.store.ListQuests(ctx, ws.ID)
	if err != nil {
		return err
	}
	for _, quest := range quests {
		if quest.TeamID != teamID {
			continue
		}
		switch quest.Status {
		case domain.QuestCompleted, domain.QuestNeedsReview, domain.QuestBlocked, domain.QuestFailed, domain.QuestCancelled:
			continue
		}
		return fmt.Errorf("отряд занят квестом %q — закройте или удалите квест", quest.Title)
	}
	return a.store.DeleteTeam(ctx, ws.ID, teamID)
}

func (a *App) SaveQuest(quest domain.Quest) (domain.Quest, error) {
	now := time.Now().UTC()
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.Quest{}, err
	}
	if quest.WorkspaceID != "" && quest.WorkspaceID != ws.ID {
		return domain.Quest{}, errors.New("quest belongs to another workspace")
	}
	quest.WorkspaceID = ws.ID
	if quest.ID == "" {
		quest.ID = domain.NewID("quest")
		quest.CreatedAt = now
	}
	if quest.CreatedAt.IsZero() {
		quest.CreatedAt = now
	}
	if quest.Status == "" {
		quest.Status = domain.QuestDraft
	}
	if quest.Importance == "" {
		quest.Importance = domain.QuestNormal
	}
	if err := a.guardTaskQuestUpdate(context.Background(), &quest); err != nil {
		return domain.Quest{}, err
	}
	quest.UpdatedAt = now
	if err := a.store.SaveQuest(context.Background(), quest); err != nil {
		return domain.Quest{}, err
	}
	return quest, nil
}

// DeleteQuest убирает квест из списка проекта.
//
// Кнопки удаления у квеста не было вовсе: черновик, поставленный по ошибке, и
// отменённый квест оставались в разделе навсегда. Список, из которого нельзя
// ничего убрать, со временем перестаёт показывать работу проекта.
//
// Удаляем только то, за чем никто не стоит, и всегда называем, что именно
// держит квест, — так же, как это делает роспуск персонажа. Хроника запусков
// остаётся: она доказательство сделанного, и интерфейс показывает её разделом
// «запуски без квеста».
func (a *App) DeleteQuest(questID string) error {
	questID = strings.TrimSpace(questID)
	if questID == "" {
		return errors.New("не указан квест")
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return err
	}
	ctx := context.Background()
	quests, err := a.store.ListQuests(ctx, ws.ID)
	if err != nil {
		return err
	}
	var target *domain.Quest
	for i := range quests {
		if quests[i].ID == questID {
			target = &quests[i]
			break
		}
	}
	if target == nil {
		return errors.New("квест не найден в открытом проекте")
	}
	for _, quest := range quests {
		if quest.ParentID == questID {
			return fmt.Errorf("у квеста есть подквест %q — сначала удалите его", quest.Title)
		}
	}
	executions, err := a.store.ListExecutions(ctx, ws.ID, 500)
	if err != nil {
		return err
	}
	for _, execution := range executions {
		if execution.QuestID != questID {
			continue
		}
		switch execution.Status {
		case domain.RunPending, domain.RunRunning, domain.RunPaused, domain.RunWaiting:
			return errors.New("по квесту идёт запуск — остановите его и повторите")
		}
	}
	flowRuns, err := a.store.ListFlowRuns(ctx, ws.ID, 500)
	if err != nil {
		return err
	}
	for _, run := range flowRuns {
		if run.QuestID != questID {
			continue
		}
		switch run.Status {
		case domain.RunPending, domain.RunRunning, domain.RunPaused, domain.RunWaiting:
			return errors.New("схема квеста ещё выполняется — остановите прогон и повторите")
		}
	}
	// Набор правок без своего квеста некому ни принять, ни откатить: карточка с
	// кнопками решения живёт у квеста. Удаление незакрытого набора оставило бы
	// правки в подвешенном состоянии, поэтому сначала решение, потом удаление.
	changeSets, err := a.store.ListChangeSets(ctx, ws.ID)
	if err != nil {
		return err
	}
	for _, set := range changeSets {
		if set.QuestID != questID {
			continue
		}
		switch set.Status {
		case domain.ChangeSetPending, domain.ChangeSetApproved, domain.ChangeSetConflict:
			return fmt.Errorf("набор правок %q ещё ждёт решения — примените или отклоните его", set.Title)
		}
	}
	// Схема, оставшаяся без единого квеста, — не схема, а замок на персонаже.
	//
	// Мастер собирает её под каждый квест, и она переживала его: узел держит
	// исполнителя по идентификатору, поэтому роспуск отказывал «замените его в
	// схеме». Заменить было негде — редактор графа скрыт, а экран уборки до
	// сих пор не имел входа. Схема, на которую больше не смотрит ни один квест,
	// уходит вместе с последним из них.
	//
	// Своя схема человека этим не задета: её не создаёт квест и на неё никто не
	// ссылается полем FlowID. Хранилище проверяет последнюю ссылку и живой прогон
	// в той же транзакции, что удаляет квест: между двумя снимками больше нет
	// окна, в котором параллельный запрос меняет ответ.
	return a.store.DeleteQuest(ctx, ws.ID, questID)
}
