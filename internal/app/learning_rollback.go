// Откат улучшения: возврат привязок и отключение неиспользуемых копий навыка.
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
)

func (a *App) failAgentImprovement(ctx context.Context, item domain.AgentImprovement, cause error) (domain.AgentImprovement, error) {
	// Compensate every partial binding before publishing failure. A definition
	// created by the failed attempt can stay in the catalog, but no agent or
	// blueprint will reference it and its project instances are disabled.
	_ = a.restoreImprovementBindings(ctx, item)
	item.Status = "failed"
	item.Failure = truncateRunes(security.Redact(cause.Error()), 1000)
	item.UpdatedAt = time.Now().UTC()
	_ = a.store.SaveAgentImprovement(ctx, item)
	return item, cause
}

func (a *App) restoreImprovementBindings(ctx context.Context, item domain.AgentImprovement) error {
	if item.BeforeSkill != nil {
		if err := a.store.SaveSkill(ctx, *item.BeforeSkill); err != nil {
			return err
		}
	}
	if (item.MemoryStatus == "promoted" || item.MemoryStatus == "confirmed") && item.MemoryID != "" {
		if item.BeforeMemory != nil {
			if err := a.store.SaveMemory(ctx, *item.BeforeMemory); err != nil {
				return err
			}
		} else {
			workspaceID := ""
			if item.AfterMemory != nil {
				workspaceID = item.AfterMemory.WorkspaceID
			}
			if err := a.store.DeleteMemory(ctx, workspaceID, item.MemoryID); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
	}
	now := time.Now().UTC()
	if (item.PromotionStatus == "promoted" || item.InstructionStatus == "promoted") && item.BlueprintID != "" {
		blueprint, err := a.store.GetBlueprint(ctx, item.BlueprintID)
		if err != nil {
			return err
		}
		if item.PromotionStatus == "promoted" {
			blueprint.SkillIDs = append([]string(nil), item.BeforeBlueprintSkillIDs...)
		}
		if item.InstructionStatus == "promoted" {
			blueprint.Rules = append([]string(nil), item.BeforeBlueprintRules...)
		}
		blueprint.UpdatedAt = now
		if err = a.store.SaveBlueprint(ctx, blueprint); err != nil {
			return err
		}
	}

	beforeSkills := item.BeforeAgentSkillIDs
	if len(beforeSkills) == 0 && item.ProjectAgentID != "" {
		beforeSkills = map[string][]string{item.ProjectAgentID: append([]string(nil), item.BeforeSkillIDs...)}
	}
	agentIDs := map[string]bool{}
	for agentID := range beforeSkills {
		agentIDs[agentID] = true
	}
	for agentID := range item.BeforeAgentRules {
		agentIDs[agentID] = true
	}
	for agentID := range agentIDs {
		agent, err := a.store.GetProjectAgent(ctx, agentID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if skillIDs, ok := beforeSkills[agentID]; ok {
			agent.SkillIDs = append([]string(nil), skillIDs...)
		}
		if rules, ok := item.BeforeAgentRules[agentID]; ok {
			agent.Rules = append([]string(nil), rules...)
		}
		agent.UpdatedAt = now
		if err = a.store.SaveProjectAgent(ctx, agent); err != nil {
			return err
		}
	}

	// An instance created after promotion inherited the Blueprint changes and
	// is absent from the historical snapshot. Remove those managed additions.
	all, err := a.store.ListAllProjectAgents(ctx)
	if err != nil {
		return err
	}
	if (item.PromotionStatus == "promoted" || item.InstructionStatus == "promoted") && item.BlueprintID != "" {
		for index := range all {
			agent := all[index]
			if agent.BlueprintID != item.BlueprintID {
				continue
			}
			changed := false
			if _, captured := beforeSkills[agent.ID]; item.PromotionStatus == "promoted" && !captured && slices.Contains(agent.SkillIDs, item.SkillID) {
				agent.SkillIDs = withoutString(agent.SkillIDs, item.SkillID)
				changed = true
			}
			if _, captured := item.BeforeAgentRules[agent.ID]; item.InstructionStatus == "promoted" && !captured && slices.Contains(agent.Rules, item.Instruction) {
				agent.Rules = withoutString(agent.Rules, item.Instruction)
				changed = true
			}
			if !changed {
				continue
			}
			agent.UpdatedAt = now
			if err = a.store.SaveProjectAgent(ctx, agent); err != nil {
				return err
			}
		}
		all, err = a.store.ListAllProjectAgents(ctx)
		if err != nil {
			return err
		}
	}
	return a.disableUnusedManagedSkillInstances(ctx, item, all, now)
}

func (a *App) disableUnusedManagedSkillInstances(ctx context.Context, item domain.AgentImprovement, agents []domain.ProjectAgent, now time.Time) error {
	workspaces := map[string]bool{item.WorkspaceID: true}
	used := map[string]bool{}
	for _, agent := range agents {
		if agent.BlueprintID == item.BlueprintID || agent.ID == item.ProjectAgentID {
			workspaces[agent.WorkspaceID] = true
		}
		if slices.Contains(agent.SkillIDs, item.SkillID) {
			used[agent.WorkspaceID] = true
		}
	}
	for workspaceID := range workspaces {
		if used[workspaceID] {
			continue
		}
		instances, err := a.store.ListProjectSkills(ctx, workspaceID)
		if err != nil {
			return err
		}
		for _, instance := range instances {
			if instance.SkillID == item.SkillID && fmt.Sprint(instance.Configuration["managedBy"]) == agentLearningManagedBy {
				instance.Enabled = false
				instance.UpdatedAt = now
				if err = a.store.SaveProjectSkill(ctx, instance); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func withoutString(values []string, remove string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != remove {
			result = append(result, value)
		}
	}
	return result
}

func skillPointer(skill domain.SkillDefinition) *domain.SkillDefinition {
	copy := skill
	return &copy
}

// RollbackAgentImprovement restores only the newest applied autonomous
// revision. Older Skill or Memory snapshots cannot overwrite later learning.
func (a *App) RollbackAgentImprovement(id string) (domain.AgentImprovement, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.AgentImprovement{}, err
	}
	a.learningReviewMu.Lock()
	defer a.learningReviewMu.Unlock()
	ctx := context.Background()
	item, err := a.store.GetAgentImprovement(ctx, strings.TrimSpace(id))
	if err != nil {
		return domain.AgentImprovement{}, err
	}
	if err = a.guardAgentImprovementWorld(ctx, item, ws.ID); err != nil {
		return domain.AgentImprovement{}, err
	}
	return a.rollbackAgentImprovementLocked(ctx, item)
}

// Blueprint-owned improvements are deliberately visible from each project
// containing an instance of that permanent specialist. Unrelated project
// worlds remain unable to mutate the shared rollout.
func (a *App) guardAgentImprovementWorld(ctx context.Context, item domain.AgentImprovement, workspaceID string) error {
	if item.WorkspaceID == workspaceID {
		return nil
	}
	if strings.TrimSpace(item.BlueprintID) == "" {
		return errForeignWorld
	}
	agents, err := a.store.ListAllProjectAgents(ctx)
	if err != nil {
		return err
	}
	for _, agent := range agents {
		if agent.WorkspaceID == workspaceID && agent.BlueprintID == item.BlueprintID {
			return nil
		}
	}
	return errForeignWorld
}

// rollbackAgentImprovementLocked is shared by explicit and automatic
// rollback. Callers must hold learningReviewMu; workspace authorization is
// deliberately kept in the public entry point above.
func (a *App) rollbackAgentImprovementLocked(ctx context.Context, item domain.AgentImprovement) (domain.AgentImprovement, error) {
	if !improvementIsApplied(item.Status) {
		return domain.AgentImprovement{}, errors.New("only an applied improvement can be rolled back")
	}
	var all []domain.AgentImprovement
	var err error
	if item.SkillID != "" {
		all, err = a.store.ListAgentImprovementsForSkill(ctx, item.SkillID, 1000)
		if err != nil {
			return domain.AgentImprovement{}, err
		}
		var lineage []domain.AgentImprovement
		if item.BlueprintID != "" {
			lineage, err = a.store.ListAgentImprovementsForBlueprint(ctx, item.BlueprintID, 1000)
		} else {
			lineage, err = a.store.ListAgentImprovements(ctx, item.WorkspaceID, 1000)
		}
		if err != nil {
			return domain.AgentImprovement{}, err
		}
		all = mergeAgentImprovements(all, lineage)
	}
	if item.MemoryID != "" {
		memoryItems, memoryErr := a.store.ListAgentImprovementsForMemory(ctx, item.MemoryID, 1000)
		if memoryErr != nil {
			return domain.AgentImprovement{}, memoryErr
		}
		all = mergeAgentImprovements(all, memoryItems)
	}
	if item.InstructionSignature != "" {
		instructionItems, instructionErr := a.store.ListAgentImprovementsForInstruction(ctx, item.InstructionSignature, 1000)
		if instructionErr != nil {
			return domain.AgentImprovement{}, instructionErr
		}
		all = mergeAgentImprovements(all, instructionItems)
	}
	for _, newer := range all {
		sameSkill := item.SkillID != "" && skillImprovementFamily(newer) == skillImprovementFamily(item)
		sameMemory := item.MemoryID != "" && newer.MemoryID == item.MemoryID
		sameInstruction := item.InstructionSignature != "" && newer.InstructionSignature == item.InstructionSignature
		if newer.ID != item.ID && (sameSkill || sameMemory || sameInstruction) && improvementIsApplied(newer.Status) && newer.CreatedAt.After(item.CreatedAt) {
			return domain.AgentImprovement{}, errors.New("a newer autonomous improvement must be rolled back first")
		}
	}
	if err = a.restoreImprovementBindings(ctx, item); err != nil {
		return domain.AgentImprovement{}, err
	}
	if item.AfterSkill != nil && (item.BeforeSkill == nil || item.AfterSkill.ID != item.BeforeSkill.ID) {
		retired := *item.AfterSkill
		retired.Configuration = cloneAnyMap(retired.Configuration)
		retired.Configuration["promotionStatus"] = "rolled_back"
		retired.Configuration["rolloutStatus"] = "rolled_back"
		retired.UpdatedAt = time.Now().UTC()
		if err = a.store.SaveSkill(ctx, retired); err != nil {
			return domain.AgentImprovement{}, err
		}
	}
	item.Status = "rolled_back"
	item.UpdatedAt = time.Now().UTC()
	if err = a.store.SaveAgentImprovement(ctx, item); err != nil {
		return domain.AgentImprovement{}, err
	}
	return item, nil
}

func skillImprovementFamily(item domain.AgentImprovement) string {
	for _, skill := range []*domain.SkillDefinition{item.AfterSkill, item.BeforeSkill} {
		if skill == nil {
			continue
		}
		if family := skillFamilyID(*skill); family != "" {
			return family
		}
		if skill.ID != "" {
			return skill.ID
		}
	}
	return item.SkillID
}

// skillFamilyID reads the lineage of a learned Skill. fmt.Sprint of a missing
// key is "<nil>", and that string used to put every learned Skill into one
// shared family: any newer improvement then blocked the rollback of any older
// regressed one, and the canary stayed wedged on every run.
func skillFamilyID(skill domain.SkillDefinition) string {
	for _, key := range []string{"familyId", "supersedesSkillId"} {
		// Revisions saved while the bug was live carry the literal "<nil>";
		// their predecessor link still names the lineage.
		value, _ := skill.Configuration[key].(string)
		if value = strings.TrimSpace(value); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func mergeAgentImprovements(groups ...[]domain.AgentImprovement) []domain.AgentImprovement {
	seen := map[string]bool{}
	result := make([]domain.AgentImprovement, 0)
	for _, group := range groups {
		for _, item := range group {
			if !seen[item.ID] {
				seen[item.ID] = true
				result = append(result, item)
			}
		}
	}
	return result
}

func learningRollbackAvailability(items []domain.AgentImprovement) map[string]bool {
	latest := map[string]domain.AgentImprovement{}
	keysByItem := map[string][]string{}
	for _, item := range items {
		if !improvementIsApplied(item.Status) {
			continue
		}
		keys := make([]string, 0, 2)
		if item.SkillID != "" {
			keys = append(keys, "skill:"+skillImprovementFamily(item))
		}
		if item.MemoryID != "" {
			keys = append(keys, "memory:"+item.MemoryID)
		}
		if item.InstructionSignature != "" {
			keys = append(keys, "instruction:"+item.InstructionSignature)
		}
		keysByItem[item.ID] = keys
		for _, key := range keys {
			if current, ok := latest[key]; !ok || item.CreatedAt.After(current.CreatedAt) {
				latest[key] = item
			}
		}
	}
	result := map[string]bool{}
	for itemID, keys := range keysByItem {
		if len(keys) == 0 {
			continue
		}
		available := true
		for _, key := range keys {
			if latest[key].ID != itemID {
				available = false
				break
			}
		}
		result[itemID] = available
	}
	return result
}
