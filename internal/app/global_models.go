package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/orchestrator"
)

const globalModelsSettingKey = "global-model-defaults-v1"

func (a *App) GlobalModelDefaults(ctx context.Context) (domain.GlobalModelDefaults, error) {
	raw, err := a.store.Setting(ctx, globalModelsSettingKey)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.GlobalModelDefaults{}, nil
	}
	if err != nil {
		return domain.GlobalModelDefaults{}, err
	}
	var defaults domain.GlobalModelDefaults
	if err = json.Unmarshal([]byte(raw), &defaults); err != nil {
		return domain.GlobalModelDefaults{}, err
	}
	return defaults, nil
}

func (a *App) SaveGlobalModelDefaults(ctx context.Context, defaults domain.GlobalModelDefaults) (domain.GlobalModelDefaults, error) {
	for _, item := range []struct {
		label  string
		choice *domain.GlobalModelChoice
	}{
		{"master", &defaults.Master}, {"archivist", &defaults.Archivist}, {"agent", &defaults.Agent},
	} {
		item.choice.ConnectionID = strings.TrimSpace(item.choice.ConnectionID)
		item.choice.Model = strings.TrimSpace(item.choice.Model)
		if item.choice.ConnectionID == "" && item.choice.Model == "" {
			continue
		}
		if item.choice.ConnectionID == "" {
			return domain.GlobalModelDefaults{}, fmt.Errorf("%s model requires a connection", item.label)
		}
		connection, err := a.ResolveConnection(ConnectionRef{ConnectionID: item.choice.ConnectionID, Label: item.label})
		if err != nil {
			return domain.GlobalModelDefaults{}, err
		}
		if item.choice.Model == "" {
			item.choice.Model = strings.TrimSpace(connection.DefaultModel)
		}
		if item.choice.Model == "" {
			return domain.GlobalModelDefaults{}, fmt.Errorf("%s connection has no default model", item.label)
		}
	}
	defaults.UpdatedAt = time.Now().UTC()
	raw, err := json.Marshal(defaults)
	if err != nil {
		return domain.GlobalModelDefaults{}, err
	}
	if err = a.store.SaveSetting(ctx, globalModelsSettingKey, string(raw)); err != nil {
		return domain.GlobalModelDefaults{}, err
	}
	return defaults, nil
}

func (a *App) globalMasterConfig(ctx context.Context, workspaceID string, cfg domain.OrchestratorConfig) (domain.OrchestratorConfig, error) {
	if cfg.ProjectModelOverride {
		return cfg, nil
	}
	defaults, err := a.GlobalModelDefaults(ctx)
	if err != nil {
		return cfg, err
	}
	if defaults.UpdatedAt.IsZero() {
		return cfg, nil
	}
	if cfg.ID == "" {
		if preset, ok := orchestrator.PresetDefaults("conductor"); ok {
			cfg = preset
		}
	}
	cfg.ConnectionID, cfg.Model = defaults.Master.ConnectionID, defaults.Master.Model
	cfg.Provider, cfg.ProviderPreset, cfg.BaseURL, cfg.APIVersion = "", "", "", ""
	cfg.WorkspaceID = workspaceID
	if cfg.Preset == "" {
		cfg.Preset = "conductor"
	}
	return cfg, nil
}
