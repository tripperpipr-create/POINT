package app

import (
	"context"
	"encoding/json"
	"local-agent-workbench/internal/connections"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
	"local-agent-workbench/internal/sandbox"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type hostFastModel struct {
	calls atomic.Int32
	block bool
}

func (m *hostFastModel) Stream(ctx context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	if m.block {
		<-ctx.Done()
		return ctx.Err()
	}
	switch m.calls.Add(1) {
	case 1:
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "list", Name: "list_files", Arguments: json.RawMessage(`{"path":""}`)}})
	case 2:
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "patch", Name: "propose_patch", Arguments: json.RawMessage(`{"path":"result.txt","content":"host-live\n","reason":"create the requested local file"}`)}})
	case 3:
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "verify", Name: "run_command", Arguments: json.RawMessage(`{"command":"echo verified","reason":"verify host command execution"}`)}})
	default:
		return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Created result.txt and ran the local check."})
	}
}
func hostFastFixture(t *testing.T, project bool) (*App, *strongWorkOrderSandbox, MasterSessions) {
	t.Helper()
	t.Setenv("REDIS_ADDR", "")
	t.Setenv("POINT_AGENT_HUB_V2", "1")
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	backend := &strongWorkOrderSandbox{recordingSandboxBackend: &recordingSandboxBackend{Manager: &sandbox.Manager{Root: t.TempDir()}}}
	a, err := New(t.TempDir(), WithSandboxBackend(backend))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Shutdown(context.Background()) })
	if project {
		openTestWorld(t, a)
	}
	conn, err := a.SaveConnection(connections.UpsertRequest{ID: "host-test", Provider: domain.ProviderOllama, PresetID: "ollama", DisplayName: "Host test", BaseURL: "http://127.0.0.1:11434"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := a.FastAgentConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c.Profile.ConnectionID = conn.ID
	c.Profile.Model = "test"
	c.Profile.MaxSteps = 10
	if _, err = a.SaveFastAgentConfig(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	sessions, err := a.MasterSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return a, backend, sessions
}
func TestHostFastAgentWritesLiveWithoutRosterOrDocker(t *testing.T) {
	for _, project := range []bool{false, true} {
		t.Run(map[bool]string{false: "point_chat", true: "project"}[project], func(t *testing.T) {
			a, backend, s := hostFastFixture(t, project)
			model := &hostFastModel{}
			a.engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
			r := FastAgentRequest{WorkspaceID: s.WorkspaceID, ConversationID: s.Active, RequestID: "exact-request", Task: "Create result.txt and verify it"}
			run, err := a.StartFastAgent(r)
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				current, e := a.store.GetRun(context.Background(), run.ID)
				if e != nil {
					t.Fatal(e)
				}
				if current.Status == domain.RunCompleted || current.Status == domain.RunFailed {
					run = current
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			a.engine.WaitFinalized(run.ID, 5*time.Second)
			if run.Status != domain.RunCompleted {
				events, _ := a.store.ListByRun(context.Background(), run.ID)
				t.Fatalf("run=%+v events=%+v", run, events)
			}
			w, err := a.store.WorkspaceByID(context.Background(), s.WorkspaceID)
			if err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(filepath.Join(w.Path, "result.txt"))
			if err != nil || string(content) != "host-live\n" {
				t.Fatalf("live file=%q error=%v", content, err)
			}
			if len(backend.records) != 0 || len(backend.processRequests) != 0 {
				t.Fatal("Fast Agent invoked the sandbox backend")
			}
			agents, _ := a.store.ListProjectAgents(context.Background(), s.WorkspaceID)
			if len(agents) != 0 {
				t.Fatal("Fast Agent materialized project agents")
			}
			approvals, _ := a.store.ApprovalsByRun(context.Background(), run.ID)
			for _, approval := range approvals {
				if approval.Status == domain.ApprovalPending {
					t.Fatalf("ordinary operation requested approval: %+v", approval)
				}
			}
			if run.ConfigurationSnapshot.Profile.ExecutionMode != "host_live" || run.ProfileID != domain.SystemFastAgentID {
				t.Fatal("system identity and executor were not pinned")
			}
			replayed, err := a.StartFastAgent(r)
			if err != nil || replayed.ID != run.ID || model.calls.Load() != 4 {
				t.Fatalf("request replay launched again: %s %v calls=%d", replayed.ID, err, model.calls.Load())
			}
			r.Task = "Different task"
			if _, err = a.StartFastAgent(r); err == nil {
				t.Fatal("changed replay accepted")
			}
			lease, exists, err := a.store.WriterLeaseV2(context.Background(), s.WorkspaceID)
			if err != nil || !exists || lease.State != "released" {
				t.Fatalf("lease=%+v %v", lease, err)
			}
		})
	}
}
// hostGitModel просит удалить ветку, а после ответа человека отчитывается.
type hostGitModel struct{ calls atomic.Int32 }

func (m *hostGitModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	if m.calls.Add(1) == 1 {
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "delete", Name: "run_command", Arguments: json.RawMessage(`{"command":"git branch -D feature","reason":"remove the merged branch"}`)}})
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Ветка не удалена: человек отклонил."})
}

// 03.10 Fast Agent удалил ветку с уникальными коммитами без спроса: на
// локальной полосе такие git-команды ждут человека, а отказ команду не пускает.
func TestHostFastAgentAsksBeforeDiscardingGitWork(t *testing.T) {
	a, _, s := hostFastFixture(t, false)
	a.engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return &hostGitModel{}, nil })
	run, err := a.StartFastAgent(FastAgentRequest{WorkspaceID: s.WorkspaceID, RequestID: "git-delete", Task: "Удали ветку feature"})
	if err != nil {
		t.Fatal(err)
	}
	var pending domain.Approval
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && pending.ID == "" {
		approvals, _ := a.store.ApprovalsByRun(context.Background(), run.ID)
		for _, approval := range approvals {
			if approval.Status == domain.ApprovalPending {
				pending = approval
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pending.ID == "" || pending.ToolName != "run_command" || !strings.Contains(pending.Reason, "ветк") {
		t.Fatalf("branch deletion ran without asking: %+v", pending)
	}
	if err = a.ResolveApproval(pending.ID, false); err != nil {
		t.Fatal(err)
	}
	a.engine.WaitFinalized(run.ID, 5*time.Second)
	events, _ := a.store.ListByRun(context.Background(), run.ID)
	for _, event := range events {
		if event.Type == domain.EventToolStarted && strings.Contains(string(event.Data), "run_command") {
			t.Fatalf("denied command started: %s", event.Data)
		}
	}
}

func TestHostFastAgentWriterLeaseAndCancellation(t *testing.T) {
	a, _, s := hostFastFixture(t, false)
	a.engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return &hostFastModel{block: true}, nil })
	run, err := a.StartFastAgent(FastAgentRequest{WorkspaceID: s.WorkspaceID, RequestID: "first", Task: "Bounded local task"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.StartFastAgent(FastAgentRequest{WorkspaceID: s.WorkspaceID, RequestID: "second", Task: "Other local task"}); err == nil || !strings.Contains(err.Error(), "writer") {
		t.Fatalf("parallel writer accepted: %v", err)
	}
	if err = a.CancelRun(run.ID); err != nil {
		t.Fatal(err)
	}
	a.engine.WaitFinalized(run.ID, 5*time.Second)
	lease, _, err := a.store.WriterLeaseV2(context.Background(), s.WorkspaceID)
	if err != nil || lease.State != "released" {
		t.Fatalf("cancelled writer holds lease: %+v %v", lease, err)
	}
}
func TestPointChatFoldersPersistAndDoNotMixProjectContext(t *testing.T) {
	a, _, initial := hostFastFixture(t, true)
	ctx := context.Background()
	s, err := a.UpdateMasterSession(ctx, MasterSessionUpdate{Action: "new", ScopeKind: "point_chat"})
	if err != nil {
		t.Fatal(err)
	}
	if s.WorkspaceID == initial.WorkspaceID || s.WorkMode != "auto" || s.Items[0].ScopeKind != "point_chat" {
		t.Fatalf("point scope=%+v", s)
	}
	scoped, err := a.WithMasterWorkspace(ctx, s.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := a.store.WorkspaceByID(ctx, s.WorkspaceID)
	if w.Path != filepath.Join(os.Getenv("USERPROFILE"), "POINT", "Chats", s.Active) {
		t.Fatalf("wrong working directory %s", w.Path)
	}
	file := filepath.Join(w.Path, "keep.txt")
	if err = os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = a.UpdateMasterSession(scoped, MasterSessionUpdate{Action: "rename", ID: s.Active, Value: "Renamed"}); err != nil {
		t.Fatal(err)
	}
	other, err := a.UpdateMasterSession(scoped, MasterSessionUpdate{Action: "new", ScopeKind: "point_chat"})
	if err != nil {
		t.Fatal(err)
	}
	if other.WorkspaceID == s.WorkspaceID {
		t.Fatal("two chats share a folder")
	}
	if _, err = a.UpdateMasterSession(scoped, MasterSessionUpdate{Action: "delete", ID: s.Active}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(file); err != nil {
		t.Fatal("deleting conversation removed its files")
	}
	dir, err := a.MasterChatDirectory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, group := range dir.Worlds {
		if group.ScopeKind == "point_chat" {
			found = true
			for _, chat := range group.Chats {
				if chat.ID == s.Active {
					t.Fatal("deleted chat remains in directory")
				}
			}
		}
	}
	if !found {
		t.Fatal("POINT directory group missing")
	}
	if a.currentWorldID() != initial.WorkspaceID {
		t.Fatal("POINT chat changed the open project")
	}
}
