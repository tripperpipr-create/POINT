package domain

import (
	"encoding/json"
	"time"
)

// MasterReplayFormat — версия записи реплея хода Мастера. Формат 2 — ответ
// текстом плюс инструменты разговора; прежние записи ждали JSON-конверт в
// тексте, и сравнивать их с нынешним промптом значит судить методику по
// чужому контракту. Такие записи обучением пропускаются.
const MasterReplayFormat = 2

type MasterReplay struct {
	Format  int             `json:"format"`
	Request json.RawMessage `json:"request"`
}

// DecodeMasterReplay достаёт сохранённый запрос, если он записан нынешним
// форматом хода.
func DecodeMasterReplay(raw string) (json.RawMessage, bool) {
	var replay MasterReplay
	if json.Unmarshal([]byte(raw), &replay) != nil || replay.Format != MasterReplayFormat || len(replay.Request) == 0 {
		return nil, false
	}
	return replay.Request, true
}

type MasterLearningConfig struct {
	Enabled bool `json:"enabled"`
}

type MasterEvidenceSignal struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspaceId"`
	ProposalID  string    `json:"proposalId,omitempty"`
	QuestID     string    `json:"questId,omitempty"`
	Kind        string    `json:"kind"`
	Outcome     string    `json:"outcome"`
	CreatedAt   time.Time `json:"createdAt"`
}

// MasterOperation is not an executor Run. Only attributed completed operations
// are eligible as evidence; provider failures are recorded independently.
type MasterOperation struct {
	ID            string             `json:"id"`
	WorkspaceID   string             `json:"workspaceId"`
	TurnID        string             `json:"turnId,omitempty"`
	ProposalID    string             `json:"proposalId,omitempty"`
	QuestID       string             `json:"questId,omitempty"`
	FlowID        string             `json:"flowId,omitempty"`
	Phase         string             `json:"phase"`
	Skills        []SkillAttribution `json:"skills"`
	InputTokens   int64              `json:"inputTokens"`
	OutputTokens  int64              `json:"outputTokens"`
	ProviderError bool               `json:"providerError,omitempty"`
	ContractError bool               `json:"contractError,omitempty"`
	Repairs       int                `json:"repairs,omitempty"`
	Feedback      string             `json:"feedback,omitempty"`
	// Replay is sanitized, project-local and never included in a library API.
	Replay    string    `json:"replay,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type MasterSkillRevision struct {
	ID          string          `json:"id"`
	Skill       SkillDefinition `json:"skill"`
	Digest      string          `json:"digest"`
	ParentID    string          `json:"parentId,omitempty"`
	WorkspaceID string          `json:"workspaceId,omitempty"`
	Status      string          `json:"status"` // builtin, candidate, canary, local, shared, rejected, rolled_back
	Reason      string          `json:"reason,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
}

type MasterSkillEvaluation struct {
	Scenario        string `json:"scenario"`
	BaselineScore   int    `json:"baselineScore"`
	CandidateScore  int    `json:"candidateScore"`
	BaselineTokens  int64  `json:"baselineTokens"`
	CandidateTokens int64  `json:"candidateTokens"`
	Passed          bool   `json:"passed"`
	DefectFixed     bool   `json:"defectFixed"`
}

type MasterLearningJob struct {
	Model       string                  `json:"model,omitempty"`
	Provider    ProviderKind            `json:"provider,omitempty"`
	ID          string                  `json:"id"`
	WorkspaceID string                  `json:"workspaceId"`
	Phase       string                  `json:"phase"`
	SkillID     string                  `json:"skillId"`
	BaselineID  string                  `json:"baselineId"`
	CandidateID string                  `json:"candidateId,omitempty"`
	ExampleIDs  []string                `json:"exampleIds"`
	Status      string                  `json:"status"` // queued, running, deferred, rejected, canary, local, shared, rolled_back
	Reason      string                  `json:"reason,omitempty"`
	Evaluations []MasterSkillEvaluation `json:"evaluations,omitempty"`
	UpdatedAt   time.Time               `json:"updatedAt"`
}

type MasterLearningBudget struct {
	MainTokens     int64 `json:"mainTokens"`
	LimitTokens    int64 `json:"limitTokens"`
	SpentTokens    int64 `json:"spentTokens"`
	ReservedTokens int64 `json:"reservedTokens"`
}
