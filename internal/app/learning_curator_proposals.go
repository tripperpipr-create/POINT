package app

import (
	"context"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// queueCuratorMergeProposals turns duplicate curator suggestions into explicit
// journal entries. Apply remains a user action; this never mutates Skills.
func (a *App) queueCuratorMergeProposals(ctx context.Context, workspaceID string) error {
	if strings.TrimSpace(workspaceID) == "" {
		return nil
	}
	skills, err := a.store.ListSkills(ctx)
	if err != nil {
		return err
	}
	agents, err := a.store.ListProjectAgents(ctx, workspaceID)
	if err != nil {
		return err
	}
	blueprints, err := a.store.ListBlueprints(ctx)
	if err != nil {
		return err
	}
	projectSkills, err := a.store.ListProjectSkills(ctx, workspaceID)
	if err != nil {
		return err
	}
	outcomes, err := a.store.ListSkillOutcomes(ctx, workspaceID, 1000)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	suggestions := buildSkillCuration(skills, agents, blueprints, projectSkills, outcomes, now)
	for _, suggestion := range suggestions {
		if suggestion.Kind != "duplicate" || suggestion.Action != "merge" {
			continue
		}
		sourceKey := "curation:" + suggestion.ID
		if existing, findErr := a.store.FindAgentImprovementByRun(ctx, sourceKey); findErr == nil && existing.ID != "" {
			continue
		}
		agentID := ""
		if len(agents) > 0 {
			agentID = agents[0].ID
		}
		item := domain.AgentImprovement{
			ID: domain.NewID("improvement"), WorkspaceID: workspaceID, ProjectAgentID: agentID,
			SourceRunID: sourceKey, Kind: "curation_merge_proposed", Status: "skipped",
			Trigger: "curator_merge_proposal", Evidence: append([]string{suggestion.Summary}, suggestion.Evidence...),
			ReviewMode: "deterministic", CreatedAt: now, UpdatedAt: now,
			SkillID: suggestion.PrimarySkillID,
		}
		if err = a.store.SaveAgentImprovement(ctx, item); err != nil {
			return err
		}
	}
	return nil
}
