package app

// Проверка провайдера живёт отдельно от ядра приложения: у сетевых источников
// она сводится к списку моделей по адресу, у локального CLI — к тому, что сам
// CLI на месте, и оба случая разошлись бы по разным местам app.go.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/providers"
	"local-agent-workbench/internal/security"
)

type ProviderProbeRequest struct {
	Provider domain.ProviderKind `json:"provider"`
	BaseURL  string              `json:"baseUrl"`
	APIKey   string              `json:"apiKey,omitempty"`
	// APIVersion нужна только Azure: без неё запрос отвергается, а в BaseURL её
	// не спрятать — нормализация адреса срезает query.
	APIVersion string `json:"apiVersion,omitempty"`
}
type ProviderProbeResult struct {
	Connected bool                  `json:"connected"`
	Provider  domain.ProviderKind   `json:"provider"`
	BaseURL   string                `json:"baseUrl"`
	Models    []providers.ModelInfo `json:"models"`
	LatencyMs int64                 `json:"latencyMs"`
	// Problem/Fix — что именно не получилось и что с этим делать.
	//
	// Неудачная проверка связи возвращалась ошибкой запроса, и человек видел
	// красный «provider connection failed: dial tcp …». Это сообщение про сокет,
	// а не про то, что делать: не запущен Ollama, не тот адрес, отклонён ключ —
	// всё выглядело одинаково. Несостоявшаяся связь — это состояние, а не сбой.
	Problem string `json:"problem,omitempty"`
	Fix     string `json:"fix,omitempty"`
}

// describeProbeFailure переводит сбой связи в причину и следующий шаг.
// Разбор идёт по тексту ошибки транспорта — иных различий у нас нет, но
// формулировки для человека собираются здесь, а не в интерфейсе.
func describeProbeFailure(provider domain.ProviderKind, baseURL string, err error) (problem, fix string) {
	text := strings.ToLower(err.Error())
	host := observability.HostOnly(baseURL)
	switch {
	case strings.Contains(text, "401"), strings.Contains(text, "403"),
		strings.Contains(text, "unauthorized"), strings.Contains(text, "invalid api key"):
		return "ключ отклонён провайдером", "проверьте API-ключ — он передаётся только этому адресу и хранится в SecretStorage"
	case strings.Contains(text, "404"), strings.Contains(text, "not found"):
		return fmt.Sprintf("адрес %s отвечает, но списка моделей по нему нет", host),
			"обычно в конце адреса нужен /v1 — проверьте базовый URL"
	case strings.Contains(text, "empty model list"):
		return "связь есть, но провайдер не вернул ни одной модели",
			ollamaHint(provider, "загрузите модель у провайдера — подключение рабочее")
	case strings.Contains(text, "timeout"), strings.Contains(text, "deadline exceeded"):
		return fmt.Sprintf("%s не ответил за 10 секунд", host),
			"проверьте, что сервис запущен и отвечает, либо увеличьте адрес до правильного порта"
	case strings.Contains(text, "refused"), strings.Contains(text, "no such host"),
		strings.Contains(text, "dial tcp"), strings.Contains(text, "connectex"):
		return fmt.Sprintf("до %s не достучаться", host),
			ollamaHint(provider, "проверьте адрес и что сервис доступен с этой машины")
	default:
		return "связь не установилась", "проверьте адрес и ключ; подробности — в хронике ядра"
	}
}

// ollamaHint добавляет подсказку про локальный Ollama: это самый частый случай,
// и «запустите ollama serve» полезнее любого общего совета.
func ollamaHint(provider domain.ProviderKind, otherwise string) string {
	if provider == domain.ProviderOllama {
		return "запустите Ollama (`ollama serve`) и убедитесь, что модель скачана: `ollama pull qwen2.5-coder`"
	}
	return otherwise
}

func (a *App) ProbeProvider(request ProviderProbeRequest) (ProviderProbeResult, error) {
	if domain.IsAgentCLIProvider(request.Provider) {
		return ProviderProbeResult{}, errors.New("CLI providers are removed; use an HTTP API provider")
	}
	if !domain.IsHTTPAPIProvider(request.Provider) {
		return ProviderProbeResult{}, errors.New("unsupported provider")
	}
	normalized, err := providers.NormalizeCompatibleBaseURL(request.BaseURL, request.Provider)
	if err != nil {
		return ProviderProbeResult{}, err
	}
	request.BaseURL = normalized
	if len(request.APIKey) > 64*1024 {
		return ProviderProbeResult{}, errors.New("API key exceeds 64 KiB")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	started := time.Now()
	models, err := providers.DiscoverModels(ctx, providers.Config{Kind: request.Provider, BaseURL: request.BaseURL, APIKey: request.APIKey, APIVersion: request.APIVersion, TimeoutSeconds: 10})
	if err != nil {
		slog.Error("provider probe failed",
			"provider", string(request.Provider),
			"host", observability.HostOnly(request.BaseURL),
			"error", security.Redact(err.Error()),
			"duration_ms", time.Since(started).Milliseconds(),
		)
		// Возвращаем состояние, а не ошибку: человеку нужен следующий шаг, а не
		// сообщение о том, что запрос не удался.
		problem, fix := describeProbeFailure(request.Provider, request.BaseURL, err)
		return ProviderProbeResult{
			Provider:  request.Provider,
			BaseURL:   strings.TrimRight(request.BaseURL, "/"),
			Models:    []providers.ModelInfo{},
			LatencyMs: time.Since(started).Milliseconds(),
			Problem:   problem,
			Fix:       fix,
		}, nil
	}
	slog.Info("provider probe ok",
		"provider", string(request.Provider),
		"host", observability.HostOnly(request.BaseURL),
		"models", len(models),
		"latency_ms", time.Since(started).Milliseconds(),
	)
	return ProviderProbeResult{Connected: true, Provider: request.Provider, BaseURL: strings.TrimRight(request.BaseURL, "/"), Models: models, LatencyMs: time.Since(started).Milliseconds()}, nil
}
