// Выполнение одного вызова инструмента и запрос разрешения у человека.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/policy"
	"local-agent-workbench/internal/providers"
	"local-agent-workbench/internal/security"
	workbenchtools "local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
)

func (e *Engine) executeTool(ctx context.Context, active *activeRun, profile domain.AgentProfile, registry *workbenchtools.Registry, patches *workbenchtools.PatchManager, observations *observationTracker, call providers.ToolCall) (domain.ToolResult, error) {
	run := e.snapshot(active)
	originalName := call.Name
	mapped, remapHint := remapToolName(call.Name, profile.AllowedTools)
	call.Name = mapped
	call.Arguments = normalizeToolArguments(call.Name, call.Arguments)
	if call.Name != originalName {
		observability.From(ctx).Info("agent tool remap",
			"run_id", run.ID,
			"from", originalName,
			"to", call.Name,
		)
	}
	if call.ArgumentError != "" {
		result := workbenchtools.FailWithHint("invalid_input", call.ArgumentError, "pass a single JSON object whose keys match the tool schema; do not wrap arguments in a string")
		e.publishOrLog(ctx, run, domain.EventToolRequested, "model", map[string]any{"tool": call.Name, "error": call.ArgumentError})
		e.publishOrLog(ctx, run, domain.EventToolFinished, "agent", map[string]any{"tool": call.Name, "durationMs": 0, "result": result})
		return result, nil
	}
	if len(call.Arguments) > maxToolArgumentBytes {
		e.publishOrLog(ctx, run, domain.EventToolRequested, "model", map[string]any{"tool": call.Name, "error": "arguments exceed 1 MiB"})
		return workbenchtools.FailWithHint("arguments_too_large", "tool arguments exceed 1 MiB", "shrink the payload; for large files use read_file startLine/endLine or propose_patch edits instead of a full rewrite"), nil
	}
	if err := e.publish(ctx, run, domain.EventToolRequested, "model", map[string]any{"tool": call.Name, "arguments": call.Arguments, "callId": call.ID}); err != nil {
		return domain.ToolResult{}, fmt.Errorf("%w: request was not persisted; tool was not executed: %v", errToolJournalIntegrity, err)
	}
	observability.From(ctx).Info("agent tool requested",
		"run_id", run.ID,
		"tool", call.Name,
		"args_bytes", len(call.Arguments),
		"args_preview", observability.Snippet(security.Redact(string(call.Arguments)), 400),
	)
	if path := toolCallTargetPath(call.Name, call.Arguments); path != "" {
		if patches != nil {
			path = relativizeToolPath(patches.FS, path)
		}
		if e.isPathForbidden(active, path) {
			result := forbiddenPathFailure(path)
			e.publishOrLog(ctx, run, domain.EventToolFinished, "agent", map[string]any{"tool": call.Name, "durationMs": 0, "result": result})
			return result, nil
		}
	}
	if toolHasWorkspaceWideAccess(call.Name) {
		if forbidden := e.firstForbiddenPath(active); forbidden != "" {
			result := workbenchtools.FailWithHint("forbidden_scope", "tool has workspace-wide access while user forbids path: "+forbidden, "use a path-scoped tool such as read_file, or ask the user to lift the path restriction")
			e.publishOrLog(ctx, run, domain.EventToolFinished, "agent", map[string]any{"tool": call.Name, "durationMs": 0, "result": result})
			return result, nil
		}
	}
	tool, ok := registry.Get(call.Name)
	if !ok || !policy.ProfileGrants(profile).Allows(call.Name) {
		hint := remapHint
		if hint == "" {
			hint = unknownToolHint(originalName, profile.AllowedTools)
		}
		return workbenchtools.FailWithHint("tool_not_allowed", "tool is not enabled for this agent", hint), nil
	}
	if validator, ok := tool.(workbenchtools.ArgumentValidator); ok {
		if invalid := validator.ValidateArguments(call.Arguments); invalid != nil {
			e.publishOrLog(ctx, run, domain.EventToolFinished, "agent", map[string]any{"tool": call.Name, "result": invalid})
			return *invalid, nil
		}
	}
	e.update(active, func(r *domain.Run) {
		if !slices.Contains(r.ToolsUsed, call.Name) {
			r.ToolsUsed = append(r.ToolsUsed, call.Name)
		}
	})
	decision := e.policy.Evaluate(profile, call.Name)
	autoApproved := e.taskAutoApproved(active, profile, call.Name)
	if autoApproved && !decision.Denied {
		decision.RequiresApproval = false
		decision.Reason = "Разрешено утверждённым заданием внутри Docker sandbox"
	}
	if decision.Denied {
		return workbenchtools.Fail("tool_denied", decision.Reason), nil
	}
	if call.Name == "propose_patch" {
		inspection, requirement := observations.CheckPatch(patches.FS, call.Arguments, e.currentWorkspaceRevision(active), run.Step)
		if requirement != nil {
			return e.rejectPatchInspection(ctx, active, call.Name, *requirement, 0), nil
		}
		if err := e.publish(ctx, e.snapshot(active), domain.EventToolStarted, "agent", map[string]any{"tool": call.Name, "callId": call.ID}); err != nil {
			return domain.ToolResult{}, fmt.Errorf("%w: start was not persisted; tool was not executed: %v", errToolJournalIntegrity, err)
		}
		started := time.Now()
		result := tool.Execute(ctx, call.Arguments)
		durationMs := time.Since(started).Milliseconds()
		if !result.OK {
			e.publishOrLog(ctx, e.snapshot(active), domain.EventToolFinished, "agent", map[string]any{"tool": call.Name, "durationMs": durationMs, "result": result})
			return result, nil
		}
		var proposal domain.PatchProposal
		if err := json.Unmarshal(result.Output, &proposal); err != nil {
			failed := workbenchtools.Fail("invalid_tool_output", "the patch tool returned an invalid proposal")
			e.publishOrLog(ctx, e.snapshot(active), domain.EventToolFinished, "agent", map[string]any{"tool": call.Name, "durationMs": durationMs, "result": failed})
			return failed, err
		}
		if !patchInspectionMatches(inspection, proposal) {
			if _, rejectErr := patches.Reject(proposal.ID); rejectErr != nil {
				slog.Warn("stale patch proposal not rejected", "run_id", active.run.ID, "proposal_id", proposal.ID, "path", inspection.Path, "error", rejectErr)
			}
			requirement = &patchInspectionRequirement{
				Code: "inspection_stale", Path: inspection.Path, RequiredTool: "read_file",
				WorkspaceRevision: e.currentWorkspaceRevision(active),
				Message:           "the file changed between inspection and patch preparation; read the complete current file again before proposing a patch",
			}
			return e.rejectPatchInspection(ctx, active, call.Name, *requirement, durationMs), nil
		}
		e.publishOrLog(ctx, e.snapshot(active), domain.EventToolFinished, "agent", map[string]any{"tool": call.Name, "durationMs": durationMs, "result": result})
		approval := e.newApproval(e.snapshot(active), call, decision.Reason, call.Arguments)
		attached, err := patches.Attach(proposal.ID, run.ID, approval.ID)
		if err != nil {
			return result, err
		}
		proposal = *attached
		if err := e.savePatch(proposal); err != nil {
			return result, fmt.Errorf("persist proposed patch: %w", err)
		}
		e.publishOrLog(ctx, e.snapshot(active), domain.EventPatchProposed, "agent", safePatchPayload(proposal))
		allow := autoApproved
		if !autoApproved {
			allow, err = e.awaitApproval(ctx, active, approval)
		} else {
			approval.Status = domain.ApprovalAllowed
			if err = e.repo.SaveApproval(ctx, approval); err != nil {
				return result, fmt.Errorf("persist task approval: %w", err)
			}
			e.publishOrLog(ctx, run, domain.EventApprovalResolved, "task", map[string]any{"approvalId": approval.ID, "status": approval.Status, "briefVersion": active.taskBrief.Version})
		}
		if err != nil {
			return result, err
		}
		if !allow {
			rejected, rejectErr := patches.Reject(proposal.ID)
			if rejectErr == nil {
				if saveErr := e.savePatch(*rejected); saveErr != nil {
					slog.Error("persist rejected patch failed", "patch_id", rejected.ID, "error", saveErr)
				}
				e.publishOrLog(context.Background(), e.snapshot(active), domain.EventPatchRejected, "user", safePatchPayload(*rejected))
			}
			return workbenchtools.Fail("patch_rejected", "user rejected the proposed patch"), nil
		}
		applied, err := patches.Apply(proposal.ID)
		if err != nil {
			return workbenchtools.Fail("patch_conflict", err.Error()), nil
		}
		e.update(active, func(r *domain.Run) {
			if !slices.Contains(r.ChangedFiles, applied.Path) {
				r.ChangedFiles = append(r.ChangedFiles, applied.Path)
			}
		})
		e.bumpWorkspaceRevision(active)
		if err := e.savePatch(*applied); err != nil {
			return domain.ToolResult{}, fmt.Errorf("%w: unknown_outcome: patch applied but its state was not persisted: %v", errToolJournalIntegrity, err)
		}
		actor := "user"
		if autoApproved {
			actor = "task"
		}
		if err := e.publish(context.Background(), e.snapshot(active), domain.EventPatchApplied, actor, safePatchPayload(*applied)); err != nil {
			return domain.ToolResult{}, fmt.Errorf("%w: unknown_outcome: patch applied but its event was not persisted: %v", errToolJournalIntegrity, err)
		}
		return workbenchtools.OK(map[string]any{"status": "applied", "path": applied.Path}), nil
	}
	approvalID := ""
	if decision.RequiresApproval {
		arguments := call.Arguments
		if previewer, ok := tool.(workbenchtools.ApprovalPreviewer); ok {
			arguments = previewer.ApprovalArguments(call.Arguments)
		}
		approval := e.newApproval(run, call, decision.Reason, arguments)
		approvalID = approval.ID
		allow, err := e.awaitApproval(ctx, active, approval)
		if err != nil {
			return domain.ToolResult{}, err
		}
		if !allow {
			return workbenchtools.Fail("approval_denied", "user denied this tool call"), nil
		}
	}
	auditedExecutable := call.Name == "run_command" || strings.HasPrefix(call.Name, "customtool_")
	var before workspace.TextSnapshot
	if auditedExecutable {
		var snapshotErr error
		before, snapshotErr = patches.FS.CaptureTextSnapshot(ctx)
		if snapshotErr != nil {
			result := workbenchtools.Fail("workspace_audit_failed", "could not capture the workspace before executing the approved tool: "+snapshotErr.Error())
			e.publishOrLog(context.Background(), e.snapshot(active), domain.EventToolFinished, "agent", map[string]any{"tool": call.Name, "durationMs": 0, "result": result})
			return result, nil
		}
	}
	if err := e.publish(ctx, e.snapshot(active), domain.EventToolStarted, "agent", map[string]any{"tool": call.Name, "callId": call.ID}); err != nil {
		return domain.ToolResult{}, fmt.Errorf("%w: start was not persisted; tool was not executed: %v", errToolJournalIntegrity, err)
	}
	started := time.Now()
	result := tool.Execute(ctx, call.Arguments)
	durationMs := time.Since(started).Milliseconds()
	errCode, errMsg := "", ""
	if result.Error != nil {
		errCode = result.Error.Code
		errMsg = result.Error.Message
	}
	observability.From(ctx).Info("agent tool finished",
		"run_id", run.ID,
		"tool", call.Name,
		"ok", result.OK,
		"error_code", errCode,
		"duration_ms", durationMs,
		"output_bytes", len(result.Output),
		"output_preview", observability.Snippet(security.Redact(string(result.Output)), 240),
		"error_message", observability.Snippet(security.Redact(errMsg), 240),
	)
	var integrityErr error
	if auditedExecutable {
		auditCtx, cancelAudit := context.WithTimeout(context.Background(), 15*time.Second)
		after, auditErr := patches.FS.CaptureTextSnapshot(auditCtx)
		cancelAudit()
		if auditErr != nil {
			summary := workspaceAuditSummary{Tool: call.Name, ApprovalID: approvalID, SnapshotComplete: false}
			e.publishOrLog(context.Background(), e.snapshot(active), domain.EventWorkspaceChanged, "agent", summary)
			result = attachWorkspaceAudit(workbenchtools.Fail("workspace_audit_failed", "the tool ran, but Point could not capture the resulting workspace safely: "+auditErr.Error()), summary)
			integrityErr = fmt.Errorf("%w: could not capture the resulting workspace: %v", errWorkspaceAuditIntegrity, auditErr)
		} else {
			summary, recordErr := e.recordExecutableChanges(active, patches, call.Name, approvalID, before, after)
			result = attachWorkspaceAudit(result, summary)
			if recordErr != nil {
				result = attachWorkspaceAudit(workbenchtools.Fail("workspace_audit_failed", "the tool ran, but Point could not persist its file-change history: "+recordErr.Error()), summary)
				integrityErr = fmt.Errorf("%w: could not persist file-change history: %v", errWorkspaceAuditIntegrity, recordErr)
			}
		}
	}
	if err := e.publish(context.Background(), e.snapshot(active), domain.EventToolFinished, "agent", map[string]any{"tool": call.Name, "callId": call.ID, "durationMs": durationMs, "result": result}); err != nil {
		return result, fmt.Errorf("%w: unknown_outcome: tool ran but its result was not persisted: %v", errToolJournalIntegrity, err)
	}
	return result, integrityErr
}

func (e *Engine) newApproval(run domain.Run, call providers.ToolCall, reason string, arguments json.RawMessage) domain.Approval {
	safe := json.RawMessage(security.Redact(string(arguments)))
	if !json.Valid(safe) {
		safe = json.RawMessage(`{}`)
	}
	return domain.Approval{ID: domain.NewID("approval"), RunID: run.ID, AgentID: run.AgentID, ToolName: call.Name, Reason: reason, Arguments: safe, Status: domain.ApprovalPending, CreatedAt: time.Now().UTC()}
}

// Решение по запросу разрешения хранится только в записи апрува: очередь
// решений и восстановление после рестарта читают статус оттуда. Незаписанное
// решение оставляет запрос вечно ждущим человека, который уже ответил.
func (e *Engine) saveApprovalState(approval domain.Approval, stage string) {
	if err := e.repo.SaveApproval(context.Background(), approval); err != nil {
		slog.Warn("approval state not persisted", "approval_id", approval.ID, "run_id", approval.RunID, "tool", approval.ToolName, "stage", stage, "status", approval.Status, "error", err)
	}
}

func (e *Engine) awaitApproval(ctx context.Context, active *activeRun, approval domain.Approval) (bool, error) {
	active.clock.stop()
	e.broker.Register(approval)
	e.update(active, func(r *domain.Run) { r.Status = domain.RunWaiting })
	e.saveApprovalState(approval, "requested")
	if err := e.saveRun(e.snapshot(active)); err != nil {
		return false, fmt.Errorf("persist run state before approval: %w", err)
	}
	e.publishOrLog(ctx, e.snapshot(active), domain.EventApprovalRequested, "agent", approval)
	allow, err := e.broker.Await(ctx, approval.ID)
	now := time.Now().UTC()
	approval.ResolvedAt = &now
	if err != nil {
		approval.Status = domain.ApprovalDenied
	} else if allow {
		approval.Status = domain.ApprovalAllowed
	} else {
		approval.Status = domain.ApprovalDenied
	}
	e.saveApprovalState(approval, "resolved")
	e.update(active, func(r *domain.Run) { r.Status = domain.RunRunning })
	if saveErr := e.saveRun(e.snapshot(active)); saveErr != nil {
		return allow, fmt.Errorf("persist run state after approval: %w", saveErr)
	}
	e.publishOrLog(context.Background(), e.snapshot(active), domain.EventApprovalResolved, "user", map[string]any{"approvalId": approval.ID, "status": approval.Status})
	active.clock.start()
	return allow, err
}

func (e *Engine) ResolveApproval(id string, allow bool) error { return e.broker.Resolve(id, allow) }

func (e *Engine) PendingApprovals(runID string) []domain.Approval { return e.broker.Pending(runID) }

var errToolJournalIntegrity = errors.New("tool_journal_integrity")
