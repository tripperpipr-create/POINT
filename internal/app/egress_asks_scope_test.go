package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestQuestEgressGrantPersistsAndOnceDoesNotSurviveRestart(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(root, "data")
	application, err := New(data)
	if err != nil {
		t.Fatal(err)
	}
	view, err := application.OpenWorkspace(project)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModePrecise, Goal: "Read dependency", ResultKind: "code",
		Criteria: []domain.AcceptanceCriterion{{ID: "c", Kind: "manual", Text: "Dependency read"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	quest := domain.Quest{ID: "quest-network", WorkspaceID: view.Workspace.ID, Title: "Network", Status: domain.QuestActive, Brief: &brief, CreatedAt: now, UpdatedAt: now}
	if err := application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	once, err := application.EnsureEgressAsk(ctx, view.Workspace.ID, quest.ID, "run-once", domain.EgressAskNetworkHost, "once.example.test:8443", "one command")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.ResolveEgressAsk(ctx, once.ID, ResolveEgressAskRequest{Action: "allow_once"}); err != nil {
		t.Fatal(err)
	}
	lasting, err := application.EnsureEgressAsk(ctx, view.Workspace.ID, quest.ID, "run-quest", domain.EgressAskNetworkHost, "quest.example.test:443", "quest dependency")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.ResolveEgressAsk(ctx, lasting.ID, ResolveEgressAskRequest{Action: "allow_quest"}); err != nil {
		t.Fatal(err)
	}
	quests, err := application.store.ListQuests(ctx, view.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	var saved *domain.TaskBrief
	for _, item := range quests {
		if item.ID == quest.ID {
			saved = item.Brief
		}
	}
	if saved == nil || saved.Version != brief.Version+1 || !domain.IsTaskBriefApproved(*saved) || len(saved.Permissions.NetworkHosts) != 1 || saved.Permissions.NetworkHosts[0] != "quest.example.test" {
		t.Fatalf("quest grant was not versioned: %#v", saved)
	}
	application.Shutdown(ctx)
	application, err = New(data)
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(ctx)
	if _, err := application.OpenWorkspace(project); err != nil {
		t.Fatal(err)
	}
	if application.networkGrants.TakeHostOnce("run-once", "once.example.test:8443") || len(application.networkGrants.QuestHostsFor(quest.ID)) != 1 {
		t.Fatal("restart restored a one-time grant or lost the quest grant")
	}
}

func TestNetworkAskUsesStructuredValidatedTarget(t *testing.T) {
	application, err := New(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := application.EnsureEgressAsk(ctx, view.Workspace.ID, "q", "r", domain.EgressAskNetworkHost, "127.0.0.1", "bad"); err == nil {
		t.Fatal("IP literal became an approvable target")
	}
	for _, target := range []string{"", "safe.example.test:8443"} {
		payload, _ := json.Marshal(map[string]any{"result": map[string]any{"error": map[string]any{
			"code": "network_denied", "message": `outbound network access to "evil.example.test" is denied`, "target": target,
		}}})
		application.handleToolFinishedEgress(domain.Event{WorkspaceID: view.Workspace.ID, RunID: "r", QuestID: "q", Data: payload})
	}
	asks, err := application.store.ListPendingEgressAsks(ctx, view.Workspace.ID)
	if err != nil || len(asks) != 1 || asks[0].Target != "safe.example.test:8443" {
		t.Fatalf("structured target handling: asks=%#v error=%v", asks, err)
	}
}

func TestQuestEgressGrantRequiresApprovedBrief(t *testing.T) {
	ctx := context.Background()
	application, err := New(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(ctx)
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	draft := domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModePrecise, Goal: "Read dependency", ResultKind: "code",
		Criteria: []domain.AcceptanceCriterion{{ID: "c", Kind: "manual", Text: "Dependency read"}},
	})
	now := time.Now().UTC()
	quest := domain.Quest{ID: "draft-network", WorkspaceID: view.Workspace.ID, Title: "Draft", Status: domain.QuestActive, Brief: &draft, CreatedAt: now, UpdatedAt: now}
	if err := application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	ask, err := application.EnsureEgressAsk(ctx, view.Workspace.ID, quest.ID, "run-draft", domain.EgressAskNetworkHost, "registry.example.test", "dependency")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.ResolveEgressAsk(ctx, ask.ID, ResolveEgressAskRequest{Action: "allow_quest"}); err == nil {
		t.Fatal("draft brief accepted a quest-wide network grant")
	}
	if hosts := application.networkGrants.QuestHostsFor(quest.ID); len(hosts) != 0 {
		t.Fatalf("draft grant reached the runtime: %v", hosts)
	}
	saved, err := application.store.GetEgressAsk(ctx, ask.ID)
	if err != nil || saved.Status != domain.EgressAskPending {
		t.Fatalf("draft grant did not remain pending: ask=%#v error=%v", saved, err)
	}
}

func TestInvalidStoredNetworkAskCanBeDenied(t *testing.T) {
	ctx := context.Background()
	application, err := New(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(ctx)
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ask := domain.EgressAsk{
		ID: domain.NewID("egress"), WorkspaceID: view.Workspace.ID,
		Kind: domain.EgressAskNetworkHost, Target: "127.0.0.1", Status: domain.EgressAskPending,
		CreatedAt: time.Now().UTC(),
	}
	if err := application.store.SaveEgressAsk(ctx, ask); err != nil {
		t.Fatal(err)
	}
	if _, err := application.ResolveEgressAsk(ctx, ask.ID, ResolveEgressAskRequest{Action: "allow_quest"}); err == nil {
		t.Fatal("invalid stored address was approved")
	}
	denied, err := application.ResolveEgressAsk(ctx, ask.ID, ResolveEgressAskRequest{Action: "deny"})
	if err != nil || denied.Status != domain.EgressAskDenied {
		t.Fatalf("invalid stored address could not be denied: ask=%#v error=%v", denied, err)
	}
}
