package app

import (
	"context"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPointChatSurvivesCoreRestart(t *testing.T) {
	a, backend, s := hostFastFixture(t, false)
	ctx := context.Background()
	a.engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return &hostFastModel{}, nil })
	run, err := a.StartFastAgent(FastAgentRequest{WorkspaceID: s.WorkspaceID, ConversationID: s.Active, RequestID: "persistent-task", Task: "Create result.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !a.engine.WaitFinalized(run.ID, 10*time.Second) {
		t.Fatal("worker did not finish")
	}
	a.Shutdown(ctx)
	restarted, err := New(a.dataDir, WithSandboxBackend(backend))
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Shutdown(ctx)
	history, err := restarted.MasterHistory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if history.Sessions.Active != s.Active || history.Sessions.WorkspaceID != s.WorkspaceID || len(history.History) != 2 {
		t.Fatalf("persistent chat=%+v history=%+v", history.Sessions, history.History)
	}
	if history.FastRun == nil || history.FastRun.ID != run.ID || history.FastRun.ScopeKind != "point_chat" {
		t.Fatal("run scope did not survive restart")
	}
	if len(history.WorkOrders) != 0 {
		t.Fatal("host task was presented as a full Point launch card")
	}
	if len(history.History[1].Content) == 0 {
		t.Fatal("execution summary missing")
	}
	file := filepath.Join(history.Sessions.WorkspacePath, "result.txt")
	if _, err = os.Stat(file); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.StartFastAgent(FastAgentRequest{WorkspaceID: s.WorkspaceID, ConversationID: s.Active, RequestID: "persistent-task", Task: "Create result.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.UpdateMasterSession(ctx, MasterSessionUpdate{WorkspaceID: s.WorkspaceID, Action: "delete", ID: s.Active}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(file); err != nil {
		t.Fatal("deleting chat undid a host file")
	}
}

func TestPinnedMasterScopeSurvivesProjectSwitch(t *testing.T) {
	a, _, s := hostFastFixture(t, true)
	ctx, err := a.WithMasterWorkspace(context.Background(), s.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	original := a.masterScopedFS(ctx).Root()
	openTestWorld(t, a)
	pinned, err := a.WithMasterWorkspace(ctx, s.WorkspaceID)
	if err != nil || a.masterScopedFS(pinned).Root() != original {
		t.Fatal("switch changed the submitted target", err)
	}
	if _, err = a.WithMasterWorkspace(context.Background(), s.WorkspaceID); err == nil {
		t.Fatal("an unbound request accepted a foreign project")
	}
}

func TestHostFastAgentHonorsExplicitAsk(t *testing.T) {
	a, _, s := hostFastFixture(t, false)
	ctx := context.Background()
	config, err := a.FastAgentConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	config.Profile.ToolPolicies = map[string]string{"propose_patch": "ASK"}
	if _, err = a.SaveFastAgentConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	a.engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return &hostFastModel{}, nil })
	run, err := a.StartFastAgent(FastAgentRequest{WorkspaceID: s.WorkspaceID, Task: "Create a file"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		approvals, err := a.store.ApprovalsByRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, approval := range approvals {
			if approval.Status == domain.ApprovalPending {
				found = true
				if err = a.ResolveApproval(approval.ID, false); err != nil {
					t.Fatal(err)
				}
			}
		}
		if found {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !found {
		t.Fatal("explicit ASK was auto-approved")
	}
	_ = a.CancelRun(run.ID)
	a.engine.WaitFinalized(run.ID, 5*time.Second)
	w, _ := a.store.WorkspaceByID(ctx, s.WorkspaceID)
	if _, err = os.Stat(filepath.Join(w.Path, "result.txt")); !os.IsNotExist(err) {
		t.Fatal("denied approval wrote a file")
	}
}
