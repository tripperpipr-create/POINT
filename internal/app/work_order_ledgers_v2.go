package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/workspace"
)

// collectWorkOrderEvidenceLedgersV2 derives the immutable audit ledger only
// from persisted descendants of this quest. It never copies context content or
// secret values into evidence.
func (a *App) collectWorkOrderEvidenceLedgersV2(ctx context.Context, order domain.WorkOrder, root domain.Quest, bundle *domain.EvidenceBundle) {
	if bundle == nil {
		return
	}
	quests, err := a.store.ListQuests(ctx, order.WorkspaceID)
	if err != nil {
		bundle.KnownLimitations = append(bundle.KnownLimitations, "Не удалось собрать ledger квеста")
		return
	}
	byQuestID := make(map[string]*domain.Quest, len(quests))
	for index := range quests {
		byQuestID[quests[index].ID] = &quests[index]
	}
	executions, err := a.store.ListExecutions(ctx, order.WorkspaceID, 500)
	if err != nil {
		bundle.KnownLimitations = append(bundle.KnownLimitations, "Не удалось собрать ledger исполнений")
		return
	}
	owned := make(map[string]domain.ExecutionInstance)
	for _, execution := range executions {
		if questDescendsFrom(execution.QuestID, root.ID, byQuestID) {
			owned[execution.ID] = execution
		}
	}

	agents, _ := a.store.ListProjectAgents(ctx, order.WorkspaceID)
	agentByID := make(map[string]domain.ProjectAgent, len(agents))
	for _, projectAgent := range agents {
		agentByID[projectAgent.ID] = projectAgent
	}
	milestoneByFlowRun := map[string]string{}
	if runtimes, runtimeErr := a.store.ListMilestoneRuntimesV2(ctx, root.ID, order.Version); runtimeErr == nil {
		for _, runtime := range runtimes {
			if runtime.FlowRunID != "" {
				milestoneByFlowRun[runtime.FlowRunID] = runtime.MilestoneID
			}
		}
	}

	runByExecution := map[string]domain.Run{}
	for executionID, execution := range owned {
		if execution.RunID != "" {
			if run, runErr := a.store.GetRun(ctx, execution.RunID); runErr == nil {
				runByExecution[executionID] = run
			}
		}
		if execution.SandboxID != "" {
			if record, recordErr := a.store.GetSandbox(ctx, execution.SandboxID); recordErr == nil {
				image := strings.TrimSpace(record.BackendImageDigest)
				if image == "" {
					image = strings.TrimSpace(record.BackendImage)
				}
				if image != "" {
					bundle.DockerImages = append(bundle.DockerImages, image)
				}
			}
		}
	}
	bundle.DockerImages = uniqueSortedStringsV2(bundle.DockerImages)

	usageByExecution := map[string]bool{}
	if usage, usageErr := a.store.ListUsageRecords(ctx, order.WorkspaceID, 5000); usageErr == nil {
		for _, record := range usage {
			execution, belongs := owned[record.ExecutionID]
			if !belongs && record.QuestID != "" {
				belongs = questDescendsFrom(record.QuestID, root.ID, byQuestID)
			}
			if !belongs {
				continue
			}
			usageByExecution[record.ExecutionID] = true
			projectAgent := agentByID[record.ProjectAgentID]
			cost := int64(0)
			costKnown := record.CostCents != nil
			if costKnown {
				cost = *record.CostCents
			}
			bundle.ModelCalls = append(bundle.ModelCalls, domain.ModelCallLedgerEntry{
				ID: record.ID, MilestoneID: milestoneByFlowRun[execution.FlowRunID], StageID: execution.FlowNodeID,
				ConnectionID: projectAgent.ConnectionID, Provider: record.Provider, Model: record.Model,
				Role: projectAgent.RoleDescription, InputTokens: record.InputTokens, OutputTokens: record.OutputTokens,
				CostCents: cost, CostKnown: costKnown, UsageReported: true, ActiveMillis: record.LatencyMs,
				Outcome: record.Outcome, CreatedAt: record.CreatedAt,
			})
		}
	}

	for executionID, run := range runByExecution {
		execution := owned[executionID]
		projectAgent := agentByID[execution.ProjectAgentID]
		// Deterministic stages persist a Run for their event history, but never
		// call a model. They have no provider/model and must not be represented
		// as an incomplete model call in the evidence ledger.
		if !usageByExecution[executionID] && strings.TrimSpace(run.Provider) != "" && strings.TrimSpace(run.Model) != "" {
			bundle.ModelCalls = append(bundle.ModelCalls, domain.ModelCallLedgerEntry{
				ID: "run:" + run.ID, MilestoneID: milestoneByFlowRun[execution.FlowRunID], StageID: execution.FlowNodeID,
				ConnectionID: projectAgent.ConnectionID, Provider: run.Provider, Model: run.Model,
				Role: projectAgent.RoleDescription, CostKnown: false, UsageReported: false,
				ActiveMillis: run.DurationMs, Outcome: string(run.Status), CreatedAt: run.StartedAt,
			})
		}
		if isLocalEvidenceProviderV2(run.Provider) {
			continue
		}
		for _, item := range run.ContextItems {
			if workspace.IsSensitive(item.Path) {
				continue
			}
			digest := strings.TrimSpace(item.Digest)
			if digest == "" {
				sum := sha256.Sum256([]byte(item.Content + item.DataBase64))
				digest = "sha256:" + hex.EncodeToString(sum[:])
			}
			bytes := item.SourceSize
			if bytes <= 0 {
				bytes = item.Size
			}
			category := strings.TrimSpace(item.Category)
			if category == "" {
				category = string(item.Kind)
			}
			bundle.ContextDisclosures = append(bundle.ContextDisclosures, domain.ContextDisclosureEntry{
				ModelConnectionID: projectAgent.ConnectionID, Model: run.Model, Category: category,
				Path: item.Path, Digest: digest, Bytes: bytes, SentAt: run.StartedAt,
			})
		}
	}
	sort.Slice(bundle.ModelCalls, func(i, j int) bool {
		if bundle.ModelCalls[i].CreatedAt.Equal(bundle.ModelCalls[j].CreatedAt) {
			return bundle.ModelCalls[i].ID < bundle.ModelCalls[j].ID
		}
		return bundle.ModelCalls[i].CreatedAt.Before(bundle.ModelCalls[j].CreatedAt)
	})
	sort.Slice(bundle.ContextDisclosures, func(i, j int) bool {
		if bundle.ContextDisclosures[i].SentAt.Equal(bundle.ContextDisclosures[j].SentAt) {
			return bundle.ContextDisclosures[i].Digest < bundle.ContextDisclosures[j].Digest
		}
		return bundle.ContextDisclosures[i].SentAt.Before(bundle.ContextDisclosures[j].SentAt)
	})
}

func isLocalEvidenceProviderV2(provider string) bool {
	return strings.EqualFold(strings.TrimSpace(provider), string(domain.ProviderOllama))
}
