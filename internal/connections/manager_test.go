package connections_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/connections"
	"local-agent-workbench/internal/domain"
)

func TestModelSelectorFallbackOnlyOnClassifiedErrors(t *testing.T) {
	selector := connections.ClassifiedSelector{}
	model, used := selector.Select("primary", []string{"fallback"}, nil)
	if used || model != "primary" {
		t.Fatalf("no error should keep primary")
	}
	model, used = selector.Select("primary", []string{"fallback"}, errors.New("something else"))
	if used || model != "primary" {
		t.Fatalf("unclassified error should not fallback")
	}
	model, used = selector.Select("primary", []string{"fallback"}, errors.New("provider rate limit"))
	if !used || model != "fallback" {
		t.Fatalf("rate limit should fallback, got %s used=%v", model, used)
	}
}

type memoryConnectionStore struct {
	items []domain.Connection
}

func (s *memoryConnectionStore) SaveConnection(_ context.Context, conn domain.Connection) error {
	for i := range s.items {
		if s.items[i].ID == conn.ID {
			s.items[i] = conn
			return nil
		}
	}
	s.items = append(s.items, conn)
	return nil
}

func (s *memoryConnectionStore) ListConnections(_ context.Context) ([]domain.Connection, error) {
	out := make([]domain.Connection, len(s.items))
	copy(out, s.items)
	return out, nil
}

func TestUpsertPreservesCreatedAt(t *testing.T) {
	store := &memoryConnectionStore{}
	mgr := connections.Manager{Store: store}
	created, err := mgr.Upsert(context.Background(), connections.UpsertRequest{
		Provider: domain.ProviderOllama, PresetID: "ollama", DisplayName: "Local",
		BaseURL: "http://127.0.0.1:11434", Status: domain.ConnectionConnected,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	updated, err := mgr.Upsert(context.Background(), connections.UpsertRequest{
		ID: created.ID, Provider: domain.ProviderOllama, PresetID: "ollama", DisplayName: "Local",
		BaseURL: "http://127.0.0.1:11434", Status: domain.ConnectionConnected,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("CreatedAt changed: %v -> %v", created.CreatedAt, updated.CreatedAt)
	}
	// Утверждение, а не комментарий в пустом теле `if`.
	//
	// Раньше условие вычислялось и выбрасывалось: тест не проверял ничего,
	// хотя выглядел проверяющим. Намерение из комментария — «время правки не
	// едет назад» — проверяемо прямо, а равенство допускается: на грубых
	// часах две правки подряд попадают в один тик.
	if updated.UpdatedAt.Before(created.UpdatedAt) {
		t.Fatalf("UpdatedAt ушло назад: %v -> %v", created.UpdatedAt, updated.UpdatedAt)
	}
	if updated.UpdatedAt.Before(created.CreatedAt) {
		t.Fatalf("UpdatedAt before CreatedAt")
	}
}

// CLI-провайдеры сняты: Point принимает только HTTP API.
func TestUpsertRejectsLocalCLI(t *testing.T) {
	store := &memoryConnectionStore{}
	mgr := connections.Manager{Store: store}
	_, err := mgr.Upsert(context.Background(), connections.UpsertRequest{
		Provider: domain.ProviderClaudeCLI, PresetID: "claude-code", DisplayName: "Claude Code CLI",
	})
	if err == nil {
		t.Fatal("локальный CLI принят после снятия CLI-провайдеров")
	}
	msg := err.Error()
	if !strings.Contains(msg, "снят") && !strings.Contains(msg, "removed") && !strings.Contains(msg, "HTTP API") {
		t.Fatalf("ожидался отказ со снятием CLI / HTTP API, got %v", err)
	}
}
