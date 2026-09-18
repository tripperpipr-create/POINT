package events

import (
	"context"
	"sync"

	"local-agent-workbench/internal/domain"
)

type Store interface {
	Append(context.Context, domain.Event) error
	ListByRun(context.Context, string) ([]domain.Event, error)
}

type MemoryStore struct {
	mu     sync.RWMutex
	events map[string][]domain.Event
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{events: make(map[string][]domain.Event)} }

func (s *MemoryStore) Append(_ context.Context, event domain.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events[event.RunID] = append(s.events[event.RunID], event)
	return nil
}

func (s *MemoryStore) ListByRun(_ context.Context, runID string) ([]domain.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Event, len(s.events[runID]))
	copy(out, s.events[runID])
	return out, nil
}
