package domain

import (
	"encoding/json"
	"time"
)

// VerificationResult — исход прогона машинных критериев наряда на конкретном
// дереве в конкретном образе. Его записывает сам Point (проверка перед
// приёмкой и приёмка), а не агент: только такой результат приёмка вправе
// переиспользовать, и только при совпавшем ключе.
type VerificationResult struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	QuestID     string `json:"questId"`
	FlowRunID   string `json:"flowRunId,omitempty"`
	ExecutionID string `json:"executionId,omitempty"`
	RunID       string `json:"runId,omitempty"`
	// BatchKey связывает дерево, образ, исполненные команды и сеть. Любое
	// отличие даёт другой ключ, и результат не находится.
	BatchKey    string `json:"batchKey"`
	TreeDigest  string `json:"treeDigest"`
	ImageDigest string `json:"imageDigest"`
	// Source — кто гонял: "pre_accept" (проверка перед приёмкой) или "accept".
	Source    string          `json:"source"`
	AllPassed bool            `json:"allPassed"`
	Evidence  json.RawMessage `json:"evidence"`
	CreatedAt time.Time       `json:"createdAt"`
}

// WorkOrderPreAcceptCheck — сводка проверки Point перед приёмкой для карточки
// наряда: сколько машинных проверок запускалось и сколько прошло.
type WorkOrderPreAcceptCheck struct {
	Passed    int       `json:"passed"`
	Total     int       `json:"total"`
	AllPassed bool      `json:"allPassed"`
	At        time.Time `json:"at"`
}
