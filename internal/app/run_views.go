package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"local-agent-workbench/internal/attachments"
	"local-agent-workbench/internal/diagnostics"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/workspace"
)

func resolveRunContext(fs *workspace.FS, inputs []domain.RunContextInput) ([]domain.RunContextItem, error) {
	preview, err := attachments.Resolve(fs, inputs)
	return preview.Items, err
}

func (a *App) PreviewContext(inputs []domain.RunContextInput) (domain.ContextPreview, error) {
	fs, err := a.fs()
	if err != nil {
		return domain.ContextPreview{}, err
	}
	preview, err := attachments.Resolve(fs, inputs)
	if err != nil {
		return domain.ContextPreview{}, err
	}
	preview.Items = publicContextItems(preview.Items)
	return preview, nil
}
func (a *App) CancelRun(runID string) error {
	run, err := a.store.GetRun(context.Background(), runID)
	if err != nil {
		return err
	}
	if err = a.guardWorld(run.WorkspaceID); err != nil {
		return err
	}
	return a.engine.Cancel(runID)
}
func (a *App) ResolveApproval(approvalID string, allow bool) error {
	approval, err := a.store.GetApproval(context.Background(), approvalID)
	if err != nil {
		return err
	}
	run, err := a.store.GetRun(context.Background(), approval.RunID)
	if err != nil {
		return err
	}
	if err = a.guardWorld(run.WorkspaceID); err != nil {
		return err
	}
	return a.engine.ResolveApproval(approvalID, allow)
}

func (a *App) RevertPatch(patchID string) (domain.PatchProposal, error) {
	ctx := context.Background()
	patch, err := a.store.GetPatch(ctx, patchID)
	if err != nil {
		return domain.PatchProposal{}, err
	}
	run, runErr := a.store.GetRun(ctx, patch.RunID)
	if runErr != nil {
		return domain.PatchProposal{}, runErr
	}
	if err = a.guardWorld(run.WorkspaceID); err != nil {
		return domain.PatchProposal{}, err
	}
	if patch.Status != "applied" {
		return domain.PatchProposal{}, fmt.Errorf("only an applied change can be reverted; current status is %s", patch.Status)
	}
	fs, err := a.fs()
	if err != nil {
		return domain.PatchProposal{}, err
	}
	if err = fs.RestoreAgentChange(patch.Path, patch.Proposed, patch.Original, patch.OriginalExisted); err != nil {
		return domain.PatchProposal{}, err
	}
	patch.Status = "reverted"
	if err = a.store.SavePatch(ctx, patch); err != nil {
		return domain.PatchProposal{}, err
	}
	encoded, _ := json.Marshal(publicPatch(patch))
	event := domain.Event{ID: domain.NewID("event"), RunID: run.ID, AgentID: run.AgentID, Type: domain.EventPatchReverted, Step: run.Step, Actor: "user", Data: encoded, CreatedAt: time.Now().UTC()}
	_ = a.store.Append(ctx, event)
	a.emitEvent(event)
	_ = a.cache.DeletePrefix(ctx, a.cachePrefix())
	return publicPatch(patch), nil
}

func (a *App) RunDetails(runID string) (RunDetails, error) {
	ctx := context.Background()
	run, err := a.store.GetRun(ctx, runID)
	if err != nil {
		return RunDetails{}, err
	}
	if err = a.guardWorld(run.WorkspaceID); err != nil {
		return RunDetails{}, err
	}
	if run.ConfigurationSnapshot.SchemaVersion < 2 {
		legacyVersion := fmt.Sprintf("run-configuration-v%d", run.ConfigurationSnapshot.SchemaVersion)
		if err = a.recordCompatibilityUsage(ctx, run.WorkspaceID, domain.CompatibilityRunSnapshotRead, legacyVersion); err != nil {
			return RunDetails{}, err
		}
	}
	if run.Status == domain.RunCompleted || run.Status == domain.RunFailed || run.Status == domain.RunCancelled || run.Status == domain.RunInterrupted {
		a.engine.WaitFinalized(runID, 5*time.Second)
	}
	eventsList, err := a.store.ListByRun(ctx, runID)
	if err != nil {
		return RunDetails{}, err
	}
	approvals, err := a.store.ApprovalsByRun(ctx, runID)
	if err != nil {
		return RunDetails{}, err
	}
	patches, err := a.store.PatchesByRun(ctx, runID)
	if err != nil {
		return RunDetails{}, err
	}
	if eventsList == nil {
		eventsList = []domain.Event{}
	}
	if approvals == nil {
		approvals = []domain.Approval{}
	}
	if patches == nil {
		patches = []domain.PatchProposal{}
	}
	publicPatchList := publicPatches(patches)
	publicEventList := publicEvents(eventsList)
	return RunDetails{
		Run: publicRun(run), Events: publicEventList, Approvals: approvals, Patches: publicPatchList,
		Diagnostics: diagnostics.Analyze(run, eventsList, approvals, patches, time.Now().UTC()),
	}, nil
}

// Диагностика завершённого прогона неизменяема: события append-only, статус
// терминальный, а провайдер при разборе не вызывается. Пересчитывать её на
// каждый bootstrap — значит платить тремя запросами и полным реплеем событий за
// факт, который уже не изменится.
//
// Ключ включает статус и время завершения: если запись прогона всё же изменится,
// ключ разойдётся и разбор выполнится заново вместо выдачи устаревшего ответа.
const diagnosticsMemoLimit = 400

func diagnosticsMemoKey(run domain.Run) string {
	// Мемоизируем только терминальный прогон с проставленным FinishedAt: в этом
	// случае разбор не смотрит на текущее время, и результат воспроизводим.
	switch run.Status {
	case domain.RunCompleted, domain.RunFailed, domain.RunCancelled, domain.RunInterrupted:
	default:
		return ""
	}
	if run.FinishedAt == nil {
		return ""
	}
	return run.ID + "|" + string(run.Status) + "|" + run.FinishedAt.UTC().Format(time.RFC3339Nano)
}

func (a *App) cachedDiagnostics(key string) (diagnostics.RunDiagnostics, bool) {
	if key == "" {
		return diagnostics.RunDiagnostics{}, false
	}
	a.diagnosticsMu.Lock()
	defer a.diagnosticsMu.Unlock()
	value, ok := a.diagnosticsMemo[key]
	return value, ok
}

func (a *App) rememberDiagnostics(key string, value diagnostics.RunDiagnostics) {
	if key == "" {
		return
	}
	a.diagnosticsMu.Lock()
	defer a.diagnosticsMu.Unlock()
	if a.diagnosticsMemo == nil {
		a.diagnosticsMemo = map[string]diagnostics.RunDiagnostics{}
	}
	// Границу держим грубо: разбор дешевле, чем сложный вытеснитель, а память
	// важнее точности попадания. Сбрасываем целиком и наполняем заново.
	if len(a.diagnosticsMemo) >= diagnosticsMemoLimit {
		a.diagnosticsMemo = map[string]diagnostics.RunDiagnostics{}
	}
	a.diagnosticsMemo[key] = value
}

// runDiagnostics — единственный путь к разбору прогона. Всё, что ему нужно,
// грузится здесь же, и результат завершённого прогона запоминается. Отдельные
// места, собиравшие события и патчи вручную, повторяли эту стоимость мимо
// памяти — просто потому, что о ней надо было помнить.
func (a *App) runDiagnostics(ctx context.Context, run domain.Run) (diagnostics.RunDiagnostics, error) {
	key := diagnosticsMemoKey(run)
	if cached, ok := a.cachedDiagnostics(key); ok {
		return cached, nil
	}
	eventsList, err := a.store.ListByRun(ctx, run.ID)
	if err != nil {
		return diagnostics.RunDiagnostics{}, err
	}
	approvals, err := a.store.ApprovalsByRun(ctx, run.ID)
	if err != nil {
		return diagnostics.RunDiagnostics{}, err
	}
	patches, err := a.store.PatchesByRun(ctx, run.ID)
	if err != nil {
		return diagnostics.RunDiagnostics{}, err
	}
	analyzed := diagnostics.Analyze(run, eventsList, approvals, patches, time.Now().UTC())
	a.rememberDiagnostics(key, analyzed)
	return analyzed, nil
}

func (a *App) recentRunDiagnostics(ctx context.Context, runs []domain.Run, limit int) ([]diagnostics.RunDiagnostics, error) {
	if limit <= 0 || limit > len(runs) {
		limit = len(runs)
	}
	result := make([]diagnostics.RunDiagnostics, 0, limit)
	for _, run := range runs[:limit] {
		analyzed, err := a.runDiagnostics(ctx, run)
		if err != nil {
			return nil, err
		}
		result = append(result, analyzed)
	}
	return result, nil
}
func (a *App) Runs() ([]domain.Run, error) {
	runs, err := a.store.ListRunsForWorkspace(context.Background(), a.currentWorldID(), 100)
	if runs == nil {
		runs = []domain.Run{}
	}
	for index := range runs {
		runs[index] = publicRun(runs[index])
	}
	return runs, err
}

func publicRun(run domain.Run) domain.Run {
	run.ContextItems = publicContextItems(run.ContextItems)
	if run.Controller.ActiveSecondsBudget > 0 {
		run.Controller.ActiveSecondsRemaining = run.Controller.RemainingActiveSeconds()
	}
	return run
}

func publicWorkflowRun(run domain.WorkflowRun) domain.WorkflowRun {
	run.ContextItems = publicContextItems(run.ContextItems)
	return run
}

func publicContextItems(items []domain.RunContextItem) []domain.RunContextItem {
	result := append([]domain.RunContextItem(nil), items...)
	for index := range result {
		result[index].DataBase64 = ""
	}
	return result
}

func publicPatch(patch domain.PatchProposal) domain.PatchProposal {
	patch.Original = ""
	patch.Proposed = ""
	patch.Diff = security.Redact(patch.Diff)
	return patch
}

func publicPatches(items []domain.PatchProposal) []domain.PatchProposal {
	result := make([]domain.PatchProposal, len(items))
	for index, patch := range items {
		result[index] = publicPatch(patch)
	}
	return result
}

func publicEvents(items []domain.Event) []domain.Event {
	result := append([]domain.Event(nil), items...)
	for index := range result {
		switch result[index].Type {
		case domain.EventPatchProposed, domain.EventPatchApplied, domain.EventPatchRejected, domain.EventPatchReverted:
			var patch domain.PatchProposal
			if json.Unmarshal(result[index].Data, &patch) == nil {
				result[index].Data, _ = json.Marshal(publicPatch(patch))
			}
		}
	}
	return result
}
