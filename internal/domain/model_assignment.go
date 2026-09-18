package domain

import "time"

// ModelBinding is a quest-scoped immutable choice. It may override an agent's
// default model without mutating the reusable ProjectAgent.
type ModelBinding struct {
	ProjectAgentID       string       `json:"projectAgentId"`
	ConnectionID         string       `json:"connectionId,omitempty"`
	Provider             ProviderKind `json:"provider"`
	Model                string       `json:"model"`
	Runtime              string       `json:"runtime"`
	RequiredCapabilities []string     `json:"requiredCapabilities,omitempty"`
	Reason               string       `json:"reason,omitempty"`
	Source               string       `json:"source"` // master | agent | workspace_default | replan
	EstimatedCostCents   int64        `json:"estimatedCostCents,omitempty"`
	FallbackMode         string       `json:"fallbackMode,omitempty"`
	FallbackModels       []string     `json:"fallbackModels,omitempty"`
}

type ModelCapabilityEvidence struct {
	ID                   string       `json:"id"`
	ConnectionID         string       `json:"connectionId"`
	Model                string       `json:"model"`
	Provider             ProviderKind `json:"provider"`
	Runtime              string       `json:"runtime"`
	Role                 string       `json:"role"`
	Capabilities         []string     `json:"capabilities"`
	ToolCalls            string       `json:"toolCalls"`
	JSONContract         string       `json:"jsonContract"`
	InspectionBeforeEdit string       `json:"inspectionBeforeEdit"`
	VerificationEvidence string       `json:"verificationEvidence"`
	WithinLimits         string       `json:"withinLimits"`
	LatencyMs            int64        `json:"latencyMs"`
	InputTokens          int          `json:"inputTokens"`
	OutputTokens         int          `json:"outputTokens"`
	Healthy              bool         `json:"healthy"`
	CreatedAt            time.Time    `json:"createdAt"`
}

type ModelCandidate struct {
	ConnectionID  string                    `json:"connectionId"`
	Provider      ProviderKind              `json:"provider"`
	Model         string                    `json:"model"`
	Runtime       string                    `json:"runtime"`
	Capabilities  []string                  `json:"capabilities,omitempty"`
	ContextWindow int                       `json:"contextWindow,omitempty"`
	MaxOutput     int                       `json:"maxOutput,omitempty"`
	Healthy       bool                      `json:"healthy"`
	HealthSource  string                    `json:"healthSource"`
	LatencyMs     int64                     `json:"latencyMs,omitempty"`
	PricingKnown  bool                      `json:"pricingKnown"`
	Certification string                    `json:"certification"`
	Experimental  bool                      `json:"experimental"`
	Adapter       AdapterCapabilityManifest `json:"adapter"`
}

// PlanStagePreview is the bounded contract shown before or after Master compiles a wave.
type PlanStagePreview struct {
	Name               string   `json:"name"`
	AgentID            string   `json:"agentId"`
	Runtime            string   `json:"runtime,omitempty"`
	Model              string   `json:"model,omitempty"`
	ConnectionID       string   `json:"connectionId,omitempty"`
	EstimatedCostCents int64    `json:"estimatedCostCents,omitempty"`
	PricingKnown       bool     `json:"pricingKnown"`
	OwnedPaths         []string `json:"ownedPaths,omitempty"`
	CriterionIDs       []string `json:"criterionIds,omitempty"`
	MergePlan          string   `json:"mergePlan,omitempty"`
	Reason             string   `json:"reason,omitempty"`
}
