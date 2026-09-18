package events

import (
	"sync"
	"time"

	"local-agent-workbench/internal/domain"
)

const (
	subscriberBufferSize = 128
	publishWaitTimeout   = 100 * time.Millisecond
)

type Hub struct {
	mu          sync.RWMutex
	subscribers map[int]subscriber
	next        int
}

type subscriber struct {
	workspaceID string
	channel     chan domain.Event
}

func NewHub() *Hub { return &Hub{subscribers: make(map[int]subscriber)} }

func (h *Hub) Publish(event domain.Event) {
	h.mu.RLock()
	type entry struct {
		id int
		ch chan domain.Event
	}
	snapshot := make([]entry, 0, len(h.subscribers))
	for id, subscriber := range h.subscribers {
		if subscriber.workspaceID != "" && subscriber.workspaceID != event.WorkspaceID {
			continue
		}
		snapshot = append(snapshot, entry{id: id, ch: subscriber.channel})
	}
	h.mu.RUnlock()

	var slow []int
	for _, subscriber := range snapshot {
		select {
		case subscriber.ch <- event:
		default:
			timer := time.NewTimer(publishWaitTimeout)
			select {
			case subscriber.ch <- event:
				timer.Stop()
			case <-timer.C:
				slow = append(slow, subscriber.id)
			}
		}
	}
	for _, id := range slow {
		h.drop(id)
	}
}

func (h *Hub) drop(id int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if existing, ok := h.subscribers[id]; ok {
		delete(h.subscribers, id)
		close(existing.channel)
	}
}

func (h *Hub) Subscribe() (<-chan domain.Event, func()) {
	return h.SubscribeWorkspace("")
}

// SubscribeWorkspace returns only events attributed to the selected
// workspace. Events from unscoped legacy rows are intentionally not exposed.
func (h *Hub) SubscribeWorkspace(workspaceID string) (<-chan domain.Event, func()) {
	h.mu.Lock()
	id := h.next
	h.next++
	channel := make(chan domain.Event, subscriberBufferSize)
	h.subscribers[id] = subscriber{workspaceID: workspaceID, channel: channel}
	h.mu.Unlock()
	cancel := func() {
		h.drop(id)
	}
	return channel, cancel
}

func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subscribers)
}
