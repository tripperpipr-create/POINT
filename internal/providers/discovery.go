package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/security"
)

const maxDiscoveryResponseBytes = 2 * 1024 * 1024

type ModelInfo struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"displayName"`
	OwnedBy     string    `json:"ownedBy,omitempty"`
	Size        int64     `json:"size,omitempty"`
	ModifiedAt  time.Time `json:"modifiedAt,omitempty"`
}

func DiscoverModels(ctx context.Context, config Config) ([]ModelInfo, error) {
	log := observability.From(ctx)
	if domain.IsAgentCLIProvider(config.Kind) {
		return nil, fmt.Errorf("provider %q is removed; Point uses HTTP API providers only", config.Kind)
	}
	requestURL := endpoint(config.BaseURL, "/models")
	switch config.Kind {
	case domain.ProviderOllama:
		requestURL = endpoint(config.BaseURL, "/api/tags")
	case domain.ProviderAzureOpenAI:
		version := strings.TrimSpace(config.APIVersion)
		if version == "" {
			version = defaultAzureAPIVersion
		}
		requestURL = endpoint(config.BaseURL, "/openai/models") + "?api-version=" + url.QueryEscape(version)
	}
	log.Info("provider discover models",
		"host", observability.HostOnly(config.BaseURL),
		"kind", string(config.Kind),
		"has_api_key", strings.TrimSpace(config.APIKey) != "",
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if key := strings.TrimSpace(config.APIKey); key != "" {
		switch config.Kind {
		case domain.ProviderOpenAI:
			request.Header.Set("Authorization", "Bearer "+key)
		case domain.ProviderAzureOpenAI:
			request.Header.Set("api-key", key)
		case domain.ProviderAnthropic:
			request.Header.Set("x-api-key", key)
			request.Header.Set("anthropic-version", anthropicVersion)
		}
	}
	started := time.Now()
	response, err := client(config.TimeoutSeconds).Do(request)
	if err != nil {
		log.Error("provider discover transport error", "host", observability.HostOnly(config.BaseURL), "error", security.Redact(err.Error()))
		return nil, fmt.Errorf("connect to provider: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		log.Error("provider discover http error",
			"host", observability.HostOnly(config.BaseURL),
			"status", response.StatusCode,
			"body", observability.Snippet(security.Redact(string(snippet)), 800),
			"duration_ms", time.Since(started).Milliseconds(),
		)
		return nil, fmt.Errorf("provider returned %s: %s", response.Status, strings.TrimSpace(string(snippet)))
	}
	limited := io.LimitReader(response.Body, maxDiscoveryResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read provider model list: %w", err)
	}
	if len(body) > maxDiscoveryResponseBytes {
		return nil, errors.New("provider model list exceeds 2 MiB")
	}
	var models []ModelInfo
	if config.Kind == domain.ProviderOllama {
		var payload struct {
			Models []struct {
				Name       string    `json:"name"`
				Model      string    `json:"model"`
				Size       int64     `json:"size"`
				ModifiedAt time.Time `json:"modified_at"`
			} `json:"models"`
		}
		if err = json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("decode Ollama model list: %w", err)
		}
		for _, item := range payload.Models {
			id := strings.TrimSpace(item.Name)
			if id == "" {
				id = strings.TrimSpace(item.Model)
			}
			models = appendModel(models, ModelInfo{ID: id, DisplayName: id, Size: item.Size, ModifiedAt: item.ModifiedAt})
		}
	} else {
		// Anthropic отдаёт тот же конверт data[], но с человеческим именем в
		// display_name — оно и показывается в списке моделей.
		var payload struct {
			Data []struct {
				ID          string `json:"id"`
				OwnedBy     string `json:"owned_by"`
				DisplayName string `json:"display_name"`
			} `json:"data"`
		}
		if err = json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("decode OpenAI-compatible model list: %w", err)
		}
		for _, item := range payload.Data {
			id := strings.TrimSpace(item.ID)
			name := strings.TrimSpace(item.DisplayName)
			if name == "" {
				name = id
			}
			models = appendModel(models, ModelInfo{ID: id, DisplayName: name, OwnedBy: strings.TrimSpace(item.OwnedBy)})
		}
	}
	if len(models) == 0 {
		return nil, errors.New("provider returned an empty model list")
	}
	sort.Slice(models, func(i, j int) bool { return strings.ToLower(models[i].ID) < strings.ToLower(models[j].ID) })
	ids := make([]string, 0, min(12, len(models)))
	for i, model := range models {
		if i >= 12 {
			break
		}
		ids = append(ids, model.ID)
	}
	log.Info("provider discover models ok",
		"host", observability.HostOnly(config.BaseURL),
		"count", len(models),
		"models", strings.Join(ids, ","),
		"duration_ms", time.Since(started).Milliseconds(),
	)
	return models, nil
}

func appendModel(models []ModelInfo, model ModelInfo) []ModelInfo {
	if model.ID == "" || len(model.ID) > 200 || len(models) >= 500 {
		return models
	}
	for _, existing := range models {
		if existing.ID == model.ID {
			return models
		}
	}
	return append(models, model)
}
