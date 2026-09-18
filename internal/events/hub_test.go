package events

import (
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestHubDeliversToSubscribers(t *testing.T) {
	hub := NewHub()
	stream, cancel := hub.Subscribe()
	defer cancel()
	hub.Publish(domain.Event{ID: "evt-1", Type: domain.EventRunCompleted})
	select {
	case event := <-stream:
		if event.ID != "evt-1" {
			t.Fatalf("event=%#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestHubDropsSlowSubscriberInsteadOfSilentLoss(t *testing.T) {
	hub := NewHub()
	stream, cancel := hub.Subscribe()
	defer cancel()
	for i := 0; i < subscriberBufferSize; i++ {
		hub.Publish(domain.Event{ID: "fill"})
	}
	if hub.SubscriberCount() != 1 {
		t.Fatalf("subscriber count before overflow=%d", hub.SubscriberCount())
	}
	hub.Publish(domain.Event{ID: "overflow"})
	if hub.SubscriberCount() != 0 {
		t.Fatalf("slow subscriber should be dropped, count=%d", hub.SubscriberCount())
	}
	// Channel must be closed after drop.
	drained := false
	deadline := time.After(time.Second)
	for !drained {
		select {
		case _, ok := <-stream:
			if !ok {
				drained = true
			}
		case <-deadline:
			t.Fatal("subscriber channel was not closed after drop")
		}
	}
}

func TestHubScopesWorkspaceSubscribers(t *testing.T) {
	hub := NewHub()
	stream, cancel := hub.SubscribeWorkspace("workspace-a")
	defer cancel()
	hub.Publish(domain.Event{ID: "wrong", WorkspaceID: "workspace-b"})
	hub.Publish(domain.Event{ID: "right", WorkspaceID: "workspace-a"})
	select {
	case event := <-stream:
		if event.ID != "right" {
			t.Fatalf("cross-workspace event delivered: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for scoped event")
	}
}
