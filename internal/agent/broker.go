package agent

import (
	"context"
	"errors"
	"sync"

	"local-agent-workbench/internal/domain"
)

var ErrApprovalNotPending = errors.New("approval is not pending")

type approvalWaiter struct {
	approval domain.Approval
	decision chan bool
}

type Broker struct {
	mu      sync.RWMutex
	pending map[string]*approvalWaiter
}

func NewBroker() *Broker { return &Broker{pending: make(map[string]*approvalWaiter)} }

func (b *Broker) Register(approval domain.Approval) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending[approval.ID] = &approvalWaiter{approval: approval, decision: make(chan bool, 1)}
}

func (b *Broker) Await(ctx context.Context, id string) (bool, error) {
	b.mu.RLock()
	waiter, ok := b.pending[id]
	b.mu.RUnlock()
	if !ok {
		return false, ErrApprovalNotPending
	}
	select {
	case allow := <-waiter.decision:
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
		return allow, nil
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
		return false, ctx.Err()
	}
}

func (b *Broker) Resolve(id string, allow bool) error {
	b.mu.RLock()
	waiter, ok := b.pending[id]
	b.mu.RUnlock()
	if !ok {
		return ErrApprovalNotPending
	}
	select {
	case waiter.decision <- allow:
		return nil
	default:
		return ErrApprovalNotPending
	}
}

func (b *Broker) Pending(runID string) []domain.Approval {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]domain.Approval, 0)
	for _, waiter := range b.pending {
		if runID == "" || waiter.approval.RunID == runID {
			result = append(result, waiter.approval)
		}
	}
	return result
}
