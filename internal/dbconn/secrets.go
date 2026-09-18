package dbconn

import (
	"strings"
	"sync"
)

// MemorySecrets holds passwords unlocked from IDE SecretStorage for the process lifetime.
// Values are never written to SQLite/bootstrap.
type MemorySecrets struct {
	mu   sync.RWMutex
	data map[string]string
}

func NewMemorySecrets() *MemorySecrets {
	return &MemorySecrets{data: make(map[string]string)}
}

func (s *MemorySecrets) Put(ref, value string) {
	ref = strings.TrimSpace(ref)
	if s == nil || ref == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if value == "" {
		delete(s.data, ref)
		return
	}
	s.data[ref] = value
}

func (s *MemorySecrets) Get(ref string) (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[strings.TrimSpace(ref)]
	return v, ok
}

func (s *MemorySecrets) Delete(ref string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, strings.TrimSpace(ref))
}
