// Учёт того, что прогон изменил в рабочей области.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
	workbenchtools "local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
)

type workspaceAuditSummary struct {
	Tool                     string   `json:"tool"`
	ApprovalID               string   `json:"approvalId,omitempty"`
	TotalChanges             int      `json:"totalChanges"`
	RevertibleChanges        int      `json:"revertibleChanges"`
	RecordedChanges          int      `json:"recordedChanges"`
	NonRevertibleChanges     int      `json:"nonRevertibleChanges"`
	OmittedRevertibleChanges int      `json:"omittedRevertibleChanges"`
	Paths                    []string `json:"paths"`
	NonRevertiblePaths       []string `json:"nonRevertiblePaths,omitempty"`
	SnapshotComplete         bool     `json:"snapshotComplete"`
	SkippedPaths             []string `json:"skippedPaths,omitempty"`
}

var errWorkspaceAuditIntegrity = errors.New("workspace mutation audit failed after executable tool started")

func (e *Engine) bumpWorkspaceRevision(active *activeRun) int {
	active.mu.Lock()
	active.workspaceRevision++
	revision := active.workspaceRevision
	active.mu.Unlock()
	return revision
}

func (e *Engine) currentWorkspaceRevision(active *activeRun) int {
	active.mu.RLock()
	defer active.mu.RUnlock()
	return active.workspaceRevision
}

func (e *Engine) recordExecutableChanges(active *activeRun, patches *workbenchtools.PatchManager, toolName, approvalID string, before, after workspace.TextSnapshot) (workspaceAuditSummary, error) {
	changes := workspace.DiffTextSnapshots(before, after)
	summary := workspaceAuditSummary{
		Tool: toolName, ApprovalID: approvalID, TotalChanges: len(changes),
		SnapshotComplete: before.Complete && after.Complete,
		Paths:            []string{}, NonRevertiblePaths: []string{},
		SkippedPaths: mergeBoundedPaths(before.SkippedPaths, after.SkippedPaths, maxWorkspaceEventPaths),
	}
	var firstErr error
	for _, change := range changes {
		if len(summary.Paths) < maxWorkspaceEventPaths {
			summary.Paths = append(summary.Paths, change.Path)
		}
		if change.Revertible {
			summary.RevertibleChanges++
			if summary.RecordedChanges >= maxRecordedExecutableChanges {
				summary.OmittedRevertibleChanges++
				continue
			}
			patch, err := patches.RecordAppliedChange(e.snapshot(active).ID, approvalID, toolName, change)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if err = e.savePatch(*patch); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			summary.RecordedChanges++
			if err = e.publish(context.Background(), e.snapshot(active), domain.EventPatchApplied, "agent", safePatchPayload(*patch)); err != nil && firstErr == nil {
				firstErr = err
			}
		} else {
			summary.NonRevertibleChanges++
			if len(summary.NonRevertiblePaths) < maxWorkspaceEventPaths {
				summary.NonRevertiblePaths = append(summary.NonRevertiblePaths, change.Path)
			}
		}
	}
	if len(changes) > 0 {
		e.update(active, func(run *domain.Run) {
			for _, change := range changes {
				if len(run.ChangedFiles) >= 5000 {
					break
				}
				if !slices.Contains(run.ChangedFiles, change.Path) {
					run.ChangedFiles = append(run.ChangedFiles, change.Path)
				}
			}
		})
		patches.FS.InvalidateIndex()
		e.bumpWorkspaceRevision(active)
	}
	if len(changes) > 0 || !summary.SnapshotComplete {
		if err := e.publish(context.Background(), e.snapshot(active), domain.EventWorkspaceChanged, "agent", summary); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return summary, firstErr
}

func mergeBoundedPaths(first, second []string, limit int) []string {
	result := make([]string, 0, min(limit, len(first)+len(second)))
	seen := make(map[string]struct{})
	for _, paths := range [][]string{first, second} {
		for _, path := range paths {
			if len(result) >= limit {
				return result
			}
			if _, exists := seen[path]; exists {
				continue
			}
			seen[path] = struct{}{}
			result = append(result, path)
		}
	}
	return result
}

func attachWorkspaceAudit(result domain.ToolResult, summary workspaceAuditSummary) domain.ToolResult {
	output := make(map[string]any)
	if len(result.Output) > 0 && json.Valid(result.Output) {
		if err := json.Unmarshal(result.Output, &output); err != nil {
			output = map[string]any{"toolOutput": json.RawMessage(append([]byte(nil), result.Output...))}
		}
	}
	output["_pointWorkspaceAudit"] = summary
	encoded, err := json.Marshal(output)
	if err == nil {
		result.Output = encoded
	}
	return result
}

func safePatchPayload(patch domain.PatchProposal) domain.PatchProposal {
	patch.Original = ""
	patch.Proposed = ""
	patch.Diff = security.Redact(patch.Diff)
	return patch
}
