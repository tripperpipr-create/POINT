package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// Сигнал хода живёт ровно столько, сколько ход: канал, взятый потоком
// событий до конца хода, закрывается при его уборке, а после неё новый не
// выдаётся. Иначе карта сигналов росла бы на каждом переподключении.
func TestMasterTurnSignalsDoNotOutliveTheTurn(t *testing.T) {
	release := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"role": "assistant", "content": "Готово"}, "done": true})
	}))
	defer provider.Close()
	a := newTestApp(t)
	world := openTestWorld(t, a)
	ctx := context.Background()
	if err := a.store.SaveOrchestratorConfig(ctx, domain.OrchestratorConfig{ID: "master", WorkspaceID: world.ID, Provider: domain.ProviderOllama, BaseURL: provider.URL, Model: "test-model", PlanningDepth: 50, Parallelism: 1}); err != nil {
		t.Fatal(err)
	}
	sessions, err := a.UpdateMasterSession(ctx, MasterSessionUpdate{Action: "new"})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := a.StartMasterTurn(ctx, MasterChatRequest{TurnID: "signal-turn", ConversationID: sessions.Active, Message: "Привет", TaskIntake: true})
	if err != nil {
		t.Fatal(err)
	}
	signal := a.MasterTurnSignal(turn)
	if signal == nil {
		t.Fatal("живой ход не выдал сигнал")
	}
	close(release)
	select {
	case <-signal:
	case <-time.After(20 * time.Second):
		t.Fatal("сигнал хода так и не сработал")
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		a.masterTurnsMu.Lock()
		live := len(a.masterTurnCancels)
		left := len(a.masterTurnSignals)
		a.masterTurnsMu.Unlock()
		if current, _ := a.MasterTurn(ctx, turn.ID); current.Status == "completed" && live == 0 {
			if left != 0 {
				t.Fatalf("после хода в карте остались сигналы: %d", left)
			}
			if a.MasterTurnSignal(turn) != nil {
				t.Fatal("законченный ход выдал новый сигнал")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("ход не закончился")
}
