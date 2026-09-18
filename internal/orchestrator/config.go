package orchestrator

import (
	"time"

	"local-agent-workbench/internal/domain"
)

func PresetDefaults(preset string) (domain.OrchestratorConfig, bool) {
	cfg := domain.OrchestratorConfig{Preset: preset, Temperature: 0.2, MaxOutputTokens: 8192}
	switch preset {
	case "conductor":
		cfg.PlanningDepth, cfg.Parallelism, cfg.ApprovalStrictness, cfg.TeamPreference = 70, 60, 40, 85
	case "dispatcher":
		cfg.PlanningDepth, cfg.Parallelism, cfg.ApprovalStrictness, cfg.TeamPreference = 35, 80, 30, 20
	case "conservative":
		cfg.PlanningDepth, cfg.Parallelism, cfg.ApprovalStrictness, cfg.TeamPreference = 55, 15, 85, 50
	case "custom":
		cfg.PlanningDepth, cfg.Parallelism, cfg.ApprovalStrictness, cfg.TeamPreference = 50, 50, 50, 50
	default:
		return domain.OrchestratorConfig{}, false
	}
	return cfg, true
}

func masterTurnTimeoutSeconds(cfg domain.OrchestratorConfig) int {
	if cfg.Provider == domain.ProviderOllama {
		return masterIntakeOllamaTimeoutSeconds
	}
	return masterIntakeTimeoutSeconds
}

// MasterTurnBudget — сколько живёт один ход Мастера вместе с раундами
// инструментов и починкой формата. Тот, кто запускает ход, обязан ждать не
// меньше: собственный потолок поверх этого рвёт поток к модели на середине,
// провайдер пишет о разрыве клиентом, а человек видит «Ответ остановлен», как
// будто нажал стоп сам.
func MasterTurnBudget(cfg domain.OrchestratorConfig) time.Duration {
	return time.Duration(masterTurnTimeoutSeconds(cfg)+30) * time.Second
}

// SelectParty keeps the historical truncate API used by older call sites.
// Prefer AssignParty for goal-aware ranking.
func SelectParty(cfg domain.OrchestratorConfig, agents []domain.ProjectAgent, proposed []string) []string {
	return AssignParty(cfg, agents, proposed, "").AgentIDs
}

func PartySize(cfg domain.OrchestratorConfig) int {
	return partySize(cfg)
}

func partySize(cfg domain.OrchestratorConfig) int {
	if cfg.Preset == "dispatcher" || cfg.TeamPreference < 35 {
		return 1
	}
	if cfg.Preset == "conductor" || cfg.TeamPreference >= 75 {
		return 3
	}
	return 2
}

// ShouldAutoStartFlow reports whether quest Start should also start a FlowRun.
// requested is true after the user confirms Start (or an API client sets startFlow).
// Conservative / high approvalStrictness still start the run when requested is true;
// strictness is applied as approval gates inside the compiled Flow, not by skipping the run.
func ShouldAutoStartFlow(cfg domain.OrchestratorConfig, requested bool) bool {
	if cfg.Preset == "conservative" || cfg.ApprovalStrictness >= 80 {
		return requested
	}
	return true
}

// UsesModelPlanner is true when a separate orchestrator model is configured.
// The model path is always guarded by strict validation and deterministic fallback.
func UsesModelPlanner(cfg domain.OrchestratorConfig) bool {
	return cfg.Provider != "" && cfg.Model != ""
}
