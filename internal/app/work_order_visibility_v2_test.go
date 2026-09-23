package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/orchestrator"
)

func TestPlannerFallbackTextExplainsDeadline(t *testing.T) {
	message := plannerFallbackText(&orchestrator.PlanTimeoutError{Phase: "reasoning", Budget: 5 * time.Minute, Cause: context.DeadlineExceeded})
	if !strings.Contains(message, "5 минут") || strings.Contains(message, "context deadline exceeded") {
		t.Fatalf("deadline fallback must be actionable and human-readable: %q", message)
	}
	if !strings.Contains(message, "во время рассуждения") {
		t.Fatalf("deadline fallback must preserve the last visible phase: %q", message)
	}
}

// TestConnectionAllowsModel закрепляет единственную проверку привязки, которую
// делят утверждение наряда и рантайм. Пустой каталог не значит «модели нет»:
// llmux и «свой endpoint» не обязаны отдавать /v1/models.
func TestConnectionAllowsModel(t *testing.T) {
	cases := []struct {
		name       string
		connection domain.Connection
		model      string
		allowed    bool
	}{
		{
			name:       "каталог пуст и DefaultModel пуст — модель задал человек",
			connection: domain.Connection{ID: "connection_llmux", DisplayName: "ЦФМодели", PresetID: "llmux"},
			model:      "Qwen3.8-27B",
			allowed:    true,
		},
		{
			name:       "каталог пуст, но есть DefaultModel — сверяем с ним",
			connection: domain.Connection{ID: "connection_default", DefaultModel: "coding-default"},
			model:      "coding-default",
			allowed:    true,
		},
		{
			name:       "каталог пуст, DefaultModel другой — отказ",
			connection: domain.Connection{ID: "connection_default", DefaultModel: "coding-default"},
			model:      "Qwen3.8-27B",
			allowed:    false,
		},
		{
			name: "каталог непустой и модель в нём есть",
			connection: domain.Connection{ID: "connection_catalog", Models: []domain.ConnectionModel{
				{ID: "gpt-test"}, {ID: "gpt-other"},
			}},
			model:   "gpt-test",
			allowed: true,
		},
		{
			name: "каталог непустой и модели в нём нет — отказ остаётся",
			connection: domain.Connection{ID: "connection_catalog", Models: []domain.ConnectionModel{
				{ID: "gpt-test"},
			}},
			model:   "Qwen3.8-27B",
			allowed: false,
		},
		{
			name:       "пустая модель при пустом каталоге — не привязка",
			connection: domain.Connection{ID: "connection_llmux"},
			model:      "   ",
			allowed:    false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := connectionAllowsModel(testCase.connection, testCase.model); got != testCase.allowed {
				t.Fatalf("connectionAllowsModel(%q)=%v, want %v", testCase.model, got, testCase.allowed)
			}
		})
	}
}

// TestFlowRunStartFailureV2 проверяет, что провал запуска узла выносится из
// nodeStates наружу: без этого квест оставался running, а причина была не видна.
func TestFlowRunStartFailureV2(t *testing.T) {
	startError := `apply stage model binding: model "Qwen3.8-27B" is not in connection "connection_06b4cde" catalog`
	run := domain.FlowRun{NodeStates: map[string]domain.FlowNodeState{
		"node_failed": {Status: "waiting_agent", Output: map[string]any{
			"waitReason": "start_failed", "startError": startError,
		}},
	}}
	message, failed := flowRunStartFailureV2(run)
	if !failed || message != startError {
		t.Fatalf("flowRunStartFailureV2=%q,%v", message, failed)
	}
	if _, failed = flowRunStartFailureV2(domain.FlowRun{NodeStates: map[string]domain.FlowNodeState{
		"node_waiting": {Output: map[string]any{"waitReason": "waiting_api_key"}},
	}}); failed {
		t.Fatal("waiting_api_key must not be reported as a start failure")
	}
	// Узел упал, но текста нет: человек всё равно обязан увидеть причину затыка.
	message, failed = flowRunStartFailureV2(domain.FlowRun{NodeStates: map[string]domain.FlowNodeState{
		"node_failed": {Output: map[string]any{"waitReason": "start_failed"}},
	}})
	if !failed || strings.TrimSpace(message) == "" {
		t.Fatalf("empty startError must still block: %q,%v", message, failed)
	}
}

// TestRequireRosterConsentV2 закрепляет шаг человека: утверждение не создаёт
// нового исполнителя молча, а ранее созданные агенты через гейт проходят.
func TestRequireRosterConsentV2(t *testing.T) {
	order := domain.WorkOrder{Roster: domain.AgentRosterPlan{Permanent: []domain.AgentDraft{
		{ID: "agentdraft_new", Name: "Разработчик проекта", RequiresConsent: true},
		{ID: "agentdraft_old", Name: "Ревьюер", RequiresConsent: true, Existing: true},
	}}}
	err := requireRosterConsentV2(order, nil)
	if err == nil || !strings.Contains(err.Error(), "Разработчик проекта") {
		t.Fatalf("missing consent must be refused by name: %v", err)
	}
	if strings.Contains(err.Error(), "Ревьюер") {
		t.Fatalf("already created agent must not need consent: %v", err)
	}
	if err = requireRosterConsentV2(order, []string{"agentdraft_new"}); err != nil {
		t.Fatalf("explicit consent must pass: %v", err)
	}
}

// TestWorkOrderLaunchOutcomeV2 закрепляет главное: провал запуска уводит квест в
// blocked с текстом ошибки, а не оставляет карточку с «выполнение началось».
func TestWorkOrderLaunchOutcomeV2(t *testing.T) {
	startError := `apply stage model binding: model "Qwen3.8-27B" is not in connection "connection_06b4cde" catalog`
	failedRun := &domain.FlowRun{NodeStates: map[string]domain.FlowNodeState{
		"node_failed": {Output: map[string]any{"waitReason": "start_failed", "startError": startError}},
		"node_key":    {Output: map[string]any{"waitReason": "waiting_api_key"}},
	}}
	status, message, note := workOrderLaunchOutcomeV2(failedRun, "")
	if status != domain.QuestBlocked || !strings.Contains(message, startError) || note != "" {
		t.Fatalf("start failure outcome=%s %q note=%q", status, message, note)
	}

	waitingRun := &domain.FlowRun{NodeStates: map[string]domain.FlowNodeState{
		"node_key": {Output: map[string]any{"waitReason": "waiting_api_key"}},
	}}
	if status, _, _ = workOrderLaunchOutcomeV2(waitingRun, ""); status != domain.QuestAwaitingUser {
		t.Fatalf("waiting_api_key outcome=%s", status)
	}

	healthyRun := &domain.FlowRun{NodeStates: map[string]domain.FlowNodeState{"node_ok": {Status: "running"}}}
	if status, _, _ = workOrderLaunchOutcomeV2(healthyRun, ""); status != domain.QuestRunning {
		t.Fatalf("healthy outcome=%s", status)
	}

	// Отказ планировщика раньше терялся целиком: карточка говорила «План
	// создан», умалчивая, что план собрал движок, а не модель.
	status, message, note = workOrderLaunchOutcomeV2(healthyRun, "planner timeout: context deadline exceeded")
	if status != domain.QuestRunning || note == "" || !strings.Contains(message, note) {
		t.Fatalf("planner fallback outcome=%s %q note=%q", status, message, note)
	}
	if !strings.Contains(note, "резервным Flow") {
		t.Fatalf("planner note must name the engine: %q", note)
	}
}

// TestResumeIsAllowedWhileWaitingForTheHuman закрепляет выход из тупика.
//
// Квест, чей узел ждёт ключ, уходит в awaiting_user. Пока resume разрешался
// только из paused и blocked, у такого квеста не оставалось ни одной кнопки:
// «Пауза» и «Отменить» — всё. Человек отдал credential, а попросить продолжить
// было нечем.
func TestResumeIsAllowedWhileWaitingForTheHuman(t *testing.T) {
	for _, status := range []domain.QuestStatus{domain.QuestPaused, domain.QuestBlocked, domain.QuestAwaitingUser} {
		if err := validateWorkOrderRuntimeControl(status, "resume"); err != nil {
			t.Fatalf("resume from %s must be allowed: %v", status, err)
		}
	}
	// Идущий квест продолжать нечем: он и так идёт. Переход running -> preflight
	// домен запрещает, и кнопка отдавала бы отказ ядра вместо действия.
	if err := validateWorkOrderRuntimeControl(domain.QuestRunning, "resume"); err == nil {
		t.Fatal("resume from running must stay refused")
	}
	// preflight — не место для отдыха. Из него не разрешено ни одно действие
	// человека, поэтому квест, оставленный там, становится тупиком хуже
	// прежнего: ни продолжить, ни поставить на паузу.
	for _, action := range []string{"resume", "pause"} {
		if err := validateWorkOrderRuntimeControl(domain.QuestPreflight, action); err == nil && action == "resume" {
			t.Fatal("resume from preflight must stay refused: preflight means the core is already working")
		}
	}
	if !domain.CanTransitionWorkOrderQuest(domain.QuestAwaitingUser, domain.QuestPreflight) {
		t.Fatal("awaiting_user -> preflight must stay allowed: resume re-runs preflight with the credential")
	}
}
