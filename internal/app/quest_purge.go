package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// Снос квеста одним решением человека: остановить, откатить, удалить.
//
// Раньше это были три разных действия, и каждое отказывало по-своему: «по
// квесту идёт запуск», «схема ещё выполняется», «набор правок ждёт решения».
// Человек, который уже решил, что этой работы быть не должно, обходил три
// отказа подряд и в конце всё равно оставался с правками в проекте — их
// откатывала другая кнопка, в другом разделе, по одному набору за раз.
//
// Порядок здесь и есть смысл: сначала останавливаем — иначе агент допишет файл
// в откаченный проект; потом откатываем — пока наборы правок ещё есть в базе;
// и только потом удаляем. Обратный порядок стирает то, чем откатывают.
type QuestPurgeResult struct {
	QuestID  string           `json:"questId"`
	Title    string           `json:"title"`
	Stopped  int              `json:"stopped"`
	Reverted int              `json:"revertedFiles"`
	Deleted  map[string]int   `json:"deleted"`
	Failures []QuestPurgeFail `json:"failures,omitempty"`
}

// QuestPurgeFail — то, что откатить не удалось. Правка осталась в проекте, и
// молчать об этом нельзя: человек нажал «удалить со всеми изменениями» и вправе
// узнать, какие изменения остались, пока он не смотрел.
type QuestPurgeFail struct {
	ChangeSetID string `json:"changeSetId,omitempty"`
	Title       string `json:"title,omitempty"`
	Reason      string `json:"reason"`
}

func (a *App) PurgeQuest(questID string) (QuestPurgeResult, error) {
	questID = strings.TrimSpace(questID)
	if questID == "" {
		return QuestPurgeResult{}, errors.New("не указан квест")
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return QuestPurgeResult{}, err
	}
	ctx := context.Background()
	quests, err := a.store.ListQuests(ctx, ws.ID)
	if err != nil {
		return QuestPurgeResult{}, err
	}
	var target *domain.Quest
	for index := range quests {
		if quests[index].ID == questID {
			target = &quests[index]
			break
		}
	}
	if target == nil {
		return QuestPurgeResult{}, errors.New("квест не найден в открытом проекте")
	}
	result := QuestPurgeResult{QuestID: questID, Title: target.Title, Deleted: map[string]int{}}

	// Подквесты сносятся первыми и целиком: у каждого своя работа, свои правки
	// и свой откат. Оставить их сиротами значило бы получить карточки, ведущие
	// к удалённому родителю, — ровно ту свалку, ради уборки которой всё это.
	for _, child := range questChildrenOf(quests, questID) {
		childResult, childErr := a.PurgeQuest(child)
		if childErr != nil {
			return result, fmt.Errorf("подквест %s: %w", child, childErr)
		}
		result.Stopped += childResult.Stopped
		result.Reverted += childResult.Reverted
		result.Failures = append(result.Failures, childResult.Failures...)
		for table, count := range childResult.Deleted {
			result.Deleted[table] += count
		}
	}

	stopped, err := a.stopQuestWork(ctx, ws.ID, questID)
	result.Stopped += stopped
	if err != nil {
		return result, err
	}
	reverted, failures := a.revertQuestChanges(ctx, ws.ID, questID)
	result.Reverted += reverted
	result.Failures = append(result.Failures, failures...)

	counts, err := a.store.PurgeQuest(ctx, ws.ID, questID)
	if err != nil {
		return result, err
	}
	for table, count := range counts {
		result.Deleted[table] += count
	}
	return result, nil
}

func questChildrenOf(quests []domain.Quest, parentID string) []string {
	var children []string
	for _, quest := range quests {
		if quest.ParentID == parentID {
			children = append(children, quest.ID)
		}
	}
	sort.Strings(children)
	return children
}

// stopQuestWork гасит всё, что по квесту ещё шевелится.
//
// Состояния правятся напрямую, а не через машину переходов наряда: та отказывает
// завершённому и отменённому квесту, а снести надо любой. Отмена здесь — не
// смена состояния работы, а её конец: следом эти строки всё равно исчезнут.
func (a *App) stopQuestWork(ctx context.Context, workspaceID, questID string) (int, error) {
	// Отложенный запуск ждёт планировщика минутами. Решение человека старше:
	// иначе снесённый квест через полчаса попробовал бы стартовать снова.
	a.cancelWorkOrderLaunchV2(questID)
	stopped := 0
	now := time.Now().UTC()
	executions, err := a.store.ListExecutions(ctx, workspaceID, 500)
	if err != nil {
		return stopped, err
	}
	for _, execution := range executions {
		if execution.QuestID != questID {
			continue
		}
		if execution.RunID != "" && a.engine.IsActiveRun(execution.RunID) {
			// Отказ движка не останавливает снос: прогон мог завершиться между
			// проверкой и отменой, и это не повод оставлять квест на месте.
			if cancelErr := a.engine.Cancel(execution.RunID); cancelErr == nil {
				stopped++
			}
		}
		switch execution.Status {
		case domain.RunCompleted, domain.RunFailed, domain.RunCancelled:
			continue
		}
		execution.Status = domain.RunCancelled
		execution.Error = "квест снесён вместе со всеми следами"
		execution.FinishedAt = &now
		if saveErr := a.store.SaveExecution(ctx, execution); saveErr != nil {
			return stopped, saveErr
		}
	}
	flowRuns, err := a.store.ListFlowRuns(ctx, workspaceID, 500)
	if err != nil {
		return stopped, err
	}
	for _, run := range flowRuns {
		if run.QuestID != questID {
			continue
		}
		switch run.Status {
		case domain.RunPending, domain.RunRunning, domain.RunPaused, domain.RunWaiting:
		default:
			continue
		}
		run.Status = domain.RunCancelled
		run.Error = "квест снесён вместе со всеми следами"
		run.FinishedAt = &now
		if saveErr := a.store.SaveFlowRun(ctx, run); saveErr != nil {
			return stopped, saveErr
		}
		stopped++
	}
	return stopped, nil
}

// revertQuestChanges возвращает проект к тому, что было до квеста.
//
// Неудача одного набора не останавливает остальные и не отменяет снос: человек
// уже решил, что этой работы быть не должно, и застрявший набор — повод
// назвать его вслух, а не оставить карточку квеста жить дальше. Всё, что
// откатить не вышло, уезжает в Failures и оттуда — в ответ человеку.
func (a *App) revertQuestChanges(ctx context.Context, workspaceID, questID string) (int, []QuestPurgeFail) {
	sets, err := a.store.ListChangeSets(ctx, workspaceID)
	if err != nil {
		return 0, []QuestPurgeFail{{Reason: "не удалось прочитать наборы правок: " + err.Error()}}
	}
	reverted := 0
	var failures []QuestPurgeFail
	for _, set := range sets {
		if set.QuestID != questID {
			continue
		}
		switch set.Status {
		case domain.ChangeSetApplied:
			outcome, revertErr := a.RevertChangeSet(set.ID)
			if revertErr != nil {
				failures = append(failures, QuestPurgeFail{ChangeSetID: set.ID, Title: set.Title, Reason: revertErr.Error()})
				continue
			}
			reverted += len(outcome.Applied)
			for _, path := range outcome.Conflicts {
				failures = append(failures, QuestPurgeFail{ChangeSetID: set.ID, Title: set.Title, Reason: "конфликт при откате: " + path})
			}
		case domain.ChangeSetPending, domain.ChangeSetApproved, domain.ChangeSetConflict:
			if _, rejectErr := a.RejectChangeSet(set.ID); rejectErr != nil {
				failures = append(failures, QuestPurgeFail{ChangeSetID: set.ID, Title: set.Title, Reason: rejectErr.Error()})
			}
		}
	}
	// Совместимость с исполнениями до наборов правок: они писали прямо в
	// рабочую папку, и откатывать у них нечего, кроме самих заплат.
	executions, err := a.store.ListExecutions(ctx, workspaceID, 500)
	if err != nil {
		return reverted, failures
	}
	for _, execution := range executions {
		if execution.QuestID != questID || execution.RunID == "" {
			continue
		}
		if questExecutionHasChangeSet(sets, execution.ID) {
			continue
		}
		patches, patchErr := a.store.PatchesByRun(ctx, execution.RunID)
		if patchErr != nil {
			continue
		}
		for _, patch := range patches {
			if patch.Status != "applied" {
				continue
			}
			if _, revertErr := a.RevertPatch(patch.ID); revertErr != nil {
				failures = append(failures, QuestPurgeFail{Title: patch.Path, Reason: revertErr.Error()})
				continue
			}
			reverted++
		}
	}
	return reverted, failures
}

func questExecutionHasChangeSet(sets []domain.ChangeSet, executionID string) bool {
	for _, set := range sets {
		if set.ExecutionID == executionID {
			return true
		}
	}
	return false
}
