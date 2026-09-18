// Квест и решения: очередь решений, перепланирование, политика оркестратора.
package httpapi

import (
	"net/http"
	"strings"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/orchestrator"
)

// Очередь решений. Узкий ответ вместо перезапроса bootstrap на 42 поля:
// экран «Решения» опрашивается часто, и таскать ради него всю историю прогонов
// значит платить историей за каждое обновление счётчика.
func (s *Server) decisions(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.Decisions(r.Context())
	s.result(w, value, err)
}

func (s *Server) resolveEgressAsk(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Action string `json:"action"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	action := strings.ToLower(strings.TrimSpace(input.Action))
	switch action {
	case "continue", "stop":
		err := s.app.ResolveSupervisionContinue(r.Context(), id, action == "continue")
		if err != nil {
			s.result(w, nil, err)
			return
		}
		ask, getErr := s.app.GetEgressAsk(r.Context(), id)
		s.result(w, ask, getErr)
	default:
		value, err := s.app.ResolveEgressAsk(r.Context(), id, app.ResolveEgressAskRequest{Action: input.Action})
		s.result(w, value, err)
	}
}

// История одного файла. Путь приходит query-параметром и проверяется границей
// рабочей папки внутри use-case — транспорт его не разбирает и не чистит.
// Разговор с Мастером. Отдельная поверхность от чата компаньона: у них разные
// собеседники, разные конфигурации и разные результаты хода.
// Политика Мастера по черновику настройки. Считает тот же код, который её
// исполняет: превью не пересказывает правила, а показывает их.
// Что агент сможет и чего не сможет — по черновику настройки. Считает тот же
// код, который это разрешает: форма показывает результат, а не ввод.
func (s *Server) agentCapability(w http.ResponseWriter, r *http.Request) {
	var input domain.AgentProfile
	if !s.decode(w, r, &input) {
		return
	}
	s.result(w, s.app.AgentCapabilityFor(r.Context(), input), nil)
}

// Что изменится, если выдать умение или экипировать навык. Считает тот же код,
// что и саму годность: обещание карточки и поведение движка совпадают.
// Цепочка передач между агентами. Эстафета работала и раньше, но уезжала в
// модель текстом: когда второй агент делал не то, оставалось гадать, не понял
// он задачу или ему не то передали.
// Сверка обещания с результатом. Квест закрывался, а определение готовности
// оставалось словами: человек видел «завершён» и не мог сказать, выполнено ли
// то, ради чего квест ставили.
func (s *Server) questOutcome(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.QuestOutcome(r.Context(), r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) listQuestReplans(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.ListQuestReplans(r.Context(), r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) replanQuest(w http.ResponseWriter, r *http.Request) {
	var input app.ReplanQuestRequest
	if !s.decode(w, r, &input) {
		return
	}
	input.QuestID = r.PathValue("id")
	value, err := s.app.ReplanQuest(r.Context(), input)
	s.result(w, value, err)
}

func (s *Server) reviseQuestBrief(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Brief           domain.TaskBrief `json:"brief"`
		ExpectedVersion int              `json:"expectedVersion"`
		ApproveVersion  int              `json:"approveVersion"`
	}
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.ReviseActiveQuestBrief(r.Context(), r.PathValue("id"), input.Brief, input.ExpectedVersion, input.ApproveVersion)
	s.result(w, value, err)
}

func (s *Server) flowHandoffs(w http.ResponseWriter, r *http.Request) {
	value, err := s.app.Handoffs(r.Context(), r.PathValue("id"))
	s.result(w, value, err)
}

func (s *Server) agentCapabilityDelta(w http.ResponseWriter, r *http.Request) {
	var input app.CapabilityDeltaRequest
	if !s.decode(w, r, &input) {
		return
	}
	s.result(w, s.app.CapabilityDeltaFor(r.Context(), input), nil)
}

func (s *Server) orchestratorPolicy(w http.ResponseWriter, r *http.Request) {
	var input domain.OrchestratorConfig
	if !s.decode(w, r, &input) {
		return
	}
	s.result(w, orchestrator.DescribePolicy(input), nil)
}

func (s *Server) decideQuestProposal(w http.ResponseWriter, r *http.Request) {
	var input app.QuestProposalDecision
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.DecideQuestProposalContext(r.Context(), input)
	s.result(w, value, err)
}

func (s *Server) saveOrchestratorConfig(w http.ResponseWriter, r *http.Request) {
	var input domain.OrchestratorConfig
	if !s.decode(w, r, &input) {
		return
	}
	value, err := s.app.SaveOrchestratorConfig(input)
	s.result(w, value, err)
}
