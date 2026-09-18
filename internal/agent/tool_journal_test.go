package agent

import (
	"context"
	"encoding/json"
	"errors"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
	workbenchtools "local-agent-workbench/internal/tools"
	"testing"
)

type journalFailureRepo struct {
	*memoryRepo
	fail domain.EventType
}

func (r *journalFailureRepo) Append(ctx context.Context, event domain.Event) error {
	if event.Type == r.fail {
		return errors.New("simulated journal failure")
	}
	return r.memoryRepo.Append(ctx, event)
}

type countedTool struct{ calls int }

func (*countedTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{Name: "read_file"}
}
func (t *countedTool) Execute(context.Context, json.RawMessage) domain.ToolResult {
	t.calls++
	return workbenchtools.OK(map[string]any{"result": "ok"})
}
func TestToolJournalFailureDoesNotLoseActionBoundary(t *testing.T) {
	for _, tc := range []struct {
		event domain.EventType
		calls int
	}{
		{domain.EventToolRequested, 0}, {domain.EventToolStarted, 0}, {domain.EventToolFinished, 1},
	} {
		t.Run(string(tc.event), func(t *testing.T) {
			repo := &journalFailureRepo{memoryRepo: newMemoryRepo(), fail: tc.event}
			engine := NewEngine(repo, nil)
			tool := &countedTool{}
			profile := domain.DefaultProfile()
			profile.AllowedTools = []string{"read_file"}
			active := &activeRun{run: domain.Run{ID: "journal", WorkspaceID: "ws"}}
			_, err := engine.executeTool(context.Background(), active, profile, workbenchtools.NewRegistry(tool), nil, nil, providers.ToolCall{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{}`)})
			if !errors.Is(err, errToolJournalIntegrity) || tool.calls != tc.calls {
				t.Fatalf("calls=%d error=%v", tool.calls, err)
			}
			events, _ := repo.ListByRun(context.Background(), "journal")
			for _, e := range events {
				if e.Type != domain.EventToolRequested && e.Type != domain.EventToolStarted {
					continue
				}
				var p struct {
					CallID string `json:"callId"`
				}
				if json.Unmarshal(e.Data, &p) != nil || p.CallID != "call-1" {
					t.Fatal("operation identity lost")
				}
			}
		})
	}
}
