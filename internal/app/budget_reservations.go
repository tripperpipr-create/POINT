package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
)

func (a *App) ReserveModelBudget(ctx context.Context, request agent.ModelBudgetRequest) (string, error) {
	reservation, limits, err := a.prepareModelBudgetReservation(ctx, request)
	if err != nil {
		return "", err
	}
	created, err := a.store.ReserveBudget(ctx, reservation, limits)
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

// prepareModelBudgetReservation is the read-only half of budget reservation.
// FastAgent uses it before creating a physical sandbox, then inserts the
// returned reservation inside its all-or-nothing launch transaction.
func (a *App) prepareModelBudgetReservation(ctx context.Context, request agent.ModelBudgetRequest) (domain.BudgetReservation, domain.BudgetReserveLimits, error) {
	settings, err := a.loadHubBudgetSettings(ctx, request.WorkspaceID)
	if err != nil {
		return domain.BudgetReservation{}, domain.BudgetReserveLimits{}, err
	}
	limits := domain.BudgetReserveLimits{DailyCents: settings.DailyCents, MonthlyCents: settings.MonthlyCents, HardStop: settings.HardStop}
	reservedCents, pricingKnown, err := a.estimateModelCost(ctx, request.WorkspaceID, string(request.Provider), request.Model, request.EstimatedInputTokens, request.MaxOutputTokens)
	if err != nil {
		return domain.BudgetReservation{}, domain.BudgetReserveLimits{}, err
	}
	// За бесплатным рантаймом цена известна и равна нулю — спрашивать у
	// человека прайс-лист не за что, и токеновый потолок квеста такой ход не
	// расходует.
	limits.FreeRuntime = !domain.RuntimeChargesForTokens(request.Provider, request.ProviderPreset)
	if limits.FreeRuntime {
		reservedCents, pricingKnown = 0, true
	}
	if !pricingKnown && (settings.HardStop && (settings.DailyCents > 0 || settings.MonthlyCents > 0)) {
		return domain.BudgetReservation{}, domain.BudgetReserveLimits{}, fmt.Errorf("budget blocked: pricing profile is required for %s/%s", request.Provider, request.Model)
	}
	limits.PricingUnknown = !pricingKnown
	now := time.Now().UTC()
	limits.DayStart = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	limits.MonthStart = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	reservation := domain.BudgetReservation{
		ID: domain.NewID("budget"), WorkspaceID: request.WorkspaceID, QuestID: request.QuestID, BudgetScopeQuestID: request.QuestID, ExecutionID: request.ExecutionID,
		RunID: request.RunID, Provider: string(request.Provider), Model: request.Model,
		EstimatedInputTokens: request.EstimatedInputTokens, MaxOutputTokens: request.MaxOutputTokens,
		ReservedTokens: request.EstimatedInputTokens + request.MaxOutputTokens, ReservedCents: reservedCents, CreatedAt: now,
	}
	return reservation, limits, nil
}

func (a *App) ReconcileModelBudget(ctx context.Context, settlement agent.ModelBudgetSettlement) error {
	reservation, err := a.store.BudgetReservation(ctx, settlement.ReservationID)
	if err != nil {
		return err
	}
	if settlement.WorkspaceID != "" && settlement.WorkspaceID != reservation.WorkspaceID {
		return errors.New("budget settlement belongs to another workspace")
	}
	actualCents, _, err := a.estimateModelCost(ctx, reservation.WorkspaceID, reservation.Provider, reservation.Model, settlement.InputTokens, settlement.OutputTokens)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err = a.store.ReconcileBudget(ctx, settlement.ReservationID, settlement.InputTokens, settlement.OutputTokens, actualCents, settlement.UsageReported, now); err != nil {
		return err
	}
	if !settlement.UsageReported {
		return nil
	}
	projectAgentID := settlement.ProjectAgentID
	if projectAgentID == "" && reservation.RunID != "" {
		run, runErr := a.store.GetRun(ctx, reservation.RunID)
		if runErr != nil {
			return runErr
		}
		projectAgentID = run.ProfileID
	}
	cost := actualCents
	outcome := strings.TrimSpace(settlement.Outcome)
	if outcome == "" {
		outcome = "usage_reported"
	}
	return a.store.InsertUsageRecord(ctx, domain.UsageRecord{
		ID: "usage_budget_" + reservation.ID, WorkspaceID: reservation.WorkspaceID, ExecutionID: reservation.ExecutionID,
		QuestID: reservation.QuestID, ProjectAgentID: projectAgentID, Provider: reservation.Provider, Model: reservation.Model,
		InputTokens: settlement.InputTokens, OutputTokens: settlement.OutputTokens, TotalTokens: settlement.InputTokens + settlement.OutputTokens,
		CostCents: &cost, LatencyMs: now.Sub(reservation.CreatedAt).Milliseconds(), Outcome: outcome, CreatedAt: now,
	})
}

func (a *App) estimateModelCost(ctx context.Context, workspaceID, provider, model string, inputTokens, outputTokens int64) (int64, bool, error) {
	if domain.ProviderKind(provider) == domain.ProviderOllama {
		return 0, true, nil
	}
	pricing, err := a.store.ModelPricingProfile(ctx, workspaceID, provider, model)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return centsForTokens(inputTokens, pricing.InputCentsPerMillion) + centsForTokens(outputTokens, pricing.OutputCentsPerMillion), true, nil
}

func centsForTokens(tokens, centsPerMillion int64) int64 {
	if tokens <= 0 || centsPerMillion <= 0 {
		return 0
	}
	return (tokens*centsPerMillion + 999999) / 1000000
}

func (a *App) SaveModelPricingProfile(profile domain.ModelPricingProfile) (domain.ModelPricingProfile, error) {
	workspace, err := a.requireWorkspace()
	if err != nil {
		return domain.ModelPricingProfile{}, err
	}
	if profile.WorkspaceID == "" {
		profile.WorkspaceID = workspace.ID
	}
	if profile.WorkspaceID != workspace.ID {
		return domain.ModelPricingProfile{}, errors.New("pricing profile belongs to another workspace")
	}
	profile.Provider, profile.Model = strings.TrimSpace(profile.Provider), strings.TrimSpace(profile.Model)
	if profile.Provider == "" || profile.Model == "" {
		return domain.ModelPricingProfile{}, errors.New("pricing provider and model are required")
	}
	if profile.InputCentsPerMillion < 0 || profile.OutputCentsPerMillion < 0 {
		return domain.ModelPricingProfile{}, errors.New("pricing values cannot be negative")
	}
	now := time.Now().UTC()
	if profile.ID == "" {
		profile.ID = domain.NewID("pricing")
		profile.CreatedAt = now
	}
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	profile.UpdatedAt = now
	if err = a.store.SaveModelPricingProfile(context.Background(), profile); err != nil {
		return domain.ModelPricingProfile{}, err
	}
	return profile, nil
}
