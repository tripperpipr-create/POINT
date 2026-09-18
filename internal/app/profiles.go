package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/storage"
)

// storedProfiles is the runtime representation of blueprints. Profiles used
// to be mirrored into a second table, which allowed their prompts to diverge.
func (a *App) storedProfiles(ctx context.Context) ([]domain.AgentProfile, error) {
	blueprints, err := a.store.ListBlueprints(ctx)
	if err != nil {
		return nil, err
	}
	profiles := make([]domain.AgentProfile, 0, len(blueprints))
	for _, blueprint := range blueprints {
		profile := domain.ProfileFromBlueprint(blueprint)
		// Return the source instruction, not a prompt compiled from itself.
		profile.SystemPrompt = blueprint.SystemPrompt
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func (a *App) SaveProfile(profile domain.AgentProfile) (domain.AgentProfile, error) {
	if profile.ID == "" {
		profile.ID = domain.NewID("profile")
		profile.CreatedAt = time.Now().UTC()
	}
	profile.UpdatedAt = time.Now().UTC()
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = profile.UpdatedAt
	}
	if profile.ProviderPreset == "" {
		profile.ProviderPreset = string(profile.Provider)
	}
	if profile.MaxOutputTokens == 0 {
		profile.MaxOutputTokens = 4096
	}
	if profile.ContextWindowTokens == 0 {
		profile.ContextWindowTokens = 32768
		if profile.ContextWindowTokens-profile.MaxOutputTokens < 1024 {
			profile.ContextWindowTokens = profile.MaxOutputTokens + 4096
		}
	}
	// Resolve the live connection before validating the legacy profile bridge.
	// This is also where an explicitly selected connection contributes its
	// default model; validating first made Connection → Model work for modern
	// actors but reject the same configuration through SaveProfile.
	if profile.ConnectionID != "" {
		if err := a.applyConnectionEndpoint(&profile); err != nil {
			return domain.AgentProfile{}, err
		}
	}
	if profile.BaseURL == "" {
		if profile.Provider == domain.ProviderOllama {
			profile.BaseURL = "http://127.0.0.1:11434"
		} else if profile.Provider == domain.ProviderAnthropic {
			profile.BaseURL = "https://api.anthropic.com/v1"
		} else {
			profile.BaseURL = "https://api.openai.com/v1"
		}
	}
	customTools, err := a.store.ListCustomTools(context.Background())
	if err != nil {
		return domain.AgentProfile{}, err
	}
	customNames := make([]string, 0, len(customTools))
	for _, tool := range customTools {
		customNames = append(customNames, tool.ID)
	}
	if err := storage.ValidateProfile(profile, customNames...); err != nil {
		return domain.AgentProfile{}, err
	}
	if err := a.recordCompatibilityUsage(context.Background(), a.currentWorldID(), domain.CompatibilityProfileSave, legacyProfileAPIVersion); err != nil {
		return domain.AgentProfile{}, err
	}
	// The blueprint is durable; the profile is its runtime form.
	saved, err := a.SaveBlueprint(domain.BlueprintFromProfile(profile))
	if err != nil {
		return domain.AgentProfile{}, err
	}
	result := domain.ProfileFromBlueprint(saved)
	result.SystemPrompt = saved.SystemPrompt
	return result, nil
}

func (a *App) DeleteProfile(id string) error {
	if id == "default" {
		return errors.New("the default profile cannot be deleted")
	}
	workflowDefinitions, err := a.store.ListWorkflows(context.Background())
	if err != nil {
		return err
	}
	for _, workflow := range workflowDefinitions {
		for _, step := range workflow.Steps {
			if step.ProfileID == id {
				return fmt.Errorf("profile is used by workflow %q; replace that step first", workflow.Name)
			}
		}
	}
	if err := a.recordCompatibilityUsage(context.Background(), a.currentWorldID(), domain.CompatibilityProfileDelete, legacyProfileAPIVersion); err != nil {
		return err
	}
	// Delete through App so blueprint/project-agent reference guards also run.
	return a.DeleteBlueprint(id)
}
