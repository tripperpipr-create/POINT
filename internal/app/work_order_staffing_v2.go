package app

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
)

// Подбор исполнителей и ветки вне хода Мастера (TODO Q15).
//
// Комплектовщик (в среднем 10 с, до 22 с) и git-агент (fetch и модель
// Архивариуса) стояли внутри хода: человек ждал карточку, пока они думали.
// Теперь ход сохраняет наряд с отметкой Roster.Selecting и кончается, а
// фоновая задача дописывает следующую версию с составом и git-планом.
// Утвердить наряд в подборе нельзя (ValidateWorkOrder). Если за время
// подбора наряд изменился, результат выбрасывается и подбор повторяется на
// новой версии.

// workOrderStaffingTimeout — сколько живёт фоновый подбор одного наряда.
const workOrderStaffingTimeout = 3 * time.Minute

type staffingJob struct{ cancel context.CancelFunc }

type workOrderStaffing struct {
	mu       sync.Mutex
	jobs     map[string]*staffingJob
	wg       sync.WaitGroup
	stopping bool
	// save сериализует «прочитать текущую версию — сохранить следующую» у хода
	// Мастера и у фонового подбора: иначе обе записи взяли бы одну версию.
	save sync.Mutex
}

// startWorkOrderStaffingV2 запускает подбор наряда; прежний подбор того же
// наряда отменяется — его версия уже устарела.
func (a *App) startWorkOrderStaffingV2(workOrderID string, cfg domain.OrchestratorConfig, apiKey string) {
	jobs := &a.staffing
	jobs.mu.Lock()
	if jobs.stopping {
		jobs.mu.Unlock()
		return
	}
	if jobs.jobs == nil {
		jobs.jobs = map[string]*staffingJob{}
	}
	if previous := jobs.jobs[workOrderID]; previous != nil {
		previous.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), workOrderStaffingTimeout)
	job := &staffingJob{cancel: cancel}
	jobs.jobs[workOrderID] = job
	jobs.wg.Add(1)
	jobs.mu.Unlock()
	go func() {
		defer jobs.wg.Done()
		defer func() {
			cancel()
			jobs.mu.Lock()
			// Отменённую задачу могла сменить новая: удаляется только своя.
			if jobs.jobs[workOrderID] == job {
				delete(jobs.jobs, workOrderID)
			}
			jobs.mu.Unlock()
		}()
		if err := a.completeWorkOrderStaffingV2(ctx, workOrderID, cfg, apiKey); err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("work order staffing failed", "work_order_id", workOrderID, "error", security.Redact(err.Error()))
		}
	}()
}

func (a *App) stopWorkOrderStaffing() {
	jobs := &a.staffing
	jobs.mu.Lock()
	jobs.stopping = true
	for _, job := range jobs.jobs {
		job.cancel()
	}
	jobs.mu.Unlock()
	jobs.wg.Wait()
}

// completeWorkOrderStaffingV2 подбирает состав и ветку и дописывает их новой
// версией, если наряд за это время не изменился.
func (a *App) completeWorkOrderStaffingV2(ctx context.Context, workOrderID string, cfg domain.OrchestratorConfig, apiKey string) error {
	for attempt := 0; attempt < 3; attempt++ {
		current, err := a.store.GetWorkOrderV2(ctx, workOrderID)
		if err != nil {
			return err
		}
		if current.State != "staffing" || !current.Roster.Selecting {
			return nil
		}
		// Сохранённый в наряде git-план несёт выбор человека из прошлой версии:
		// git-агент оставит его, если репозитории не сдвинулись.
		plan, commitMode := a.masterWorkOrderGitV2(ctx, current, &current, cfg, apiKey)
		selection, selectErr := a.selectAgentsForWorkOrder(ctx, current, cfg, apiKey)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		saved, changed, err := a.applyWorkOrderStaffingV2(ctx, current, plan, commitMode, selection, selectErr, cfg)
		if changed {
			continue
		}
		if err != nil {
			return err
		}
		if selectErr != nil {
			return selectErr
		}
		slog.Info("work order staffed", "work_order_id", saved.ID, "version", saved.Version, "state", saved.State, "agents", len(selection.AgentIDs), "drafts", len(selection.Drafts))
		return nil
	}
	return errors.New("work order kept changing while its roster was being selected")
}

// applyWorkOrderStaffingV2 пишет следующую версию под замком сохранения.
// changed — наряд успел смениться, результат устарел.
func (a *App) applyWorkOrderStaffingV2(ctx context.Context, basis domain.WorkOrder, plan *domain.GitPlan, commitMode string, selection agentSelectionResult, selectErr error, cfg domain.OrchestratorConfig) (domain.WorkOrder, bool, error) {
	a.staffing.save.Lock()
	defer a.staffing.save.Unlock()
	latest, err := a.store.GetWorkOrderV2(ctx, basis.ID)
	if err != nil {
		return domain.WorkOrder{}, false, err
	}
	if latest.Version != basis.Version || latest.Digest != basis.Digest {
		return domain.WorkOrder{}, true, nil
	}
	next := latest
	next.Version++
	next.UpdatedAt = time.Now().UTC()
	next.Git, next.Delivery.CommitMode = plan, commitMode
	if selectErr != nil {
		next.Roster = domain.AgentRosterPlan{SelectionError: strings.TrimSpace(security.Redact(selectErr.Error()))}
	} else {
		next.Roster = a.rosterFromSelection(ctx, selection)
		if len(selection.Drafts) == 0 && len(selection.AgentIDs) > 0 {
			next.State = "ready"
		}
	}
	saved, err := a.SaveWorkOrderV2(ctx, next)
	if err != nil {
		return domain.WorkOrder{}, false, err
	}
	if selectErr == nil {
		if err = a.recordWorkOrderSelectionV2(ctx, saved, selection, cfg); err != nil {
			return saved, false, err
		}
	}
	return saved, false, nil
}

// recordWorkOrderSelectionV2 — привязки подбора и событие жизненного цикла.
func (a *App) recordWorkOrderSelectionV2(ctx context.Context, saved domain.WorkOrder, selection agentSelectionResult, cfg domain.OrchestratorConfig) error {
	if saved.State != "ready" && saved.State != "staffing" {
		return nil
	}
	if err := a.store.ReplaceAgentSelectionBindings(ctx, saved.ID, saved.ConversationID, saved.WorkspaceID, selection.Digest, saved.Version, selection.AgentIDs); err != nil {
		return err
	}
	kind := "agent_selection_completed"
	if saved.State == "staffing" {
		kind = "agent_selection_needs_creation"
	}
	_ = a.store.SaveAgentLifecycleEvent(ctx, domain.AgentLifecycleEvent{
		ID: domain.NewID("agentlife"), WorkspaceID: saved.WorkspaceID, WorkOrderID: saved.ID,
		Kind: kind, Detail: map[string]any{
			"selectionDigest": selection.Digest, "revision": saved.Version, "agentIds": selection.AgentIDs,
			"draftCount": len(selection.Drafts), "state": saved.State,
			"model": cfg.Model, "fallbackReason": selection.Fallback, "validation": "accepted",
		}, CreatedAt: saved.UpdatedAt,
	})
	return nil
}

// resumeWorkOrderStaffingV2 — подбор, прерванный остановкой ядра, идёт заново.
// Ключа API хода у восстановления нет: комплектовщик и git-агент без модели
// работают по своим детерминированным правилам.
func (a *App) resumeWorkOrderStaffingV2(ctx context.Context, workspaceID string) {
	orders, err := a.store.ListWorkOrdersForWorkspaceV2(ctx, workspaceID)
	if err != nil {
		return
	}
	var cfg domain.OrchestratorConfig
	loaded := false
	for _, order := range orders {
		if order.State != "staffing" || !order.Roster.Selecting {
			continue
		}
		if !loaded {
			cfg, _ = a.masterConfig(ctx, workspaceID)
			loaded = true
		}
		a.startWorkOrderStaffingV2(order.ID, cfg, "")
	}
}
