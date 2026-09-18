package app

import (
	"context"
	"encoding/json"
	"local-agent-workbench/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Живой след хода доходит до журнала событий целиком: не только имя
// инструмента, но и то, с чем к нему обратились и чем он ответил.
//
// Путь длинный — оркестратор, ход, хранилище, — и раньше по нему ехала одна
// строка: «tools: read_file». Лента показывала «Изучаю проект…» и молчала обо
// всём остальном, поэтому проверка идёт от настоящего ответа провайдера до
// того, что прочитает вебвью.
func TestMasterTurnEventsCarryToolDetail(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		round := calls.Add(1)
		w.Header().Set("Content-Type", "application/x-ndjson")
		encoder := json.NewEncoder(w)
		if round == 1 {
			call := map[string]any{"function": map[string]any{"name": "list_files", "arguments": map[string]string{"path": "."}}}
			_ = encoder.Encode(map[string]any{"message": map[string]any{"role": "assistant", "tool_calls": []any{call}}, "done": true})
			return
		}
		_ = encoder.Encode(map[string]any{"message": map[string]any{"role": "assistant", "content": `{"intent":"chat","reply":"Посмотрел каталог."}`}, "done": true})
	}))
	defer provider.Close()
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Shutdown(context.Background())
	world := openTestWorld(t, a)
	ctx := context.Background()
	if err = a.store.SaveOrchestratorConfig(ctx, domain.OrchestratorConfig{ID: "master", WorkspaceID: world.ID, Provider: domain.ProviderOllama, BaseURL: provider.URL, Model: "test-model", PlanningDepth: 50, Parallelism: 1}); err != nil {
		t.Fatal(err)
	}
	sessions, err := a.UpdateMasterSession(ctx, MasterSessionUpdate{Action: "new"})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := a.StartMasterTurn(ctx, MasterChatRequest{TurnID: "trace-turn", ConversationID: sessions.Active, Message: "Что в проекте?", TaskIntake: true})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if turn, err = a.MasterTurn(ctx, turn.ID); err != nil {
			t.Fatal(err)
		}
		if turn.Status == "completed" || turn.Status == "failed" || turn.Status == "cancelled" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if turn.Status != "completed" {
		t.Fatalf("ход не дошёл до конца: %+v", turn)
	}
	events, err := a.MasterTurnEvents(ctx, turn.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var start, done *domain.MasterTurnEvent
	for i := range events {
		switch events[i].Type {
		case "tools":
			start = &events[i]
		case "tool_result":
			done = &events[i]
		}
	}
	if start == nil || done == nil {
		t.Fatalf("след обращения не доехал до журнала хода: %+v", events)
	}
	// Text остаётся короткой строкой для сборок оболочки, которые о
	// подробностях не знают, а подробности едут отдельным полем.
	if start.Text != "list_files" {
		t.Fatalf("строка ожидания потеряла имя инструмента: %+v", start)
	}
	var argument struct {
		Tool     string `json:"tool"`
		Argument string `json:"argument"`
		Round    int    `json:"round"`
	}
	if json.Unmarshal([]byte(start.Detail), &argument) != nil || argument.Tool != "list_files" || argument.Argument != "." {
		t.Fatalf("довод обращения потерян: %q", start.Detail)
	}
	if argument.Round < 1 {
		t.Fatalf("круг обращения не назван: %q", start.Detail)
	}
	var outcome struct {
		Tool   string `json:"tool"`
		Result string `json:"result"`
		Failed bool   `json:"failed"`
	}
	if json.Unmarshal([]byte(done.Detail), &outcome) != nil || outcome.Tool != "list_files" || strings.TrimSpace(outcome.Result) == "" {
		t.Fatalf("исход обращения не описан: %q", done.Detail)
	}
	if outcome.Failed {
		t.Fatalf("удачное обращение показано неудачным: %q", done.Detail)
	}
}
