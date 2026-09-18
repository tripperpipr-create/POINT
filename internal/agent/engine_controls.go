// Управление идущим прогоном: пауза, продолжение, отмена, правки на ходу.
package agent

import (
	"errors"
	"fmt"
	"strings"

	"local-agent-workbench/internal/attachments"
	"local-agent-workbench/internal/domain"
)

func (e *Engine) Pause(runID string) error {
	e.mu.RLock()
	active, ok := e.active[runID]
	e.mu.RUnlock()
	if !ok {
		return errors.New("run is not active")
	}
	active.controlMu.Lock()
	defer active.controlMu.Unlock()
	if active.paused {
		return nil
	}
	active.pauseRequested = true
	if active.pauseReason == "" {
		active.pauseReason = domain.PauseReasonUserRequested
	}
	return nil
}

func (e *Engine) requestPause(active *activeRun, reason string) {
	active.controlMu.Lock()
	defer active.controlMu.Unlock()
	active.pauseRequested = true
	active.pauseReason = reason
}

func (e *Engine) ExtendActiveTime(runID string) error {
	e.mu.RLock()
	active, ok := e.active[runID]
	e.mu.RUnlock()
	if !ok {
		return errors.New("run is not active")
	}
	if err := active.clock.extend(); err != nil {
		return err
	}
	e.syncControllerState(active, active.pauseReason, true)
	return e.saveRun(e.snapshot(active))
}

func (e *Engine) Resume(runID string) error {
	e.mu.RLock()
	active, ok := e.active[runID]
	e.mu.RUnlock()
	if !ok {
		return errors.New("run is not active")
	}
	active.controlMu.Lock()
	if !active.paused || active.resumeCh == nil {
		active.controlMu.Unlock()
		return errors.New("run is not paused")
	}
	ch := active.resumeCh
	active.resumeCh = nil // Claim the signal while holding the lock; concurrent resume must not close twice.
	active.controlMu.Unlock()
	close(ch)
	return nil
}

func (e *Engine) InjectRunMessage(runID, message, learningIntent string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return errors.New("message is required")
	}
	learningIntent = strings.ToLower(strings.TrimSpace(learningIntent))
	if learningIntent != "" && learningIntent != "correction" {
		return errors.New("unsupported learning intent")
	}
	e.mu.RLock()
	active, ok := e.active[runID]
	e.mu.RUnlock()
	if !ok {
		return errors.New("run is not active")
	}
	active.amendmentsMu.Lock()
	active.amendments.PendingMessages = append(active.amendments.PendingMessages, domain.RunMessageAmendment{Content: message, LearningIntent: learningIntent})
	active.amendmentsMu.Unlock()
	return nil
}

func (e *Engine) ForbidRunFile(runID, path string) error {
	path = normalizeAmendmentPath(path)
	if path == "" {
		return errors.New("path is required")
	}
	e.mu.RLock()
	active, ok := e.active[runID]
	e.mu.RUnlock()
	if !ok {
		return errors.New("run is not active")
	}
	active.amendmentsMu.Lock()
	defer active.amendmentsMu.Unlock()
	for _, existing := range active.amendments.ForbiddenPaths {
		if pathsForbiddenMatch(path, existing) {
			return nil
		}
	}
	active.amendments.ForbiddenPaths = append(active.amendments.ForbiddenPaths, path)
	return nil
}

func (e *Engine) AmendRunContext(runID string, action domain.ContextAmendAction, itemID string) error {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return errors.New("itemId is required")
	}
	switch action {
	case domain.ContextAmendPin, domain.ContextAmendUnpin, domain.ContextAmendRemove:
	default:
		return fmt.Errorf("unsupported context action %q", action)
	}
	e.mu.RLock()
	active, ok := e.active[runID]
	e.mu.RUnlock()
	if !ok {
		return errors.New("run is not active")
	}
	active.amendmentsMu.Lock()
	active.amendments.ContextAmends = append(active.amendments.ContextAmends, domain.ContextAmendment{Action: action, ItemID: itemID})
	active.amendmentsMu.Unlock()
	return nil
}

// AddRunContext queues backend-resolved immutable snapshots for the next safe
// model checkpoint. Aggregate limits are checked again while holding the
// amendment queue lock so concurrent additions cannot bypass them.
func (e *Engine) AddRunContext(runID string, items []domain.RunContextItem) error {
	if len(items) == 0 {
		return errors.New("at least one context item is required")
	}
	e.mu.RLock()
	active, ok := e.active[runID]
	e.mu.RUnlock()
	if !ok {
		return errors.New("run is not active")
	}
	active.amendmentsMu.Lock()
	defer active.amendmentsMu.Unlock()

	active.mu.RLock()
	combined := append([]domain.RunContextItem(nil), active.run.ContextItems...)
	active.mu.RUnlock()
	for _, amendment := range active.amendments.ContextAmends {
		if amendment.Action == domain.ContextAmendAdd && amendment.Item != nil {
			combined = append(combined, *amendment.Item)
		}
	}
	pending := make([]domain.ContextAmendment, 0, len(items))
	for index := range items {
		item := items[index]
		if strings.TrimSpace(item.ID) == "" {
			return fmt.Errorf("context item %d has no immutable id", index+1)
		}
		for _, existing := range combined {
			if existing.ID == item.ID {
				return fmt.Errorf("context item %q is already attached", item.ID)
			}
			if item.Digest != "" && existing.Digest == item.Digest && existing.Path == item.Path {
				return fmt.Errorf("context item %q is already attached", item.Label)
			}
		}
		item.Amendable = false
		item.Pending = false
		combined = append(combined, item)
		queued := item
		pending = append(pending, domain.ContextAmendment{
			Action: domain.ContextAmendAdd, ItemID: queued.ID, Item: &queued,
		})
	}
	if err := attachments.ValidateSnapshotLimits(combined); err != nil {
		return err
	}
	active.amendments.ContextAmends = append(active.amendments.ContextAmends, pending...)
	return nil
}

func (e *Engine) RunAmendments(runID string) domain.ExecutionAmendments {
	e.mu.RLock()
	active, ok := e.active[runID]
	e.mu.RUnlock()
	if !ok {
		return domain.ExecutionAmendments{}
	}
	active.amendmentsMu.Lock()
	defer active.amendmentsMu.Unlock()
	return cloneAmendments(active.amendments)
}

func (e *Engine) Cancel(runID string) error {
	e.mu.RLock()
	active, ok := e.active[runID]
	e.mu.RUnlock()
	if !ok {
		return errors.New("run is not active")
	}
	active.cancel()
	return nil
}

func (e *Engine) StopAll() {
	e.mu.Lock()
	e.stopping = true
	items := make([]*activeRun, 0, len(e.active))
	for _, item := range e.active {
		items = append(items, item)
	}
	e.mu.Unlock()
	for _, item := range items {
		item.cancel()
	}
	e.wg.Wait()
}

func (e *Engine) discardUnstarted(runID string) {
	e.mu.Lock()
	delete(e.active, runID)
	e.mu.Unlock()
	e.wg.Done()
}
