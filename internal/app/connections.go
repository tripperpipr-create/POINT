package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

// ConnectionRef — то, чем сущность Хаба указывает на своё подключение.
//
// ConnectionID появился миграцией 31; до неё агент, компаньон и Дирижёр хранили
// копии provider/preset/URL, а ключ подбирался поиском первого подключения с
// тем же пресетом. При двух ключах одного провайдера — личном и рабочем, dev и
// prod — запрос молча уходил с чужим. Остальные поля здесь именно для тех
// строк, которым связь ещё не досталась.
type ConnectionRef struct {
	ConnectionID   string
	ProviderPreset string
	Provider       domain.ProviderKind
	Label          string
}

// ErrConnectionAmbiguous возвращается вместо молчаливого выбора первого
// совпадения: это ровно тот случай, который раньше и ломался.
var ErrConnectionAmbiguous = errors.New("подключение выбрано неоднозначно")

func (a *App) ListConnections() ([]domain.Connection, error) {
	list, err := a.store.ListConnections(context.Background())
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []domain.Connection{}
	}
	return list, nil
}

// ResolveConnection отвечает на единственный вопрос: каким подключением идёт
// этот запрос. Точная ссылка выигрывает всегда; совместимость по пресету
// работает, только когда кандидат ровно один.
func (a *App) ResolveConnection(ref ConnectionRef) (domain.Connection, error) {
	list, err := a.store.ListConnections(context.Background())
	if err != nil {
		return domain.Connection{}, err
	}
	if id := strings.TrimSpace(ref.ConnectionID); id != "" {
		for _, item := range list {
			if item.ID == id {
				return item, nil
			}
		}
		return domain.Connection{}, fmt.Errorf("подключение %q не найдено: выберите его заново в разделе «Связи»", id)
	}
	var matches []domain.Connection
	for _, item := range list {
		switch {
		case ref.ProviderPreset != "" && item.PresetID == ref.ProviderPreset:
			matches = append(matches, item)
		case ref.ProviderPreset == "" && ref.Provider != "" && item.Provider == ref.Provider:
			matches = append(matches, item)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return domain.Connection{}, fmt.Errorf("для %s нет подключения: создайте его в разделе «Связи»", refLabel(ref))
	default:
		names := make([]string, 0, len(matches))
		for _, item := range matches {
			name := item.DisplayName
			if name == "" {
				name = item.ID
			}
			names = append(names, name)
		}
		return domain.Connection{}, fmt.Errorf("%w: для %s подходят %s. Выберите одно из них явно",
			ErrConnectionAmbiguous, refLabel(ref), strings.Join(names, ", "))
	}
}

func refLabel(ref ConnectionRef) string {
	if ref.Label != "" {
		return ref.Label
	}
	if ref.ProviderPreset != "" {
		return ref.ProviderPreset
	}
	if ref.Provider != "" {
		return string(ref.Provider)
	}
	return "этой настройки"
}

// DeleteConnection отказывается удалять подключение, на которое ссылаются:
// иначе профиль продолжал бы утверждать, что настроен, а следующий квест падал
// бы без внятной причины.
func (a *App) DeleteConnection(id string) error {
	ctx := context.Background()
	users, err := a.store.ConnectionUsage(ctx, id)
	if err != nil {
		return err
	}
	if len(users) > 0 {
		return fmt.Errorf("подключение используется: %s. Сначала переключите их на другое подключение", strings.Join(users, ", "))
	}
	return a.store.DeleteConnection(ctx, id)
}

func (a *App) SetDefaultConnection(id string) error {
	return a.store.SetDefaultConnection(context.Background(), id)
}

// ProbeConnection проверяет уже сохранённое подключение и запоминает каталог
// моделей. Ключ приходит транзитом из SecretStorage расширения и не пишется ни
// в SQLite, ни в журнал — сохраняется только список моделей.
func (a *App) ProbeConnection(id, apiKey string) (domain.Connection, error) {
	ctx := context.Background()
	list, err := a.store.ListConnections(ctx)
	if err != nil {
		return domain.Connection{}, err
	}
	var conn domain.Connection
	found := false
	for _, item := range list {
		if item.ID == id {
			conn, found = item, true
			break
		}
	}
	if !found {
		return domain.Connection{}, fmt.Errorf("подключение %q не найдено", id)
	}
	baseURL := conn.BaseURL
	if baseURL == "" {
		for _, preset := range domain.BuiltInProviderCatalog() {
			if preset.ID == conn.PresetID {
				baseURL = preset.BaseURL
				break
			}
		}
	}
	result, err := a.ProbeProvider(ProviderProbeRequest{Provider: conn.Provider, BaseURL: baseURL, APIKey: apiKey, APIVersion: conn.APIVersion})
	if err != nil {
		return domain.Connection{}, err
	}
	now := time.Now().UTC()
	conn.LastProbeAt = &now
	conn.UpdatedAt = now
	if result.Connected {
		conn.Status = domain.ConnectionConnected
		conn.LastError = ""
		conn.BaseURL = result.BaseURL
		conn.Models = describeModels(result.Models)
		conn.CatalogUpdatedAt = &now
	} else {
		// Неудачная проверка не стирает прежний каталог: список моделей
		// перестал бы показываться из-за одного сетевого сбоя.
		conn.Status = domain.ConnectionError
		conn.LastError = strings.TrimSpace(result.Problem + " " + result.Fix)
	}
	if err = a.store.SaveConnection(ctx, conn); err != nil {
		return domain.Connection{}, err
	}
	return conn, nil
}

// describeModels склеивает то, что прислал провайдер, с встроенным
// справочником. State говорит, откуда взялись пределы, чтобы интерфейс мог
// честно сказать «неизвестно» вместо выдуманного числа.
func describeModels(discovered []providers.ModelInfo) []domain.ConnectionModel {
	result := make([]domain.ConnectionModel, 0, len(discovered))
	for _, item := range discovered {
		model := domain.ConnectionModel{ID: item.ID, DisplayName: item.DisplayName, OwnedBy: item.OwnedBy, State: "unknown"}
		if known, ok := domain.LookupModel(item.ID); ok {
			model.ContextWindow = known.ContextWindow
			model.MaxOutput = known.MaxOutput
			model.Capabilities = known.Capabilities
			model.State = "known"
		}
		result = append(result, model)
	}
	return result
}

// applyConnectionEndpoint переносит в профиль адрес и версию API из связанного
// подключения. Профиль без ссылки остаётся как есть — это compatibility-путь,
// и ломать его переносом ничего не должно.
func (a *App) applyConnectionEndpoint(profile *domain.AgentProfile) error {
	if profile == nil || strings.TrimSpace(profile.ConnectionID) == "" {
		return nil
	}
	conn, err := a.ResolveConnection(ConnectionRef{
		ConnectionID:   profile.ConnectionID,
		ProviderPreset: profile.ProviderPreset,
		Provider:       profile.Provider,
		Label:          "агента «" + profile.Name + "»",
	})
	if err != nil {
		return err
	}
	if conn.BaseURL != "" {
		profile.BaseURL = conn.BaseURL
	}
	profile.APIVersion = conn.APIVersion
	profile.ProviderPreset = conn.PresetID
	if conn.Provider != "" {
		profile.Provider = conn.Provider
	}
	if strings.TrimSpace(profile.Model) == "" && strings.TrimSpace(conn.DefaultModel) != "" {
		profile.Model = conn.DefaultModel
	}
	return nil
}

func (a *App) resolveCompanionConnection(cfg domain.CompanionConfig) (domain.CompanionConfig, error) {
	if strings.TrimSpace(cfg.ConnectionID) == "" {
		return cfg, nil
	}
	conn, err := a.ResolveConnection(ConnectionRef{
		ConnectionID: cfg.ConnectionID, ProviderPreset: cfg.ProviderPreset,
		Provider: cfg.Provider, Label: "компаньона",
	})
	if err != nil {
		return domain.CompanionConfig{}, err
	}
	cfg.Provider, cfg.ProviderPreset, cfg.BaseURL, cfg.APIVersion = conn.Provider, conn.PresetID, conn.BaseURL, conn.APIVersion
	if strings.TrimSpace(cfg.Model) == "" && strings.TrimSpace(conn.DefaultModel) != "" {
		cfg.Model = conn.DefaultModel
	}
	return cfg, nil
}

func (a *App) resolveOrchestratorConnection(cfg domain.OrchestratorConfig) (domain.OrchestratorConfig, error) {
	if strings.TrimSpace(cfg.ConnectionID) == "" {
		return cfg, nil
	}
	conn, err := a.ResolveConnection(ConnectionRef{
		ConnectionID: cfg.ConnectionID, ProviderPreset: cfg.ProviderPreset,
		Provider: cfg.Provider, Label: "Мастера",
	})
	if err != nil {
		return domain.OrchestratorConfig{}, err
	}
	cfg.Provider, cfg.ProviderPreset, cfg.BaseURL, cfg.APIVersion = conn.Provider, conn.PresetID, conn.BaseURL, conn.APIVersion
	if strings.TrimSpace(cfg.Model) == "" && strings.TrimSpace(conn.DefaultModel) != "" {
		cfg.Model = conn.DefaultModel
	}
	return cfg, nil
}
