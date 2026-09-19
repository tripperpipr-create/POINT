package orchestrator

import (
	"fmt"
	"sort"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/textutil"
)

// Assignment is the deterministic party chosen by the Orchestrator.
type Assignment struct {
	AgentIDs  []string
	Reason    string
	Mode      string // deterministic | model-fallback
	Breakdown []domain.AgentSelectionBreakdown
}

// CandidateSignal contains bounded operational evidence calculated by Point
// Core. A completed run alone is deliberately not a success signal.
type CandidateSignal struct {
	Attempts           int
	ConfirmedSuccesses int
	ActiveExecutions   int
	AverageLatencyMs   int64
	AverageCostCents   int64
}

// AssignParty ranks Companion proposals and available agents into a party.
// When a model planner is configured but unavailable, this remains the fallback.
func AssignParty(cfg domain.OrchestratorConfig, agents []domain.ProjectAgent, proposed []string, goal string) Assignment {
	return AssignPartyWithSignals(cfg, agents, proposed, goal, nil)
}

func AssignPartyWithSignals(cfg domain.OrchestratorConfig, agents []domain.ProjectAgent, proposed []string, goal string, signals map[string]CandidateSignal) Assignment {
	limit := partySize(cfg)
	if limit <= 0 || len(agents) == 0 {
		return Assignment{Mode: "deterministic", Reason: "нет доступных проектных агентов"}
	}

	byID := make(map[string]domain.ProjectAgent, len(agents))
	for _, agent := range agents {
		byID[agent.ID] = agent
	}

	type candidate struct {
		id        string
		score     int
		order     int
		breakdown domain.AgentSelectionBreakdown
	}
	seen := map[string]bool{}
	candidates := make([]candidate, 0, len(agents)+len(proposed))
	goalScore := func(agent domain.ProjectAgent) int {
		return scoreAgentForGoal(agent, goal)
	}

	add := func(id string, score, order int) {
		if id == "" || seen[id] || byID[id].ID == "" {
			return
		}
		agent := byID[id]
		signal, hasSignal := signals[id]
		roleScore := goalScore(agent)
		evidenceScore := successBoost(agent)
		if hasSignal {
			evidenceScore = 0
			if signal.Attempts > 0 {
				evidenceScore = min(24, signal.ConfirmedSuccesses*24/signal.Attempts)
			}
		}
		verificationScore := 0
		if containsAny(agent.AllowedTools, "run_command") {
			verificationScore = 10
		}
		loadPenalty := signal.ActiveExecutions * 15
		latencyPenalty := min(10, int(signal.AverageLatencyMs/30000))
		costPenalty := min(12, int(signal.AverageCostCents/10))
		prior := score - roleScore
		total := prior + roleScore + evidenceScore + verificationScore - loadPenalty - latencyPenalty - costPenalty
		seen[id] = true
		candidates = append(candidates, candidate{id: id, score: total, order: order, breakdown: domain.AgentSelectionBreakdown{
			AgentID: id, RoleFit: roleScore, Evidence: evidenceScore, Verification: verificationScore,
			LoadPenalty: loadPenalty, LatencyPenalty: latencyPenalty, CostPenalty: costPenalty,
			Matched: matchedTerms(agent, goal), ConfirmedSuccesses: signal.ConfirmedSuccesses, Attempts: signal.Attempts,
		}})
	}

	// Companion proposal order is a strong prior, then re-rank by goal fit.
	for index, id := range proposed {
		agent, ok := byID[id]
		if !ok {
			continue
		}
		score := 1000 - index*40 + goalScore(agent)
		add(id, score, index)
	}
	for index, agent := range agents {
		if seen[agent.ID] {
			// Refresh score with full goal fit even if already proposed.
			continue
		}
		score := goalScore(agent)
		add(agent.ID, score, 1000+index)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].order < candidates[j].order
	})

	if limit > len(candidates) {
		limit = len(candidates)
	}
	ids := make([]string, 0, limit)
	breakdown := make([]domain.AgentSelectionBreakdown, 0, limit)
	roles := map[string]bool{}
	for len(ids) < limit && len(candidates) > 0 {
		best := 0
		bestScore := candidates[0].score + diversityBonus(byID[candidates[0].id], roles)
		for index := 1; index < len(candidates); index++ {
			score := candidates[index].score + diversityBonus(byID[candidates[index].id], roles)
			if score > bestScore {
				best, bestScore = index, score
			}
		}
		item := candidates[best]
		item.breakdown.Diversity = diversityBonus(byID[item.id], roles)
		item.breakdown.Total = item.score + item.breakdown.Diversity
		ids = append(ids, item.id)
		breakdown = append(breakdown, item.breakdown)
		roles[agentRoleSignature(byID[item.id])] = true
		candidates = append(candidates[:best], candidates[best+1:]...)
	}

	mode := "deterministic"
	reason := fmt.Sprintf("пресет %s · отряд %d", cfg.Preset, len(ids))
	if UsesModelPlanner(cfg) {
		mode = "model-fallback"
		reason += " · модель недоступна, детерминированный выбор"
	} else {
		reason += " · движок Point"
	}
	if len(proposed) > 0 {
		reason += " · учтена рекомендация компаньона"
	}
	return Assignment{AgentIDs: ids, Reason: reason, Mode: mode, Breakdown: breakdown}
}

func containsAny(values []string, expected ...string) bool {
	for _, value := range values {
		for _, item := range expected {
			if value == item {
				return true
			}
		}
	}
	return false
}

func agentRoleSignature(agent domain.ProjectAgent) string {
	tokens := textutil.Tokens(agent.RoleDescription + " " + agent.Mission)
	if len(tokens) == 0 {
		return agent.ID
	}
	return tokens[0]
}

func diversityBonus(agent domain.ProjectAgent, selected map[string]bool) int {
	if len(selected) == 0 || selected[agentRoleSignature(agent)] {
		return 0
	}
	return 10
}

func successBoost(agent domain.ProjectAgent) int {
	if agent.TasksCompleted <= 0 {
		return 0
	}
	return min(8, agent.SuccessCount*8/agent.TasksCompleted)
}

func scoreAgentForGoal(agent domain.ProjectAgent, goal string) int {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return 0
	}
	haystack := strings.Join([]string{
		agent.Name, agent.RoleDescription, agent.Mission,
		strings.Join(agent.Goals, " "), strings.Join(agent.SkillIDs, " "), strings.Join(agent.AllowedTools, " "),
	}, " ")
	score := textutil.Overlap(textutil.Tokens(goal), textutil.Tokens(haystack)) * 12
	goalLower := strings.ToLower(goal)
	haystackLower := strings.ToLower(haystack)
	for trigger, hints := range map[string][]string{
		"security": {"security", "review", "backend", "безопас", "аудит"},
		"auth":     {"security", "backend", "identity", "auth", "review"},
		"frontend": {"frontend", "ui", "ux", "design"},
		"backend":  {"backend", "api", "database", "server"},
		"test":     {"test", "qa", "review", "verifier"},
		"review":   {"review", "qa", "architect", "verifier"},
		"архитект": {"architect", "architecture", "review"},
		"мигр":     {"database", "backend", "architect", "review"},
		"oauth":    {"security", "backend", "auth", "identity"},
	} {
		if !strings.Contains(goalLower, trigger) {
			continue
		}
		for _, hint := range hints {
			if strings.Contains(haystackLower, hint) {
				score += 18
			}
		}
	}
	return score
}

// Служебные слова не объясняют выбор.
//
