package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Мастер обязан видеть ростер до того, как пообещает исполнителя: пустой проект
// заканчивался советом «создайте агента», а кем именно закрывать работу, не
// знал ни он, ни человек.
func TestMasterReadRosterShowsCandidatesAndGaps(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	agent := rosterTestAgent(t, application, "Backend", "Backend-разработчик", "Держит серверную часть проекта")
	tools := newMasterReadTools(nil, application.store, world.ID, application.ObserveRoster)

	result := tools.Execute(context.Background(), masterRosterToolName, json.RawMessage(`{"goal":"Собрать backend API с /health","requiredTools":["read_file","propose_patch","run_command"],"maxAgents":2}`))
	if !result.OK {
		t.Fatalf("read_roster не ответил: %#v", result.Error)
	}
	var payload struct {
		Selected []struct {
			AgentID   string `json:"agentId"`
			Readiness string `json:"readiness"`
		} `json:"selected"`
		Budget struct {
			MaxProjectAgents int `json:"maxProjectAgents"`
		} `json:"budget"`
		Note string `json:"note"`
	}
	if err := json.Unmarshal(result.Output, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Selected) != 1 || payload.Selected[0].AgentID != agent.ID {
		t.Fatalf("готовый исполнитель не назван: %s", string(result.Output))
	}
	if payload.Selected[0].Readiness == "" || payload.Budget.MaxProjectAgents != 2 {
		t.Fatalf("ответ без готовности или бюджета: %s", string(result.Output))
	}
	if !strings.Contains(payload.Note, "сервер") {
		t.Fatalf("модель обязана знать, что ростер собирает сервер: %q", payload.Note)
	}
}

// На пустом проекте инструмент отдаёт заполненную форму найма: гибрид держится
// на том, что черновик уже есть, а модель его лишь уточняет.
func TestMasterReadRosterReturnsFilledDraftForm(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	tools := newMasterReadTools(nil, application.store, world.ID, application.ObserveRoster)

	result := tools.Execute(context.Background(), masterRosterToolName, json.RawMessage(`{"goal":"Собрать backend API с /health"}`))
	if !result.OK {
		t.Fatalf("read_roster не ответил: %#v", result.Error)
	}
	var payload struct {
		Gaps []struct {
			Kind      string `json:"kind"`
			DraftForm struct {
				Name          string   `json:"name"`
				Role          string   `json:"role"`
				Mission       string   `json:"mission"`
				RequiredTools []string `json:"requiredTools"`
			} `json:"draftForm"`
		} `json:"gaps"`
	}
	if err := json.Unmarshal(result.Output, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Gaps) != 1 || payload.Gaps[0].Kind != "missing_primary" {
		t.Fatalf("пустой проект обязан назвать пробел исполнителя: %s", string(result.Output))
	}
	form := payload.Gaps[0].DraftForm
	if form.Name == "" || form.Role == "" || form.Mission == "" || len(form.RequiredTools) == 0 {
		t.Fatalf("форма найма пришла пустой: %s", string(result.Output))
	}
}

// Инструмент читает и только читает: без цели он отказывает, а без наблюдателя
// отвечает отказом, а не паникой.
func TestMasterReadRosterStaysReadOnly(t *testing.T) {
	application, world := rosterTestApp(t, "dispatcher")
	tools := newMasterReadTools(nil, application.store, world.ID, application.ObserveRoster)

	if result := tools.Execute(context.Background(), masterRosterToolName, json.RawMessage(`{"goal":"   "}`)); result.OK || result.Error == nil || result.Error.Code != "invalid_input" {
		t.Fatalf("сводка без цели обязана быть отвергнута: %#v", result)
	}
	if result := tools.Execute(context.Background(), "run_command", json.RawMessage(`{"command":"whoami"}`)); result.OK || result.Error == nil || result.Error.Code != "tool_not_allowed" {
		t.Fatalf("граница чтения сдвинулась: %#v", result)
	}

	without := newMasterReadTools(nil, application.store, world.ID, nil)
	result := without.Execute(context.Background(), masterRosterToolName, json.RawMessage(`{"goal":"Собрать backend API"}`))
	if result.OK || result.Error == nil || result.Error.Code != "storage_unavailable" {
		t.Fatalf("без наблюдателя инструмент обязан отказать мягко: %#v", result)
	}
}

// Имя инструмента объявлено там, где его ищет сверка словарей интерфейса: без
// русского имени разговор написал бы «обратился к инструменту».
func TestMasterRosterToolIsNamedForTheUI(t *testing.T) {
	if masterRosterToolName != "read_roster" {
		t.Fatalf("имя инструмента разошлось со словарями интерфейса: %q", masterRosterToolName)
	}
	definition := masterRosterDefinition()
	if definition.Name != masterRosterToolName || !strings.Contains(string(definition.InputSchema), `"goal"`) {
		t.Fatalf("определение инструмента потеряло сводку требований: %#v", definition)
	}
}
