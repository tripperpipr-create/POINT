package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"local-agent-workbench/internal/domain"
)

type UndoRunRequest struct {
	PatchIDs []string `json:"patchIds,omitempty"` // empty = all applied patches for the run
}

type UndoRunResult struct {
	RunID    string                 `json:"runId"`
	Reverted []domain.PatchProposal `json:"reverted"`
	Skipped  []string               `json:"skipped,omitempty"`
}

// UndoRunPatches reverts applied patches for a run (Cursor Keep/Undo session bar).
// Empty PatchIDs reverts every applied patch in reverse chronological order.
func (a *App) UndoRunPatches(runID string, request UndoRunRequest) (UndoRunResult, error) {
	ctx := context.Background()
	run, err := a.store.GetRun(ctx, runID)
	if err != nil {
		return UndoRunResult{}, err
	}
	if err = a.guardWorld(run.WorkspaceID); err != nil {
		return UndoRunResult{}, err
	}
	patches, err := a.store.PatchesByRun(ctx, runID)
	if err != nil {
		return UndoRunResult{}, err
	}
	want := map[string]bool{}
	for _, id := range request.PatchIDs {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}
	filter := len(want) > 0
	var targets []domain.PatchProposal
	for i := len(patches) - 1; i >= 0; i-- {
		p := patches[i]
		if p.Status != "applied" {
			continue
		}
		if filter && !want[p.ID] {
			continue
		}
		targets = append(targets, p)
	}
	if len(targets) == 0 {
		return UndoRunResult{}, errors.New("no applied patches to undo for this run")
	}
	result := UndoRunResult{RunID: runID}
	for _, patch := range targets {
		reverted, revertErr := a.RevertPatch(patch.ID)
		if revertErr != nil {
			result.Skipped = append(result.Skipped, fmt.Sprintf("%s: %v", patch.ID, revertErr))
			continue
		}
		result.Reverted = append(result.Reverted, reverted)
	}
	if len(result.Reverted) == 0 {
		return result, fmt.Errorf("undo failed: %s", strings.Join(result.Skipped, "; "))
	}
	return result, nil
}
