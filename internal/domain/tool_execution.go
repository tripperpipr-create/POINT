package domain

import (
	"encoding/json"
	"time"
)

type ToolExecutionApprovalStatus string

const (
	ToolExecutionApprovalPending  ToolExecutionApprovalStatus = "pending"
	ToolExecutionApprovalAllowed  ToolExecutionApprovalStatus = "allowed"
	ToolExecutionApprovalDenied   ToolExecutionApprovalStatus = "denied"
	ToolExecutionApprovalConsumed ToolExecutionApprovalStatus = "consumed"
)

// ToolExecutionApproval is a durable, one-time grant for a manual custom-tool
// invocation. Digests bind the decision to one workspace, immutable tool
// definition and exact argument bytes without persisting unredacted arguments.
type ToolExecutionApproval struct {
	ID              string                      `json:"id"`
	WorkspaceID     string                      `json:"workspaceId"`
	ToolID          string                      `json:"toolId"`
	ToolDigest      string                      `json:"toolDigest"`
	ArgumentsDigest string                      `json:"argumentsDigest"`
	Arguments       json.RawMessage             `json:"arguments"`
	Reason          string                      `json:"reason"`
	Status          ToolExecutionApprovalStatus `json:"status"`
	CreatedAt       time.Time                   `json:"createdAt"`
	ExpiresAt       time.Time                   `json:"expiresAt"`
	ResolvedAt      *time.Time                  `json:"resolvedAt,omitempty"`
	ConsumedAt      *time.Time                  `json:"consumedAt,omitempty"`
}
