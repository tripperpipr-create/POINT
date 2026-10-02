package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/policy"
	"local-agent-workbench/internal/storage"
	"time"
)

type FastAgentConfig struct {
	Profile       domain.AgentProfile `json:"profile"`
	SkillIDs      []string            `json:"skillIds"`
	Tokens        int                 `json:"tokens"`
	ActiveSeconds int                 `json:"activeSeconds"`
}

func (a *App) FastAgentConfig(ctx context.Context) (FastAgentConfig, error) {
	var c FastAgentConfig
	raw, err := a.store.Setting(ctx, "system-fast-agent-v1")
	if err == nil {
		err = json.Unmarshal([]byte(raw), &c)
		return c, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return c, err
	}
	c = FastAgentConfig{Profile: domain.DefaultProfile(), Tokens: 200000, ActiveSeconds: 3600}
	c.Profile.ID = domain.SystemFastAgentID
	c.Profile.Name = "Fast Agent"
	c.Profile.ExecutionMode = "host_live"
	c.Profile.AllowedTools = appendUniqueStrings(c.Profile.AllowedTools, "read_skill", "search_skills")
	defaults, err := a.GlobalModelDefaults(ctx)
	if err != nil {
		return c, err
	}
	c.Profile.ConnectionID, c.Profile.Model = defaults.Agent.ConnectionID, defaults.Agent.Model
	c.Profile.BaseURL = "" // endpoints are resolved from the connection at launch
	return c, nil
}

func (a *App) SaveFastAgentConfig(ctx context.Context, c FastAgentConfig) (FastAgentConfig, error) {
	if c.Tokens < 1024 || c.Tokens > 2000000 || c.ActiveSeconds < 1 || c.ActiveSeconds > 86400 || c.Profile.MaxSteps < 1 || c.Profile.MaxSteps > 1000 {
		return c, errors.New("Fast Agent budget is outside supported bounds")
	}
	allowed := map[string]bool{}
	for _, n := range append(domain.DefaultProfile().AllowedTools, "validate_syntax", "read_skill", "search_skills") {
		allowed[n] = true
	}
	for _, n := range c.Profile.AllowedTools {
		if !allowed[n] {
			return c, fmt.Errorf("system Fast Agent does not support tool %q", n)
		}
	}
	if c.Profile.ConnectionID == "" || c.Profile.Model == "" {
		return c, errors.New("Fast Agent requires a connection and model")
	}
	c.Profile.ID = domain.SystemFastAgentID
	c.Profile.Name = "Fast Agent"
	c.Profile.ExecutionMode = "host_live"
	c.Profile.EquippedSkills = nil
	c.Profile.SkillCatalog = nil
	c.Profile.UpdatedAt = time.Now().UTC()
	if err := a.applyConnectionEndpoint(&c.Profile); err != nil {
		return c, err
	}
	if domain.IsAgentCLIProvider(c.Profile.Provider) {
		return c, errors.New("Fast Agent requires a native tool-capable model connection")
	}
	if err := storage.ValidateProfile(c.Profile); err != nil {
		return c, err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return c, err
	}
	err = a.store.SaveSetting(ctx, "system-fast-agent-v1", string(raw))
	return c, err
}

// Pin available library revisions once. Skills narrow instructions, never tool grants.
func (a *App) availableRuntimeSkills(ctx context.Context, workspaceID string, p domain.AgentProfile) ([]domain.SkillRuntime, error) {
	items, err := a.store.ListSkills(ctx)
	if err != nil {
		return nil, err
	}
	instances, err := a.store.ListProjectSkills(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	byID := map[string]domain.ProjectSkillInstance{}
	for _, i := range instances {
		byID[i.SkillID] = i
	}
	var out []domain.SkillRuntime
	for _, s := range items {
		if skillRetired(s) {
			continue
		}
		i, exists := byID[s.ID]
		if exists && !i.Enabled {
			continue
		}
		// Learned project-local skills must not leak into another scope.
		if ws, ok := s.Configuration["workspaceId"].(string); ok && ws != "" && ws != workspaceID {
			continue
		}
		grants := policy.ProfileGrants(p)
		compatible := true
		for _, n := range s.RequiredTools {
			if !grants.Allows(n) {
				compatible = false
			}
		}
		for k, v := range s.PermissionDelta {
			if policy.PolicyForTool(p, k) != v {
				compatible = false
			}
		}
		if compatible {
			out = append(out, skillRuntimeFrom(s, i, exists))
		}
	}
	return out, nil
}
