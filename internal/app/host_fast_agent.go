package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/attachments"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
	"strings"
	"time"
)

func (a *App) startHostFastAgent(ctx context.Context, r FastAgentRequest) (domain.Run, error) {
	a.hostLaunchMu.Lock()
	defer a.hostLaunchMu.Unlock()
	ctx, err := a.WithMasterWorkspace(ctx, r.WorkspaceID)
	if err != nil {
		return domain.Run{}, err
	}
	scope := ctx.Value(masterScopeKey{}).(masterScope)
	r.Task = strings.TrimSpace(r.Task)
	if r.Task == "" || len(r.Task) > 64*1024 {
		return domain.Run{}, errors.New("task is required and must fit 64 KiB")
	}
	if r.PreflightFingerprint != "" {
		return domain.Run{}, errors.New("legacy preflight fingerprints cannot authorize a host_live launch; refresh the Fast Agent request")
	}
	if r.RequestID == "" {
		r.RequestID = domain.NewID("fast-request")
	}
	if len(r.RequestID) > 128 {
		return domain.Run{}, errors.New("requestId exceeds 128 bytes")
	}
	identity, _ := json.Marshal(struct {
		Workspace, Conversation, Task string
		Context                       []domain.RunContextInput
	}{scope.Workspace.ID, r.ConversationID, r.Task, r.ContextItems})
	sum := sha256.Sum256(identity)
	hash := hex.EncodeToString(sum[:])
	key := scope.Workspace.ID + ":" + r.RequestID
	if replay, ok, e := a.store.HostFastReplay(ctx, key, hash); e != nil || ok {
		return replay, e
	}
	if r.ConversationID != "" {
		items, e := a.store.MasterConversations(ctx, scope.Workspace.ID)
		if e != nil {
			return domain.Run{}, e
		}
		found := false
		for _, item := range items {
			if item.ID == r.ConversationID {
				found = true
			}
		}
		if !found {
			return domain.Run{}, errors.New("conversation belongs to a different workspace")
		}
	}
	config, err := a.FastAgentConfig(ctx)
	if err != nil {
		return domain.Run{}, err
	}
	p := config.Profile
	if p.ConnectionID == "" || p.Model == "" {
		return domain.Run{}, errors.New("configure the global Fast Agent connection and model")
	}
	if err = a.applyConnectionEndpoint(&p); err != nil {
		return domain.Run{}, err
	}
	normalizeRuntimeProfileDefaults(&p)
	p.ID = domain.SystemFastAgentID
	p.ExecutionMode = "host_live"
	p.SkillCatalog, err = a.availableRuntimeSkills(ctx, scope.Workspace.ID, p)
	if err != nil {
		return domain.Run{}, err
	}
	p.EquippedSkills, err = resolveEquippedSkills(a.store, scope.Workspace.ID, domain.ProjectAgent{SkillIDs: config.SkillIDs}, p)
	if err != nil {
		return domain.Run{}, err
	}
	rules := a.masterScopedRules(ctx)
	if rules.Text != "" {
		p.Rules = append(p.Rules, security.Redact(rules.Text))
	}
	preview, err := attachments.Resolve(scope.FS, r.ContextItems)
	if err != nil {
		return domain.Run{}, err
	}
	order := domain.NormalizeWorkOrder(domain.WorkOrder{
		State: "ready", Goal: r.Task, Scope: []string{"Requested local task"}, Criteria: []domain.AcceptanceCriterion{{ID: "done", Text: "Requested changes are present; report checks and incomplete work", Kind: "manual"}},
		WorkspaceID: scope.Workspace.ID, Workspace: domain.WorkspacePlan{Mode: "existing", Path: scope.Workspace.Path, Isolation: "host_live"}, ConversationID: r.ConversationID,
		Stack: domain.StackPresetRef{ID: "host", Version: "1", Category: "general", Source: "benchmark"}, Roster: domain.AgentRosterPlan{Permanent: []domain.AgentDraft{{ID: domain.SystemFastAgentID, Existing: true}}},
		Routing:    domain.ModelRoutingPolicy{Mode: "fixed", FixedConnectionID: p.ConnectionID, FixedModel: p.Model, FallbackMode: "wait", Certification: "experimental", Adapter: domain.AdapterCapabilityManifest{Tools: true, StructuredOutput: true, ContextTokens: p.ContextWindowTokens}},
		Budget:     domain.BudgetEnvelope{Preset: "medium", Tokens: int64(config.Tokens), ActiveSeconds: config.ActiveSeconds, MaxParallel: 1, MaxReplans: 2, MaxAttempts: 3, MaxSteps: p.MaxSteps, MaxProjectAgents: 1},
		Completion: domain.CompletionProfile{ID: "host-fast", Version: "1", Checks: []domain.CompletionCheck{{Kind: domain.CompletionCheckAcceptance}}}, Delivery: domain.DeliveryPolicy{ApplyMode: "automatic", CommitMode: "none", KeepPartialDays: 30},
	})
	snapshots, refs := fastAgentSourceSnapshotsV2(scope.Workspace.ID, preview.Items)
	order.Sources = refs
	b, err := taskBriefFromWorkOrderV2(order)
	if err != nil {
		return domain.Run{}, err
	}
	b.FastAgent = true
	b, err = domain.ApproveTaskBrief(b)
	if err != nil {
		return domain.Run{}, err
	}
	prepared := preparedFastAgentV2{run: preparedAgentRun{workspace: scope.Workspace, fs: scope.FS, profile: p, projectAgentID: domain.SystemFastAgentID, task: r.Task, context: preview}, order: order, brief: b, snapshots: snapshots}
	launch, err := a.prepareFastAgentLaunchCommitV2(ctx, prepared, order)
	if err != nil {
		return domain.Run{}, err
	}
	launch.HostRequestKey, launch.HostFingerprint = key, hash
	launch.HostRecordUserMessage = !r.FromMaster
	launch.Run.ConfigurationSnapshot = launch.Run.ConfigurationSnapshot.WithTargetWorkspace(scope.Workspace)
	launch.Execution.Snapshot = launch.Run.ConfigurationSnapshot
	unlock, err := a.hostWriterLock(ctx, scope.Workspace)
	if err != nil {
		return domain.Run{}, err
	}
	handedOff := false
	defer func() {
		if !handedOff {
			unlock()
		}
	}()
	approval, err := a.store.CommitFastAgentLaunchV2(ctx, launch)
	if err != nil {
		return domain.Run{}, err
	}
	run, err := a.engine.Start(agent.StartInput{Configuration: launch.Run.ConfigurationSnapshot, TaskBrief: &b, Workspace: scope.Workspace, Task: r.Task, APIKey: r.APIKey, ContextItems: preview.Items, RunID: launch.Run.ID, AgentID: launch.Run.AgentID, StartedAt: launch.Run.StartedAt, InitialBudgetReservationID: launch.Reservation.ID, ExecutionID: launch.Execution.ID, QuestID: approval.QuestID,
		OnFinished: func(f domain.Run) {
			defer unlock()
			a.finishHostFastAgent(context.WithoutCancel(ctx), approval, launch.Execution, f, r.ConversationID)
		},
	})
	if err == nil {
		handedOff = true
	}
	if err != nil {
		f := launch.Run
		f.Status = domain.RunFailed
		f.Error = err.Error()
		now := time.Now().UTC()
		f.FinishedAt = &now
		_ = a.store.SaveRun(ctx, f)
		a.finishHostFastAgent(ctx, approval, launch.Execution, f, r.ConversationID)
	}
	return run, err
}

func (a *App) finishHostFastAgent(ctx context.Context, approval domain.WorkOrderApproval, execution domain.ExecutionInstance, run domain.Run, conversation string) {
	execution.Status, execution.Result, execution.Error, execution.FinishedAt, execution.DurationMs = run.Status, run.Result, run.Error, run.FinishedAt, run.DurationMs
	_ = a.store.SaveExecution(ctx, execution)
	_ = a.store.ReleaseBudgetReservations(ctx, run.ID, time.Now().UTC())
	_ = a.store.ReleaseWriterLeaseV2(ctx, approval.QuestID)
	q, err := a.store.GetQuest(ctx, approval.QuestID)
	if err != nil {
		return
	}
	q.Status = domain.QuestBlocked
	if run.Status == domain.RunCompleted {
		q.Status = domain.QuestCompleted
	}
	q.UpdatedAt = time.Now().UTC()
	q.FinishedAt = run.FinishedAt
	_ = a.store.SaveQuest(ctx, q)
	_ = a.markFastAgentMilestoneV2(ctx, approval, q.Status)
	if conversation != "" {
		text := fmt.Sprintf("Fast Agent: %s\n%s\nИзменённые файлы: %s", run.Status, run.Result, strings.Join(run.ChangedFiles, ", "))
		text += a.hostCheckSummary(ctx, run.ID)
		if run.Error != "" {
			text += "\nПричина остановки: " + run.Error
		}
		_ = a.store.SaveCompanionMessage(ctx, domain.CompanionMessage{ID: "host-result-" + run.ID, WorkspaceID: run.WorkspaceID, ConversationID: conversation, Speaker: "master", Role: "assistant", Content: security.Redact(text), CreatedAt: time.Now().UTC()})
	}
}
