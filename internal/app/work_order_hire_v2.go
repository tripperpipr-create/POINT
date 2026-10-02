package app

import (
	"context"
	"errors"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/storage"
)

type HireWorkOrderAgentV2Request struct {
	DraftID         string              `json:"draftId"`
	ExpectedVersion int                 `json:"expectedVersion"`
	ExpectedDigest  string              `json:"expectedDigest"`
	IdempotencyKey  string              `json:"idempotencyKey"`
	AgentID         string              `json:"agentId,omitempty"`
	Agent           domain.ProjectAgent `json:"agent,omitempty"`
}

func (a *App) HireWorkOrderAgentV2(ctx context.Context, orderID string, req HireWorkOrderAgentV2Request) (storage.WorkOrderHireResult, error) {
	if strings.TrimSpace(orderID) == "" || strings.TrimSpace(req.DraftID) == "" || req.ExpectedVersion <= 0 || strings.TrimSpace(req.ExpectedDigest) == "" || strings.TrimSpace(req.IdempotencyKey) == "" {
		return storage.WorkOrderHireResult{}, errors.New("для найма нужны наряд, черновик, версия, digest и ключ повтора")
	}
	order, err := a.store.GetWorkOrderV2(ctx, orderID)
	if err != nil {
		return storage.WorkOrderHireResult{}, err
	}
	if order.WorkspaceID != a.masterWorldID(ctx) {
		return storage.WorkOrderHireResult{}, errors.New("наряд принадлежит другому проекту")
	}
	requestHash := storage.WorkOrderHireRequestHash(req.DraftID, req.AgentID, req.Agent)
	if replay, found, replayErr := a.store.WorkOrderHireReplayV2(ctx, orderID, req.DraftID, req.IdempotencyKey, req.ExpectedVersion, req.ExpectedDigest, requestHash); replayErr != nil || found {
		return replay, replayErr
	}
	var agent domain.ProjectAgent
	var blueprint *domain.AgentBlueprint
	if req.AgentID != "" {
		agent, err = a.store.GetProjectAgent(ctx, req.AgentID)
		if err != nil {
			return storage.WorkOrderHireResult{}, err
		}
		if agent.WorkspaceID != order.WorkspaceID || agent.Temporary || agent.Status != domain.ProjectAgentActive {
			return storage.WorkOrderHireResult{}, errors.New("существующий исполнитель недоступен для наряда")
		}
	} else {
		if req.Agent.ID != "" {
			return storage.WorkOrderHireResult{}, errors.New("нового исполнителя нельзя создать с готовым id")
		}
		agent, err = a.prepareProjectAgentContext(ctx, req.Agent)
		if err != nil {
			return storage.WorkOrderHireResult{}, err
		}
		if agent.Temporary || agent.Status != domain.ProjectAgentActive {
			return storage.WorkOrderHireResult{}, errors.New("нужен постоянный активный исполнитель")
		}
		if agent.BlueprintID == "" {
			b := domain.AgentBlueprint{ID: domain.NewID("blueprint"), Name: agent.Name, RoleDescription: agent.RoleDescription, Personality: agent.Personality, Mission: agent.Mission, SystemPrompt: agent.SystemPrompt, Goals: agent.Goals, Rules: agent.Rules, Constraints: agent.Constraints, SkillIDs: agent.SkillIDs, AllowedTools: agent.AllowedTools, ToolPolicies: agent.ToolPolicies, ConnectionID: agent.ConnectionID, Provider: agent.Provider, ProviderPreset: agent.ProviderPreset, BaseURL: agent.BaseURL, PrimaryModel: agent.PrimaryModel, FallbackModels: agent.FallbackModels, Temperature: agent.Temperature, MaxOutputTokens: agent.MaxOutputTokens, ContextWindowTokens: agent.ContextWindowTokens, ReasoningEffort: agent.ReasoningEffort, MaxSteps: agent.MaxSteps, MaxDurationSeconds: agent.MaxDurationSeconds, ApprovalMode: agent.ApprovalMode, CreatedAt: agent.CreatedAt, UpdatedAt: agent.UpdatedAt}
			agent.BlueprintID = b.ID
			blueprint = &b
		}
	}
	runnable := a.projectAgentReadiness(ctx, agent).State != "BLOCKED"
	for _, member := range order.Roster.Permanent {
		if !member.Existing || member.ID == agent.ID {
			continue
		}
		other, getErr := a.store.GetProjectAgent(ctx, member.ID)
		if getErr != nil || a.projectAgentReadiness(ctx, other).State == "BLOCKED" {
			runnable = false
		}
	}
	return a.store.HireWorkOrderAgentV2(ctx, orderID, req.DraftID, req.IdempotencyKey, req.ExpectedVersion, req.ExpectedDigest, requestHash, agent, blueprint, req.AgentID != "", runnable)
}
