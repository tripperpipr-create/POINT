package app

import (
	"context"
	"encoding/json"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/orchestrator"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestMasterTurnStreamsDeduplicatesAndCancels(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"role": "assistant", "content": `{"intent":"chat","reply":"Partial reply`}, "done": false})
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer provider.Close()
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Shutdown(context.Background())
	world := openTestWorld(t, a)
	ctx := context.Background()
	err = a.store.SaveOrchestratorConfig(ctx, domain.OrchestratorConfig{ID: "master", WorkspaceID: world.ID, Provider: domain.ProviderOllama, BaseURL: provider.URL, Model: "test-model", PlanningDepth: 50, Parallelism: 1})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := a.UpdateMasterSession(ctx, MasterSessionUpdate{Action: "new"})
	if err != nil {
		t.Fatal(err)
	}
	req := MasterChatRequest{TurnID: "repeat-safe", ConversationID: sessions.Active, Message: "Explain the project", TaskIntake: true}
	turn, err := a.StartMasterTurn(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		turn, err = a.MasterTurn(ctx, req.TurnID)
		if err != nil {
			t.Fatal(err)
		}
		if turn.Reply == "Partial reply" {
			break
		}
		if turn.Status == "failed" {
			t.Fatalf("model failed: %+v", turn)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if turn.Reply != "Partial reply" || turn.Status != "streaming" {
		t.Fatalf("no live fragment: %+v", turn)
	}
	again, err := a.StartMasterTurn(ctx, req)
	if err != nil || again.ID != turn.ID || calls.Load() != 1 {
		t.Fatalf("request repeated: %+v %v calls=%d", again, err, calls.Load())
	}
	changed := req
	changed.Message = "Different request"
	if _, err = a.StartMasterTurn(ctx, changed); err == nil {
		t.Fatal("reused ID with different payload")
	}
	if err = a.CancelMasterTurn(ctx, turn.ID); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		turn, _ = a.MasterTurn(ctx, turn.ID)
		if turn.Status == "cancelled" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if turn.Status != "cancelled" {
		t.Fatalf("cancel failed: %+v", turn)
	}
	page, err := a.MasterPage(ctx, sessions.Active, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	users, partial := 0, 0
	for _, m := range page.Items {
		if m.Role == "user" {
			users++
		}
		if m.Role == "assistant" && m.Content == "Partial reply" && m.Mode == "cancelled" {
			partial++
		}
	}
	if users != 1 || partial != 1 {
		t.Fatalf("messages lost or duplicated: %+v", page.Items)
	}
	events, err := a.MasterTurnEvents(ctx, turn.ID, 0)
	if err != nil || len(events) < 2 {
		t.Fatalf("events missing: %+v %v", events, err)
	}
	replay, err := a.MasterTurnEvents(ctx, turn.ID, events[0].Sequence)
	if err != nil || len(replay) != len(events)-1 {
		t.Fatalf("event replay: %+v %v", replay, err)
	}
}

// Свой срок хода не может быть короче бюджета модели: короткий потолок обрывал
// поток на середине ответа и записывал это как остановку человеком.
func TestMasterTurnDeadlineOutlivesModelBudget(t *testing.T) {
	for _, provider := range []domain.ProviderKind{domain.ProviderOllama, domain.ProviderOpenAI} {
		cfg := domain.OrchestratorConfig{Provider: provider}
		budget := orchestrator.MasterTurnBudget(cfg)
		deadline := masterTurnDeadline(cfg)
		if deadline <= budget {
			t.Fatalf("provider=%s: срок хода %s не переживает бюджет модели %s", provider, deadline, budget)
		}
	}
}
