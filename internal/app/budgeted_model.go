package app

import (
	"context"
	"fmt"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

// modelBudgetScope identifies model calls that do not belong to the agent
// engine (Master, Companion, planner, probes and background learning). Keeping
// them behind the same controller makes the workspace hard limit a real global
// ceiling instead of an agent-only limit.
type modelBudgetScope struct {
	WorkspaceID string
	// ProviderPreset нужен бюджету, а не протоколу: вид провайдера не говорит,
	// тарифицирует ли endpoint токены, а пресет говорит.
	ProviderPreset string
	QuestID        string
	ExecutionID    string
	RunID          string
	ProjectAgentID string
	Outcome        string
}

type budgetedModel struct {
	app      *App
	inner    providers.Model
	provider domain.ProviderKind
	scope    modelBudgetScope
}

func (a *App) budgetedModelFactory(scope modelBudgetScope) func(providers.Config) (providers.Model, error) {
	return func(config providers.Config) (providers.Model, error) {
		model, err := providers.New(config)
		if err != nil {
			return nil, err
		}
		scoped := scope
		if scoped.ProviderPreset == "" {
			scoped.ProviderPreset = config.Preset
		}
		return a.wrapBudgetedModel(model, config.Kind, scoped), nil
	}
}

func (a *App) wrapBudgetedModel(model providers.Model, provider domain.ProviderKind, scope modelBudgetScope) providers.Model {
	if model == nil || a == nil || a.store == nil || scope.WorkspaceID == "" {
		return model
	}
	return &budgetedModel{app: a, inner: model, provider: provider, scope: scope}
}

func (m *budgetedModel) Stream(ctx context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	maxOutput := int64(request.MaxOutputTokens)
	if maxOutput <= 0 {
		// Provider defaults vary and are often much larger than the answer the
		// caller expects. Reserve a conservative bounded default when a legacy
		// caller did not specify one.
		maxOutput = 4096
	}
	reservationID, err := m.app.ReserveModelBudget(ctx, agent.ModelBudgetRequest{
		WorkspaceID: m.scope.WorkspaceID, QuestID: m.scope.QuestID, ExecutionID: m.scope.ExecutionID,
		RunID: m.scope.RunID, Provider: m.provider, ProviderPreset: m.scope.ProviderPreset, Model: request.Model,
		EstimatedInputTokens: int64(agent.EstimateModelInputTokens(request.Messages, request.Tools)),
		MaxOutputTokens:      maxOutput,
	})
	if err != nil {
		return fmt.Errorf("reserve model budget: %w", err)
	}
	request.MaxOutputTokens = int(maxOutput)
	var inputTokens, outputTokens int64
	usageReported := false
	streamErr := m.inner.Stream(ctx, request, func(event providers.ModelEvent) error {
		if event.Kind == providers.EventUsage {
			usageReported = true
			if int64(event.InputTokens) > inputTokens {
				inputTokens = int64(event.InputTokens)
			}
			if int64(event.OutputTokens) > outputTokens {
				outputTokens = int64(event.OutputTokens)
			}
		}
		return emit(event)
	})
	reconcileErr := m.app.ReconcileModelBudget(context.Background(), agent.ModelBudgetSettlement{
		ReservationID: reservationID, WorkspaceID: m.scope.WorkspaceID, ProjectAgentID: m.scope.ProjectAgentID,
		Provider: m.provider, Model: request.Model, InputTokens: inputTokens, OutputTokens: outputTokens,
		UsageReported: usageReported, Outcome: m.scope.Outcome,
	})
	if reconcileErr != nil {
		if streamErr != nil {
			return fmt.Errorf("model stream failed (%v); reconcile model budget: %w", streamErr, reconcileErr)
		}
		return fmt.Errorf("reconcile model budget: %w", reconcileErr)
	}
	return streamErr
}
