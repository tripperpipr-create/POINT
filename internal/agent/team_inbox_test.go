package agent

import (
	"testing"

	"local-agent-workbench/internal/domain"
)

// Широковещательное событие хранилище доставленным не отмечает, и прежде оно
// вставлялось в разговор на каждом шаге. Теперь — один раз за прогон, а свои
// же сообщения этапа агенту не возвращаются.
func TestTeamInboxInjectsEachEventOncePerRun(t *testing.T) {
	active := &activeRun{correlation: runCorrelation{FlowRunID: "flow", FlowNodeID: "verify"}}
	events := []domain.TeamEvent{
		{ID: "status-implement", FlowNodeID: "implement", Kind: "status", Message: "реализация готова"},
		{ID: "status-own", FlowNodeID: "verify", Kind: "status", Message: "проверка завершена"},
	}
	first := unseenTeamEvents(active, events)
	if len(first) != 1 || first[0].ID != "status-implement" {
		t.Fatalf("first step: %#v", first)
	}
	if again := unseenTeamEvents(active, events); len(again) != 0 {
		t.Fatalf("broadcast repeated on the next step: %#v", again)
	}
	later := unseenTeamEvents(active, append(events, domain.TeamEvent{ID: "question-review", FlowNodeID: "review", Kind: "question", Message: "?"}))
	if len(later) != 1 || later[0].ID != "question-review" {
		t.Fatalf("new event lost: %#v", later)
	}
}
