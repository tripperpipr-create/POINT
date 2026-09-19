package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
)

// Сверка обещания с результатом.
//
// У квеста есть определение готовности — список того, что должно случиться.
// Но никто его не сверял: квест закрывался, а обещание оставалось словами.
// Человек видел «завершён» и не мог сказать, выполнено ли то, ради чего
// квест ставили.
//
// Здесь каждый пункт сопоставляется с записанными фактами. Пункт считается
// выполненным только при наличии доказательства — не по факту закрытия квеста
// и не по утверждению агента.

type Promise struct {
	Text string `json:"text"`
	Met  bool   `json:"met"`
	// Evidence — чем именно подтверждено. Для невыполненного — чего не хватает.
	Evidence string `json:"evidence"`
}

type QuestOutcome struct {
	QuestID  string    `json:"questId"`
	Title    string    `json:"title"`
	Status   string    `json:"status"`
	Promises []Promise `json:"promises"`
	// Met/Total — сколько обещаний подтверждено фактами.
	Met   int `json:"met"`
	Total int `json:"total"`
	// Verified — была ли успешная проверка после последнего изменения.
	Verified bool `json:"verified"`
	// AppliedFiles — что в итоге легло в рабочую копию.
	AppliedFiles []string `json:"appliedFiles,omitempty"`
	// Honest — итог одной фразой, без смягчения.
	Honest   string                    `json:"honest"`
	Evidence *agent.CompletionEvidence `json:"evidence,omitempty"`
	// UnverifiedReason explains why proof was withheld even if runs finished.
	UnverifiedReason string `json:"unverifiedReason,omitempty"`
}

// promiseKinds грубо определяет, о чём пункт готовности, чтобы искать под него
// доказательство. Пункт может говорить сразу о двух вещах — «проверить, что
// изменения приняты». Раньше побеждало первое совпадение, и половина требования
// молча пропадала: пункт засчитывался по проверке, хотя изменений не было.
// Названы обе стороны — значит нужны обе.
func promiseKinds(text string) (verification bool, changes bool) {
	lower := strings.ToLower(text)
	contains := func(words ...string) bool {
		for _, word := range words {
			if strings.Contains(lower, word) {
				return true
			}
		}
		return false
	}
	return contains("провер", "тест", "линт", "сборк"),
		contains("изменени", "прин", "правк", "патч")
}

// QuestOutcome сверяет определение готовности квеста с записанными фактами.
func (a *App) QuestOutcome(ctx context.Context, questID string) (QuestOutcome, error) {
	if strings.TrimSpace(questID) == "" {
		return QuestOutcome{}, errors.New("questId is required")
	}
	workspaceID := a.currentWorldID()
	quests, err := a.store.ListQuests(ctx, workspaceID)
	if err != nil {
		return QuestOutcome{}, err
	}
	var quest domain.Quest
	found := false
	for _, item := range quests {
		if item.ID == questID {
			quest, found = item, true
			break
		}
	}
	if !found {
		return QuestOutcome{}, errors.New("quest not found")
	}

	executions, err := a.store.ListExecutions(ctx, workspaceID, 200)
	if err != nil {
		return QuestOutcome{}, err
	}
	changeSets, err := a.store.ListChangeSets(ctx, workspaceID)
	if err != nil {
		return QuestOutcome{}, err
	}

	// Применённые наборы этого квеста — единственное свидетельство того, что
	// работа дошла до рабочей копии, а не осталась в песочнице.
	runIDs := map[string]bool{}
	executionIDs := map[string]bool{}
	for _, execution := range executions {
		if execution.QuestID != quest.ID {
			continue
		}
		executionIDs[execution.ID] = true
		if execution.RunID != "" {
			runIDs[execution.RunID] = true
		}
	}
	applied := make([]string, 0, 8)
	for _, set := range changeSets {
		if set.QuestID != quest.ID && !executionIDs[set.ExecutionID] {
			continue
		}
		if set.Status != domain.ChangeSetApplied {
			continue
		}
		for _, item := range set.Items {
			applied = append(applied, item.Path)
		}
	}

	if quest.Brief != nil {
		return a.taskBriefOutcome(ctx, quest, quests, executions, applied)
	}

	// Проверка засчитывается только по записанной диагностике прогона: слово
	// агента доказательством не является — на этом стоит весь гейт завершения.
	verified := false
	for runID := range runIDs {
		run, runErr := a.store.GetRun(ctx, runID)
		if runErr != nil {
			continue
		}
		report, repErr := a.runDiagnostics(ctx, run)
		if repErr != nil {
			continue
		}
		if report.Verification.Recorded {
			verified = true
			break
		}
	}

	outcome := QuestOutcome{
		QuestID: quest.ID, Title: quest.Title, Status: string(quest.Status),
		Promises: []Promise{}, Verified: verified, AppliedFiles: applied,
	}
	for _, text := range quest.DefinitionOfDone {
		promise := Promise{Text: text}
		wantsVerification, wantsChanges := promiseKinds(text)
		hasChanges := len(applied) > 0
		switch {
		case wantsVerification && wantsChanges:
			// Пункт требует обоих фактов — засчитывается только по обоим.
			promise.Met = verified && hasChanges
			switch {
			case promise.Met:
				promise.Evidence = fmt.Sprintf("применено файлов: %d, проверка зафиксирована", len(applied))
			case !hasChanges:
				promise.Evidence = "ни один набор изменений не применён"
			default:
				promise.Evidence = "успешной проверки в хронике прогонов нет"
			}
		case wantsVerification:
			promise.Met = verified
			if verified {
				promise.Evidence = "проверка зафиксирована после последнего изменения"
			} else {
				promise.Evidence = "успешной проверки в хронике прогонов нет"
			}
		case wantsChanges:
			promise.Met = hasChanges
			if promise.Met {
				promise.Evidence = fmt.Sprintf("применено файлов: %d", len(applied))
			} else {
				promise.Evidence = "ни один набор изменений не применён"
			}
		default:
			// Неизвестный пункт не подгоняется под ближайший факт: «не знаю»
			// честнее, чем «наверное, выполнено».
			promise.Evidence = "автоматически не проверяется — судите сами"
		}
		if promise.Met {
			outcome.Met++
		}
		outcome.Promises = append(outcome.Promises, promise)
	}
	outcome.Total = len(outcome.Promises)

	switch {
	case outcome.Total == 0:
		outcome.Honest = "У квеста нет определения готовности — сверять не с чем."
	case outcome.Met == outcome.Total:
		outcome.Honest = "Все проверяемые обещания подтверждены фактами."
	case outcome.Met == 0:
		outcome.Honest = "Ни одно обещание не подтверждено записанными фактами."
	default:
		outcome.Honest = fmt.Sprintf("Подтверждено %d из %d обещаний; остальное фактами не закрыто.",
			outcome.Met, outcome.Total)
	}
	return outcome, nil
}

// A structured task is compared with its approved criteria, never with words
// such as "test" or "change" in its prose. Applying a Change Set is a later act.
func (a *App) taskBriefOutcome(ctx context.Context, quest domain.Quest, quests []domain.Quest, executions []domain.ExecutionInstance, applied []string) (QuestOutcome, error) {
	out := QuestOutcome{QuestID: quest.ID, Title: quest.Title, Status: string(quest.Status), AppliedFiles: applied, Promises: []Promise{}}
	count := 0
	byID := make(map[string]*domain.Quest, len(quests))
	for i := range quests {
		byID[quests[i].ID] = &quests[i]
	}
	for i := range executions {
		e := &executions[i]
		if !questDescendsFrom(e.QuestID, quest.ID, byID) {
			continue
		}
		count++
	}
	// Prefer the newest completed execution whose run brief matches the parent
	// quest brief. Stage-scoped briefs (bootstrap/integrate/impl_review) must not
	// erase implement/accept verification that already bound to parent criteria.
	proof, mergedProof, err := a.parentBriefCompletionProof(ctx, quest, executions, byID)
	if err != nil {
		return out, err
	}
	if count > 1 && quest.Brief.Permissions.WriteFiles && a.requiresMergedResultCheck(ctx, quest, executions, byID) {
		if merged := a.mergedResultCompletionEvidence(ctx, quest); merged != nil {
			proof = merged
		} else if mergedProof != nil {
			proof = mergedProof
		} else if !a.hasMergedResultEvidence(ctx, quest) {
			proof = nil
			out.UnverifiedReason = "несколько исполнителей меняли файлы: нет отдельной проверки объединённого результата (merged-result)"
		}
	}
	out.Evidence = proof
	states := map[string]agent.CriterionEvidence{}
	if proof != nil {
		for _, c := range proof.Criteria {
			states[c.CriterionID] = c
		}
	}
	for _, c := range quest.Brief.Criteria {
		p := Promise{Text: c.Text, Evidence: "нет актуального доказательства по этому критерию"}
		if c.Kind == "manual" {
			p.Evidence = "требуется оценка пользователя"
		} else if e, ok := states[c.ID]; ok {
			p.Met = e.Status == "satisfied" && e.Kind == c.Kind
			p.Evidence = e.Status
			if e.Check != nil {
				p.Evidence = e.Check.Tool + ": " + e.Check.Detail + "; " + e.Status
			}
		}
		if p.Met {
			out.Met++
		}
		out.Promises = append(out.Promises, p)
	}
	out.Total = len(out.Promises)
	out.Verified = proof != nil && proof.Status == "verified" && out.Total > 0 && out.Met == out.Total
	switch {
	case out.Verified:
		out.Honest = "Критерии задания подтверждены. Результат готов к просмотру; применение изменений — отдельное решение."
	case out.UnverifiedReason != "":
		out.Honest = out.UnverifiedReason
	case proof != nil && proof.Status == "needs_review":
		out.Honest = "Исполнение закончено; результат требует оценки пользователя."
	default:
		out.Honest = "Выполнение всех критериев текущего задания пока не подтверждено."
	}
	return out, nil
}

// parentBriefCompletionProof walks completed executions newest-first and returns
// completion evidence from the first run whose started brief matches the parent
// quest brief. When the flow includes an Accept agent stage, only that stage's
// proof counts — implement must not finalize intake.
func (a *App) parentBriefCompletionProof(ctx context.Context, quest domain.Quest, executions []domain.ExecutionInstance, byID map[string]*domain.Quest) (*agent.CompletionEvidence, *agent.CompletionEvidence, error) {
	if quest.Brief == nil {
		return nil, nil, nil
	}
	acceptNodes := a.acceptAgentNodeIDs(ctx, quest)
	type candidate struct {
		exec domain.ExecutionInstance
	}
	var ordered []candidate
	for i := range executions {
		e := executions[i]
		if !questDescendsFrom(e.QuestID, quest.ID, byID) {
			continue
		}
		if e.Status != domain.RunCompleted || strings.TrimSpace(e.RunID) == "" {
			continue
		}
		ordered = append(ordered, candidate{exec: e})
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].exec.StartedAt.After(ordered[j].exec.StartedAt)
	})
	parentDigest := domain.TaskBriefDigest(*quest.Brief)
	requireAccept := len(acceptNodes) > 0
	for _, item := range ordered {
		proof, mergedProof, bound, stageRole, err := a.completionProofFromRun(ctx, item.exec.RunID, quest, parentDigest)
		if err != nil {
			return nil, nil, err
		}
		if !bound {
			continue
		}
		if requireAccept {
			if stageRole != domain.StageRoleAccept && !acceptNodes[item.exec.FlowNodeID] {
				continue
			}
		}
		return proof, mergedProof, nil
	}
	return nil, nil, nil
}

func (a *App) acceptAgentNodeIDs(ctx context.Context, quest domain.Quest) map[string]bool {
	out := map[string]bool{}
	if strings.TrimSpace(quest.FlowID) == "" {
		return out
	}
	flow, err := a.store.GetFlow(ctx, quest.FlowID)
	if err != nil {
		return out
	}
	for _, node := range flow.Nodes {
		if node.Kind == domain.FlowNodeAgent && domain.FlowNodeStageRole(node) == domain.StageRoleAccept {
			out[node.ID] = true
		}
	}
	return out
}

func (a *App) completionProofFromRun(ctx context.Context, runID string, quest domain.Quest, parentDigest string) (*agent.CompletionEvidence, *agent.CompletionEvidence, bool, string, error) {
	run, err := a.store.GetRun(ctx, runID)
	if err != nil {
		return nil, nil, false, "", err
	}
	events, err := a.store.ListByRun(ctx, run.ID)
	if err != nil {
		return nil, nil, false, "", err
	}
	bound := false
	stageRole := ""
	var proof *agent.CompletionEvidence
	var mergedProof *agent.CompletionEvidence
	for _, event := range events {
		if event.Type == domain.EventRunStarted {
			var p struct {
				Brief     *domain.TaskBrief `json:"taskBrief"`
				StageRole string            `json:"stageRole"`
			}
			if json.Unmarshal(event.Data, &p) == nil {
				if p.Brief != nil {
					bound = p.Brief.Version == quest.Brief.Version && domain.TaskBriefDigest(*p.Brief) == parentDigest
				}
				stageRole = strings.TrimSpace(p.StageRole)
			}
		}
		// No persisted earlier success survives subsequent executable work in this run.
		if event.Type == domain.EventToolRequested || event.Type == domain.EventPatchApplied || event.Type == domain.EventWorkspaceChanged {
			proof = nil
		}
		if event.Type == domain.EventCompletionChecked {
			var p struct {
				Status    string                    `json:"status"`
				CheckKind string                    `json:"checkKind"`
				Evidence  *agent.CompletionEvidence `json:"evidence"`
			}
			proof = nil
			if json.Unmarshal(event.Data, &p) == nil && p.Status == "accepted_after_revision" && p.Evidence != nil && p.Evidence.BriefVersion == quest.Brief.Version {
				proof = p.Evidence
				if p.CheckKind == "merged-result" {
					mergedProof = p.Evidence
				}
			}
		}
	}
	if !bound || run.Status != domain.RunCompleted {
		return nil, nil, bound, stageRole, nil
	}
	return proof, mergedProof, true, stageRole, nil
}

// requiresMergedResultCheck is true for parallel/multi-root writers. A serial
// inheritance chain (bootstrap → implement → …) already carries one integrated
// sandbox tip and must not demand a separate merged-result completion check.
func (a *App) requiresMergedResultCheck(ctx context.Context, quest domain.Quest, executions []domain.ExecutionInstance, byID map[string]*domain.Quest) bool {
	type writerMeta struct {
		id        string
		parents   []string
		hasRecord bool
	}
	writers := make([]writerMeta, 0, len(executions))
	for i := range executions {
		e := &executions[i]
		if !questDescendsFrom(e.QuestID, quest.ID, byID) {
			continue
		}
		meta := writerMeta{id: e.ID}
		if e.SandboxID != "" {
			if record, err := a.store.GetSandbox(ctx, e.SandboxID); err == nil {
				meta.hasRecord = true
				meta.parents = sandboxParentExecutionIDs(record)
			}
		}
		writers = append(writers, meta)
	}
	if len(writers) <= 1 {
		return false
	}
	sawRecord := false
	rootCount := 0
	for _, writer := range writers {
		if !writer.hasRecord {
			continue
		}
		sawRecord = true
		if len(writer.parents) > 1 {
			return true
		}
		if len(writer.parents) == 0 {
			rootCount++
		}
	}
	if !sawRecord {
		return true
	}
	return rootCount > 1
}

// hasMergedResultEvidence is true when a dedicated merged-result completion
// check exists, or when a Flow Join recorded a successful parallel sandbox merge.
func (a *App) hasMergedResultEvidence(ctx context.Context, quest domain.Quest) bool {
	if a.mergedResultCompletionEvidence(ctx, quest) != nil {
		return true
	}
	if quest.FlowID == "" {
		return false
	}
	runs, err := a.store.ListFlowRunsByFlowID(ctx, quest.FlowID)
	if err != nil {
		return false
	}
	for _, run := range runs {
		if quest.ID != "" && run.QuestID != "" && run.QuestID != quest.ID {
			continue
		}
		for _, state := range run.NodeStates {
			mergeID, _ := state.Output["mergeChangeSetId"].(string)
			if strings.TrimSpace(mergeID) == "" {
				continue
			}
			if wait, _ := state.Output["waitReason"].(string); wait == "sandbox_merge_conflict" {
				continue
			}
			if verified, _ := state.Output["mergedResultVerified"].(bool); verified {
				return true
			}
			// Legacy merges before the explicit flag still count when conflict-free.
			if count, ok := state.Output["mergeConflictCount"].(float64); ok && count == 0 {
				return true
			}
			if count, ok := state.Output["mergeConflictCount"].(int); ok && count == 0 {
				return true
			}
			if _, ok := state.Output["mergeConflicts"]; !ok {
				return true
			}
		}
	}
	return false
}

// mergedResultCompletionEvidence returns CompletionEvidence from an accepted
// EventCompletionChecked tagged checkKind=merged-result for this quest tree.
func (a *App) mergedResultCompletionEvidence(ctx context.Context, quest domain.Quest) *agent.CompletionEvidence {
	ws, err := a.requireWorkspace()
	if err != nil {
		return nil
	}
	executions, err := a.store.ListExecutions(ctx, ws.ID, 500)
	if err != nil {
		return nil
	}
	quests, err := a.store.ListQuests(ctx, ws.ID)
	if err != nil {
		return nil
	}
	byID := make(map[string]*domain.Quest, len(quests))
	for i := range quests {
		byID[quests[i].ID] = &quests[i]
	}
	var best *agent.CompletionEvidence
	var bestAt time.Time
	for _, exec := range executions {
		if exec.RunID == "" || exec.Status != domain.RunCompleted {
			continue
		}
		if !questDescendsFrom(exec.QuestID, quest.ID, byID) {
			continue
		}
		events, listErr := a.store.ListByRun(ctx, exec.RunID)
		if listErr != nil {
			continue
		}
		for _, event := range events {
			if event.Type != domain.EventCompletionChecked {
				continue
			}
			var p struct {
				Status    string                    `json:"status"`
				CheckKind string                    `json:"checkKind"`
				Evidence  *agent.CompletionEvidence `json:"evidence"`
			}
			if json.Unmarshal(event.Data, &p) != nil || p.CheckKind != "merged-result" {
				continue
			}
			if p.Status != "accepted_after_revision" || p.Evidence == nil {
				continue
			}
			if quest.Brief != nil && p.Evidence.BriefVersion != quest.Brief.Version {
				continue
			}
			if best == nil || event.CreatedAt.After(bestAt) {
				best = p.Evidence
				bestAt = event.CreatedAt
			}
		}
	}
	return best
}
