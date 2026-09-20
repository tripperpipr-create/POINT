package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"local-agent-workbench/internal/domain"
)

var ErrBudgetLimitExceeded = errors.New("budget limit exceeded")

func (s *SQLite) ReserveBudget(ctx context.Context, reservation domain.BudgetReservation, limits domain.BudgetReserveLimits) (domain.BudgetReservation, error) {
	if reservation.EstimatedInputTokens < 0 || reservation.MaxOutputTokens < 0 || reservation.ReservedTokens < 0 || reservation.ReservedCents < 0 {
		return domain.BudgetReservation{}, errors.New("budget reservation values cannot be negative")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return domain.BudgetReservation{}, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return domain.BudgetReservation{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	if err = reserveBudgetWith(ctx, conn, &reservation, limits); err != nil {
		return domain.BudgetReservation{}, err
	}
	if _, err = conn.ExecContext(ctx, `COMMIT`); err != nil {
		return domain.BudgetReservation{}, err
	}
	committed = true
	reservation.Status = domain.BudgetReserved
	return reservation, nil
}

type budgetSQL interface {
	sqlExecer
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// reserveBudgetWith performs the complete budget check and insertion on the
// caller's transaction. Launch paths use it so the first provider request is
// reserved in the same commit as the Run that will consume it.
func reserveBudgetWith(ctx context.Context, db budgetSQL, reservation *domain.BudgetReservation, limits domain.BudgetReserveLimits) error {
	if reservation == nil {
		return errors.New("budget reservation is required")
	}
	if reservation.EstimatedInputTokens < 0 || reservation.MaxOutputTokens < 0 || reservation.ReservedTokens < 0 || reservation.ReservedCents < 0 {
		return errors.New("budget reservation values cannot be negative")
	}
	var dailySpent, monthlySpent, dailyReserved, monthlyReserved int64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(cost_cents),0) FROM usage_records WHERE workspace_id=? AND created_at>=? AND id NOT LIKE 'usage_budget_%'`, reservation.WorkspaceID, formatTime(limits.DayStart)).Scan(&dailySpent); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(cost_cents),0) FROM usage_records WHERE workspace_id=? AND created_at>=? AND id NOT LIKE 'usage_budget_%'`, reservation.WorkspaceID, formatTime(limits.MonthStart)).Scan(&monthlySpent); err != nil {
		return err
	}
	var dailyReservationSpent, monthlyReservationSpent int64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN status=? THEN actual_cents WHEN status=? THEN reserved_cents ELSE 0 END),0) FROM budget_reservations WHERE workspace_id=? AND created_at>=?`, domain.BudgetReconciled, domain.BudgetConservative, reservation.WorkspaceID, formatTime(limits.DayStart)).Scan(&dailyReservationSpent); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN status=? THEN actual_cents WHEN status=? THEN reserved_cents ELSE 0 END),0) FROM budget_reservations WHERE workspace_id=? AND created_at>=?`, domain.BudgetReconciled, domain.BudgetConservative, reservation.WorkspaceID, formatTime(limits.MonthStart)).Scan(&monthlyReservationSpent); err != nil {
		return err
	}
	dailySpent += dailyReservationSpent
	monthlySpent += monthlyReservationSpent
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(reserved_cents),0) FROM budget_reservations WHERE workspace_id=? AND status=? AND created_at>=?`, reservation.WorkspaceID, domain.BudgetReserved, formatTime(limits.DayStart)).Scan(&dailyReserved); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(reserved_cents),0) FROM budget_reservations WHERE workspace_id=? AND status=? AND created_at>=?`, reservation.WorkspaceID, domain.BudgetReserved, formatTime(limits.MonthStart)).Scan(&monthlyReserved); err != nil {
		return err
	}
	if limits.HardStop && limits.DailyCents > 0 && dailySpent+dailyReserved+reservation.ReservedCents > limits.DailyCents {
		return fmt.Errorf("%w: daily spent=%d reserved=%d request=%d limit=%d cents", ErrBudgetLimitExceeded, dailySpent, dailyReserved, reservation.ReservedCents, limits.DailyCents)
	}
	if limits.HardStop && limits.MonthlyCents > 0 && monthlySpent+monthlyReserved+reservation.ReservedCents > limits.MonthlyCents {
		return fmt.Errorf("%w: monthly spent=%d reserved=%d request=%d limit=%d cents", ErrBudgetLimitExceeded, monthlySpent, monthlyReserved, reservation.ReservedCents, limits.MonthlyCents)
	}
	if err := checkQuestBudget(ctx, db, reservation, limits); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `INSERT INTO budget_reservations(id,workspace_id,quest_id,budget_scope_quest_id,execution_id,run_id,provider,model,estimated_input_tokens,max_output_tokens,reserved_tokens,reserved_cents,actual_input_tokens,actual_output_tokens,actual_cents,usage_reported,status,created_at,reconciled_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,NULL)`,
		reservation.ID, reservation.WorkspaceID, reservation.QuestID, reservation.BudgetScopeQuestID, reservation.ExecutionID, reservation.RunID, reservation.Provider, reservation.Model,
		reservation.EstimatedInputTokens, reservation.MaxOutputTokens, reservation.ReservedTokens, reservation.ReservedCents, 0, 0, 0, 0, domain.BudgetReserved, formatTime(reservation.CreatedAt))
	return err
}

func (s *SQLite) ReconcileBudget(ctx context.Context, id string, inputTokens, outputTokens, actualCents int64, usageReported bool, now time.Time) error {
	status := domain.BudgetConservative
	if usageReported {
		status = domain.BudgetReconciled
	}
	result, err := s.db.ExecContext(ctx, `UPDATE budget_reservations SET actual_input_tokens=?,actual_output_tokens=?,actual_cents=?,usage_reported=?,status=?,reconciled_at=? WHERE id=? AND status=?`,
		inputTokens, outputTokens, actualCents, boolToInt(usageReported), status, formatTime(now), id, domain.BudgetReserved)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("budget reservation %q is not active", id)
	}
	return nil
}

func (s *SQLite) BudgetReservation(ctx context.Context, id string) (domain.BudgetReservation, error) {
	var item domain.BudgetReservation
	var usageReported int
	var created string
	var reconciled sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id,workspace_id,quest_id,budget_scope_quest_id,execution_id,run_id,provider,model,estimated_input_tokens,max_output_tokens,reserved_tokens,reserved_cents,actual_input_tokens,actual_output_tokens,actual_cents,usage_reported,status,created_at,reconciled_at FROM budget_reservations WHERE id=?`, id).
		Scan(&item.ID, &item.WorkspaceID, &item.QuestID, &item.BudgetScopeQuestID, &item.ExecutionID, &item.RunID, &item.Provider, &item.Model, &item.EstimatedInputTokens, &item.MaxOutputTokens, &item.ReservedTokens, &item.ReservedCents, &item.ActualInputTokens, &item.ActualOutputTokens, &item.ActualCents, &usageReported, &item.Status, &created, &reconciled)
	if err != nil {
		return domain.BudgetReservation{}, err
	}
	item.UsageReported = usageReported != 0
	item.CreatedAt = parseTime(created)
	if reconciled.Valid {
		value := parseTime(reconciled.String)
		item.ReconciledAt = &value
	}
	return item, nil
}

func (s *SQLite) ReleaseBudgetReservations(ctx context.Context, runID string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE budget_reservations SET status=?,reconciled_at=? WHERE run_id=? AND status=?`, domain.BudgetReleased, formatTime(now), runID, domain.BudgetReserved)
	return err
}

func (s *SQLite) ListBudgetReservations(ctx context.Context, workspaceID string, limit int) ([]domain.BudgetReservation, error) {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,workspace_id,quest_id,budget_scope_quest_id,execution_id,run_id,provider,model,estimated_input_tokens,max_output_tokens,reserved_tokens,reserved_cents,actual_input_tokens,actual_output_tokens,actual_cents,usage_reported,status,created_at,reconciled_at FROM budget_reservations WHERE workspace_id=? ORDER BY created_at DESC LIMIT ?`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.BudgetReservation{}
	for rows.Next() {
		var item domain.BudgetReservation
		var usageReported int
		var created string
		var reconciled sql.NullString
		if err = rows.Scan(&item.ID, &item.WorkspaceID, &item.QuestID, &item.BudgetScopeQuestID, &item.ExecutionID, &item.RunID, &item.Provider, &item.Model, &item.EstimatedInputTokens, &item.MaxOutputTokens, &item.ReservedTokens, &item.ReservedCents, &item.ActualInputTokens, &item.ActualOutputTokens, &item.ActualCents, &usageReported, &item.Status, &created, &reconciled); err != nil {
			return nil, err
		}
		item.UsageReported = usageReported != 0
		item.CreatedAt = parseTime(created)
		if reconciled.Valid {
			value := parseTime(reconciled.String)
			item.ReconciledAt = &value
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *SQLite) SaveModelPricingProfile(ctx context.Context, profile domain.ModelPricingProfile) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO model_pricing_profiles(id,workspace_id,provider,model,input_cents_per_million,output_cents_per_million,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(workspace_id,provider,model) DO UPDATE SET input_cents_per_million=excluded.input_cents_per_million,output_cents_per_million=excluded.output_cents_per_million,updated_at=excluded.updated_at`, profile.ID, profile.WorkspaceID, profile.Provider, profile.Model, profile.InputCentsPerMillion, profile.OutputCentsPerMillion, formatTime(profile.CreatedAt), formatTime(profile.UpdatedAt))
	return err
}

func (s *SQLite) ModelPricingProfile(ctx context.Context, workspaceID, provider, model string) (domain.ModelPricingProfile, error) {
	var profile domain.ModelPricingProfile
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id,workspace_id,provider,model,input_cents_per_million,output_cents_per_million,created_at,updated_at FROM model_pricing_profiles WHERE workspace_id=? AND provider=? AND model=?`, workspaceID, provider, model).Scan(&profile.ID, &profile.WorkspaceID, &profile.Provider, &profile.Model, &profile.InputCentsPerMillion, &profile.OutputCentsPerMillion, &created, &updated)
	if err != nil {
		return domain.ModelPricingProfile{}, err
	}
	profile.CreatedAt, profile.UpdatedAt = parseTime(created), parseTime(updated)
	return profile, nil
}

func (s *SQLite) ListModelPricingProfiles(ctx context.Context, workspaceID string) ([]domain.ModelPricingProfile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,workspace_id,provider,model,input_cents_per_million,output_cents_per_million,created_at,updated_at FROM model_pricing_profiles WHERE workspace_id=? ORDER BY provider,model`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.ModelPricingProfile{}
	for rows.Next() {
		var profile domain.ModelPricingProfile
		var created, updated string
		if err = rows.Scan(&profile.ID, &profile.WorkspaceID, &profile.Provider, &profile.Model, &profile.InputCentsPerMillion, &profile.OutputCentsPerMillion, &created, &updated); err != nil {
			return nil, err
		}
		profile.CreatedAt, profile.UpdatedAt = parseTime(created), parseTime(updated)
		result = append(result, profile)
	}
	return result, rows.Err()
}

// CheckQuestBudget is a read-only preflight. ReserveBudget repeats this check
// under its write lock; only the durable reservation authorizes a model call.
func (s *SQLite) CheckQuestBudget(ctx context.Context, workspaceID, questID string, expectedTokens int64) error {
	if expectedTokens < 0 {
		return errors.New("expected budget tokens cannot be negative")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `BEGIN`); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), `ROLLBACK`)
	reservation := domain.BudgetReservation{WorkspaceID: workspaceID, QuestID: questID, ReservedTokens: expectedTokens}
	return checkQuestBudget(ctx, conn, &reservation, domain.BudgetReserveLimits{})
}

type questBudgetCap struct {
	ID, ParentID  string
	Tokens, Cents int64
}

func checkQuestBudget(ctx context.Context, conn budgetSQL, reservation *domain.BudgetReservation, limits domain.BudgetReserveLimits) error {
	questID := reservation.QuestID
	if questID == "" {
		questID = reservation.BudgetScopeQuestID
	}
	if questID == "" {
		return nil
	}
	var ancestors []questBudgetCap
	visited := make(map[string]bool)
	for questID != "" {
		if visited[questID] {
			return fmt.Errorf("quest budget ancestry cycle at %q", questID)
		}
		visited[questID] = true
		var cap questBudgetCap
		err := conn.QueryRowContext(ctx, `SELECT id,parent_id,budget_tokens,budget_cents FROM quests WHERE workspace_id=? AND id=?`, reservation.WorkspaceID, questID).
			Scan(&cap.ID, &cap.ParentID, &cap.Tokens, &cap.Cents)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("quest %s not found in current workspace", questID)
		}
		if err != nil {
			return err
		}
		ancestors = append(ancestors, cap)
		questID = cap.ParentID
	}
	root := &ancestors[len(ancestors)-1]
	reservation.BudgetScopeQuestID = root.ID
	// Keep the legacy caller-supplied limit as an additional root cap.
	root.Tokens = tighterBudgetLimit(root.Tokens, limits.QuestTokenLimit)
	root.Cents = tighterBudgetLimit(root.Cents, limits.QuestCostLimit)
	for _, cap := range ancestors {
		tokenCap := cap.Tokens
		if limits.FreeRuntime {
			// Токены бесплатного рантайма не расходуют потолок квеста, но
			// денежный потолок предка остаётся в силе: платный ход внутри того
			// же квеста по-прежнему упрётся в него.
			tokenCap = 0
		}
		if tokenCap <= 0 && cap.Cents <= 0 {
			continue
		}
		if limits.PricingUnknown && cap.Cents > 0 {
			return fmt.Errorf("budget blocked: pricing profile is required for %s/%s (quest %s)", reservation.Provider, reservation.Model, cap.ID)
		}
		spentTokens, spentCents, reservedTokens, reservedCents, err := questBudgetUsage(ctx, conn, reservation.WorkspaceID, cap.ID)
		if err != nil {
			return err
		}
		if budgetWouldExceed(tokenCap, spentTokens, reservedTokens, reservation.ReservedTokens) {
			return fmt.Errorf("%w: quest token budget %s spent=%d reserved=%d request=%d limit=%d", ErrBudgetLimitExceeded, cap.ID, spentTokens, reservedTokens, reservation.ReservedTokens, tokenCap)
		}
		if budgetWouldExceed(cap.Cents, spentCents, reservedCents, reservation.ReservedCents) {
			return fmt.Errorf("%w: quest cost budget %s spent=%d reserved=%d request=%d limit=%d cents", ErrBudgetLimitExceeded, cap.ID, spentCents, reservedCents, reservation.ReservedCents, cap.Cents)
		}
	}
	return nil
}

func tighterBudgetLimit(stored, supplied int64) int64 {
	if supplied > 0 && (stored <= 0 || supplied < stored) {
		return supplied
	}
	return stored
}

// Subtraction avoids wrapping an int64 sum into apparent spare capacity.
func budgetWouldExceed(limit, spent, reserved, request int64) bool {
	return limit > 0 && (spent > limit || reserved > limit-spent || request > limit-spent-reserved)
}

func questBudgetUsage(ctx context.Context, conn budgetSQL, workspaceID, questID string) (spentTokens, spentCents, reservedTokens, reservedCents int64, err error) {
	const scope = `WITH RECURSIVE scope(id) AS (
		SELECT id FROM quests WHERE workspace_id=? AND id=?
		UNION
		SELECT q.id FROM quests q JOIN scope p ON q.parent_id=p.id WHERE q.workspace_id=?
	) `
	err = conn.QueryRowContext(ctx, scope+`SELECT COALESCE(SUM(total_tokens),0),COALESCE(SUM(cost_cents),0)
		FROM usage_records u WHERE workspace_id=? AND quest_id IN (SELECT id FROM scope)
		AND NOT EXISTS (SELECT 1 FROM budget_reservations b WHERE b.workspace_id=u.workspace_id AND u.id='usage_budget_'||b.id)`,
		workspaceID, questID, workspaceID, workspaceID).Scan(&spentTokens, &spentCents)
	if err != nil {
		return
	}
	var settledTokens, settledCents int64
	// The OR counts a reservation once. QuestID attributes modern root-scoped
	// records to their child; the scope column also covers pre-root legacy rows.
	err = conn.QueryRowContext(ctx, scope+`SELECT
		COALESCE(SUM(CASE WHEN status='reconciled' THEN actual_input_tokens+actual_output_tokens WHEN status='conservative' THEN reserved_tokens ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN status='reconciled' THEN actual_cents WHEN status='conservative' THEN reserved_cents ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN status='reserved' THEN reserved_tokens ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN status='reserved' THEN reserved_cents ELSE 0 END),0)
		FROM budget_reservations WHERE workspace_id=? AND (quest_id IN (SELECT id FROM scope) OR budget_scope_quest_id IN (SELECT id FROM scope))`,
		workspaceID, questID, workspaceID, workspaceID).Scan(&settledTokens, &settledCents, &reservedTokens, &reservedCents)
	spentTokens += settledTokens
	spentCents += settledCents
	return
}
