package domain

import "time"

// AgentBenchmarkSet is a personal, versioned set of real tasks for one
// permanent project agent. Digest identifies the criteria, not the results.
type AgentBenchmarkSet struct {
	ID             string               `json:"id"`
	WorkspaceID    string               `json:"workspaceId"`
	ProjectAgentID string               `json:"projectAgentId"`
	SkillID        string               `json:"skillId,omitempty"`
	Name           string               `json:"name"`
	Description    string               `json:"description,omitempty"`
	Revision       int                  `json:"revision"`
	Digest         string               `json:"digest"`
	Cases          []AgentBenchmarkCase `json:"cases"`
	CreatedAt      time.Time            `json:"createdAt"`
	UpdatedAt      time.Time            `json:"updatedAt"`
}

type AgentBenchmarkCase struct {
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Task                string    `json:"task"`
	ExpectedStatus      RunStatus `json:"expectedStatus"`
	RequireHealthy      bool      `json:"requireHealthy"`
	RequireVerification bool      `json:"requireVerification"`
	MaximumToolFailures int       `json:"maximumToolFailures"`
}

// AgentBenchmarkEvaluation records how a fixed set revision performed on
// exact existing Runs. It stores raw counts and per-case reasons instead of a
// composite score.
type AgentBenchmarkEvaluation struct {
	ID             string                      `json:"id"`
	WorkspaceID    string                      `json:"workspaceId"`
	ProjectAgentID string                      `json:"projectAgentId"`
	BenchmarkSetID string                      `json:"benchmarkSetId"`
	SetRevision    int                         `json:"setRevision"`
	SetDigest      string                      `json:"setDigest"`
	Label          string                      `json:"label"`
	Cases          []AgentBenchmarkCaseOutcome `json:"cases"`
	Metrics        AgentBenchmarkMetrics       `json:"metrics"`
	CreatedAt      time.Time                   `json:"createdAt"`
}

type AgentBenchmarkCaseOutcome struct {
	CaseID               string             `json:"caseId"`
	CaseName             string             `json:"caseName"`
	RunID                string             `json:"runId"`
	ConfigurationDigest  string             `json:"configurationDigest"`
	ProfileDigest        string             `json:"profileDigest"`
	SkillAttributions    []SkillAttribution `json:"skillAttributions"`
	RunStatus            RunStatus          `json:"runStatus"`
	Health               string             `json:"health"`
	ToolCalls            int                `json:"toolCalls"`
	ToolFailures         int                `json:"toolFailures"`
	VerificationRequired bool               `json:"verificationRequired"`
	VerificationRecorded bool               `json:"verificationRecorded"`
	Passed               bool               `json:"passed"`
	Reasons              []string           `json:"reasons"`
}

type AgentBenchmarkMetrics struct {
	Cases                int `json:"cases"`
	Passed               int `json:"passed"`
	Completed            int `json:"completed"`
	Healthy              int `json:"healthy"`
	ToolCalls            int `json:"toolCalls"`
	ToolFailures         int `json:"toolFailures"`
	VerificationRequired int `json:"verificationRequired"`
	VerificationRecorded int `json:"verificationRecorded"`
}

type AgentBenchmarkComparison struct {
	BenchmarkSetID string                `json:"benchmarkSetId"`
	SetRevision    int                   `json:"setRevision"`
	SetDigest      string                `json:"setDigest"`
	BeforeID       string                `json:"beforeId"`
	AfterID        string                `json:"afterId"`
	Before         AgentBenchmarkMetrics `json:"before"`
	After          AgentBenchmarkMetrics `json:"after"`
	RegressedCases []string              `json:"regressedCases"`
	RecoveredCases []string              `json:"recoveredCases"`
	GatePassed     bool                  `json:"gatePassed"`
	Reasons        []string              `json:"reasons"`
	ComparedAt     time.Time             `json:"comparedAt"`
}
