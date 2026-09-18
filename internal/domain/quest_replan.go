package domain

import "time"

// QuestReplan records one governed plan revision for a structured quest.
type QuestReplan struct {
	ID           string    `json:"id"`
	WorkspaceID  string    `json:"workspaceId"`
	QuestID      string    `json:"questId"`
	FlowID       string    `json:"flowId"`
	FlowRunID    string    `json:"flowRunId,omitempty"`
	Seq          int       `json:"seq"`
	Reason       string    `json:"reason"`
	CriterionIDs []string  `json:"criterionIds,omitempty"`
	StageDigests []string  `json:"stageDigests,omitempty"`
	StoppedRuns  []string  `json:"stoppedRuns,omitempty"`
	UpdatedNodes []string  `json:"updatedNodes,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

// ReplanStagePatch updates one unstarted (or stopped) agent stage.
type ReplanStagePatch struct {
	NodeID      string `json:"nodeId"`
	AgentID     string `json:"agentId,omitempty"`
	Instruction string `json:"instruction,omitempty"`
	Name        string `json:"name,omitempty"`
}
