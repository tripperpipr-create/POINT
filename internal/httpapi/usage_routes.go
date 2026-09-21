// Расход, бюджет, статистика, обучение и бенчмарки агентов.
package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/domain"
)

func (s *Server) recordUsage(w http.ResponseWriter, r *http.Request) {
	var input domain.UsageRecord
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.RecordUsage(input)
	s.result(w, value, err)
}

func (s *Server) saveBudget(w http.ResponseWriter, r *http.Request) {
	var input app.HubBudgetSettings
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveHubBudget(input)
	s.result(w, value, err)
}

func (s *Server) saveModelPricing(w http.ResponseWriter, r *http.Request) {
	var input domain.ModelPricingProfile
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveModelPricingProfile(input)
	s.result(w, value, err)
}

func (s *Server) statistics(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.Statistics(r.URL.Query().Get("workspaceId"))
	s.result(w, value, err)
}

func (s *Server) rollbackAgentImprovement(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.RollbackAgentImprovement(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) promoteAgentImprovement(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.PromoteAgentImprovement(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) rejectAgentImprovement(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.RejectAgentImprovement(r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) searchExperience(w http.ResponseWriter, r *http.Request) {
	limit := 30
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	value, err := s.app.SearchExperience(r.URL.Query().Get("q"), limit)
	s.result(w, value, err)
}

func (s *Server) listAgentBenchmarks(w http.ResponseWriter, _ *http.Request) {
	value, err := s.app.ListAgentBenchmarkSets()
	s.result(w, value, err)
}

func (s *Server) saveAgentBenchmark(w http.ResponseWriter, r *http.Request) {
	var input domain.AgentBenchmarkSet
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveAgentBenchmarkSet(input)
	s.result(w, value, err)
}

func (s *Server) evaluateAgentBenchmark(w http.ResponseWriter, r *http.Request) {
	var input app.AgentBenchmarkEvaluationRequest
	if !s.decode(w, r, &input) {
		return
	}
	input.BenchmarkSetID = r.PathValue("id")
	value, err := s.app.EvaluateAgentBenchmark(input)
	s.result(w, value, err)
}

func (s *Server) listAgentBenchmarkEvaluations(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	value, err := s.app.ListAgentBenchmarkEvaluations(limit)
	s.result(w, value, err)
}

func (s *Server) compareAgentBenchmarks(w http.ResponseWriter, r *http.Request) {
	var input app.AgentBenchmarkComparisonRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.CompareAgentBenchmarks(input)
	s.result(w, value, err)
}

func (s *Server) previewManualLearning(w http.ResponseWriter, r *http.Request) {
	var input app.ManualLearningRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.PreviewManualLearning(input)
	s.result(w, value, err)
}

func (s *Server) applyManualLearning(w http.ResponseWriter, r *http.Request) {
	var input app.ManualLearningRequest
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ApplyManualLearning(input)
	s.result(w, value, err)
}
