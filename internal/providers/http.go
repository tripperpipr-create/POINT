package providers

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// NormalizeCompatibleBaseURL accepts a company gateway such as LLMux.
// A bare host becomes {scheme}://{host}/v1; an explicit path is kept.
func NormalizeCompatibleBaseURL(raw string, kind domain.ProviderKind) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("base URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("base URL must be an absolute HTTP or HTTPS URL")
	}
	if parsed.User != nil {
		return "", errors.New("credentials in the provider URL are not allowed")
	}
	// Anthropic живёт по тому же правилу, что и OpenAI-совместимые шлюзы: голый
	// хост дополняется /v1. Azure — исключение: у него путь строится под каждый
	// запрос из имени deployment, и приписанная версия пути всё сломала бы.
	if kind == domain.ProviderOpenAI || kind == domain.ProviderAnthropic {
		path := strings.TrimRight(parsed.Path, "/")
		if path == "" {
			parsed.Path = "/v1"
		} else {
			parsed.Path = path
		}
	}
	if kind == domain.ProviderAzureOpenAI {
		parsed.Path = strings.TrimRight(parsed.Path, "/")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func client(timeoutSeconds int) *http.Client {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 120
	}
	return &http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second}
}

// streamingClient разводит два срока, которые общий Timeout смешивал в один.
// Заголовки провайдер присылает сразу, как только принял запрос: их отсутствие
// — это молчание, и ждать его столько же, сколько целый ответ, незачем. Сам
// поток после этого не ограничен ничем, кроме контекста вызова: у медленной
// модели ответ идёт долго, и обрывать его на полуслове по общему сроку значит
// терять уже написанное.
func streamingClient(config Config) *http.Client {
	if config.HeaderTimeoutSeconds <= 0 {
		return client(config.TimeoutSeconds)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = time.Duration(config.HeaderTimeoutSeconds) * time.Second
	return &http.Client{Transport: transport}
}

func endpoint(base, path string) string { return strings.TrimRight(base, "/") + path }

type apiTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Parameters  any    `json:"parameters"`
	} `json:"function"`
}
