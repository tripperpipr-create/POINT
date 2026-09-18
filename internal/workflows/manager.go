package workflows

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/textutil"
)

type Repository interface {
	SaveWorkflowRun(context.Context, domain.WorkflowRun) error
}

type AgentEngine interface {
	Start(agent.StartInput) (domain.Run, error)
	Cancel(string) error
}

type StartInput struct {
	ApplicationVersion string
	Workflow           domain.AgentWorkflow
	Profiles           []domain.AgentProfile
	CustomTools        []domain.CustomTool
	Workspace          domain.Workspace
	Task               string
	APIKeys            map[string]string
	ContextItems       []domain.RunContextItem
	OnFinished         func(domain.WorkflowRun) error
}

type activeExecution struct {
	mu         sync.RWMutex
	run        domain.WorkflowRun
	cancel     context.CancelFunc
	currentRun string
	external   chan externalCompletion
}

type externalCompletion struct {
	stepID string
	status domain.RunStatus
	result string
	err    string
}
type Manager struct {
	repo     Repository
	engine   AgentEngine
	mu       sync.RWMutex
	active   map[string]*activeExecution
	wg       sync.WaitGroup
	stopping bool
}

func NewManager(repo Repository, engine AgentEngine) *Manager {
	return &Manager{repo: repo, engine: engine, active: make(map[string]*activeExecution)}
}

func (m *Manager) Start(input StartInput) (domain.WorkflowRun, error) {
	input.Task = strings.TrimSpace(input.Task)
	if input.Task == "" {
		return domain.WorkflowRun{}, errors.New("workflow task is required")
	}
	if len(input.Task) > 64*1024 {
		return domain.WorkflowRun{}, errors.New("workflow task exceeds 64 KiB")
	}
	profiles := make(map[string]domain.AgentProfile, len(input.Profiles))
	for _, profile := range input.Profiles {
		profiles[profile.ID] = profile
	}
	stepRuns := make([]domain.WorkflowStepRun, len(input.Workflow.Steps))
	for index, step := range input.Workflow.Steps {
		profile, ok := profiles[step.ProfileID]
		if !ok && step.Kind != "manual" {
			return domain.WorkflowRun{}, fmt.Errorf("workflow step %d references a missing profile", index+1)
		}
		stepRuns[index] = domain.WorkflowStepRun{StepID: step.ID, StepName: step.Name, ProfileID: step.ProfileID, ProfileName: profile.Name, Kind: step.Kind, Status: domain.RunPending}
	}
	now := time.Now().UTC()
	run := domain.WorkflowRun{
		ID: domain.NewID("workflowrun"), WorkflowID: input.Workflow.ID, WorkspaceID: input.Workspace.ID,
		Task: input.Task, ContextItems: append([]domain.RunContextItem(nil), input.ContextItems...),
		Snapshot: domain.NewWorkflowSnapshot(input.ApplicationVersion, input.Workflow, now),
		Status:   domain.RunRunning, CurrentStep: 0, StepRuns: stepRuns, StartedAt: now,
	}
	ctx, cancel := context.WithCancel(context.Background())
	active := &activeExecution{run: run, cancel: cancel, external: make(chan externalCompletion, 1)}
	m.mu.Lock()
	if m.stopping {
		m.mu.Unlock()
		cancel()
		return domain.WorkflowRun{}, errors.New("workflow manager is stopping")
	}
	m.active[run.ID] = active
	m.wg.Add(1)
	m.mu.Unlock()
	if err := m.save(run); err != nil {
		cancel()
		m.mu.Lock()
		delete(m.active, run.ID)
		m.mu.Unlock()
		m.wg.Done()
		return domain.WorkflowRun{}, err
	}
	go m.execute(ctx, active, input, profiles)
	slog.Info("workflow started",
		"workflow_run_id", run.ID,
		"workflow_id", input.Workflow.ID,
		"workspace_id", input.Workspace.ID,
		"steps", len(input.Workflow.Steps),
		"task_preview", observability.Snippet(security.Redact(input.Task), 160),
	)
	return run, nil
}

func (m *Manager) execute(ctx context.Context, active *activeExecution, input StartInput, profiles map[string]domain.AgentProfile) {
	defer func() {
		active.cancel()
		finalRun := m.snapshot(active)
		if input.OnFinished != nil {
			if err := input.OnFinished(finalRun); err != nil {
				m.fail(active, fmt.Errorf("finalize workflow execution: %w", err))
				finalRun = m.snapshot(active)
			}
		}
		m.mu.Lock()
		delete(m.active, finalRun.ID)
		// Publish successful completion only after finalization and removal
		// from the active set. Readers may immediately delete the definition.
		if finalRun.Status == domain.RunCompleted {
			if err := m.save(finalRun); err != nil {
				m.fail(active, fmt.Errorf("persist finalized workflow: %w", err))
			}
		}
		m.mu.Unlock()
		m.wg.Done()
	}()
	previousResult := ""
	for index, step := range input.Workflow.Steps {
		if ctx.Err() != nil {
			m.finishCancelled(active)
			return
		}
		if !conditionPasses(step.Condition, m.snapshot(active).StepRuns, index) {
			finished := time.Now().UTC()
			m.update(active, func(run *domain.WorkflowRun) {
				run.CurrentStep = index + 1
				run.StepRuns[index].Status = domain.RunCompleted
				run.StepRuns[index].Result = "Skipped: condition not met"
				run.StepRuns[index].StartedAt, run.StepRuns[index].FinishedAt = &finished, &finished
			})
			if err := m.save(m.snapshot(active)); err != nil {
				m.fail(active, err)
				return
			}
			continue
		}
		profile := profiles[step.ProfileID]
		started := time.Now().UTC()
		slog.Info("workflow step start",
			"workflow_run_id", m.snapshot(active).ID,
			"step", index+1,
			"step_id", step.ID,
			"step_name", step.Name,
			"kind", step.Kind,
			"profile_id", step.ProfileID,
		)
		m.update(active, func(run *domain.WorkflowRun) {
			run.CurrentStep = index + 1
			if step.Kind == "cursor" || step.Kind == "manual" {
				run.StepRuns[index].Status = domain.RunWaiting
			} else {
				run.StepRuns[index].Status = domain.RunRunning
			}
			run.StepRuns[index].StartedAt = &started
		})
		if err := m.save(m.snapshot(active)); err != nil {
			m.fail(active, fmt.Errorf("persist workflow step start: %w", err))
			return
		}
		if step.Kind == "cursor" || step.Kind == "manual" {
			select {
			case completed := <-active.external:
				if completed.stepID != step.ID {
					m.fail(active, errors.New("external step completion did not match current step"))
					return
				}
				finished := time.Now().UTC()
				resultPreview, resultTruncated := boundedPreview(completed.result, 64*1024)
				m.update(active, func(run *domain.WorkflowRun) {
					s := &run.StepRuns[index]
					s.Status, s.Result, s.ResultTruncated, s.Error, s.FinishedAt = completed.status, resultPreview, resultTruncated, security.Redact(completed.err), &finished
				})
				if err := m.save(m.snapshot(active)); err != nil {
					m.fail(active, err)
					return
				}
				if completed.status != domain.RunCompleted {
					if step.OnFailure == "skip" {
						continue
					}
					m.fail(active, fmt.Errorf("step %q failed: %s", step.Name, completed.err))
					return
				}
				previousResult = completed.result
				continue
			case <-ctx.Done():
				m.finishCancelled(active)
				return
			}
		}
		completion := make(chan domain.Run, 1)
		configuration := domain.NewRunConfigurationSnapshot(input.ApplicationVersion, profile, input.CustomTools, started)
		child, err := m.engine.Start(agent.StartInput{
			Configuration: configuration,
			Workspace:     input.Workspace,
			Task:          stepTask(input.Task, step),
			APIKey:        input.APIKeys[profile.ID],
			ContextItems:  stageContext(input.ContextItems, previousResult, step),
			OnFinished: func(run domain.Run) {
				completion <- run
			},
		})
		if err != nil {
			m.failStep(active, index, err)
			return
		}
		active.mu.Lock()
		active.currentRun = child.ID
		active.run.StepRuns[index].RunID = child.ID
		active.mu.Unlock()
		if err := m.save(m.snapshot(active)); err != nil {
			_ = m.engine.Cancel(child.ID)
			m.fail(active, fmt.Errorf("persist workflow run: %w", err))
			return
		}
		select {
		case child = <-completion:
			active.mu.Lock()
			active.currentRun = ""
			active.mu.Unlock()
			finished := time.Now().UTC()
			resultPreview, resultTruncated := boundedPreview(child.Result, 64*1024)
			m.update(active, func(run *domain.WorkflowRun) {
				run.StepRuns[index].Status = child.Status
				run.StepRuns[index].Result = resultPreview
				run.StepRuns[index].ResultTruncated = resultTruncated
				run.StepRuns[index].Error = child.Error
				run.StepRuns[index].FinishedAt = &finished
			})
			if err := m.save(m.snapshot(active)); err != nil {
				m.fail(active, fmt.Errorf("persist workflow step result: %w", err))
				return
			}
			if child.Status != domain.RunCompleted {
				if step.OnFailure == "skip" {
					continue
				}
				if child.Status == domain.RunCancelled {
					m.finishCancelled(active)
				} else {
					m.fail(active, fmt.Errorf("step %q failed: %s", step.Name, child.Error))
				}
				return
			}
			previousResult = child.Result
		case <-ctx.Done():
			_ = m.engine.Cancel(child.ID)
			select {
			case <-completion:
			case <-time.After(5 * time.Second):
			}
			m.finishCancelled(active)
			return
		}
	}
	m.complete(active, previousResult)
}

func conditionPasses(condition *domain.WorkflowCondition, steps []domain.WorkflowStepRun, index int) bool {
	if condition == nil || condition.Type == "" || condition.Type == "always" {
		return true
	}
	if condition.Type != "previous_status" || index == 0 {
		return false
	}
	return string(steps[index-1].Status) == condition.Value
}

func (m *Manager) Claim(id, stepID string) (string, error) {
	m.mu.RLock()
	active, ok := m.active[id]
	m.mu.RUnlock()
	if !ok {
		return "", errors.New("workflow run is not active")
	}
	now := time.Now().UTC()
	token := domain.NewID("claim")
	active.mu.Lock()
	defer active.mu.Unlock()
	index := active.run.CurrentStep - 1
	if index < 0 || index >= len(active.run.StepRuns) || active.run.StepRuns[index].StepID != stepID {
		return "", errors.New("step is not current")
	}
	step := &active.run.StepRuns[index]
	if step.Status != domain.RunWaiting {
		return "", errors.New("step is not waiting for an external worker")
	}
	if step.ClaimToken != "" {
		return "", errors.New("step is already claimed")
	}
	step.ClaimToken, step.ClaimedAt, step.LastHeartbeatAt, step.Status = token, &now, &now, domain.RunRunning
	active.run.DurationMs = time.Since(active.run.StartedAt).Milliseconds()
	if err := m.save(active.run); err != nil {
		return "", err
	}
	return token, nil
}

func (m *Manager) Heartbeat(id, stepID, token string) error {
	m.mu.RLock()
	active, ok := m.active[id]
	m.mu.RUnlock()
	if !ok {
		return errors.New("workflow run is not active")
	}
	now := time.Now().UTC()
	active.mu.Lock()
	defer active.mu.Unlock()
	index := active.run.CurrentStep - 1
	if index < 0 || index >= len(active.run.StepRuns) {
		return errors.New("step is not current")
	}
	step := &active.run.StepRuns[index]
	if step.StepID != stepID || step.ClaimToken == "" || step.ClaimToken != token {
		return errors.New("invalid claim token")
	}
	step.LastHeartbeatAt = &now
	return m.save(active.run)
}

func (m *Manager) Complete(id, stepID, token string, status domain.RunStatus, result, message string) error {
	m.mu.RLock()
	active, ok := m.active[id]
	m.mu.RUnlock()
	if !ok {
		return errors.New("workflow run is not active")
	}
	active.mu.RLock()
	index := active.run.CurrentStep - 1
	valid := index >= 0 && index < len(active.run.StepRuns) && active.run.StepRuns[index].StepID == stepID && active.run.StepRuns[index].ClaimToken == token
	active.mu.RUnlock()
	if !valid {
		return errors.New("invalid claim token or non-current step")
	}
	if status != domain.RunCompleted && status != domain.RunFailed && status != domain.RunCancelled {
		return errors.New("unsupported external completion status")
	}
	select {
	case active.external <- externalCompletion{stepID: stepID, status: status, result: result, err: message}:
		return nil
	default:
		return errors.New("step completion already submitted")
	}
}

func stepTask(task string, step domain.WorkflowStep) string {
	var value strings.Builder
	value.WriteString("Общая цель workflow:\n")
	value.WriteString(task)
	value.WriteString("\n\nТекущий этап: ")
	value.WriteString(step.Name)
	if strings.TrimSpace(step.Instruction) != "" {
		value.WriteString("\nИнструкция этапа:\n")
		value.WriteString(strings.TrimSpace(step.Instruction))
	}
	value.WriteString("\n\nВыполни только этот этап и верни результат, пригодный для следующего агента.")
	return value.String()
}

func stageContext(original []domain.RunContextItem, previousResult string, step domain.WorkflowStep) []domain.RunContextItem {
	const maxItems = 16
	const maxBytes = 1024 * 1024
	const maxHandoffBytes = 256 * 1024
	items := make([]domain.RunContextItem, 0, maxItems)
	total := 0
	if step.IncludeOriginalContext {
		for _, item := range original {
			if len(items) >= maxItems || total+len(item.Content) > maxBytes {
				break
			}
			items = append(items, item)
			total += len(item.Content)
		}
	}
	if step.IncludePreviousResult && strings.TrimSpace(previousResult) != "" && len(items) < maxItems && total < maxBytes {
		content := security.Redact(previousResult)
		limit := maxHandoffBytes
		if remaining := maxBytes - total; remaining < limit {
			limit = remaining
		}
		truncated := false
		if len(content) > limit {
			content = textutil.BoundedBytes(content, limit)
			truncated = true
		}
		items = append(items, domain.RunContextItem{ID: domain.NewID("context"), Kind: domain.ContextText, Label: "Результат предыдущего этапа", Content: content, Size: int64(len(content)), Truncated: truncated})
	}
	return items
}

func boundedPreview(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	return textutil.BoundedBytes(value, limit), true
}

func (m *Manager) Cancel(id string) error {
	m.mu.RLock()
	active, ok := m.active[id]
	m.mu.RUnlock()
	if !ok {
		return errors.New("workflow run is not active")
	}
	active.mu.RLock()
	currentRun := active.currentRun
	active.mu.RUnlock()
	active.cancel()
	if currentRun != "" {
		_ = m.engine.Cancel(currentRun)
	}
	return nil
}

func (m *Manager) IsWorkflowActive(workflowID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, active := range m.active {
		active.mu.RLock()
		matches := active.run.WorkflowID == workflowID
		active.mu.RUnlock()
		if matches {
			return true
		}
	}
	return false
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	m.stopping = true
	items := make([]*activeExecution, 0, len(m.active))
	for _, item := range m.active {
		items = append(items, item)
	}
	m.mu.Unlock()
	for _, item := range items {
		item.cancel()
		item.mu.RLock()
		currentRun := item.currentRun
		item.mu.RUnlock()
		if currentRun != "" {
			_ = m.engine.Cancel(currentRun)
		}
	}
	m.wg.Wait()
}

func (m *Manager) snapshot(active *activeExecution) domain.WorkflowRun {
	active.mu.RLock()
	defer active.mu.RUnlock()
	return active.run
}

func (m *Manager) update(active *activeExecution, change func(*domain.WorkflowRun)) {
	active.mu.Lock()
	change(&active.run)
	active.run.DurationMs = time.Since(active.run.StartedAt).Milliseconds()
	active.mu.Unlock()
}

func (m *Manager) complete(active *activeExecution, result string) {
	now := time.Now().UTC()
	m.update(active, func(run *domain.WorkflowRun) {
		run.Status, run.Result, run.FinishedAt = domain.RunCompleted, result, &now
	})

	snapshot := m.snapshot(active)
	slog.Info("workflow completed", "workflow_run_id", snapshot.ID, "duration_ms", snapshot.DurationMs, "steps", len(snapshot.StepRuns))
}

func (m *Manager) failStep(active *activeExecution, index int, err error) {
	now := time.Now().UTC()
	m.update(active, func(run *domain.WorkflowRun) {
		run.StepRuns[index].Status = domain.RunFailed
		run.StepRuns[index].Error = security.Redact(err.Error())
		run.StepRuns[index].FinishedAt = &now
	})
	m.fail(active, err)
}

func (m *Manager) fail(active *activeExecution, err error) {
	now := time.Now().UTC()
	m.update(active, func(run *domain.WorkflowRun) {
		run.Status, run.Error, run.FinishedAt = domain.RunFailed, security.Redact(err.Error()), &now
	})
	snapshot := m.snapshot(active)
	if saveErr := m.save(snapshot); saveErr != nil {
		slog.Error("persist failed workflow failed", "workflow_run_id", snapshot.ID, "error", saveErr)
	}
	slog.Error("workflow failed", "workflow_run_id", snapshot.ID, "error", snapshot.Error)
}

func (m *Manager) finishCancelled(active *activeExecution) {
	now := time.Now().UTC()
	m.update(active, func(run *domain.WorkflowRun) {
		run.Status, run.Error, run.FinishedAt = domain.RunCancelled, "Workflow cancelled", &now
		if run.CurrentStep > 0 && run.CurrentStep <= len(run.StepRuns) && (run.StepRuns[run.CurrentStep-1].Status == domain.RunRunning || run.StepRuns[run.CurrentStep-1].Status == domain.RunWaiting) {
			run.StepRuns[run.CurrentStep-1].Status = domain.RunCancelled
			run.StepRuns[run.CurrentStep-1].FinishedAt = &now
		}
	})
	snapshot := m.snapshot(active)
	if err := m.save(snapshot); err != nil {
		slog.Error("persist cancelled workflow failed", "workflow_run_id", snapshot.ID, "error", err)
	}
}

func (m *Manager) save(run domain.WorkflowRun) error {
	safe := run
	safe.Task = security.Redact(safe.Task)
	safe.Error = security.Redact(safe.Error)
	safe.Result = security.Redact(safe.Result)
	safe.ContextItems = append([]domain.RunContextItem(nil), safe.ContextItems...)
	for index := range safe.ContextItems {
		safe.ContextItems[index].Content = security.Redact(safe.ContextItems[index].Content)
	}
	for index := range safe.StepRuns {
		safe.StepRuns[index].Result = security.Redact(safe.StepRuns[index].Result)
		safe.StepRuns[index].Error = security.Redact(safe.StepRuns[index].Error)
	}
	return m.repo.SaveWorkflowRun(context.Background(), safe)
}
