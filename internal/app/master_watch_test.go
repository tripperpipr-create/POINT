package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestEnsureAndResolveEgressAsk(t *testing.T) {
	dir := t.TempDir()
	application, err := New(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })

	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wsID := view.Workspace.ID
	ctx := context.Background()
	ask, err := application.EnsureEgressAsk(ctx, wsID, "quest-1", "run-1", domain.EgressAskNetworkHost, "repo.packagist.org", "composer needs packagist")
	if err != nil {
		t.Fatal(err)
	}
	if ask.Status != domain.EgressAskPending {
		t.Fatalf("status=%s", ask.Status)
	}
	again, err := application.EnsureEgressAsk(ctx, wsID, "quest-1", "run-1", domain.EgressAskNetworkHost, "repo.packagist.org", "retry")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != ask.ID {
		t.Fatalf("expected dedupe %s vs %s", ask.ID, again.ID)
	}

	resolved, err := application.ResolveEgressAsk(ctx, ask.ID, ResolveEgressAskRequest{Action: "allow_once"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != domain.EgressAskAllowedOnce {
		t.Fatalf("status=%s", resolved.Status)
	}
	hosts := application.networkGrants.HostsFor("run-1", "quest-1")
	if len(hosts) == 0 || hosts[0] != "repo.packagist.org" {
		t.Fatalf("grants=%v", hosts)
	}

	queue, err := application.Decisions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range queue.Items {
		if item.ID == ask.ID {
			t.Fatal("resolved ask must leave decisions queue")
		}
	}
}

func TestGitRemoteEgressAskInDecisions(t *testing.T) {
	dir := t.TempDir()
	application, err := New(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ask, err := application.EnsureEgressAsk(ctx, view.Workspace.ID, "", "run-9", domain.EgressAskGitRemote, "https://github.com/evil/x.git", "unconfirmed")
	if err != nil {
		t.Fatal(err)
	}
	queue, err := application.Decisions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range queue.Items {
		if item.ID == ask.ID && item.Kind == DecisionEgress {
			found = true
			if item.Resolve.Path == "" || item.Resolve.Accept != "allow_quest" {
				t.Fatalf("resolve=%#v", item.Resolve)
			}
		}
	}
	if !found {
		t.Fatal("expected git egress decision")
	}
}

func TestMasterWatchIntervenesOnDuplicatePlan(t *testing.T) {
	dir := t.TempDir()
	application, err := New(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	wsID := view.Workspace.ID
	run := domain.Run{
		ID: domain.NewID("run"), WorkspaceID: wsID, AgentID: "agent", Status: domain.RunRunning,
		Task: "test", StartedAt: time.Now().UTC(),
	}
	if err = application.store.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveRunCheckpoint(ctx, domain.RunCheckpoint{
		RunID: run.ID, Seq: 1, CreatedAt: time.Now().UTC(), IdenticalToolPlans: 2, NextStep: 3, Step: 2,
	}); err != nil {
		t.Fatal(err)
	}
	application.masterWatchMu.Lock()
	delete(application.masterWatchIntervened, run.ID)
	application.masterWatchMu.Unlock()
	application.interveneDuplicatePlan(ctx, run, 2)

	asks, err := application.store.ListPendingEgressAsks(ctx, wsID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ask := range asks {
		if ask.Kind == domain.EgressAskSupervision && ask.RunID == run.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected supervision ask, got %#v", asks)
	}
}

func TestRepeatDeniedEgressContinuesOffline(t *testing.T) {
	dir := t.TempDir()
	application, err := New(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	quest := domain.Quest{
		ID: "quest-deny", WorkspaceID: view.Workspace.ID, Title: "Ship", Status: domain.QuestActive,
		CreatedAt: now, UpdatedAt: now, Controller: map[string]any{},
	}
	recordDeniedEgress(&quest, "evil.example")
	if err = application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	ask, err := application.EnsureEgressAsk(ctx, view.Workspace.ID, quest.ID, "run-deny", domain.EgressAskNetworkHost, "evil.example", "retry")
	if err != nil {
		t.Fatal(err)
	}
	if ask.Status != domain.EgressAskDenied {
		t.Fatalf("status=%s", ask.Status)
	}
	pending, err := application.store.ListPendingEgressAsks(ctx, view.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range pending {
		if item.Target == "evil.example" {
			t.Fatal("repeat deny must not enqueue another pending ask")
		}
	}
}

func TestWatchShouldSkipStallDuringCommandAndUserWait(t *testing.T) {
	if !watchShouldSkipStall(domain.RunCheckpoint{InFlightCallID: "call-1"}, domain.Run{Status: domain.RunRunning}) {
		t.Fatal("in-flight tool must not count as stall")
	}
	if !watchShouldSkipStall(domain.RunCheckpoint{LastToolPlan: `{"name":"run_command"}`}, domain.Run{Status: domain.RunRunning}) {
		t.Fatal("run_command heartbeat must not count as stall")
	}
	if !watchShouldSkipStall(domain.RunCheckpoint{}, domain.Run{Status: domain.RunWaiting}) {
		t.Fatal("waiting for user must not count as stall")
	}
	if watchShouldSkipStall(domain.RunCheckpoint{IdenticalToolPlans: 2}, domain.Run{Status: domain.RunRunning}) {
		t.Fatal("idle duplicate plans should still be inspectable")
	}
	stale := domain.RunCheckpoint{
		InFlightCallID: "call-old", HeartbeatAt: time.Now().UTC().Add(-20 * time.Minute),
	}
	if watchShouldSkipStall(stale, domain.Run{Status: domain.RunRunning}) {
		t.Fatal("hung in-flight tool past operation timeout should be inspectable")
	}
}
