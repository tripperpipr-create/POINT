package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/orchestrator"
)

func (a *App) SaveOrchestratorConfig(cfg domain.OrchestratorConfig) (domain.OrchestratorConfig, error) {
	now := time.Now().UTC()
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.OrchestratorConfig{}, err
	}
	if cfg.WorkspaceID != "" && cfg.WorkspaceID != ws.ID {
		return domain.OrchestratorConfig{}, errors.New("orchestrator workspace does not match the open project")
	}
	cfg.WorkspaceID = ws.ID
	cfg.Preset = strings.TrimSpace(cfg.Preset)
	if cfg.Preset == "" {
		cfg.Preset = "conductor"
	}
	if _, ok := orchestrator.PresetDefaults(cfg.Preset); !ok {
		return domain.OrchestratorConfig{}, fmt.Errorf("unsupported orchestrator preset %q", cfg.Preset)
	}
	for _, setting := range []struct {
		name  string
		value int
	}{
		{"planningDepth", cfg.PlanningDepth},
		{"parallelism", cfg.Parallelism},
		{"approvalStrictness", cfg.ApprovalStrictness},
		{"teamPreference", cfg.TeamPreference},
	} {
		if setting.value < 0 || setting.value > 100 {
			return domain.OrchestratorConfig{}, fmt.Errorf("orchestrator %s must be between 0 and 100", setting.name)
		}
	}
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.ProviderPreset = strings.TrimSpace(cfg.ProviderPreset)
	if strings.TrimSpace(cfg.ConnectionID) != "" {
		cfg, err = a.resolveOrchestratorConnection(cfg)
		if err != nil {
			return domain.OrchestratorConfig{}, err
		}
	}
	if cfg.Provider == "" && cfg.Model != "" || cfg.Provider != "" && cfg.Model == "" {
		return domain.OrchestratorConfig{}, errors.New("orchestrator provider and model must be configured together")
	}
	if cfg.Provider != "" {
		if cfg.Provider != domain.ProviderOllama && cfg.Provider != domain.ProviderOpenAI && cfg.Provider != domain.ProviderAnthropic && cfg.Provider != domain.ProviderAzureOpenAI {
			return domain.OrchestratorConfig{}, errors.New("orchestrator provider is not supported")
		}
		if cfg.BaseURL == "" {
			if cfg.Provider == domain.ProviderOllama {
				cfg.BaseURL = "http://127.0.0.1:11434"
			} else if cfg.Provider == domain.ProviderAnthropic {
				cfg.BaseURL = "https://api.anthropic.com/v1"
			} else if cfg.Provider == domain.ProviderOpenAI {
				cfg.BaseURL = "https://api.openai.com/v1"
			} else {
				return domain.OrchestratorConfig{}, errors.New("Azure OpenAI requires a connection with resource URL and API version")
			}
		}
		if err := checkProviderURL("orchestrator", cfg.BaseURL); err != nil {
			return domain.OrchestratorConfig{}, err
		}
		if cfg.Provider == domain.ProviderAzureOpenAI && strings.TrimSpace(cfg.APIVersion) == "" {
			return domain.OrchestratorConfig{}, errors.New("Azure OpenAI connection requires an API version")
		}
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = 0.2
	}
	if cfg.Temperature < 0 || cfg.Temperature > 2 {
		return domain.OrchestratorConfig{}, errors.New("orchestrator temperature must be between 0 and 2")
	}
	if cfg.MaxOutputTokens == 0 {
		cfg.MaxOutputTokens = 8192
	}
	if cfg.MaxOutputTokens < 128 || cfg.MaxOutputTokens > 16384 {
		return domain.OrchestratorConfig{}, errors.New("orchestrator max output tokens must be between 128 and 16384")
	}
	if cfg.ID == "" {
		cfg.ID = domain.NewID("orchestrator")
		cfg.CreatedAt = now
	}
	if cfg.CreatedAt.IsZero() {
		cfg.CreatedAt = now
	}
	cfg.UpdatedAt = now
	if err := a.store.SaveOrchestratorConfig(context.Background(), cfg); err != nil {
		return domain.OrchestratorConfig{}, err
	}
	return cfg, nil
}

func (a *App) loadOrchestratorConfig(workspaceID string) (domain.OrchestratorConfig, bool) {
	if workspaceID == "" {
		return domain.OrchestratorConfig{}, false
	}
	cfg, err := a.store.GetOrchestratorConfig(context.Background(), workspaceID)
	if err != nil {
		return domain.OrchestratorConfig{}, false
	}
	resolved, err := a.resolveOrchestratorConnection(cfg)
	if err != nil {
		return domain.OrchestratorConfig{}, false
	}
	resolved, err = a.applyCheapModelRoute(context.Background(), workspaceID, resolved)
	if err != nil {
		return domain.OrchestratorConfig{}, false
	}
	return resolved, true
}
