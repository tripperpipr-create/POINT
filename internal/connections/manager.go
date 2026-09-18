package connections

import (
	"context"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"

	"local-agent-workbench/internal/security"
)

type Store interface {
	SaveConnection(ctx context.Context, conn domain.Connection) error
	ListConnections(ctx context.Context) ([]domain.Connection, error)
}

// SecretStore is backed by VS Code SecretStorage on the extension side.
// Go only keeps secretRef identifiers, never raw credentials.
type SecretStore interface {
	Get(ctx context.Context, ref string) (string, error)
}

type Manager struct {
	Store   Store
	Secrets SecretStore
}

type UpsertRequest struct {
	ID          string                  `json:"id"`
	Provider    domain.ProviderKind     `json:"provider"`
	PresetID    string                  `json:"presetId"`
	DisplayName string                  `json:"displayName"`
	BaseURL     string                  `json:"baseUrl"`
	SecretRef   string                  `json:"secretRef"`
	Status      domain.ConnectionStatus `json:"status"`
	// APIVersion нужна только Azure; DefaultModel избавляет нового агента от
	// пустого поля модели — подключение уже знает, какой обычно пользуются.
	APIVersion   string `json:"apiVersion"`
	DefaultModel string `json:"defaultModel"`
}

type ModelMeta struct {
	Provider      string   `json:"provider,omitempty"`
	Model         string   `json:"model"`
	Family        string   `json:"family,omitempty"`
	Capabilities  []string `json:"capabilities,omitempty"`
	ContextWindow int      `json:"contextWindow,omitempty"`
	MaxOutput     int      `json:"maxOutput,omitempty"`
	PricingKnown  bool     `json:"pricingKnown"`
	LimitsKnown   bool     `json:"limitsKnown"`
	State         string   `json:"state"` // confirmed | known | unknown
}

// ModelSelector picks primary or classified fallback models.
type ModelSelector interface {
	Select(primary string, fallbacks []string, err error) (string, bool)
}

type ClassifiedSelector struct{}

func (ClassifiedSelector) Select(primary string, fallbacks []string, err error) (string, bool) {
	if err == nil {
		return primary, false
	}
	if !isFallbackError(err) {
		return primary, false
	}
	if len(fallbacks) == 0 {
		return primary, false
	}
	return fallbacks[0], true
}

func isFallbackError(err error) bool {
	msg := strings.ToLower(err.Error())
	markers := []string{
		"rate limit", "timeout", "timed out", "429", "502", "503", "504",
		"temporarily unavailable", "service unavailable", "overloaded",
		"connection refused", "connection reset", "provider error", "upstream closed",
		"unexpected eof", "stream without sending",
		"went to reasoning", "ran out during reasoning",
	}
	for _, marker := range markers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func (m Manager) Upsert(ctx context.Context, req UpsertRequest) (domain.Connection, error) {
	now := time.Now().UTC()
	conn := domain.Connection{
		ID: req.ID, Provider: req.Provider, PresetID: req.PresetID, DisplayName: req.DisplayName,
		BaseURL: req.BaseURL, SecretRef: req.SecretRef, Status: req.Status,
		APIVersion: strings.TrimSpace(req.APIVersion), DefaultModel: strings.TrimSpace(req.DefaultModel),
		CreatedAt: now, UpdatedAt: now,
	}
	if conn.ID == "" {
		conn.ID = domain.NewID("connection")
	} else if existing, err := m.findByID(ctx, conn.ID); err != nil {
		return domain.Connection{}, err
	} else if existing != nil {
		conn.CreatedAt = existing.CreatedAt
		if conn.SecretRef == "" {
			conn.SecretRef = existing.SecretRef
		}
		conn.LastError = existing.LastError
		conn.LastProbeAt = existing.LastProbeAt
		// Каталог моделей и признак «по умолчанию» принадлежат подключению, а не
		// форме: сохранение имени не должно стирать результат проверки.
		conn.Models = existing.Models
		conn.CatalogUpdatedAt = existing.CatalogUpdatedAt
		conn.IsDefault = existing.IsDefault
		if conn.APIVersion == "" {
			conn.APIVersion = existing.APIVersion
		}
		if conn.DefaultModel == "" {
			conn.DefaultModel = existing.DefaultModel
		}
	}
	if domain.IsAgentCLIProvider(conn.Provider) {
		return domain.Connection{}, fmt.Errorf("провайдер %q снят: Point работает только через HTTP API", conn.Provider)
	}
	if !domain.IsHTTPAPIProvider(conn.Provider) {
		return domain.Connection{}, fmt.Errorf("провайдер подключения %q не поддерживается", conn.Provider)
	}
	if conn.Provider == domain.ProviderAzureOpenAI && strings.TrimSpace(conn.BaseURL) == "" {
		return domain.Connection{}, fmt.Errorf("укажите адрес ресурса Azure OpenAI")
	}
	if conn.Provider == domain.ProviderAzureOpenAI && strings.TrimSpace(conn.APIVersion) == "" {
		return domain.Connection{}, fmt.Errorf("укажите API version Azure OpenAI")
	}
	if conn.Provider == domain.ProviderOpenAI || conn.Provider == domain.ProviderAnthropic || conn.Provider == domain.ProviderAzureOpenAI {
		if strings.TrimSpace(conn.BaseURL) == "" {
			return domain.Connection{}, fmt.Errorf("укажите адрес OpenAI-совместимого сервиса")
		}
		normalized, err := providers.NormalizeCompatibleBaseURL(conn.BaseURL, conn.Provider)
		if err != nil {
			return domain.Connection{}, err
		}
		conn.BaseURL = normalized
	}
	if conn.DisplayName == "" {
		conn.DisplayName = string(conn.Provider)
	}
	if conn.Status == "" {
		conn.Status = domain.ConnectionUnknown
	}
	// Ссылка на секрет заводится только тем, у кого секрет есть. Локальному CLI и
	// Ollama он не нужен: первый берёт вход из своей же учётной записи, второй
	// работает без ключа, — а пустая ссылка в хранилище выглядит как потерянный
	// ключ.
	if strings.TrimSpace(conn.SecretRef) == "" && conn.Provider != domain.ProviderOllama {
		conn.SecretRef = "point.connection." + conn.ID
	}
	if err := m.Store.SaveConnection(ctx, conn); err != nil {
		return domain.Connection{}, err
	}
	return conn, nil
}

func (m Manager) findByID(ctx context.Context, id string) (*domain.Connection, error) {
	list, err := m.Store.ListConnections(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			item := list[i]
			return &item, nil
		}
	}
	return nil, nil
}

func (m Manager) List(ctx context.Context) ([]domain.Connection, error) {
	return m.Store.ListConnections(ctx)
}

func (m Manager) CredentialForCall(ctx context.Context, conn domain.Connection) (string, error) {
	if conn.SecretRef == "" {
		return "", nil
	}
	if m.Secrets == nil {
		return "", fmt.Errorf("secret store unavailable; credential must come from OS keychain via SecretStorage")
	}
	return m.Secrets.Get(ctx, conn.SecretRef)
}

func (m Manager) MarkStatus(ctx context.Context, id string, status domain.ConnectionStatus, lastError string) (domain.Connection, error) {
	list, err := m.Store.ListConnections(ctx)
	if err != nil {
		return domain.Connection{}, err
	}
	for _, conn := range list {
		if conn.ID != id {
			continue
		}
		now := time.Now().UTC()
		conn.Status = status
		// Ошибка приходит снаружи и может нести ключ или строку подключения:
		// она сохраняется и показывается на экране «Связи».
		conn.LastError = security.Redact(lastError)
		conn.LastProbeAt = &now
		conn.UpdatedAt = now
		if err := m.Store.SaveConnection(ctx, conn); err != nil {
			return domain.Connection{}, err
		}
		return conn, nil
	}
	return domain.Connection{}, fmt.Errorf("connection %s not found", id)
}

// DefaultModelCatalog отдаёт встроенный справочник семейств, а не три
// выдуманные строки, как раньше. Интерфейс берёт отсюда контекстное окно и
// возможности, когда провайдер их не прислал; `Model` здесь — префикс
// семейства, а `State: "known"` честно говорит, что это ориентир, а не факт о
// конкретной версии.
func DefaultModelCatalog() []ModelMeta {
	references := domain.ModelReferences()
	result := make([]ModelMeta, 0, len(references)+1)
	for _, reference := range references {
		result = append(result, ModelMeta{
			Model:         reference.Prefix,
			Family:        reference.Family,
			Capabilities:  reference.Capabilities,
			ContextWindow: reference.ContextWindow,
			MaxOutput:     reference.MaxOutput,
			State:         "known",
		})
	}
	return result
}
