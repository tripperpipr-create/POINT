package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/diagnostics"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
)

// Повтор проваленного этапа с правками и без человека, где он не нужен.
//
// Уровни самостоятельности решает сервер, а не модель и не окно:
//   - сбой среды (класс transient) Point повторяет сам, без правок;
//   - правки, не трогающие договор, — среда песочницы из закрытого списка и
//     указание исполнителю — Мастер предлагает, и повтор идёт сразу;
//   - правка команды критерия меняет утверждённое, поэтому ждёт разрешения
//     человека на конкретный diff, а после «да» повтор запускается тут же.
//
// Запускает повтор расширение: ключ модели этапов живёт только у него, а ядро
// без окон всё равно гаснет. Ядро решает, можно ли, и перепроверяет каждый
// запрос — окно не может выдать ручную правку за безопасную.

const (
	stageRetryProposalKey     = "stageRetryProposal"
	stageAutoRetriesKey       = "stageAutoRetries"
	maxAutoRetriesPerStage    = 2
	maxAutoRetriesPerQuest    = 3
	stageAutoRetryDelaySecs   = 30
	retryInstructionOutputKey = "retryInstruction"
	maxRetryInstructionRunes  = 2000
)

// Источники повтора: человек нажал кнопку, окно повторило само по решению
// сервера.
const (
	StageRetrySourceHuman = "human"
	StageRetrySourceAuto  = "auto"
)

var (
	errStageRetryLimit    = errors.New("лимит повторов без человека исчерпан: решение за человеком")
	errStageRetryProposal = errors.New("предложение повтора устарело или не найдено")
)

// StageRetryCriterionChange — правка команды одного критерия.
type StageRetryCriterionChange struct {
	CriterionID     string `json:"criterionId"`
	PreviousCommand string `json:"previousCommand"`
	Command         string `json:"command"`
	Reason          string `json:"reason,omitempty"`
}

// StageRetryProposal — предложение Мастера повторить проваленный этап.
type StageRetryProposal struct {
	Digest        string                      `json:"digest"`
	NodeID        string                      `json:"nodeId"`
	NodeName      string                      `json:"nodeName,omitempty"`
	FailureAt     string                      `json:"failureAt"`
	Instruction   string                      `json:"instruction,omitempty"`
	Criteria      []StageRetryCriterionChange `json:"criteria,omitempty"`
	Diagnosis     string                      `json:"diagnosis,omitempty"`
	NeedsApproval bool                        `json:"needsApproval"`
	AutoApply     bool                        `json:"autoApply"`
	CreatedAt     string                      `json:"createdAt"`
}

// StageRetryProposalInput — то, что предлагает Мастер.
type StageRetryProposalInput struct {
	QuestID     string                      `json:"questId"`
	Instruction string                      `json:"instruction,omitempty"`
	Criteria    []StageRetryCriterionChange `json:"criteria,omitempty"`
	Diagnosis   string                      `json:"diagnosis,omitempty"`
}

// stageRetryPlan — что применить при этом повторе.
type stageRetryPlan struct {
	Source      string
	Instruction string
	Criteria    []StageRetryCriterionChange
	Proposal    string
}

func (p stageRetryPlan) journalNote() string {
	parts := []string{"источник: " + p.Source}
	if p.Instruction != "" {
		parts = append(parts, "указание исполнителю")
	}
	for _, change := range p.Criteria {
		parts = append(parts, "проверка "+change.CriterionID+" изменена")
	}
	if p.Proposal != "" {
		parts = append(parts, "предложение "+p.Proposal[:min(len(p.Proposal), 19)])
	}
	return strings.Join(parts, "; ")
}

// stageAutoRetryCounts — сколько раз этап и квест уже повторялись без человека.
func stageAutoRetryCounts(quest domain.Quest, nodeID string) (int, int) {
	counts, _ := quest.Controller[stageAutoRetriesKey].(map[string]any)
	total := 0
	for _, value := range counts {
		total += intFromAny(value)
	}
	return intFromAny(counts[nodeID]), total
}

func stageAutoRetryAvailable(quest domain.Quest, nodeID string) bool {
	node, total := stageAutoRetryCounts(quest, nodeID)
	return node < maxAutoRetriesPerStage && total < maxAutoRetriesPerQuest
}

// stageAutoRetryInfo — разрешение повторить без правок, записанное в провал.
func stageAutoRetryInfo(quest domain.Quest, nodeID string, diagnosis StageFailureDiagnosis) map[string]any {
	node, _ := stageAutoRetryCounts(quest, nodeID)
	info := map[string]any{"attempt": node + 1, "max": maxAutoRetriesPerStage, "delaySeconds": stageAutoRetryDelaySecs}
	switch {
	case diagnosis.Class != diagnostics.FailureTransient:
		info["allowed"] = false
	case !stageAutoRetryAvailable(quest, nodeID):
		info["allowed"] = false
		info["reason"] = "повторы без человека исчерпаны"
	default:
		info["allowed"] = true
		info["reason"] = "сбой среды: Point повторит этап без изменений"
	}
	return info
}

func countStageAutoRetry(quest *domain.Quest, nodeID string) {
	counts, _ := quest.Controller[stageAutoRetriesKey].(map[string]any)
	next := map[string]any{}
	for key, value := range counts {
		next[key] = value
	}
	next[nodeID] = intFromAny(next[nodeID]) + 1
	quest.Controller[stageAutoRetriesKey] = next
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	}
	return 0
}

// stageFailureRecord — отметка провала, которую ждёт квест.
func stageFailureRecord(quest domain.Quest) (map[string]any, string, string) {
	failure, _ := quest.Controller[stageFailureKey].(map[string]any)
	nodeID, _ := failure["nodeId"].(string)
	at, _ := failure["at"].(string)
	return failure, nodeID, at
}

// ProposeStageRetryV2 проверяет и сохраняет предложение Мастера. Класс
// предложения — нужно ли разрешение — считает сервер по содержанию правок.
func (a *App) ProposeStageRetryV2(ctx context.Context, workspaceID string, input StageRetryProposalInput) (StageRetryProposal, error) {
	quest, err := a.workOrderQuestV2(ctx, workspaceID, strings.TrimSpace(input.QuestID))
	if err != nil {
		return StageRetryProposal{}, err
	}
	if !stageFailurePending(quest) {
		return StageRetryProposal{}, errNoStageFailureV2
	}
	failure, nodeID, failureAt := stageFailureRecord(quest)
	nodeName, _ := failure["nodeName"].(string)
	proposal := StageRetryProposal{NodeID: nodeID, NodeName: nodeName, FailureAt: failureAt,
		Diagnosis: truncateRunes(security.Redact(strings.TrimSpace(input.Diagnosis)), 1200),
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	instruction := strings.TrimSpace(input.Instruction)
	if len([]rune(instruction)) > maxRetryInstructionRunes {
		return StageRetryProposal{}, fmt.Errorf("указание длиннее %d знаков", maxRetryInstructionRunes)
	}
	proposal.Instruction = security.Redact(instruction)
	if len(input.Criteria) > 0 {
		changes, changeErr := a.validateCriterionChanges(ctx, quest, input.Criteria)
		if changeErr != nil {
			return StageRetryProposal{}, changeErr
		}
		proposal.Criteria = changes
	}
	if proposal.Instruction == "" && len(proposal.Criteria) == 0 {
		return StageRetryProposal{}, errors.New("в предложении нет правок: для повтора как есть хватит кнопки «Повторить этап»")
	}
	proposal.NeedsApproval = len(proposal.Criteria) > 0
	proposal.AutoApply = !proposal.NeedsApproval && stageAutoRetryAvailable(quest, nodeID)
	proposal.Digest = stageRetryProposalDigest(proposal)
	encoded, _ := json.Marshal(proposal)
	var stored map[string]any
	_ = json.Unmarshal(encoded, &stored)
	loaded := quest.Status
	quest.Controller[stageRetryProposalKey] = stored
	quest.UpdatedAt = time.Now().UTC()
	if err = a.saveLoadedQuest(ctx, quest, loaded, "stage_retry_proposal"); err != nil {
		return StageRetryProposal{}, err
	}
	return proposal, nil
}

// validateCriterionChanges — критерий существует, машинный, команда новая и не
// прячет свой код выхода.
func (a *App) validateCriterionChanges(ctx context.Context, quest domain.Quest, input []StageRetryCriterionChange) ([]StageRetryCriterionChange, error) {
	approved := a.approvedCriteriaV2(ctx, quest)
	if len(approved) == 0 {
		return nil, errors.New("у квеста нет утверждённых критериев")
	}
	current := a.effectiveCriterionCommands(ctx, quest)
	seen := map[string]bool{}
	changes := make([]StageRetryCriterionChange, 0, len(input))
	for _, change := range input {
		id := strings.TrimSpace(change.CriterionID)
		command := strings.TrimSpace(change.Command)
		var criterion *domain.AcceptanceCriterion
		for index := range approved {
			if approved[index].ID == id {
				criterion = &approved[index]
				break
			}
		}
		switch {
		case criterion == nil:
			return nil, fmt.Errorf("критерия %q нет в наряде", id)
		case criterion.Kind == "manual" || criterion.Tool != "run_command":
			return nil, fmt.Errorf("критерий %q не машинная проверка: его команду не правят", id)
		case seen[id]:
			return nil, fmt.Errorf("критерий %q назван дважды", id)
		case command == "":
			return nil, fmt.Errorf("пустая команда для %q", id)
		case command == current[id]:
			return nil, fmt.Errorf("команда %q не изменилась", id)
		case len(command) > 2000:
			return nil, fmt.Errorf("команда %q длиннее 2000 знаков", id)
		}
		if _, fragment, masked := maskedCriterionEvidence(*criterion, map[string]any{"command": command}); masked {
			return nil, fmt.Errorf("команда %q прячет код выхода (%s): проверка прошла бы и при провале", id, fragment)
		}
		seen[id] = true
		changes = append(changes, StageRetryCriterionChange{CriterionID: id, PreviousCommand: current[id], Command: security.Redact(command),
			Reason: truncateRunes(security.Redact(strings.TrimSpace(change.Reason)), 300)})
	}
	return changes, nil
}

func stageRetryProposalDigest(proposal StageRetryProposal) string {
	payload, _ := json.Marshal(struct {
		NodeID, FailureAt, Runtime, Instruction string
		Criteria                                []StageRetryCriterionChange
		// Runtime остался в отпечатке пустым полем: прежде предложение могло
		// нести выбор среды, и уже сохранённые предложения считали отпечаток с
		// ним. Без поля они стали бы «устаревшими» посреди ожидания человека.
	}{proposal.NodeID, proposal.FailureAt, "", proposal.Instruction, proposal.Criteria})
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func storedStageRetryProposal(quest domain.Quest) (StageRetryProposal, bool) {
	raw, ok := quest.Controller[stageRetryProposalKey].(map[string]any)
	if !ok {
		return StageRetryProposal{}, false
	}
	encoded, _ := json.Marshal(raw)
	var proposal StageRetryProposal
	if json.Unmarshal(encoded, &proposal) != nil || proposal.Digest == "" {
		return StageRetryProposal{}, false
	}
	return proposal, true
}

// resolveStageRetryPlanV2 переводит запрос повтора в план и отвергает всё, на
// что у источника нет права.
func resolveStageRetryPlanV2(quest domain.Quest, request WorkOrderQuestControlRequest) (stageRetryPlan, error) {
	_, nodeID, failureAt := stageFailureRecord(quest)
	source := strings.TrimSpace(request.Source)
	if source == "" {
		source = StageRetrySourceHuman
	}
	if source != StageRetrySourceHuman && source != StageRetrySourceAuto {
		return stageRetryPlan{}, fmt.Errorf("неизвестный источник повтора %q", source)
	}
	plan := stageRetryPlan{Source: source}
	if digest := strings.TrimSpace(request.ProposalDigest); digest != "" {
		proposal, ok := storedStageRetryProposal(quest)
		if !ok || proposal.Digest != digest || proposal.FailureAt != failureAt || proposal.NodeID != nodeID || stageRetryProposalDigest(proposal) != digest {
			return stageRetryPlan{}, errStageRetryProposal
		}
		if source == StageRetrySourceAuto && (!proposal.AutoApply || proposal.NeedsApproval || !stageAutoRetryAvailable(quest, nodeID)) {
			return stageRetryPlan{}, errStageRetryLimit
		}
		plan.Instruction, plan.Criteria, plan.Proposal = proposal.Instruction, proposal.Criteria, proposal.Digest
		return plan, nil
	}
	if source == StageRetrySourceAuto {
		failure, _, _ := stageFailureRecord(quest)
		auto, _ := failure["autoRetry"].(map[string]any)
		if allowed, _ := auto["allowed"].(bool); !allowed || !stageAutoRetryAvailable(quest, nodeID) {
			return stageRetryPlan{}, errStageRetryLimit
		}
	}
	return plan, nil
}

// effectiveCriterionCommands — команда каждого машинного критерия с учётом
// разрешённых поправок.
func (a *App) effectiveCriterionCommands(ctx context.Context, quest domain.Quest) map[string]string {
	commands := map[string]string{}
	for _, criterion := range a.approvedCriteriaV2(ctx, quest) {
		var args struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(criterion.Arguments, &args) == nil {
			commands[criterion.ID] = strings.TrimSpace(args.Command)
		}
	}
	for id, amendment := range a.criterionAmendments(ctx, quest.ID) {
		if _, ok := commands[id]; ok {
			commands[id] = amendment.Command
		}
	}
	return commands
}

// approvedCriteriaV2 — критерии утверждённого наряда квеста; у квеста без
// наряда — критерии его задания.
func (a *App) approvedCriteriaV2(ctx context.Context, quest domain.Quest) []domain.AcceptanceCriterion {
	if approval, err := a.store.WorkOrderApprovalByQuestV2(ctx, quest.ID); err == nil && len(approval.WorkOrder.Criteria) > 0 {
		return append([]domain.AcceptanceCriterion(nil), approval.WorkOrder.Criteria...)
	}
	if quest.Brief != nil {
		return append([]domain.AcceptanceCriterion(nil), quest.Brief.Criteria...)
	}
	return nil
}

func (a *App) criterionAmendments(ctx context.Context, questID string) map[string]domain.CriterionAmendment {
	if a == nil || a.store == nil || strings.TrimSpace(questID) == "" {
		return map[string]domain.CriterionAmendment{}
	}
	items, err := a.store.CriterionAmendmentsV2(ctx, questID)
	if err != nil {
		return map[string]domain.CriterionAmendment{}
	}
	return domain.LatestCriterionAmendments(items)
}

// amendedCriterionArguments — аргументы критерия с действующей поправкой.
func amendedCriterionArguments(arguments json.RawMessage, amendment domain.CriterionAmendment) json.RawMessage {
	var args map[string]any
	if json.Unmarshal(arguments, &args) != nil || args == nil {
		args = map[string]any{}
	}
	args["command"] = amendment.Command
	encoded, err := json.Marshal(args)
	if err != nil {
		return arguments
	}
	return encoded
}
