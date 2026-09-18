package domain

import "time"

type BudgetReservationStatus string

const (
	BudgetReserved     BudgetReservationStatus = "reserved"
	BudgetReconciled   BudgetReservationStatus = "reconciled"
	BudgetConservative BudgetReservationStatus = "conservative"
	BudgetReleased     BudgetReservationStatus = "released"
)

// BudgetReservation is the durable claim made before one provider request.
// A missing provider usage report keeps the conservative maximum charged.
type BudgetReservation struct {
	ID                   string                  `json:"id"`
	WorkspaceID          string                  `json:"workspaceId"`
	QuestID              string                  `json:"questId,omitempty"`
	BudgetScopeQuestID   string                  `json:"budgetScopeQuestId,omitempty"`
	ExecutionID          string                  `json:"executionId,omitempty"`
	RunID                string                  `json:"runId"`
	Provider             string                  `json:"provider"`
	Model                string                  `json:"model"`
	EstimatedInputTokens int64                   `json:"estimatedInputTokens"`
	MaxOutputTokens      int64                   `json:"maxOutputTokens"`
	ReservedTokens       int64                   `json:"reservedTokens"`
	ReservedCents        int64                   `json:"reservedCents"`
	ActualInputTokens    int64                   `json:"actualInputTokens,omitempty"`
	ActualOutputTokens   int64                   `json:"actualOutputTokens,omitempty"`
	ActualCents          int64                   `json:"actualCents,omitempty"`
	UsageReported        bool                    `json:"usageReported"`
	Status               BudgetReservationStatus `json:"status"`
	CreatedAt            time.Time               `json:"createdAt"`
	ReconciledAt         *time.Time              `json:"reconciledAt,omitempty"`
}

type BudgetReserveLimits struct {
	DailyCents   int64
	MonthlyCents int64
	HardStop     bool
	// PricingUnknown prevents a child from bypassing an ancestor cost cap.
	PricingUnknown bool
	// FreeRuntime снимает токеновый потолок квеста: за бесплатным рантаймом
	// счётчик токенов не защищает ничего, кроме как от зацикливания, а от
	// зацикливания есть предел ходов и времени. Денежные потолки остаются: их
	// задаёт человек, и обнулять их из-за вида рантайма нельзя.
	FreeRuntime     bool
	QuestTokenLimit int64
	QuestCostLimit  int64
	DayStart        time.Time
	MonthStart      time.Time
}

// ModelPricingProfile is user-owned because provider prices are not safe to
// guess. Prices are integer cents per one million tokens.
type ModelPricingProfile struct {
	ID                    string    `json:"id"`
	WorkspaceID           string    `json:"workspaceId"`
	Provider              string    `json:"provider"`
	Model                 string    `json:"model"`
	InputCentsPerMillion  int64     `json:"inputCentsPerMillion"`
	OutputCentsPerMillion int64     `json:"outputCentsPerMillion"`
	CreatedAt             time.Time `json:"createdAt"`
	UpdatedAt             time.Time `json:"updatedAt"`
}
