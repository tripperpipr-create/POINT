// Сборка навыка из разобранного прогона: ревизии, подпись, доказательства.
package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

func buildLearnedSkill(review learningReview, previous *domain.SkillDefinition, agent domain.ProjectAgent, blueprint *domain.AgentBlueprint, ownerID, ownerKind, signature string, tools, sourceRuns, sourceWorkspaces, sourceAgents []string, revision int, now time.Time, recovery bool) (domain.SkillDefinition, string) {
	allowed := make(map[string]bool, len(agent.AllowedTools))
	for _, tool := range agent.AllowedTools {
		allowed[tool] = true
	}
	required := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool != "read_skill" && allowed[tool] && !slices.Contains(required, tool) {
			required = append(required, tool)
		}
	}
	promotionStatus := "project_only"
	promotionReason := "no_blueprint"
	if blueprint != nil && stringSetContainsAll(blueprint.AllowedTools, required) {
		promotionStatus = "candidate"
		promotionReason = "awaiting_canary_outcomes"
	} else if blueprint != nil {
		promotionReason = "requires_project_tools"
	}
	config := map[string]any{
		"managedBy": agentLearningManagedBy, "ownerId": ownerID, "ownerKind": ownerKind,
		"blueprintId": agent.BlueprintID, "agentId": agent.ID, "workspaceId": agent.WorkspaceID,
		"signature": signature, "sourceRuns": sourceRuns, "revision": revision,
		"sourceWorkspaces": sourceWorkspaces, "sourceAgents": sourceAgents, "workflowTools": tools,
		"lastSourceRunId": sourceRuns[len(sourceRuns)-1], "autoApplied": true,
		"promotionStatus": promotionStatus, "promotionReason": promotionReason, "promotionWorkspaceCount": len(sourceWorkspaces),
	}
	instructions := review.Instructions
	if !recovery {
		if previous != nil {
			instructions = review.Instructions + fmt.Sprintf("\n\nПодтверждено системой на %d успешных запусках.", len(sourceRuns))
		} else {
			instructions = review.Instructions + "\n\nПодтверждено системой на 1 успешном запуске."
		}
	}
	if previous != nil {
		copy := *previous
		familyID := fmt.Sprint(previous.Configuration["familyId"])
		if familyID == "" {
			familyID = previous.ID
		}
		copy.ID = fmt.Sprintf("skill-learned-%s-r%d", signature[:12], revision)
		copy.CreatedAt = now
		copy.Description = review.Description
		copy.Instructions = instructions
		copy.RequiredTools = required
		copy.PermissionDelta = map[string]domain.ToolPolicy{}
		copy.References = nil
		copy.Scripts = nil
		config["familyId"] = familyID
		config["supersedesSkillId"] = previous.ID
		copy.Configuration = config
		copy.UpdatedAt = now
		return copy, promotionStatus
	}
	name := truncateRunes(review.Name, 100) + " · " + signature[:6]
	return domain.SkillDefinition{
		ID: "skill-learned-" + signature[:16], Name: name, Description: review.Description,
		Instructions:  instructions,
		RequiredTools: required, PermissionDelta: map[string]domain.ToolPolicy{}, Configuration: config,
		CreatedAt: now, UpdatedAt: now,
	}, promotionStatus
}

func nextLearningEvidence(previous *domain.SkillDefinition, runID, workspaceID, agentID string) (int, []string, []string, []string) {
	revision := 1
	sourceRuns, sourceWorkspaces, sourceAgents := make([]string, 0, 20), make([]string, 0, 20), make([]string, 0, 20)
	if previous != nil {
		if value, ok := previous.Configuration["revision"].(float64); ok {
			revision = int(value) + 1
		} else if value, ok := previous.Configuration["revision"].(int); ok {
			revision = value + 1
		}
		sourceRuns = configurationStrings(previous.Configuration, "sourceRuns")
		sourceWorkspaces = configurationStrings(previous.Configuration, "sourceWorkspaces")
		sourceAgents = configurationStrings(previous.Configuration, "sourceAgents")
		if len(sourceWorkspaces) == 0 {
			sourceWorkspaces = appendUniqueString(sourceWorkspaces, fmt.Sprint(previous.Configuration["workspaceId"]))
		}
		if len(sourceAgents) == 0 {
			sourceAgents = appendUniqueString(sourceAgents, fmt.Sprint(previous.Configuration["agentId"]))
		}
	}
	sourceRuns = appendUniqueString(sourceRuns, runID)
	sourceWorkspaces = appendUniqueString(sourceWorkspaces, workspaceID)
	sourceAgents = appendUniqueString(sourceAgents, agentID)
	if len(sourceRuns) > 20 {
		sourceRuns = sourceRuns[len(sourceRuns)-20:]
	}
	return revision, sourceRuns, sourceWorkspaces, sourceAgents
}

func managedSkillMatches(skill domain.SkillDefinition, ownerID, signature string) bool {
	configuredOwner := fmt.Sprint(skill.Configuration["ownerId"])
	if configuredOwner == "" {
		configuredOwner = fmt.Sprint(skill.Configuration["agentId"])
	}
	return fmt.Sprint(skill.Configuration["managedBy"]) == agentLearningManagedBy &&
		fmt.Sprint(skill.Configuration["promotionStatus"]) != "rolled_back" &&
		configuredOwner == ownerID && fmt.Sprint(skill.Configuration["signature"]) == signature
}

func learningSignature(agentID string, tools []string) string {
	copy := append([]string(nil), tools...)
	// The order is retained; only whitespace/case are normalized. Different
	// verified workflows remain separate while repeat runs patch one skill.
	for index := range copy {
		copy[index] = strings.ToLower(strings.TrimSpace(copy[index]))
	}
	hash := sha256.Sum256([]byte(agentID + "\x00" + strings.Join(copy, "\x00")))
	return hex.EncodeToString(hash[:])
}

func configurationStrings(configuration map[string]any, key string) []string {
	result := make([]string, 0)
	switch values := configuration[key].(type) {
	case []any:
		for _, value := range values {
			result = appendUniqueString(result, fmt.Sprint(value))
		}
	case []string:
		for _, value := range values {
			result = appendUniqueString(result, value)
		}
	}
	return result
}

func appendUniqueString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value != "" && !slices.Contains(values, value) {
		return append(values, value)
	}
	return values
}

func stringSetContainsAll(have, required []string) bool {
	set := make(map[string]bool, len(have))
	for _, value := range have {
		set[value] = true
	}
	for _, value := range required {
		if !set[value] {
			return false
		}
	}
	return true
}

func allowedLearningTools(tools, allowed []string) []string {
	result := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool != "read_skill" && slices.Contains(allowed, tool) {
			result = appendUniqueString(result, tool)
		}
	}
	return result
}

func learningPromotionEvidence(status, reason string, workspaceCount int) string {
	switch status {
	case "promoted":
		return fmt.Sprintf("универсальный skill · подтверждён независимых проектов: %d", workspaceCount)
	case "candidate":
		return fmt.Sprintf("canary-кандидат · исходных проектов: %d; ждёт 3 реальных запуска минимум в 2 проектах", workspaceCount)
	default:
		if reason == "no_blueprint" {
			return "только этот проект · у агента нет постоянного Blueprint"
		}
		return "только этот проект · процедура требует проектного профиля tools"
	}
}
