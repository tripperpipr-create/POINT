package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/egress"
	"local-agent-workbench/internal/policy"
)

// Действующие права агента (Q05, этап 1). Решение о вызове инструмента
// складывается из четырёх мест: выдача профиля (policy.ProfileGrants), сужение
// утверждённым заданием (agent.RestrictTaskProfile), политика
// (policy.Engine.Evaluate) и автоподтверждение задания в сильной песочнице
// (agent.TaskAutoApproves). Здесь они не пересказываются, а зовутся те же
// функции, что у исполнения: иначе показанное разойдётся с исполняемым молча.

// Итог по инструменту.
const (
	EffectAllow         = "allow"
	EffectAsk           = "ask"
	EffectDeny          = "deny"
	EffectNotGranted    = "not_granted"
	EffectExcludedByJob = "excluded_by_task"
)

type EffectiveToolPermission struct {
	Tool        string                `json:"tool"`
	DisplayName string                `json:"displayName,omitempty"`
	Risk        domain.ToolRisk       `json:"risk"`
	Effect      string                `json:"effect"`
	Granted     bool                  `json:"granted"`
	ExcludedBy  string                `json:"excludedBy,omitempty"`
	Policy      domain.ToolPolicy     `json:"policy,omitempty"`
	Source      policy.DecisionSource `json:"source,omitempty"`
	// AutoApproved — вызов, которому политика велит спросить, исполняется без
	// окна по праву утверждённого задания в сильной песочнице.
	AutoApproved bool   `json:"autoApproved,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type EffectiveNetwork struct {
	Mode   string   `json:"mode"`
	Allow  []string `json:"allow"`
	Deny   []string `json:"deny"`
	Source string   `json:"source"`
	Error  string   `json:"error,omitempty"`
}

type EffectivePermissions struct {
	WorkspaceID       string                    `json:"workspaceId"`
	AgentID           string                    `json:"agentId"`
	QuestID           string                    `json:"questId,omitempty"`
	TaskBriefApplied  bool                      `json:"taskBriefApplied"`
	TaskBriefVersion  int                       `json:"taskBriefVersion,omitempty"`
	TaskPermissions   *domain.TaskPermissions   `json:"taskPermissions,omitempty"`
	ApprovalMode      domain.ApprovalMode       `json:"approvalMode,omitempty"`
	StrongSandbox     bool                      `json:"strongSandbox"`
	Tools             []EffectiveToolPermission `json:"tools"`
	Network           EffectiveNetwork          `json:"network"`
}

// EffectivePermissions считает права агента текущего проекта, а с questId —
// под утверждённым заданием этого квеста.
func (a *App) EffectivePermissions(ctx context.Context, agentID, questID string) (EffectivePermissions, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return EffectivePermissions{}, err
	}
	projectAgent, err := a.store.GetProjectAgent(ctx, strings.TrimSpace(agentID))
	if err != nil {
		return EffectivePermissions{}, err
	}
	if projectAgent.WorkspaceID != ws.ID {
		return EffectivePermissions{}, errors.New("project agent belongs to another workspace")
	}
	profile := domain.ProfileFromProjectAgent(projectAgent)
	if err = a.enrichProjectAgentForRun(ws.ID, projectAgent, &profile, nil); err != nil {
		return EffectivePermissions{}, err
	}
	var brief *domain.TaskBrief
	if questID = strings.TrimSpace(questID); questID != "" {
		quest, questErr := a.store.GetQuest(ctx, questID)
		if questErr != nil {
			return EffectivePermissions{}, questErr
		}
		if quest.WorkspaceID != ws.ID {
			return EffectivePermissions{}, errors.New("quest belongs to another workspace")
		}
		if quest.Brief != nil && domain.IsTaskBriefApproved(*quest.Brief) {
			brief = quest.Brief
		}
	}
	customTools, err := a.store.ListCustomTools(ctx)
	if err != nil {
		return EffectivePermissions{}, err
	}
	strong := a.sandboxBackend != nil && a.sandboxBackend.Capabilities().StrongOSBoundary
	return effectivePermissionsFor(profile, brief, customTools, strong, policy.Engine{TrustedCustomTool: a.trustedCustomTool}, ws.ID, projectAgent.ID, questID)
}

func effectivePermissionsFor(profile domain.AgentProfile, brief *domain.TaskBrief, customTools []domain.CustomTool, strong bool, engine policy.Engine, workspaceID, agentID, questID string) (EffectivePermissions, error) {
	result := EffectivePermissions{
		WorkspaceID: workspaceID, AgentID: agentID, QuestID: questID,
		ApprovalMode: profile.ApprovalMode, StrongSandbox: strong,
	}
	restricted := profile
	if brief != nil {
		var err error
		if restricted, err = agent.RestrictTaskProfile(profile, brief, customTools); err != nil {
			return EffectivePermissions{}, err
		}
		permissions := brief.Permissions
		result.TaskBriefApplied, result.TaskBriefVersion, result.TaskPermissions = true, brief.Version, &permissions
	}
	customNames := map[string]bool{}
	for _, tool := range customTools {
		customNames[tool.ID] = true
	}
	granted := policy.ProfileGrants(profile)
	running := policy.ProfileGrants(restricted)
	names := map[string]bool{}
	for _, item := range domain.BuiltInToolCatalog() {
		names[item.Name] = true
	}
	for _, name := range profile.AllowedTools {
		names[name] = true
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	for _, name := range ordered {
		entry := EffectiveToolPermission{Tool: name, Risk: policy.RiskForTool(name), Granted: granted.Allows(name)}
		if item, ok := domain.ToolCatalogEntry(name); ok {
			entry.DisplayName = item.DisplayName
		}
		switch {
		case !entry.Granted:
			entry.Effect, entry.Reason = EffectNotGranted, "инструмент не выдан агенту"
		case !running.Allows(name):
			entry.Effect, entry.ExcludedBy = EffectExcludedByJob, agent.TaskToolExclusion(name, brief, customNames)
			entry.Reason = "утверждённое задание убирает инструмент"
		default:
			decision := engine.Evaluate(restricted, name)
			entry.Policy, entry.Source, entry.Reason = decision.Policy, decision.Source, decision.Reason
			switch {
			case decision.Denied:
				entry.Effect = EffectDeny
			case decision.RequiresApproval && agent.TaskAutoApproves(brief, restricted, name, strong):
				entry.Effect, entry.AutoApproved = EffectAllow, true
				entry.Reason = "Разрешено утверждённым заданием внутри Docker sandbox"
			case decision.RequiresApproval:
				entry.Effect = EffectAsk
			default:
				entry.Effect = EffectAllow
			}
		}
		result.Tools = append(result.Tools, entry)
	}
	result.Network = effectiveNetwork(restricted.ToolPolicies, brief != nil)
	return result, nil
}

// effectiveNetwork — сеть песочницы в том виде, в каком её соберёт снимок
// конфигурации прогона (domain.NewRunConfigurationSnapshot).
func effectiveNetwork(policies map[string]string, fromBrief bool) EffectiveNetwork {
	network := EffectiveNetwork{Source: "agent_profile", Allow: []string{}, Deny: []string{}}
	if fromBrief {
		network.Source = "task_brief"
	}
	compiled, err := egress.CompileToolPolicies(policies)
	if err != nil {
		// Исполнение в этом случае закрывает сеть целиком.
		network.Mode, network.Error = "DENY", err.Error()
	} else {
		network.Mode = compiled.Mode
		for _, rule := range compiled.Rules {
			network.Allow = append(network.Allow, fmt.Sprintf("%s:%d/%s", rule.FQDN, rule.Port, rule.Protocol))
		}
	}
	for key, value := range policies {
		if strings.HasPrefix(strings.ToLower(key), "network:") && strings.EqualFold(strings.TrimSpace(value), "DENY") {
			network.Deny = append(network.Deny, strings.TrimSpace(key[len("network:"):]))
		}
	}
	slices.Sort(network.Deny)
	return network
}
