// Разбор квеста: что Point вынес из него для обучения Мастера и агентов.
package app

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// QuestRetrospective — «что Point вынес из квеста»: наблюдения, откуда они
// взялись, что из них выросло и чем закончилась проверка выросшего. Разбор
// собирается при чтении из записанных данных, без модели: он не может
// выдумать урок, которого нет в журнале, и не устаревает от правки записей.
type QuestRetrospective struct {
	QuestID        string                          `json:"questId"`
	Status         domain.QuestStatus              `json:"status"`
	Observations   []QuestRetrospectiveObservation `json:"observations"`
	MasterLearning []QuestRetrospectiveMasterJob   `json:"masterLearning"`
	AgentLearning  []QuestRetrospectiveImprovement `json:"agentLearning"`
}

// QuestRetrospectiveObservation — одно наблюдение и его источник: ход
// Мастера (дефект), решение человека, шлюз проверки или прогон агента.
type QuestRetrospectiveObservation struct {
	Source    string    `json:"source"` // master | human | gate | agent
	Kind      string    `json:"kind"`
	Outcome   string    `json:"outcome,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
}

// QuestRetrospectiveMasterJob — задание обучения методики Мастера, для
// которого ходы этого квеста стали примерами.
type QuestRetrospectiveMasterJob struct {
	JobID   string `json:"jobId"`
	SkillID string `json:"skillId"`
	Phase   string `json:"phase"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
}

// QuestRetrospectiveImprovement — что выучил агент по прогону этого квеста и
// чем закончилась проверка: канарейка, откат, доказательство.
type QuestRetrospectiveImprovement struct {
	ID                string   `json:"id"`
	ProjectAgentID    string   `json:"projectAgentId"`
	Kind              string   `json:"kind"`
	Status            string   `json:"status"`
	SkillID           string   `json:"skillId,omitempty"`
	SkillName         string   `json:"skillName,omitempty"`
	Instruction       string   `json:"instruction,omitempty"`
	Canary            string   `json:"canary,omitempty"`
	CanaryReasons     []string `json:"canaryReasons,omitempty"`
	Effect            string   `json:"effect,omitempty"`
	Failure           string   `json:"failure,omitempty"`
	RollbackAvailable bool     `json:"rollbackAvailable"`
}

func (a *App) QuestRetrospective(ctx context.Context, questID string) (QuestRetrospective, error) {
	quest, err := a.WorkOrderQuestV2(ctx, strings.TrimSpace(questID))
	if err != nil {
		return QuestRetrospective{}, err
	}
	if err = a.guardWorld(quest.WorkspaceID); err != nil {
		return QuestRetrospective{}, err
	}
	approval, err := a.store.WorkOrderApprovalByQuestV2(ctx, quest.ID)
	if err != nil {
		return QuestRetrospective{}, err
	}
	ws := quest.WorkspaceID
	result := QuestRetrospective{QuestID: quest.ID, Status: quest.Status,
		Observations: []QuestRetrospectiveObservation{}, MasterLearning: []QuestRetrospectiveMasterJob{}, AgentLearning: []QuestRetrospectiveImprovement{}}

	// Ходы Мастера, которые готовили этот квест: приём задания по предложению
	// и планирование по квесту.
	ops, err := a.store.MasterOperations(ctx, ws)
	if err != nil {
		return QuestRetrospective{}, err
	}
	opIDs := map[string]bool{}
	for _, op := range ops {
		if op.QuestID != quest.ID && (approval.WorkOrder.ProposalID == "" || op.ProposalID != approval.WorkOrder.ProposalID) {
			continue
		}
		opIDs[op.ID] = true
		for _, defect := range op.Defects {
			result.Observations = append(result.Observations, QuestRetrospectiveObservation{
				Source: "master", Kind: op.Phase + "/" + defect.Kind, Detail: defect.Reason, CreatedAt: op.CreatedAt})
		}
		if op.Feedback == "down" {
			result.Observations = append(result.Observations, QuestRetrospectiveObservation{
				Source: "human", Kind: op.Phase + "/feedback", Outcome: "down", CreatedAt: op.CreatedAt})
		}
	}
	signals, err := a.store.MasterEvidence(ctx, domain.MasterOperation{WorkspaceID: ws, ProposalID: approval.WorkOrder.ProposalID, QuestID: quest.ID})
	if err != nil {
		return QuestRetrospective{}, err
	}
	for _, signal := range signals {
		result.Observations = append(result.Observations, QuestRetrospectiveObservation{
			Source: retrospectiveSignalSource(signal.Kind), Kind: signal.Kind, Outcome: signal.Outcome, Detail: signal.Detail, CreatedAt: signal.CreatedAt})
	}
	jobs, err := a.store.MasterLearningJobs(ctx, ws)
	if err != nil {
		return QuestRetrospective{}, err
	}
	for _, job := range jobs {
		if slices.ContainsFunc(job.ExampleIDs, func(id string) bool { return opIDs[id] }) {
			result.MasterLearning = append(result.MasterLearning, QuestRetrospectiveMasterJob{
				JobID: job.ID, SkillID: job.SkillID, Phase: job.Phase, Status: job.Status, Reason: job.Reason})
		}
	}

	// Прогоны агентов квеста и его этапов Flow.
	questIDs := map[string]bool{quest.ID: true}
	if quests, listErr := a.store.ListQuests(ctx, ws); listErr == nil {
		for _, candidate := range quests {
			if candidate.ParentID == quest.ID {
				questIDs[candidate.ID] = true
			}
		}
	}
	executions, err := a.store.ListExecutions(ctx, ws, 0)
	if err != nil {
		return QuestRetrospective{}, err
	}
	runIDs := map[string]bool{}
	for _, execution := range executions {
		if questIDs[execution.QuestID] && strings.TrimSpace(execution.RunID) != "" {
			runIDs[execution.RunID] = true
		}
	}
	for runID := range runIDs {
		runSignals, signalErr := a.store.ListLearningSignalsForRun(ctx, runID)
		if signalErr != nil {
			return QuestRetrospective{}, signalErr
		}
		for _, signal := range runSignals {
			result.Observations = append(result.Observations, QuestRetrospectiveObservation{
				Source: "agent", Kind: string(signal.Kind), Outcome: signal.Status, Detail: signal.Summary, CreatedAt: signal.CreatedAt})
		}
	}
	improvements, err := a.store.ListAgentImprovements(ctx, ws, 1000)
	if err != nil {
		return QuestRetrospective{}, err
	}
	rollback := learningRollbackAvailability(improvements)
	for _, item := range improvements {
		if !runIDs[item.SourceRunID] {
			continue
		}
		entry := QuestRetrospectiveImprovement{ID: item.ID, ProjectAgentID: item.ProjectAgentID, Kind: item.Kind, Status: item.Status,
			SkillID: item.SkillID, Instruction: item.Instruction, Effect: item.Effect, Failure: item.Failure, RollbackAvailable: rollback[item.ID]}
		if item.AfterSkill != nil {
			entry.SkillName = item.AfterSkill.Name
		}
		if item.CanaryEvaluation != nil {
			entry.Canary = item.CanaryEvaluation.Status
			entry.CanaryReasons = append([]string(nil), item.CanaryEvaluation.Reasons...)
		}
		result.AgentLearning = append(result.AgentLearning, entry)
	}
	sort.SliceStable(result.Observations, func(i, j int) bool {
		return result.Observations[i].CreatedAt.Before(result.Observations[j].CreatedAt)
	})
	return result, nil
}

func retrospectiveSignalSource(kind string) string {
	switch kind {
	case "evidence_gate":
		return "gate"
	default:
		return "human"
	}
}
