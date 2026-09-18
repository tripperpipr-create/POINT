package app

import (
	"context"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

func capabilityApp(t *testing.T) *App {
	t.Helper()
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	openTestWorld(t, application)
	return application
}

// Право менять файлы без доказательства — самая частая причина того, что квест
// «завершён», а работоспособность никто не подтвердил. Настройка обязана
// сообщать об этом до запуска, а не после.
func TestAgentCapabilityBlocksWritingWithoutVerification(t *testing.T) {
	application := capabilityApp(t)

	writer := domain.AgentProfile{
		Name: "Кузнец", Provider: domain.ProviderOllama, Model: "qwen",
		AllowedTools: []string{"read_file", "propose_patch"}, MaxSteps: 30,
	}
	capability := application.AgentCapabilityFor(context.Background(), writer)
	if !capability.CanWrite {
		t.Fatal("агент с propose_patch обязан считаться пишущим")
	}
	if capability.CanVerify {
		t.Fatal("без run_command и верификатора подтверждать нечем")
	}
	if len(capability.Blocking) == 0 {
		t.Fatal("правка файлов без доказательства обязана блокировать запуск")
	}
	if !strings.Contains(strings.Join(capability.Blocking, " "), "подтвердить результат") {
		t.Fatalf("причина блокировки не названа понятно: %v", capability.Blocking)
	}
	if !strings.Contains(strings.Join(capability.Lines, " "), "закроется без доказательства") {
		t.Fatalf("последствие не объяснено: %v", capability.Lines)
	}

	// С правом запускать команды та же настройка становится рабочей.
	writer.AllowedTools = append(writer.AllowedTools, "run_command")
	fixed := application.AgentCapabilityFor(context.Background(), writer)
	if !fixed.CanVerify || len(fixed.Blocking) != 0 {
		t.Fatalf("добавление run_command обязано снять блокировку: %+v", fixed)
	}
	if !strings.Contains(strings.Join(fixed.Lines, " "), "подтвердит проверкой") {
		t.Fatalf("доказательство завершения не отражено: %v", fixed.Lines)
	}
}

// Настройка обязана показывать, что именно остановится и спросит человека:
// иначе список умений ничего не говорит о том, как пойдёт работа.
func TestAgentCapabilityNamesWhatWillAskForApproval(t *testing.T) {
	application := capabilityApp(t)

	profile := domain.AgentProfile{
		Name: "Страж", Provider: domain.ProviderOllama, Model: "qwen",
		AllowedTools: []string{"read_file", "propose_patch", "run_command"},
		MaxSteps:     30, ApprovalMode: domain.ApprovalSafe,
	}
	capability := application.AgentCapabilityFor(context.Background(), profile)
	if len(capability.NeedsApproval) == 0 {
		t.Fatal("правка и команды обязаны требовать подтверждения в безопасном профиле")
	}
	joined := strings.Join(capability.Lines, " ")
	if !strings.Contains(joined, "Остановится и спросит вас") {
		t.Fatalf("подтверждения не объяснены: %v", capability.Lines)
	}
	if !strings.Contains(joined, "Сможет:") {
		t.Fatalf("возможности не перечислены: %v", capability.Lines)
	}
}

func TestAgentCapabilityCatchesEmptyAndBlindSetups(t *testing.T) {
	application := capabilityApp(t)

	empty := application.AgentCapabilityFor(context.Background(), domain.AgentProfile{Name: "Пустой", MaxSteps: 30})
	if len(empty.Blocking) < 2 {
		t.Fatalf("без модели и умений обязаны быть две причины: %v", empty.Blocking)
	}
	if !strings.Contains(strings.Join(empty.Lines, " "), "не сможет ничего") {
		t.Fatalf("пустая настройка не объяснена: %v", empty.Lines)
	}

	blind := application.AgentCapabilityFor(context.Background(), domain.AgentProfile{
		Name: "Слепой", Provider: domain.ProviderOllama, Model: "qwen",
		AllowedTools: []string{"run_command"}, MaxSteps: 30,
	})
	if len(blind.Warnings) == 0 || !strings.Contains(strings.Join(blind.Warnings, " "), "вслепую") {
		t.Fatalf("запуск команд без чтения файлов обязан предупреждать: %+v", blind.Warnings)
	}

	shallow := application.AgentCapabilityFor(context.Background(), domain.AgentProfile{
		Name: "Короткий", Provider: domain.ProviderOllama, Model: "qwen",
		AllowedTools: []string{"read_file"}, MaxSteps: 5,
	})
	if !strings.Contains(strings.Join(shallow.Warnings, " "), "лимит ходов низкий") {
		t.Fatalf("низкий лимит ходов обязан предупреждать: %+v", shallow.Warnings)
	}
}

// Шаг починки обязан приходить вместе с причиной. Интерфейс раньше выводил его
// из слов в тексте: любая переформулировка уводила кнопку «исправить» не туда,
// и оба набора тестов оставались зелёными.
func TestCapabilityBlockersNameTheStepToFix(t *testing.T) {
	application := capabilityApp(t)
	ctx := context.Background()

	writer := domain.AgentProfile{
		Name: "Правщик", Provider: domain.ProviderOllama, Model: "q",
		AllowedTools: []string{"propose_patch"}, MaxSteps: 30,
	}
	capability := application.AgentCapabilityFor(ctx, writer)
	if len(capability.Blockers) != 1 || capability.Blockers[0].Step != "tools" {
		t.Fatalf("правка без доказательства чинится на шаге умений: %+v", capability.Blockers)
	}

	naked := application.AgentCapabilityFor(ctx, domain.AgentProfile{Name: "Пустой"})
	steps := map[string]bool{}
	for _, blocker := range naked.Blockers {
		if strings.TrimSpace(blocker.Step) == "" {
			t.Fatalf("причина без шага починки: %+v", blocker)
		}
		steps[blocker.Step] = true
	}
	for _, want := range []string{"model", "tools", "limits"} {
		if !steps[want] {
			t.Fatalf("шаг %q не назван ни одной причиной: %+v", want, naked.Blockers)
		}
	}

	// Два представления одной причины не должны расходиться: текст выводится
	// из структуры, а не заполняется рядом.
	if len(naked.Blocking) != len(naked.Blockers) {
		t.Fatalf("списки причин разошлись: %d текстов против %d записей",
			len(naked.Blocking), len(naked.Blockers))
	}
	for i, blocker := range naked.Blockers {
		if naked.Blocking[i] != blocker.Text {
			t.Fatalf("текст причины %d не совпадает со структурой: %q против %q",
				i, naked.Blocking[i], blocker.Text)
		}
	}
}

// Модель названа, провайдера нет — и агент числился готовым. Отказ приходил
// только на старте квеста, словами providers.New: «unsupported provider kind ""».
func TestAgentCapabilityBlocksModelWithoutProvider(t *testing.T) {
	application := capabilityApp(t)

	orphan := application.AgentCapabilityFor(context.Background(), domain.AgentProfile{
		Name: "Без провайдера", Model: "qwen",
		AllowedTools: []string{"read_file"}, MaxSteps: 30,
	})
	if len(orphan.Blocking) == 0 {
		t.Fatal("агент без провайдера объявлен готовым — запуск упадёт на providers.New")
	}
	if !strings.Contains(strings.Join(orphan.Blocking, " "), "провайдер") {
		t.Fatalf("причина не названа провайдером: %v", orphan.Blocking)
	}

	// CLI-провайдеры сняты: Cursor больше не является готовым путём запуска.
	cursor := application.AgentCapabilityFor(context.Background(), domain.AgentProfile{
		Name: "Курсор", Provider: domain.ProviderCursor,
		AllowedTools: []string{"read_file"}, MaxSteps: 30,
	})
	if len(cursor.Blocking) == 0 {
		t.Fatal("агент Cursor объявлен готовым после снятия CLI")
	}
	joined := strings.Join(cursor.Blocking, " ")
	if !strings.Contains(joined, "CLI") && !strings.Contains(joined, "сняты") {
		t.Fatalf("ожидалась блокировка снятого CLI, got %v", cursor.Blocking)
	}
}

// Читающими считаются группы, объявленные в policy.ProjectReadingGroups, а не
// пара категорий, вписанная сюда руками. Проверка нужна ровно потому, что
// git-инструменты однажды уже переехали из группы `read` в свою: агент с одним
// git_diff молча перестал бы считаться читающим, а вместе с ним поехали бы и
// предупреждения о слепом запуске команд.
func TestAgentCapabilityCountsEveryProjectReadingGroup(t *testing.T) {
	application := capabilityApp(t)
	for _, tool := range []string{"read_file", "project_map", "git_diff", "git_log"} {
		profile := domain.AgentProfile{
			Name: "Читатель", Provider: domain.ProviderOllama, Model: "qwen",
			AllowedTools: []string{tool}, MaxSteps: 30,
		}
		if capability := application.AgentCapabilityFor(context.Background(), profile); !capability.CanRead {
			t.Errorf("агент с %q не считается читающим", tool)
		}
	}
	// Навык читает надетый навык, а не проект: читающим он агента не делает.
	skillOnly := domain.AgentProfile{
		Name: "Навык", Provider: domain.ProviderOllama, Model: "qwen",
		AllowedTools: []string{"read_skill"}, MaxSteps: 30,
	}
	if application.AgentCapabilityFor(context.Background(), skillOnly).CanRead {
		t.Error("read_skill засчитан за чтение проекта")
	}
}
