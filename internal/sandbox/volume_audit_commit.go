package sandbox

import "context"

// VolumeAuditCommit extends the durable operation through the agent's patch and
// result journal. A crash after mirror commit must not silently erase history.
type VolumeAuditCommit interface {
	UsesVolume(string) bool
	BeginVolumeAudit(context.Context, string) error
	FinishVolumeAudit(context.Context, string) error
}

func (b *ContainerBackend) BeginVolumeAudit(_ context.Context, root string) error {
	s, err := b.stateFor(root)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.AuditPending {
		s.taint("Previous command audit was not committed")
	}
	s.AuditPending = true
	return saveVolumeState(s)
}
func (b *ContainerBackend) FinishVolumeAudit(_ context.Context, root string) error {
	s, err := b.stateFor(root)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.AuditPending = false
	return saveVolumeState(s)
}
