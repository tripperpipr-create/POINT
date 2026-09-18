package orchestrator_test

import (
	"strconv"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/orchestrator"
)

// Превью настройки обязано совпадать с исполнением. Раньше политика считалась
// дважды — в JS для показа и в Go для дела — и расходилась: пользователь видел
// «высокая параллельность», а движок давал ровно четырёх агентов.
func TestPolicyMatchesTheEnginesOwnLimits(t *testing.T) {
	for _, preset := range []string{"conductor", "dispatcher", "conservative", "custom"} {
		cfg, ok := orchestrator.PresetDefaults(preset)
		if !ok {
			t.Fatalf("пресет %s не найден", preset)
		}
		policy := orchestrator.DescribePolicy(cfg)

		if policy.PartySize != orchestrator.PartySize(cfg) {
			t.Fatalf("%s: размер отряда в превью %d, у движка %d", preset, policy.PartySize, orchestrator.PartySize(cfg))
		}
		if policy.MaxSubquestSteps != orchestrator.MaxSubquestSteps(cfg) {
			t.Fatalf("%s: подзадач в превью %d, у движка %d", preset, policy.MaxSubquestSteps, orchestrator.MaxSubquestSteps(cfg))
		}
		if policy.MaxConcurrentAgents != orchestrator.MaxConcurrentAgents(cfg) {
			t.Fatalf("%s: параллельность в превью %d, у движка %d", preset, policy.MaxConcurrentAgents, orchestrator.MaxConcurrentAgents(cfg))
		}
		if len(policy.Lines) < 4 {
			t.Fatalf("%s: политика описана слишком скупо: %v", preset, policy.Lines)
		}
		// Числа обязаны попасть в текст: строка без конкретики не даёт
		// пользователю ничего, кроме ощущения настройки.
		joined := strings.Join(policy.Lines, " ")
		for _, want := range []string{
			itoa(policy.PartySize), itoa(policy.MaxSubquestSteps), itoa(policy.MaxConcurrentAgents),
		} {
			if !strings.Contains(joined, want) {
				t.Fatalf("%s: в описании нет числа %s: %v", preset, want, policy.Lines)
			}
		}
	}
}

func TestPolicyReflectsStrictnessAndPlanner(t *testing.T) {
	strict := domain.OrchestratorConfig{Preset: "custom", ApprovalStrictness: 90, TeamPreference: 50, PlanningDepth: 50, Parallelism: 50}
	policy := orchestrator.DescribePolicy(strict)
	if !policy.RequiresExplicitStart {
		t.Fatal("высокая строгость обязана останавливать Flow перед выводом")
	}
	if !strings.Contains(strings.Join(policy.Lines, " "), "подтверждение") {
		t.Fatalf("строгость не объяснена словами: %v", policy.Lines)
	}

	relaxed := strict
	relaxed.ApprovalStrictness = 20
	if orchestrator.DescribePolicy(relaxed).RequiresExplicitStart {
		t.Fatal("низкая строгость не должна требовать подтверждения перед выводом")
	}

	withModel := strict
	withModel.Provider = domain.ProviderOllama
	withModel.Model = "qwen:7b"
	planned := orchestrator.DescribePolicy(withModel)
	if !planned.UsesModelPlanner || !strings.Contains(strings.Join(planned.Lines, " "), "qwen:7b") {
		t.Fatalf("модель планировщика не отражена: %+v", planned)
	}
	if !strings.Contains(strings.Join(planned.Lines, " "), "детерминированный") {
		t.Fatal("человек должен знать, что при сбое модели работа не встанет")
	}
}

func itoa(value int) string { return strconv.Itoa(value) }
