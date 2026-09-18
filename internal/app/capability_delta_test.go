package app

import (
	"context"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

// Ради этого навык и берут: узнать, что агент начнёт мочь. «Требует
// run_command» такого ответа не даёт.
func TestCapabilityDeltaExplainsWhatEquippingChanges(t *testing.T) {
	application := capabilityApp(t)

	writer := domain.AgentProfile{
		Name: "Кузнец", Provider: domain.ProviderOllama, Model: "qwen",
		AllowedTools: []string{"read_file", "propose_patch"}, MaxSteps: 30,
	}
	delta := application.CapabilityDeltaFor(context.Background(), CapabilityDeltaRequest{
		Profile:  writer,
		AddTools: []string{"run_command"},
	})

	if len(delta.Before.Blocking) == 0 {
		t.Fatal("до выдачи умения агент обязан быть заблокирован")
	}
	if len(delta.After.Blocking) != 0 {
		t.Fatalf("после выдачи блокировка обязана сняться: %v", delta.After.Blocking)
	}
	if len(delta.Resolved) == 0 {
		t.Fatal("снятое препятствие обязано попасть в разницу")
	}
	joined := strings.Join(delta.Lines, " ")
	if !strings.Contains(joined, "Снимется препятствие") {
		t.Fatalf("снятие препятствия не объяснено: %v", delta.Lines)
	}
	if !strings.Contains(joined, "запускать команды") || !strings.Contains(joined, "подтверждать результат") {
		t.Fatalf("новые возможности не названы: %v", delta.Lines)
	}
	// Право запускать команды в безопасном профиле означает новые вопросы к
	// человеку — он будет отвечать на них в каждом прогоне.
	if !strings.Contains(joined, "спрашивать") {
		t.Fatalf("изменение подтверждений не отражено: %v", delta.Lines)
	}
}

// Обещать эффект там, где его нет, — худший вид подсказки: человек выдаёт
// умение, ничего не меняется, и доверие к карточке пропадает.
func TestCapabilityDeltaAdmitsWhenNothingChanges(t *testing.T) {
	application := capabilityApp(t)

	profile := domain.AgentProfile{
		Name: "Страж", Provider: domain.ProviderOllama, Model: "qwen",
		AllowedTools: []string{"read_file", "run_command"}, MaxSteps: 30,
	}
	delta := application.CapabilityDeltaFor(context.Background(), CapabilityDeltaRequest{
		Profile:  profile,
		AddTools: []string{"read_file"},
	})
	if len(delta.Gained) != 0 || len(delta.Resolved) != 0 {
		t.Fatalf("повторная выдача того же умения ничего не меняет: %+v", delta)
	}
	if !strings.Contains(strings.Join(delta.Lines, " "), "Ничего не изменится") {
		t.Fatalf("отсутствие эффекта не признано: %v", delta.Lines)
	}
}

// Отбор умения обязан показывать последствие так же честно, как выдача.
func TestCapabilityDeltaShowsWhatRevokingBreaks(t *testing.T) {
	application := capabilityApp(t)

	profile := domain.AgentProfile{
		Name: "Кузнец", Provider: domain.ProviderOllama, Model: "qwen",
		AllowedTools: []string{"read_file", "propose_patch", "run_command"}, MaxSteps: 30,
	}
	delta := application.CapabilityDeltaFor(context.Background(), CapabilityDeltaRequest{
		Profile:     profile,
		RemoveTools: []string{"run_command"},
	})
	if len(delta.Introduced) == 0 {
		t.Fatal("отбор верификатора обязан вводить препятствие")
	}
	joined := strings.Join(delta.Lines, " ")
	if !strings.Contains(joined, "Появится препятствие") {
		t.Fatalf("новое препятствие не названо: %v", delta.Lines)
	}
	if !strings.Contains(joined, "Пропадёт") {
		t.Fatalf("утраченные возможности не названы: %v", delta.Lines)
	}
}
