package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

func (a *App) indexLearningPrinciple(ctx context.Context, item domain.AgentImprovement, run domain.Run, questID, kind string) error {
	content := strings.TrimSpace(item.Instruction)
	key := strings.TrimSpace(item.InstructionKey)
	if content == "" && item.AfterMemory != nil {
		content = strings.TrimSpace(item.AfterMemory.Content)
		key = strings.TrimSpace(item.MemoryKey)
	}
	if content == "" && item.AfterSkill != nil && kind == "failure" {
		content = truncateRunes(strings.TrimSpace(item.AfterSkill.Description), 600)
		key = "recovery-" + strings.TrimSpace(item.AfterSkill.ID)
	}
	if content == "" || !safePortableMemory(content) {
		return nil
	}
	if key == "" {
		key = "principle"
	}
	signature := learningPrincipleSignature(item.ProjectAgentID, kind, content)
	record := domain.LearningPrinciple{
		ID: "principle-" + signature[:16], WorkspaceID: item.WorkspaceID, ProjectAgentID: item.ProjectAgentID,
		BlueprintID: item.BlueprintID, QuestID: questID, Kind: kind, Key: key, Content: content,
		Signature: signature, SourceRunID: run.ID, ImprovementID: item.ID, CreatedAt: time.Now().UTC(),
	}
	return a.store.SaveLearningPrinciple(ctx, record)
}

func learningPrincipleSignature(agentID, kind, content string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(content), " "))
	hash := sha256.Sum256([]byte(agentID + "\x00" + kind + "\x00" + normalized))
	return hex.EncodeToString(hash[:])
}

func workflowDigest(ownerID, role string, tools []string, verificationRequired bool) string {
	copy := append([]string(nil), tools...)
	for index := range copy {
		copy[index] = strings.ToLower(strings.TrimSpace(copy[index]))
	}
	contract := "no_verification"
	if verificationRequired {
		contract = "verification_required"
	}
	role = strings.ToLower(strings.Join(strings.Fields(role), " "))
	hash := sha256.Sum256([]byte(ownerID + "\x00" + role + "\x00" + contract + "\x00" + strings.Join(copy, "\x00")))
	return hex.EncodeToString(hash[:])
}
