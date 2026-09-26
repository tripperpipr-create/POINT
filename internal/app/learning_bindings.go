// Память и инструкции агента: что к нему привязывается по итогам учения.
package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

func (a *App) planLearningMemory(ctx context.Context, item *domain.AgentImprovement, review learningReview, blueprint *domain.AgentBlueprint, agent domain.ProjectAgent, run domain.Run, now time.Time) (*domain.MemoryRecord, error) {
	if item == nil || blueprint == nil || review.MemoryDecision != "learn" || !safePortableMemory(review.Memory) {
		return nil, nil
	}
	signature := portableMemorySignature(blueprint.ID, review.Memory)
	memoryID := "memory-learned-" + signature[:16]
	workspaces := []string{run.WorkspaceID}
	previous, err := a.store.ListAgentImprovementsForBlueprint(ctx, blueprint.ID, 1000)
	if err != nil {
		return nil, err
	}
	for _, candidate := range previous {
		if !improvementIsApplied(candidate.Status) || candidate.MemorySignature != signature {
			continue
		}
		switch candidate.MemoryStatus {
		case "candidate", "promoted", "confirmed":
			workspaces = appendUniqueString(workspaces, candidate.WorkspaceID)
			for _, workspaceID := range candidate.MemorySourceWorkspaces {
				workspaces = appendUniqueString(workspaces, workspaceID)
			}
		}
	}
	confidence := 0.75 + float64(len(workspaces))*0.05
	if confidence > 0.95 {
		confidence = 0.95
	}
	memory := domain.MemoryRecord{
		ID: memoryID, Kind: domain.MemoryProfile, OwnerID: blueprint.ID, Content: review.Memory,
		Source: "agent-hub-self-improvement:" + signature[:12], Confidence: confidence, Pinned: true,
		CreatedAt: now, UpdatedAt: now,
	}
	item.MemoryID = memoryID
	item.MemoryKey = review.MemoryKey
	item.MemorySignature = signature
	item.MemorySourceWorkspaces = append([]string(nil), workspaces...)
	item.AfterMemory = memoryPointer(memory)
	if stored, getErr := a.store.GetMemory(ctx, memoryID); getErr == nil {
		item.MemoryStatus = "confirmed"
		item.BeforeMemory = memoryPointer(stored)
		item.AfterMemory = memoryPointer(stored)
		item.Evidence = append(item.Evidence, fmt.Sprintf("постоянная память подтверждена проектов: %d", len(workspaces)))
		return nil, nil
	} else if !errors.Is(getErr, sql.ErrNoRows) {
		return nil, getErr
	}
	if len(workspaces) < 2 {
		item.MemoryStatus = "candidate"
		item.Evidence = append(item.Evidence, fmt.Sprintf("память-кандидат · подтверждено проектов: %d из 2", len(workspaces)))
		return nil, nil
	}
	item.MemoryStatus = "promoted"
	item.Evidence = append(item.Evidence, fmt.Sprintf("постоянная память Blueprint · подтверждено проектов: %d", len(workspaces)))
	return &memory, nil
}

func portableMemorySignature(blueprintID, content string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(content), " "))
	hash := sha256.Sum256([]byte(blueprintID + "\x00" + normalized))
	return hex.EncodeToString(hash[:])
}

func memoryPointer(memory domain.MemoryRecord) *domain.MemoryRecord {
	copy := memory
	return &copy
}

func (a *App) planLearningInstruction(ctx context.Context, item *domain.AgentImprovement, review learningReview, blueprint *domain.AgentBlueprint, run domain.Run) error {
	if item == nil || blueprint == nil || review.InstructionDecision != "learn" || !safePortableInstruction(review.Instruction) {
		return nil
	}
	signature := portableInstructionSignature(blueprint.ID, review.Instruction)
	workspaces := []string{run.WorkspaceID}
	previous, err := a.store.ListAgentImprovementsForBlueprint(ctx, blueprint.ID, 1000)
	if err != nil {
		return err
	}
	for _, candidate := range previous {
		if !improvementIsApplied(candidate.Status) || candidate.InstructionSignature != signature {
			continue
		}
		switch candidate.InstructionStatus {
		case "candidate", "promoted", "confirmed":
			workspaces = appendUniqueString(workspaces, candidate.WorkspaceID)
			for _, workspaceID := range candidate.InstructionSourceWorkspaces {
				workspaces = appendUniqueString(workspaces, workspaceID)
			}
		}
	}
	item.InstructionKey = review.InstructionKey
	item.Instruction = review.Instruction
	item.InstructionSignature = signature
	item.InstructionSourceWorkspaces = append([]string(nil), workspaces...)
	if slices.Contains(blueprint.Rules, review.Instruction) {
		item.InstructionStatus = "confirmed"
		item.Evidence = append(item.Evidence, fmt.Sprintf("постоянная инструкция подтверждена проектов: %d", len(workspaces)))
		return nil
	}
	if len(workspaces) < 2 {
		item.InstructionStatus = "candidate"
		item.Evidence = append(item.Evidence, fmt.Sprintf("инструкция-кандидат · подтверждено проектов: %d из 2", len(workspaces)))
		return nil
	}
	item.InstructionStatus = "promoted"
	item.BeforeBlueprintRules = append([]string(nil), blueprint.Rules...)
	blueprint.Rules = append(blueprint.Rules, review.Instruction)
	item.AfterBlueprintRules = append([]string(nil), blueprint.Rules...)
	item.Evidence = append(item.Evidence, fmt.Sprintf("постоянная инструкция Blueprint · подтверждено проектов: %d", len(workspaces)))
	return nil
}

func portableInstructionSignature(blueprintID, instruction string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(instruction), " "))
	hash := sha256.Sum256([]byte(blueprintID + "\x00" + normalized))
	return hex.EncodeToString(hash[:])
}

// learningBindings calculates the full mutation before the audit row is
// written. A promoted skill reaches only compatible instances; a promoted
// instruction reaches every instance of the same permanent specialist.
func (a *App) learningBindings(ctx context.Context, source domain.ProjectAgent, learned domain.SkillDefinition, promotionStatus, instruction, instructionStatus string) ([]domain.ProjectAgent, map[string][]string, map[string][]string, error) {
	selected := map[string]domain.ProjectAgent{source.ID: source}
	skillTargets := map[string]bool{source.ID: true}
	instructionTargets := map[string]bool{}
	if (promotionStatus == "promoted" || instructionStatus == "promoted") && source.BlueprintID != "" {
		all, err := a.store.ListAllProjectAgents(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, candidate := range all {
			if candidate.BlueprintID != source.BlueprintID {
				continue
			}
			if promotionStatus == "promoted" && stringSetContainsAll(candidate.AllowedTools, learned.RequiredTools) {
				skillTargets[candidate.ID] = true
				selected[candidate.ID] = candidate
			}
			if instructionStatus == "promoted" {
				instructionTargets[candidate.ID] = true
				selected[candidate.ID] = candidate
			}
		}
	}
	bindings := make([]domain.ProjectAgent, 0, len(selected))
	beforeSkills := make(map[string][]string, len(selected))
	beforeRules := make(map[string][]string, len(selected))
	for _, boundAgent := range selected {
		beforeSkills[boundAgent.ID] = append([]string(nil), boundAgent.SkillIDs...)
		beforeRules[boundAgent.ID] = append([]string(nil), boundAgent.Rules...)
		if skillTargets[boundAgent.ID] {
			if superseded := strings.TrimSpace(fmt.Sprint(learned.Configuration["supersedesSkillId"])); superseded != "" {
				boundAgent.SkillIDs = withoutString(boundAgent.SkillIDs, superseded)
			}
			if !slices.Contains(boundAgent.SkillIDs, learned.ID) {
				boundAgent.SkillIDs = append(boundAgent.SkillIDs, learned.ID)
			}
			boundAgent.SkillIDs = capLearnedSkillIDs(boundAgent.SkillIDs, learned.ID, maxLearnedSkillsPerAgent)
		}
		if instructionTargets[boundAgent.ID] && !slices.Contains(boundAgent.Rules, instruction) {
			boundAgent.Rules = append(boundAgent.Rules, instruction)
		}
		bindings = append(bindings, boundAgent)
	}
	return bindings, beforeSkills, beforeRules, nil
}

func snapshotAgentSkillIDs(agents []domain.ProjectAgent) map[string][]string {
	result := make(map[string][]string, len(agents))
	for _, agent := range agents {
		result[agent.ID] = append([]string(nil), agent.SkillIDs...)
	}
	return result
}

func snapshotAgentRules(agents []domain.ProjectAgent) map[string][]string {
	result := make(map[string][]string, len(agents))
	for _, agent := range agents {
		result[agent.ID] = append([]string(nil), agent.Rules...)
	}
	return result
}

func (a *App) ensureLearnedProjectSkill(ctx context.Context, workspaceID, skillID, ownerID, ownerKind, promotionStatus string, now time.Time) error {
	instances, err := a.store.ListProjectSkills(ctx, workspaceID)
	if err != nil {
		return err
	}
	for _, instance := range instances {
		if instance.SkillID == skillID {
			instance.Enabled = true
			instance.Configuration = map[string]any{"managedBy": agentLearningManagedBy, "ownerId": ownerID, "ownerKind": ownerKind, "promotionStatus": promotionStatus}
			instance.UpdatedAt = now
			return a.store.SaveProjectSkill(ctx, instance)
		}
	}
	return a.store.SaveProjectSkill(ctx, domain.ProjectSkillInstance{
		ID: domain.NewID("projectskill"), WorkspaceID: workspaceID, SkillID: skillID,
		Configuration: map[string]any{"managedBy": agentLearningManagedBy, "ownerId": ownerID, "ownerKind": ownerKind, "promotionStatus": promotionStatus}, Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
}
