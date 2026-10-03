package app

import (
	"context"
	"log/slog"
	"strings"

	"local-agent-workbench/internal/security"
)

// Брошенная работа — дело мира, а не процесса.
//
// Ядра проектов делят одну базу, и при открытом приложении их живёт
// несколько. Раньше старт любого ядра считал брошенным всё живое в базе:
// холодный старт проекта B ставил на паузу идущий квест проекта A, прерывал
// его прогоны и ход Мастера, откладывал его обучение и удалял временные
// беседы. Теперь ядро разбирает только свой мир и только один раз за процесс:
// работу соседнего мира разберёт его собственное ядро, когда поднимется.

// startupWorldID — мир, для которого запущено ядро (WORKSPACE_ROOT). Граница и
// корень мира нормализуются одинаково (Abs, EvalSymlinks, Clean), поэтому мир
// находится по пути ещё до OpenWorkspace. Мира нет в базе — разбирать нечего.
func (a *App) startupWorldID(ctx context.Context) string {
	if strings.TrimSpace(a.workspaceBoundary) == "" {
		return ""
	}
	world, err := a.store.WorkspaceByPath(ctx, a.workspaceBoundary)
	if err != nil {
		return ""
	}
	return world.ID
}

// recoverAbandonedWorld переводит брошенную работу мира в честные состояния.
// Порядок прежний: сначала хранилище (прогоны, ходы, обучение, временные
// беседы), затем починка незавершённых итогов, затем пауза живых квестов.
func (a *App) recoverAbandonedWorld(ctx context.Context, workspaceID string) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return
	}
	a.mu.Lock()
	if a.recoveredWorlds == nil {
		a.recoveredWorlds = map[string]bool{}
	}
	done := a.recoveredWorlds[workspaceID]
	a.recoveredWorlds[workspaceID] = true
	a.mu.Unlock()
	if done {
		return
	}
	if err := a.store.RecoverAbandonedWork(ctx, workspaceID); err != nil {
		slog.Error("abandoned work recovery failed", "workspace_id", workspaceID, "error", security.Redact(err.Error()))
	}
	// This one-shot idempotent repair runs before generic interrupted-work
	// recovery can reinterpret the historic cancelled root.
	a.reconcileBrokenWorkOrderFinalizationsV2(ctx, workspaceID)
	a.reconcileNoopWorkOrderResumesV2(ctx, workspaceID)
	// A quest left in a live state belongs to a process that no longer exists;
	// resolve that before anything else can read it as progress.
	a.pauseInterruptedWorkOrderQuestsV2(ctx, workspaceID)
	a.resumeWorkOrderStaffingV2(ctx, workspaceID)
}
