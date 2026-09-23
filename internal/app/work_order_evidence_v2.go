package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/changesets"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
)

// errWorkOrderDeliveryConflictV2 separates "somebody else changed the project"
// from a broken quest: the transfer is rolled back whole either way, but only
// this class of failure hands verified work to a human instead of blocking it.
var errWorkOrderDeliveryConflictV2 = errors.New("delivery conflicts with external workspace changes")

// finalizeWorkOrderQuestAfterFlowV2 is the sole Flow terminal path for an
// approved WorkOrder. It may produce completed only through the storage
// evidence gate; failed or incomplete proof is retained as blocked.
func (a *App) finalizeWorkOrderQuestAfterFlowV2(approval domain.WorkOrderApproval, flowSucceeded bool) {
	ctx := context.Background()
	quest, err := a.workOrderQuestV2(ctx, approval.WorkOrder.WorkspaceID, approval.QuestID)
	if err != nil {
		slog.Error("work order evidence finalizer could not load quest", "quest_id", approval.QuestID, "error", security.Redact(err.Error()))
		if releaseErr := a.store.ReleaseWriterLeaseV2(ctx, approval.QuestID); releaseErr != nil {
			slog.Error("work order writer lease was not released", "quest_id", approval.QuestID, "error", security.Redact(releaseErr.Error()))
		}
		return
	}
	if quest.Status == domain.QuestRunning {
		quest, err = a.setWorkOrderQuestStatusV2(ctx, quest, domain.QuestVerifying, "Проверяем критерии на итоговой ревизии")
		if err != nil {
			a.blockWorkOrderFinalizationV2(ctx, quest, "Не удалось начать проверку WorkOrder: "+security.Redact(err.Error()), err)
			return
		}
	}
	bundle := a.buildWorkOrderEvidenceV2(ctx, approval, quest, flowSucceeded)
	machineReady := workOrderMachineEvidenceSatisfiedV2(approval.WorkOrder, bundle)
	if flowSucceeded && machineReady && approval.WorkOrder.Delivery.ApplyMode == "automatic" {
		if quest.Status == domain.QuestVerifying {
			quest, err = a.setWorkOrderQuestStatusV2(ctx, quest, domain.QuestApplying, "Проверки пройдены; переносим результат в проект")
			if err != nil {
				a.blockWorkOrderFinalizationV2(ctx, quest, "Не удалось начать доставку WorkOrder: "+security.Redact(err.Error()), err)
				return
			}
		}
		applied, commits, applyErr := a.applyWorkOrderChangeSetsV2(ctx, approval.WorkOrder, quest, bundle.ID)
		bundle.ChangedFiles = append(bundle.ChangedFiles, applied...)
		bundle.CommitIDs = append(bundle.CommitIDs, commits...)
		bundle.ChangedFiles = uniqueSortedStringsV2(bundle.ChangedFiles)
		if applyErr != nil {
			bundle.DeliveryConflict = errors.Is(applyErr, errWorkOrderDeliveryConflictV2)
			bundle.KnownLimitations = append(bundle.KnownLimitations, "Автоматический перенос не завершён: "+security.Redact(applyErr.Error()))
		} else {
			// The profile runs on the delivered revision: build, tests and a
			// started service prove the result the user will actually open,
			// not the sandbox copy that produced it.
			profile := a.runCompletionProfileV2(ctx, approval.WorkOrder, approval.WorkOrder.Workspace.Path)
			bundle.VerificationChecks = append(bundle.VerificationChecks, profile...)
			// The result stays in the project even when its own checks fail —
			// deleting hours of work would be worse — so the card has to say
			// plainly what the user is now looking at.
			if failed := failedCompletionCheckKindsV2(profile); len(failed) > 0 {
				bundle.KnownLimitations = append(bundle.KnownLimitations,
					"Результат перенесён в проект, но не прошёл проверки профиля: "+strings.Join(failed, ", "))
			}
			bundle.DeliveryVerified = true
		}
	} else if flowSucceeded && machineReady && approval.WorkOrder.Delivery.ApplyMode == "manual" {
		// In professional mode delivery means a verified isolated result is
		// available for review; it has deliberately not touched the workspace.
		bundle.DeliveryVerified = true
		bundle.DeliveryTarget = "isolated_review"
	}
	if bundle.DeliveryTarget == "" {
		bundle.DeliveryTarget = approval.WorkOrder.Workspace.Path
	}
	bundle.WorkspaceRevision = workOrderRevisionV2(approval.WorkOrder, quest, bundle)
	if approval.WorkOrder.Delivery.CommitMode == "squash" && len(bundle.CommitIDs) == 0 {
		bundle.DeliveryVerified = false
		bundle.KnownLimitations = append(bundle.KnownLimitations, "Итоговый commit с Quest ID не создан")
	}
	if bundle.DeliveryVerified {
		bundle.DeliveryReceipt = &domain.DeliveryReceipt{
			ID: domain.NewID("delivery"), QuestID: quest.ID, WorkOrderDigest: bundle.BriefDigest,
			Target: bundle.DeliveryTarget, WorkspaceRevision: bundle.WorkspaceRevision,
			URL: approval.WorkOrder.Delivery.ApplicationURL,
			// Running is a fact about containers, not a line in the policy:
			// the receipt may claim it only when the approved service check
			// actually started them.
			ServicesRunning: approval.WorkOrder.Delivery.KeepServicesRunning &&
				orderRequiresCompletionCheckV2(approval.WorkOrder, "service_start") &&
				completionCheckSatisfiedV2(bundle.VerificationChecks, "service_start"),
			DeliveredAt: time.Now().UTC(),
		}
		bundle.DeliveryReceipt.ComposeFile, _ = discoverComposeFileV2(bundle.DeliveryTarget)
		if len(bundle.CommitIDs) > 0 {
			bundle.DeliveryReceipt.CommitID = bundle.CommitIDs[len(bundle.CommitIDs)-1]
		}
	}
	status, gateErr := a.store.FinalizeWorkOrderQuestV2(ctx, quest.ID, bundle)
	if gateErr != nil {
		a.blockWorkOrderFinalizationV2(ctx, quest, "Evidence gate не принял итог: "+security.Redact(gateErr.Error()), gateErr)
		bundle.Assurance = domain.WorkOrderAssuranceFailed
		bundle.OutcomeSummary = "Финализация заблокирована: " + security.Redact(gateErr.Error())
		a.publishWorkOrderOutcomeV2(ctx, approval, quest, domain.QuestBlocked, bundle)
		if isFastAgentQuestV2(quest) {
			_ = a.markFastAgentMilestoneV2(ctx, approval, domain.QuestBlocked)
		}
		a.queueQuestSubagentEvaluations(quest.ID)
		return
	}
	if stored, evidenceErr := a.store.GetEvidenceBundle(ctx, quest.ID); evidenceErr == nil {
		bundle = stored
	}
	a.publishWorkOrderOutcomeV2(ctx, approval, quest, status, bundle)
	a.recordMasterEvidence(ctx, approval.WorkOrder, quest.ID, "evidence_gate", string(status))
	if bundle.Assurance == domain.WorkOrderAssuranceVerified {
		a.queueQuestSubagentEvaluations(quest.ID)
	}
	launchMode, _ := quest.Controller["launchMode"].(string)
	slog.Info("work order evidence finalized", "launch_mode", launchMode, "work_order_id", approval.WorkOrder.ID,
		"quest_id", quest.ID, "run_id", a.workOrderRunIDV2(ctx, quest), "evidence_gate", status,
		"status", status, "flow_succeeded", flowSucceeded, "delivery_verified", bundle.DeliveryVerified)
}

func (a *App) publishWorkOrderOutcomeV2(ctx context.Context, approval domain.WorkOrderApproval, quest domain.Quest, status domain.QuestStatus, bundle domain.EvidenceBundle) {
	title := "Заблокировано"
	level := "error"
	switch {
	case status == domain.QuestCompleted && bundle.Assurance == domain.WorkOrderAssuranceVerified:
		title, level = "Готово", "success"
	case status == domain.QuestCompleted:
		title, level = "Готово с ограничениями", "warning"
	}
	passed, unavailable, failed := []string{}, []string{}, []string{}
	for _, check := range bundle.VerificationChecks {
		name := strings.TrimSpace(check.Kind)
		if name == "" {
			name = check.ID
		}
		switch {
		case check.Satisfied:
			passed = append(passed, name)
		case check.ExitCode == nil:
			unavailable = append(unavailable, name)
		default:
			failed = append(failed, name)
		}
	}
	for _, criterion := range approval.WorkOrder.Criteria {
		if criterion.Kind == "manual" {
			unavailable = append(unavailable, criterion.Text)
		}
	}
	line := func(label string, values []string) string {
		if len(values) == 0 {
			return label + ": нет"
		}
		return label + ": " + strings.Join(uniqueSortedStringsV2(values), ", ")
	}
	files := uniqueSortedStringsV2(bundle.ChangedFiles)
	content := []string{
		title,
		fmt.Sprintf("Изменено файлов: %d", len(files)),
		line("Файлы", files),
		line("Выполненные проверки", passed),
		line("Невыполненные/ручные проверки", unavailable),
		line("Проваленные проверки", failed),
		line("Ограничения", bundle.KnownLimitations),
	}
	if bundle.DeliveryReceipt != nil && bundle.DeliveryReceipt.ServicesRunning && strings.TrimSpace(bundle.DeliveryReceipt.URL) != "" {
		content = append(content, "Приложение: "+bundle.DeliveryReceipt.URL)
	}
	if status == domain.QuestBlocked {
		content = append(content, "Нужно действие: устраните указанную ошибку и повторите запуск квеста.")
	} else if bundle.Assurance == domain.WorkOrderAssurancePartial && len(unavailable) > 0 {
		content = append(content, "Нужно действие: при необходимости выполните перечисленные ручные проверки.")
	}
	_, err := a.store.SaveCompanionMessageOnce(ctx, domain.CompanionMessage{
		ID: "master-workorder-final-" + quest.ID, WorkspaceID: approval.WorkOrder.WorkspaceID,
		ConversationID: approval.WorkOrder.ConversationID, Speaker: "master", Role: "assistant",
		Content: strings.Join(content, "\n"), Level: level, Mode: "quest_completion",
		ProposalID: approval.WorkOrder.ProposalID, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		slog.Error("could not publish deterministic work order outcome", "quest_id", quest.ID, "error", security.Redact(err.Error()))
	}
}

func isFastAgentQuestV2(quest domain.Quest) bool {
	mode, _ := quest.Controller["launchMode"].(string)
	return strings.EqualFold(strings.TrimSpace(mode), "fast_agent_v2")
}

func (a *App) buildWorkOrderEvidenceV2(ctx context.Context, approval domain.WorkOrderApproval, quest domain.Quest, flowSucceeded bool) domain.EvidenceBundle {
	order := approval.WorkOrder
	bundle := domain.EvidenceBundle{
		Version: domain.CurrentWorkOrderEvidenceVersion, ID: domain.NewID("evidence"), QuestID: quest.ID, PointVersion: Version,
		SourceDigest: domain.WorkOrderSourceDigest(order), BriefDigest: domain.WorkOrderDigest(order),
		EnvironmentDigest:  workOrderEnvironmentDigestV2(order),
		SourceVersions:     append([]domain.SourceSnapshotRef(nil), order.Sources...),
		StackPreset:        order.Stack,
		DockerImages:       []string{},
		Criteria:           make([]domain.CriterionEvidence, 0, len(order.Criteria)),
		VerificationChecks: []domain.VerificationCheck{},
		ModelCalls:         []domain.ModelCallLedgerEntry{},
		ContextDisclosures: []domain.ContextDisclosureEntry{},
		CreatedAt:          time.Now().UTC(),
	}
	if note, _ := quest.Controller["plannerNote"].(string); strings.TrimSpace(note) != "" {
		bundle.KnownLimitations = append(bundle.KnownLimitations, strings.TrimSpace(note))
	}
	if len(order.Network) > 0 {
		raw, _ := json.Marshal(order.Network)
		sum := sha256.Sum256(raw)
		bundle.NetworkPolicyDigest = "sha256:" + hex.EncodeToString(sum[:])
	}
	outcome, outcomeErr := a.QuestOutcome(ctx, quest.ID)
	executionFailures := a.workOrderExecutionFailuresV2(ctx, order.WorkspaceID, quest.FlowRunID)
	proof := map[string]struct {
		satisfied bool
		summary   string
	}{}
	proofChecks := map[string]domain.VerificationCheck{}
	if outcomeErr == nil {
		for index, criterion := range order.Criteria {
			if index < len(outcome.Promises) {
				proof[criterion.ID] = struct {
					satisfied bool
					summary   string
				}{outcome.Promises[index].Met, outcome.Promises[index].Evidence}
			}
		}
		if outcome.Evidence != nil {
			for _, criterion := range outcome.Evidence.Criteria {
				if criterion.Check == nil {
					continue
				}
				var arguments struct {
					Command string `json:"command"`
				}
				_ = json.Unmarshal(criterion.Check.Arguments, &arguments)
				proofChecks[criterion.CriterionID] = domain.VerificationCheck{
					ID: criterion.CriterionID, Kind: workOrderVerificationKindV2(criterion.CriterionID, order.Criteria),
					Command: strings.TrimSpace(arguments.Command), ExitCode: criterion.Check.ExitCode,
					Satisfied: criterion.Status == "satisfied" && criterion.Check.Status == "passed" && !criterion.Check.TimedOut,
					Summary:   security.Redact(criterion.Check.Detail),
				}
			}
		}
		bundle.ChangedFiles = append(bundle.ChangedFiles, outcome.AppliedFiles...)
		if !outcome.Verified && outcome.Honest != "" {
			bundle.KnownLimitations = append(bundle.KnownLimitations, outcome.Honest)
		}
	} else if len(executionFailures) == 0 {
		bundle.KnownLimitations = append(bundle.KnownLimitations, "Невозможно собрать итог проверки: "+security.Redact(outcomeErr.Error()))
	}
	for _, criterion := range order.Criteria {
		item := proof[criterion.ID]
		ev := domain.CriterionEvidence{CriterionID: criterion.ID, Satisfied: item.satisfied, Tool: criterion.Tool, Summary: item.summary}
		if criterion.Kind == "manual" {
			ev.Satisfied, ev.Summary = false, "Требуется ручная приёмка"
		}
		if !flowSucceeded && criterion.Kind != "manual" {
			ev.Satisfied = false
		}
		if criterion.Tool == "run_command" && len(criterion.Arguments) > 0 {
			var args struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(criterion.Arguments, &args) == nil && strings.TrimSpace(args.Command) != "" {
				ev.Command = args.Command
				bundle.ReproductionCommands = append(bundle.ReproductionCommands, args.Command)
			}
		}
		bundle.Criteria = append(bundle.Criteria, ev)
		if criterion.Kind != "manual" {
			check, ok := proofChecks[criterion.ID]
			if !ok {
				check = domain.VerificationCheck{ID: criterion.ID, Kind: workOrderVerificationKindV2(criterion.ID, order.Criteria), Command: ev.Command, Satisfied: false, Summary: "Нет привязанного результата проверки"}
			}
			if check.Command == "" {
				check.Command = ev.Command
			}
			if ok {
				bundle.Criteria[len(bundle.Criteria)-1].ExitCode = check.ExitCode
			}
			bundle.VerificationChecks = append(bundle.VerificationChecks, check)
		}
	}
	if !flowSucceeded {
		bundle.KnownLimitations = append(bundle.KnownLimitations, "Flow завершился с ошибкой")
		bundle.KnownLimitations = append(bundle.KnownLimitations, executionFailures...)
	}
	a.collectWorkOrderEvidenceLedgersV2(ctx, order, quest, &bundle)
	return bundle
}

func (a *App) workOrderExecutionFailuresV2(ctx context.Context, workspaceID, flowRunID string) []string {
	if strings.TrimSpace(flowRunID) == "" {
		return nil
	}
	executions, err := a.store.ListExecutions(ctx, workspaceID, 500)
	if err != nil {
		return nil
	}
	result := []string{}
	for _, execution := range executions {
		if execution.FlowRunID != flowRunID || execution.Status != domain.RunFailed {
			continue
		}
		detail := strings.TrimSpace(execution.Error)
		if detail == "" {
			detail = strings.TrimSpace(execution.Result)
		}
		if detail == "" {
			detail = "этап завершился с ошибкой без диагностического сообщения"
		}
		result = append(result, "Ошибка этапа "+execution.FlowNodeID+": "+security.Redact(detail))
	}
	return uniqueSortedStringsV2(result)
}

func workOrderMachineEvidenceSatisfiedV2(order domain.WorkOrder, bundle domain.EvidenceBundle) bool {
	checks := make(map[string]domain.VerificationCheck, len(bundle.VerificationChecks))
	for _, check := range bundle.VerificationChecks {
		checks[check.ID] = check
	}
	criteria := make(map[string]domain.CriterionEvidence, len(bundle.Criteria))
	for _, item := range bundle.Criteria {
		criteria[item.CriterionID] = item
	}
	for _, criterion := range order.Criteria {
		if criterion.Kind == "manual" {
			continue
		}
		item, itemOK := criteria[criterion.ID]
		check, checkOK := checks[criterion.ID]
		// No exit code means the check was unavailable or requires a human. It
		// lowers assurance but must not prevent delivery. A check that really
		// ran and failed remains a hard stop.
		if itemOK && item.ExitCode != nil || checkOK && check.ExitCode != nil {
			if !verificationCheckSatisfiesCriterionV2(criterion, item, check) {
				return false
			}
		}
	}
	return true
}

func verificationCheckSatisfiesCriterionV2(criterion domain.AcceptanceCriterion, item domain.CriterionEvidence, check domain.VerificationCheck) bool {
	if !check.Satisfied || check.ExitCode == nil || item.ExitCode == nil ||
		strings.TrimSpace(check.Command) == "" || strings.TrimSpace(item.Command) == "" ||
		strings.TrimSpace(check.Command) != strings.TrimSpace(item.Command) {
		return false
	}
	expected := 0
	if criterion.ExpectedExitCode != nil {
		expected = *criterion.ExpectedExitCode
	}
	return *check.ExitCode == expected && *item.ExitCode == expected
}

func workOrderVerificationKindV2(criterionID string, criteria []domain.AcceptanceCriterion) string {
	for _, criterion := range criteria {
		if criterion.ID == criterionID {
			if criterion.Kind == "reproduction" {
				return "reproduction"
			}
			return "acceptance"
		}
	}
	return "acceptance"
}

func (a *App) applyWorkOrderChangeSetsV2(ctx context.Context, order domain.WorkOrder, root domain.Quest, evidenceID string) ([]string, []string, error) {
	if info, err := os.Stat(order.Workspace.Path); err != nil || !info.IsDir() {
		return nil, nil, fmt.Errorf("approved delivery workspace is unavailable")
	}
	quests, err := a.store.ListQuests(ctx, order.WorkspaceID)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[string]*domain.Quest, len(quests))
	for index := range quests {
		byID[quests[index].ID] = &quests[index]
	}
	executions, err := a.store.ListExecutions(ctx, order.WorkspaceID, 500)
	if err != nil {
		return nil, nil, err
	}
	ownedExecutions := map[string]bool{}
	for _, execution := range executions {
		if questDescendsFrom(execution.QuestID, root.ID, byID) {
			ownedExecutions[execution.ID] = true
		}
	}
	sets, err := a.store.ListChangeSets(ctx, order.WorkspaceID)
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(sets, func(i, j int) bool { return sets[i].CreatedAt.Before(sets[j].CreatedAt) })
	changed := []string{}
	newlyApplied := []string{}
	rollback := func(cause error) ([]string, []string, error) {
		for index := len(newlyApplied) - 1; index >= 0; index-- {
			_, _ = (changesets.Applier{Store: a.store}).Revert(ctx, order.Workspace.Path, newlyApplied[index])
		}
		return nil, nil, cause
	}
	for _, set := range sets {
		if !questDescendsFrom(set.QuestID, root.ID, byID) && !ownedExecutions[set.ExecutionID] {
			continue
		}
		switch set.Status {
		case domain.ChangeSetApplied:
			for _, item := range set.Items {
				changed = append(changed, item.Path)
			}
		case domain.ChangeSetPending, domain.ChangeSetApproved:
			result, applyErr := a.applyChangeSetAtPathV2(ctx, order.WorkspaceID, order.Workspace.Path, set.ID)
			if applyErr != nil {
				return rollback(fmt.Errorf("apply change set %s: %w", set.ID, applyErr))
			}
			if len(result.Conflicts) > 0 || result.ChangeSet.Status == domain.ChangeSetConflict {
				return rollback(fmt.Errorf("%w: change set %s conflicts with external workspace changes: %s", errWorkOrderDeliveryConflictV2, set.ID, strings.Join(result.Conflicts, ", ")))
			}
			newlyApplied = append(newlyApplied, set.ID)
			changed = append(changed, result.Applied...)
		case domain.ChangeSetConflict:
			return rollback(fmt.Errorf("%w: change set %s has unresolved conflicts", errWorkOrderDeliveryConflictV2, set.ID))
		}
	}
	changed = uniqueSortedStringsV2(changed)
	commits := []string{}
	if order.Delivery.CommitMode == "squash" {
		commitID, commitErr := createWorkOrderSquashCommitV2(ctx, order.Workspace.Path, root.ID, evidenceID, changed)
		if commitErr != nil {
			return rollback(commitErr)
		}
		commits = append(commits, commitID)
	}
	return changed, commits, nil
}

func (a *App) applyChangeSetAtPathV2(ctx context.Context, workspaceID, workspacePath, changeSetID string) (changesets.ApplyResult, error) {
	set, err := a.store.GetChangeSet(ctx, changeSetID)
	if err != nil {
		return changesets.ApplyResult{}, err
	}
	if set.WorkspaceID != workspaceID {
		return changesets.ApplyResult{}, fmt.Errorf("change set belongs to another workspace")
	}
	for _, dependencyID := range set.DependsOn {
		dependency, getErr := a.store.GetChangeSet(ctx, dependencyID)
		if getErr != nil {
			return changesets.ApplyResult{}, fmt.Errorf("load change set dependency %s: %w", dependencyID, getErr)
		}
		if dependency.WorkspaceID != workspaceID || dependency.Status != domain.ChangeSetApplied {
			return changesets.ApplyResult{}, fmt.Errorf("change set prerequisite %s is not applied in the approved workspace", dependencyID)
		}
	}
	return (changesets.Applier{Store: a.store}).Apply(ctx, workspacePath, changeSetID)
}

func workOrderEnvironmentDigestV2(order domain.WorkOrder) string {
	raw, _ := json.Marshal(struct {
		Workspace domain.WorkspacePlan      `json:"workspace"`
		Stack     domain.StackPresetRef     `json:"stack"`
		Routing   domain.ModelRoutingPolicy `json:"routing"`
		Network   []domain.NetworkGrant     `json:"network"`
	}{order.Workspace, order.Stack, order.Routing, order.Network})
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func workOrderRevisionV2(order domain.WorkOrder, quest domain.Quest, bundle domain.EvidenceBundle) string {
	raw, _ := json.Marshal(struct {
		WorkOrder string   `json:"workOrder"`
		Quest     string   `json:"quest"`
		Flow      string   `json:"flow"`
		Files     []string `json:"files"`
	}{domain.WorkOrderDigest(order), quest.ID, quest.FlowRunID, uniqueSortedStringsV2(bundle.ChangedFiles)})
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func uniqueSortedStringsV2(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
