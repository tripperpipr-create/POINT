package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/policy"
)

// runtimeSnapshotForProjectAgent resolves the same effective connection,
// skills and custom-tool set for every execution entry point. Pending Cursor,
// merged and headless executions must not capture different configuration
// evidence merely because they were created through different APIs.
func (a *App) runtimeSnapshotForProjectAgent(workspaceID string, projectAgent domain.ProjectAgent, capturedAt time.Time) (domain.RunConfigurationSnapshot, error) {
	if projectAgent.WorkspaceID != workspaceID {
		return domain.RunConfigurationSnapshot{}, fmt.Errorf("project agent belongs to another workspace")
	}
	profile := domain.ProfileFromProjectAgent(projectAgent)
	if err := a.applyConnectionEndpoint(&profile); err != nil {
		return domain.RunConfigurationSnapshot{}, err
	}
	normalizeRuntimeProfileDefaults(&profile)
	if err := a.enrichProjectAgentForRun(workspaceID, projectAgent, &profile, nil); err != nil {
		return domain.RunConfigurationSnapshot{}, err
	}
	if err := readinessFailure(projectAgent, a.AgentCapabilityFor(context.Background(), profile)); err != nil {
		return domain.RunConfigurationSnapshot{}, err
	}
	customTools, err := a.store.ListCustomTools(context.Background())
	if err != nil {
		return domain.RunConfigurationSnapshot{}, err
	}
	return domain.NewRunConfigurationSnapshot(Version, profile, customTools, capturedAt), nil
}

// enrichProjectAgentForRun merges skills, policies, portable profile memory and
// project-scoped memory into the runnable profile/context.
func (a *App) enrichProjectAgentForRun(workspaceID string, agent domain.ProjectAgent, profile *domain.AgentProfile, inputs *[]domain.RunContextInput) error {
	if profile == nil {
		return nil
	}
	if profile.ToolPolicies == nil && len(agent.ToolPolicies) > 0 {
		profile.ToolPolicies = cloneStringMap(agent.ToolPolicies)
	}
	if len(profile.FallbackModels) == 0 && len(agent.FallbackModels) > 0 {
		profile.FallbackModels = append([]string(nil), agent.FallbackModels...)
	}

	skills, err := resolveEquippedSkills(a.store, workspaceID, agent, *profile)
	if err != nil {
		return err
	}
	profile.EquippedSkills = skills
	if len(skills) > 0 && !containsString(profile.AllowedTools, "read_skill") {
		profile.AllowedTools = append(append([]string(nil), profile.AllowedTools...), "read_skill")
	}
	if inputs == nil {
		return nil
	}
	memories, err := a.store.ListMemories(context.Background(), workspaceID)
	if err != nil {
		return err
	}
	for _, memory := range memories {
		if !memory.Pinned && memory.Kind != domain.MemoryProfile && memory.Kind != domain.MemoryAgent && memory.Kind != domain.MemoryProject && memory.Kind != domain.MemoryQuest {
			continue
		}
		if memory.Kind == domain.MemoryProfile && (agent.BlueprintID == "" || memory.OwnerID != agent.BlueprintID) {
			continue
		}
		if memory.Kind == domain.MemoryAgent && memory.OwnerID != "" && memory.OwnerID != agent.ID {
			continue
		}
		content := strings.TrimSpace(memory.Content)
		if content == "" {
			continue
		}
		label := string(memory.Kind) + " memory"
		*inputs = append(*inputs, domain.RunContextInput{
			Kind: domain.ContextText, Label: label, Content: content,
			Category: "memory", AddedBy: "memory-retrieval",
			Reason: "Scoped persistent memory for this execution", Source: memory.ID,
			Relevance: memory.Confidence, Pinned: memory.Pinned,
		})
	}
	return nil
}

type skillStore interface {
	ListSkills(ctx context.Context) ([]domain.SkillDefinition, error)
	ListProjectSkills(ctx context.Context, workspaceID string) ([]domain.ProjectSkillInstance, error)
}

func resolveEquippedSkills(store skillStore, workspaceID string, agent domain.ProjectAgent, profile domain.AgentProfile) ([]domain.SkillRuntime, error) {
	if store == nil || len(agent.SkillIDs) == 0 {
		return nil, nil
	}
	skills, err := store.ListSkills(context.Background())
	if err != nil {
		return nil, err
	}
	skillByID := map[string]domain.SkillDefinition{}
	for _, skill := range skills {
		skillByID[skill.ID] = skill
	}
	projectSkills, err := store.ListProjectSkills(context.Background(), workspaceID)
	if err != nil {
		return nil, err
	}
	instanceBySkill := map[string]domain.ProjectSkillInstance{}
	for _, instance := range projectSkills {
		instanceBySkill[instance.SkillID] = instance
	}
	var equipped []domain.SkillRuntime
	for _, skillID := range agent.SkillIDs {
		skill, ok := skillByID[skillID]
		if !ok {
			continue
		}
		instance, hasInstance := instanceBySkill[skillID]
		if hasInstance && !instance.Enabled {
			continue
		}
		runGrants := policy.ProfileGrants(profile)
		for _, tool := range skill.RequiredTools {
			if !runGrants.Allows(tool) {
				return nil, fmt.Errorf("skill %q requires tool %q, but the agent has not explicitly allowed it", skill.Name, tool)
			}
		}
		for key, requiredPolicy := range skill.PermissionDelta {
			actualPolicy := policy.PolicyForTool(profile, key)
			if actualPolicy != requiredPolicy {
				return nil, fmt.Errorf("skill %q requires %s=%s, but the effective agent policy is %s", skill.Name, key, requiredPolicy, actualPolicy)
			}
		}
		equipped = append(equipped, skillRuntimeFrom(skill, instance, hasInstance))
	}
	return equipped, nil
}

// skillRuntimeFrom переводит определение навыка в то, что уезжает в промпт.
// Общая для агента и помощника: надетый навык должен выглядеть одинаково у
// обоих, иначе одна и та же практика читается по-разному.
func skillRuntimeFrom(skill domain.SkillDefinition, instance domain.ProjectSkillInstance, hasInstance bool) domain.SkillRuntime {
	runtime := domain.SkillRuntime{
		ID:                     skill.ID,
		Name:                   skill.Name,
		Description:            strings.TrimSpace(skill.Description),
		Instructions:           strings.TrimSpace(skill.Instructions),
		References:             append([]string(nil), skill.References...),
		Scripts:                append([]string(nil), skill.Scripts...),
		RequiredTools:          append([]string(nil), skill.RequiredTools...),
		PermissionRequirements: cloneToolPolicyMap(skill.PermissionDelta),
	}
	if len(skill.Configuration) > 0 {
		runtime.Configuration = cloneAnyMap(skill.Configuration)
	}
	if hasInstance && len(instance.Configuration) > 0 {
		if runtime.Configuration == nil {
			runtime.Configuration = map[string]any{}
		}
		for key, value := range instance.Configuration {
			runtime.Configuration[key] = value
		}
	}
	return runtime
}

func cloneToolPolicyMap(values map[string]domain.ToolPolicy) map[string]domain.ToolPolicy {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]domain.ToolPolicy, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func containsString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneAnyMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
