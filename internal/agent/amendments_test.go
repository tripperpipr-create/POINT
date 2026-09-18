package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

type gateModel struct {
	mu        sync.Mutex
	calls     int
	firstDone chan struct{}
}

func (m *gateModel) Stream(ctx context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	m.calls++
	call := m.calls
	m.mu.Unlock()
	if call == 1 {
		select {
		case <-m.firstDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if call >= 2 {
		return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "done"})
	}
	args, _ := json.Marshal(map[string]any{"path": "main.go"})
	return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "read-1", Name: "read_file", Arguments: args}})
}

func TestPauseResumeAtCheckpoint(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &gateModel{firstDone: make(chan struct{})}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })

	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"read_file"}
	profile.MaxSteps = 3
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws", Path: t.TempDir()},
		Task:          "inspect main.go",
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	if err := engine.Pause(run.ID); err != nil {
		t.Fatal(err)
	}
	close(model.firstDone)

	deadline := time.After(3 * time.Second)
	for {
		repo.mu.Lock()
		stored := repo.runs[run.ID]
		repo.mu.Unlock()
		if stored.Status == domain.RunPaused {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("run never paused, status=%s", stored.Status)
		case <-time.After(20 * time.Millisecond):
		}
	}

	if err := engine.Resume(run.ID); err != nil {
		t.Fatal(err)
	}

	finished := time.After(5 * time.Second)
	for {
		repo.mu.Lock()
		stored := repo.runs[run.ID]
		repo.mu.Unlock()
		if stored.Status == domain.RunCompleted || stored.Status == domain.RunFailed {
			return
		}
		select {
		case <-finished:
			t.Fatalf("run did not finish, status=%s", stored.Status)
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func TestForbiddenFileBlocksReadTool(t *testing.T) {
	active := &activeRun{run: domain.Run{ID: "run-forbid"}}
	active.amendments.ForbiddenPaths = []string{"secrets/config.env"}
	engine := NewEngine(newMemoryRepo(), nil)
	if !engine.isPathForbidden(active, "secrets/config.env") {
		t.Fatal("expected secrets path to be forbidden")
	}
	result := forbiddenPathFailure("secrets/config.env")
	if result.OK || result.Error == nil || result.Error.Code != "file_forbidden" {
		t.Fatalf("expected forbidden failure, got %#v", result)
	}
}

func TestApplyContextAmendments(t *testing.T) {
	items := []domain.RunContextItem{
		{ID: "ctx-1", Label: "one", Pinned: false},
		{ID: "ctx-2", Label: "two", Pinned: false},
	}
	applyContextAmendments(&items, []domain.ContextAmendment{
		{Action: domain.ContextAmendAdd, ItemID: "ctx-3", Item: &domain.RunContextItem{ID: "ctx-3", Label: "three", Content: "new evidence", Pending: true}},
		{Action: domain.ContextAmendPin, ItemID: "ctx-1"},
		{Action: domain.ContextAmendRemove, ItemID: "ctx-2"},
	})
	if len(items) != 2 || !items[0].Pinned || items[0].ID != "ctx-1" || items[1].ID != "ctx-3" || items[1].Pending {
		t.Fatalf("unexpected items after amendments: %#v", items)
	}
}

func TestAddRunContextQueuesImmutableSnapshotAndEnforcesAggregateLimit(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	active := &activeRun{run: domain.Run{
		ID: "run-live-context", Status: domain.RunRunning, StartedAt: time.Now().UTC(),
		ContextItems: []domain.RunContextItem{{ID: "ctx-existing", Kind: domain.ContextText, Label: "existing", Content: "base", Size: 4, SourceSize: 4, ExtractedSize: 4}},
	}}
	engine.mu.Lock()
	engine.active[active.run.ID] = active
	engine.mu.Unlock()

	item := domain.RunContextItem{ID: "ctx-live", Kind: domain.ContextText, Label: "live", Content: "new evidence", Digest: "digest-live", Size: 12, SourceSize: 12, ExtractedSize: 12}
	if err := engine.AddRunContext(active.run.ID, []domain.RunContextItem{item}); err != nil {
		t.Fatal(err)
	}
	item.Content = "caller mutation"
	queued := engine.RunAmendments(active.run.ID)
	if len(queued.ContextAmends) != 1 || queued.ContextAmends[0].Item == nil || queued.ContextAmends[0].Item.Content != "new evidence" {
		t.Fatalf("queued context=%#v", queued.ContextAmends)
	}
	queued.ContextAmends[0].Item.Content = "returned mutation"
	if fresh := engine.RunAmendments(active.run.ID); fresh.ContextAmends[0].Item.Content != "new evidence" {
		t.Fatal("RunAmendments leaked its queued item pointer")
	}

	history := newConversationHistory(BuildStableMessages(domain.AgentProfile{SystemPrompt: "system"}, active.run.ContextItems, "task"))
	engine.applyPendingAmendments(active, history, domain.AgentProfile{SystemPrompt: "system"}, nil)
	if len(active.run.ContextItems) != 2 || active.run.ContextItems[1].Content != "new evidence" {
		t.Fatalf("live context was not applied: %#v", active.run.ContextItems)
	}
	if err := engine.AddRunContext(active.run.ID, []domain.RunContextItem{{ID: "duplicate", Kind: domain.ContextText, Label: "live", Content: "new evidence", Digest: "digest-live", Size: 12, SourceSize: 12, ExtractedSize: 12}}); err == nil {
		t.Fatal("duplicate immutable snapshot must be rejected")
	}

	tooMany := make([]domain.RunContextItem, 16)
	for index := range tooMany {
		tooMany[index] = domain.RunContextItem{ID: domain.NewID("ctx"), Kind: domain.ContextText, Label: "extra", Content: "x", Size: 1, SourceSize: 1, ExtractedSize: 1}
	}
	if err := engine.AddRunContext(active.run.ID, tooMany); err == nil || !strings.Contains(err.Error(), "more than 16") {
		t.Fatalf("expected aggregate item limit, got %v", err)
	}
	if pending := engine.RunAmendments(active.run.ID); len(pending.ContextAmends) != 0 {
		t.Fatalf("failed batch was partially queued: %#v", pending.ContextAmends)
	}
}

func TestInjectRunMessageQueuesAmendment(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	active := &activeRun{run: domain.Run{ID: "run-msg", AgentID: "agent-msg", Status: domain.RunRunning, StartedAt: time.Now().UTC()}}
	engine.mu.Lock()
	engine.active[active.run.ID] = active
	engine.mu.Unlock()

	if err := engine.InjectRunMessage(active.run.ID, " steer left ", "correction"); err != nil {
		t.Fatal(err)
	}
	amendments := engine.RunAmendments(active.run.ID)
	if len(amendments.PendingMessages) != 1 || amendments.PendingMessages[0].Content != "steer left" || amendments.PendingMessages[0].LearningIntent != "correction" {
		t.Fatalf("pending messages=%#v", amendments.PendingMessages)
	}

	history := newConversationHistory([]providers.Message{{Role: "system", Content: "sys"}})
	engine.applyPendingAmendments(active, history, domain.AgentProfile{SystemPrompt: "sys"}, nil)
	messages, _, err := history.Prepare(nil, 100000)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range messages {
		if message.Role == "user" && message.Content == "steer left" {
			found = true
		}
	}
	if !found {
		t.Fatalf("injected message missing from history: %#v", messages)
	}
	events, err := repo.ListByRun(context.Background(), active.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != domain.EventRunMessageInjected || events[0].Actor != "user" || !strings.Contains(string(events[0].Data), `"learningIntent":"correction"`) || strings.Contains(string(events[0].Data), "sk-secret") {
		t.Fatalf("injected message learning event=%#v", events)
	}
}

func TestContextAmendmentRebuildsStableModelPrefix(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	profile := domain.AgentProfile{SystemPrompt: "system"}
	active := &activeRun{run: domain.Run{
		ID: "run-context", Status: domain.RunRunning, Task: "task",
		ContextItems: []domain.RunContextItem{{ID: "ctx-remove", Kind: domain.ContextText, Content: "remove-me"}},
	}}
	active.amendments.ContextAmends = []domain.ContextAmendment{{Action: domain.ContextAmendRemove, ItemID: "ctx-remove"}}
	history := newConversationHistory(BuildStableMessages(profile, active.run.ContextItems, active.run.Task))
	engine.applyPendingAmendments(active, history, profile, nil)
	messages, _, err := history.Prepare(nil, 100000)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if strings.Contains(message.Content, "remove-me") {
			t.Fatalf("removed context remained in model history: %#v", messages)
		}
	}
}

func TestForbiddenPathDisablesWorkspaceWideTools(t *testing.T) {
	for _, name := range []string{"project_map", "search_code", "list_files", "search_text", "git_diff", "run_command", "customtool_example"} {
		if !toolHasWorkspaceWideAccess(name) {
			t.Fatalf("%s must be treated as workspace-wide while a path is forbidden", name)
		}
	}
	if toolHasWorkspaceWideAccess("read_file") || toolHasWorkspaceWideAccess("propose_patch") {
		t.Fatal("path-addressed tools should remain available for non-forbidden targets")
	}
}

func TestConcurrentResumeSignalsCheckpointOnlyOnce(t *testing.T) {
	engine := NewEngine(newMemoryRepo(), nil)
	ch := make(chan struct{})
	engine.active["paused"] = &activeRun{paused: true, resumeCh: ch}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = engine.Resume("paused") }()
	}
	wg.Wait()
	select {
	case <-ch:
	default:
		t.Fatal("checkpoint was not resumed")
	}
}
