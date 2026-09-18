package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"local-agent-workbench/internal/egress"
)

// RunConfigurationSnapshot captures every persisted agent setting that can
// influence a run. API keys are intentionally excluded and remain in memory.
type RunConfigurationSnapshot struct {
	SchemaVersion       int                `json:"schemaVersion"`
	ApplicationVersion  string             `json:"applicationVersion"`
	CapturedAt          time.Time          `json:"capturedAt"`
	Profile             AgentProfile       `json:"profile"`
	CustomTools         []CustomTool       `json:"customTools"`
	ProfileDigest       string             `json:"profileDigest,omitempty"`
	ConfigurationDigest string             `json:"configurationDigest,omitempty"`
	EgressPolicyDigest  string             `json:"egressPolicyDigest,omitempty"`
	EgressPolicyMode    string             `json:"egressPolicyMode,omitempty"`
	SkillAttributions   []SkillAttribution `json:"skillAttributions,omitempty"`
}

func NewRunConfigurationSnapshot(applicationVersion string, profile AgentProfile, customTools []CustomTool, capturedAt time.Time) RunConfigurationSnapshot {
	profile.AllowedTools = append([]string(nil), profile.AllowedTools...)
	profile.FallbackModels = append([]string(nil), profile.FallbackModels...)
	if len(profile.EquippedSkills) > 0 {
		skills := make([]SkillRuntime, len(profile.EquippedSkills))
		copy(skills, profile.EquippedSkills)
		for index := range skills {
			skills[index].References = append([]string(nil), skills[index].References...)
			skills[index].Scripts = append([]string(nil), skills[index].Scripts...)
			skills[index].RequiredTools = append([]string(nil), skills[index].RequiredTools...)
			skills[index].PermissionRequirements = cloneToolPolicies(skills[index].PermissionRequirements)
		}
		profile.EquippedSkills = skills
	}
	if len(profile.ToolPolicies) > 0 {
		policies := make(map[string]string, len(profile.ToolPolicies))
		for name, value := range profile.ToolPolicies {
			policies[name] = value
		}
		profile.ToolPolicies = policies
	}
	selectedTools := make([]CustomTool, 0, len(customTools))
	for _, tool := range customTools {
		if slices.Contains(profile.AllowedTools, tool.ID) {
			selectedTools = append(selectedTools, tool)
		}
	}
	egressPolicy, policyErr := egress.CompileToolPolicies(profile.ToolPolicies)
	if policyErr != nil {
		// Persisted legacy data is fail-closed. Current save/start validation
		// reports the original policy error before a run can launch.
		egressPolicy, _ = egress.Compile("DENY", nil, egress.Quota{})
	}
	snapshot := RunConfigurationSnapshot{
		SchemaVersion:      3,
		ApplicationVersion: applicationVersion,
		CapturedAt:         capturedAt.UTC(),
		Profile:            profile,
		CustomTools:        selectedTools,
		EgressPolicyDigest: egressPolicy.Digest,
		EgressPolicyMode:   egressPolicy.Mode,
	}
	snapshot.ProfileDigest = digestJSON(profile)
	snapshot.SkillAttributions = make([]SkillAttribution, 0, len(profile.EquippedSkills))
	for _, skill := range profile.EquippedSkills {
		if attribution := SkillRuntimeAttribution(skill); attribution.SkillID != "" {
			snapshot.SkillAttributions = append(snapshot.SkillAttributions, attribution)
		}
	}
	snapshot.ConfigurationDigest = digestJSON(struct {
		ApplicationVersion string       `json:"applicationVersion"`
		Profile            AgentProfile `json:"profile"`
		CustomTools        []CustomTool `json:"customTools"`
		EgressPolicyDigest string       `json:"egressPolicyDigest"`
	}{applicationVersion, profile, selectedTools, snapshot.EgressPolicyDigest})
	return snapshot
}

// WithEffectiveModel records the model that actually ran after a provider
// fallback and recomputes digests so learning and statistics match evidence.
func (s RunConfigurationSnapshot) WithEffectiveModel(model string) RunConfigurationSnapshot {
	model = strings.TrimSpace(model)
	if model == "" || s.Profile.Model == model {
		return s
	}
	profile := s.Profile
	profile.Model = model
	return NewRunConfigurationSnapshot(s.ApplicationVersion, profile, s.CustomTools, s.CapturedAt)
}

// SkillRuntimeAttribution returns the exact immutable identity that was sent
// to a model. Revision is user-facing metadata; Digest is the authoritative
// version key used for comparisons and rollback evidence.
func SkillRuntimeAttribution(skill SkillRuntime) SkillAttribution {
	if strings.TrimSpace(skill.ID) == "" {
		return SkillAttribution{}
	}
	promotionStatus := ""
	if value, ok := skill.Configuration["promotionStatus"]; ok && value != nil {
		promotionStatus = strings.TrimSpace(fmt.Sprint(value))
	}
	identity := skill
	identity.Configuration = skillVersionConfiguration(skill.Configuration)
	return SkillAttribution{
		SkillID: skill.ID, Name: skill.Name, Revision: skillRevisionNumber(skill.Configuration["revision"]),
		Digest: strings.TrimPrefix(digestJSON(identity), "sha256:"), PromotionStatus: promotionStatus,
	}
}

func skillVersionConfiguration(configuration map[string]any) map[string]any {
	if fmt.Sprint(configuration["managedBy"]) != "agent-hub-self-improvement" {
		return cloneAnyValues(configuration)
	}
	metadata := map[string]bool{
		"managedBy": true, "ownerId": true, "ownerKind": true, "blueprintId": true,
		"agentId": true, "workspaceId": true, "signature": true, "sourceRuns": true,
		"revision": true, "sourceWorkspaces": true, "sourceAgents": true, "workflowTools": true,
		"lastSourceRunId": true, "autoApplied": true, "promotionStatus": true,
		"promotionReason": true, "promotionWorkspaceCount": true, "evalGate": true,
		"supersedesSkillId": true, "familyId": true,
	}
	result := map[string]any{}
	for key, value := range configuration {
		if !metadata[key] {
			result[key] = value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// SkillDefinitionAttribution projects a persisted definition to the exact
// runtime shape before hashing it. CreatedAt/UpdatedAt are excluded; exact
// permission requirements are included because they are part of run authority.
func SkillDefinitionAttribution(skill SkillDefinition) SkillAttribution {
	return SkillRuntimeAttribution(SkillRuntime{
		ID: skill.ID, Name: skill.Name, Description: strings.TrimSpace(skill.Description),
		Instructions: strings.TrimSpace(skill.Instructions), References: append([]string(nil), skill.References...),
		Scripts: append([]string(nil), skill.Scripts...), RequiredTools: append([]string(nil), skill.RequiredTools...),
		PermissionRequirements: cloneToolPolicies(skill.PermissionDelta),
		Configuration:          cloneAnyValues(skill.Configuration),
	})
}

func cloneToolPolicies(values map[string]ToolPolicy) map[string]ToolPolicy {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]ToolPolicy, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func cloneAnyValues(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func digestJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func skillRevisionNumber(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		return 0
	}
}
