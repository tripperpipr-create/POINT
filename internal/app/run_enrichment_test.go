package app

import (
	"context"
	"testing"

	"local-agent-workbench/internal/domain"
)

type attributionSkillStore struct {
	skills    []domain.SkillDefinition
	instances []domain.ProjectSkillInstance
}

func (store attributionSkillStore) ListSkills(context.Context) ([]domain.SkillDefinition, error) {
	return append([]domain.SkillDefinition(nil), store.skills...), nil
}

func (store attributionSkillStore) ListProjectSkills(context.Context, string) ([]domain.ProjectSkillInstance, error) {
	return append([]domain.ProjectSkillInstance(nil), store.instances...), nil
}

func TestResolveEquippedSkillsPreservesManagedRevisionUnderProjectOverrides(t *testing.T) {
	store := attributionSkillStore{
		skills: []domain.SkillDefinition{{
			ID: "skill-a", Name: "A", Instructions: "Do A",
			Configuration: map[string]any{"revision": 7, "signature": "managed-signature", "promotionStatus": "candidate"},
		}},
		instances: []domain.ProjectSkillInstance{{
			WorkspaceID: "workspace-a", SkillID: "skill-a", Enabled: true,
			Configuration: map[string]any{"promotionStatus": "project_only", "localOption": "enabled"},
		}},
	}
	resolved, err := resolveEquippedSkills(store, "workspace-a", domain.ProjectAgent{SkillIDs: []string{"skill-a"}}, domain.AgentProfile{})
	if err != nil || len(resolved) != 1 {
		t.Fatalf("resolved=%#v err=%v", resolved, err)
	}
	configuration := resolved[0].Configuration
	if configuration["revision"] != 7 || configuration["signature"] != "managed-signature" || configuration["promotionStatus"] != "project_only" || configuration["localOption"] != "enabled" {
		t.Fatalf("merged configuration=%#v", configuration)
	}
}
