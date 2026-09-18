package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

const (
	ModelRouteCoding = "coding"
	ModelRouteCheap  = "cheap"
)

type WorkspaceModelRouting = domain.WorkspaceModelRouting

type ResolvedModelRoute struct {
	Purpose          string                  `json:"purpose"`
	ConnectionID     string                  `json:"connectionId,omitempty"`
	Provider         domain.ProviderKind     `json:"provider,omitempty"`
	ProviderPreset   string                  `json:"providerPreset,omitempty"`
	BaseURL          string                  `json:"baseUrl,omitempty"`
	APIVersion       string                  `json:"apiVersion,omitempty"`
	Model            string                  `json:"model,omitempty"`
	ConnectionStatus domain.ConnectionStatus `json:"connectionStatus,omitempty"`
	Configured       bool                    `json:"configured"`
	Required         bool                    `json:"required"`
	Ready            bool                    `json:"ready"`
	BlockReason      string                  `json:"blockReason,omitempty"`
}

type WorkspaceModelRoutingView struct {
	domain.WorkspaceModelRouting
	CodingReady       bool   `json:"codingReady"`
	CodingRequired    bool   `json:"codingRequired"`
	CodingBlockReason string `json:"codingBlockReason"`
}

func (a *App) SaveWorkspaceModelRouting(routing domain.WorkspaceModelRouting) (WorkspaceModelRoutingView, error) {
	workspace, err := a.requireWorkspace()
	if err != nil {
		return WorkspaceModelRoutingView{}, err
	}
	if routing.WorkspaceID != "" && routing.WorkspaceID != workspace.ID {
		return WorkspaceModelRoutingView{}, errors.New("model routing workspace does not match the open project")
	}
	routing.WorkspaceID = workspace.ID
	routing.CodingConnectionID = strings.TrimSpace(routing.CodingConnectionID)
	routing.CodingModel = strings.TrimSpace(routing.CodingModel)
	routing.CheapConnectionID = strings.TrimSpace(routing.CheapConnectionID)
	routing.CheapModel = strings.TrimSpace(routing.CheapModel)
	for _, item := range []struct {
		id      string
		purpose string
	}{
		{routing.CodingConnectionID, ModelRouteCoding},
		{routing.CheapConnectionID, ModelRouteCheap},
	} {
		if item.id == "" {
			continue
		}
		connection, resolveErr := a.ResolveConnection(ConnectionRef{ConnectionID: item.id, Label: item.purpose + "-маршрута"})
		if resolveErr != nil {
			return WorkspaceModelRoutingView{}, resolveErr
		}
		if item.purpose == ModelRouteCheap && !cheapRouteProvider(connection.Provider) {
			return WorkspaceModelRoutingView{}, fmt.Errorf("провайдер %q не поддерживается для cheap-маршрута Мастера", connection.Provider)
		}
	}
	routing.UpdatedAt = time.Now().UTC()
	if err = a.store.SaveWorkspaceModelRouting(context.Background(), routing); err != nil {
		return WorkspaceModelRoutingView{}, err
	}
	return a.workspaceModelRoutingView(routing)
}

func (a *App) GetWorkspaceModelRouting() (WorkspaceModelRoutingView, error) {
	workspace, err := a.requireWorkspace()
	if err != nil {
		return WorkspaceModelRoutingView{}, err
	}
	routing, err := a.store.GetWorkspaceModelRouting(context.Background(), workspace.ID)
	if errors.Is(err, sql.ErrNoRows) {
		routing = domain.WorkspaceModelRouting{WorkspaceID: workspace.ID}
	} else if err != nil {
		return WorkspaceModelRoutingView{}, err
	}
	return a.workspaceModelRoutingView(routing)
}

// ResolveModelRoute разрешает только workspace-override. Пустой маршрут
// означает, что вызывающий оставляет модель конкретного агента или Мастера.
func (a *App) ResolveModelRoute(purpose string) (ResolvedModelRoute, error) {
	workspace, err := a.requireWorkspace()
	if err != nil {
		return ResolvedModelRoute{}, err
	}
	return a.resolveModelRoute(context.Background(), workspace.ID, purpose)
}

func (a *App) resolveModelRoute(ctx context.Context, workspaceID, purpose string) (ResolvedModelRoute, error) {
	routing, err := a.store.GetWorkspaceModelRouting(ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		routing = domain.WorkspaceModelRouting{WorkspaceID: workspaceID}
	} else if err != nil {
		return ResolvedModelRoute{}, err
	}
	return a.resolveStoredModelRoute(routing, purpose)
}

func (a *App) resolveStoredModelRoute(routing domain.WorkspaceModelRouting, purpose string) (ResolvedModelRoute, error) {
	route := ResolvedModelRoute{Purpose: purpose}
	switch purpose {
	case ModelRouteCoding:
		route.ConnectionID, route.Model = routing.CodingConnectionID, routing.CodingModel
		route.Required = strings.TrimSpace(route.ConnectionID) != ""
	case ModelRouteCheap:
		route.ConnectionID, route.Model = routing.CheapConnectionID, routing.CheapModel
	default:
		return ResolvedModelRoute{}, fmt.Errorf("unknown model route purpose %q", purpose)
	}
	route.ConnectionID = strings.TrimSpace(route.ConnectionID)
	route.Model = strings.TrimSpace(route.Model)
	route.Configured = route.ConnectionID != "" || route.Model != ""
	if !route.Configured {
		return route, nil
	}
	// Маршрут только с моделью намеренно оставляет подключение самого актора.
	if route.ConnectionID == "" {
		route.Ready = route.Model != ""
		return route, nil
	}
	connection, err := a.ResolveConnection(ConnectionRef{ConnectionID: route.ConnectionID, Label: purpose + "-маршрута"})
	if err != nil {
		route.BlockReason = err.Error()
		return route, nil
	}
	route.Provider = connection.Provider
	route.ProviderPreset = connection.PresetID
	route.BaseURL = connection.BaseURL
	route.APIVersion = connection.APIVersion
	route.ConnectionStatus = connection.Status
	if route.Model == "" {
		route.Model = strings.TrimSpace(connection.DefaultModel)
	}
	switch {
	case route.Model == "":
		route.BlockReason = fmt.Sprintf("для подключения «%s» не выбрана модель", modelRouteConnectionName(connection))
	case purpose == ModelRouteCoding && connection.Status != domain.ConnectionConnected:
		route.BlockReason = fmt.Sprintf("подключение «%s» не готово: статус %s", modelRouteConnectionName(connection), connection.Status)
		if detail := strings.TrimSpace(connection.LastError); detail != "" {
			route.BlockReason += " — " + detail
		}
	default:
		route.Ready = true
	}
	return route, nil
}

func (a *App) workspaceModelRoutingView(routing domain.WorkspaceModelRouting) (WorkspaceModelRoutingView, error) {
	route, err := a.resolveStoredModelRoute(routing, ModelRouteCoding)
	if err != nil {
		return WorkspaceModelRoutingView{}, err
	}
	view := WorkspaceModelRoutingView{
		WorkspaceModelRouting: routing,
		CodingRequired:        strings.TrimSpace(routing.CodingConnectionID) != "",
	}
	// Без обязательного connectionId модель конкретного агента остаётся
	// fallback-путём; CodingReady описывает именно выбранное подключение.
	if view.CodingRequired {
		view.CodingReady = route.Ready
		if !route.Ready {
			view.CodingBlockReason = route.BlockReason
		}
	}
	return view, nil
}

func (a *App) applyCodingModelRoute(workspaceID string, profile *domain.AgentProfile) error {
	route, err := a.resolveModelRoute(context.Background(), workspaceID, ModelRouteCoding)
	if err != nil {
		return err
	}
	if !route.Configured {
		return nil
	}
	if route.Required && !route.Ready {
		return codingRouteUnavailableError(route)
	}
	if route.ConnectionID != "" {
		profile.ConnectionID = route.ConnectionID
		profile.Provider = route.Provider
		profile.ProviderPreset = route.ProviderPreset
		profile.BaseURL = route.BaseURL
		profile.APIVersion = route.APIVersion
	}
	if route.Model != "" {
		profile.Model = route.Model
	}
	return nil
}

func (a *App) ensureCodingModelRouteReady(workspaceID string) error {
	route, err := a.resolveModelRoute(context.Background(), workspaceID, ModelRouteCoding)
	if err != nil {
		return err
	}
	if route.Required && !route.Ready {
		return codingRouteUnavailableError(route)
	}
	return nil
}

func codingRouteUnavailableError(route ResolvedModelRoute) error {
	reason := strings.TrimSpace(route.BlockReason)
	if reason == "" {
		reason = "выбранное подключение не готово"
	}
	return fmt.Errorf("coding-маршрут обязателен, но недоступен: %s", reason)
}

func (a *App) applyCheapModelRoute(ctx context.Context, workspaceID string, cfg domain.OrchestratorConfig) (domain.OrchestratorConfig, error) {
	route, err := a.resolveModelRoute(ctx, workspaceID, ModelRouteCheap)
	if err != nil {
		return domain.OrchestratorConfig{}, err
	}
	// Cheap — необязательная оптимизация. Пустой или устаревший маршрут не
	// меняет сохранённую цель Мастера.
	if !route.Configured || !route.Ready {
		return cfg, nil
	}
	if route.ConnectionID != "" {
		cfg.ConnectionID = route.ConnectionID
		cfg.Provider = route.Provider
		cfg.ProviderPreset = route.ProviderPreset
		cfg.BaseURL = route.BaseURL
		cfg.APIVersion = route.APIVersion
	}
	if route.Model != "" {
		cfg.Model = route.Model
	}
	return cfg, nil
}

func cheapRouteProvider(provider domain.ProviderKind) bool {
	switch provider {
	case domain.ProviderOllama, domain.ProviderOpenAI, domain.ProviderAnthropic, domain.ProviderAzureOpenAI:
		return true
	default:
		return false
	}
}

func modelRouteConnectionName(connection domain.Connection) string {
	if name := strings.TrimSpace(connection.DisplayName); name != "" {
		return name
	}
	return connection.ID
}
