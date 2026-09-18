package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

type fakeGitRunner struct {
	branch string
}

func (f *fakeGitRunner) Run(_ context.Context, dir string, arguments ...string) ([]byte, error) {
	if len(arguments) == 0 {
		return nil, nil
	}
	switch arguments[0] {
	case "clone":
		project := filepath.Join(dir, arguments[len(arguments)-1])
		_ = os.MkdirAll(project, 0o700)
		_ = os.WriteFile(filepath.Join(project, "composer.json"), []byte(`{"name":"example/php-composer-app","require":{"php":">=8.3"}}`), 0o600)
		_ = os.WriteFile(filepath.Join(project, "README.md"), []byte("# Backend test task\n\nImplement tax and coupon endpoints.\n"), 0o600)
		_ = os.MkdirAll(filepath.Join(project, ".git"), 0o700)
		return []byte("cloned\n"), nil
	case "rev-parse":
		return []byte("abc123\n"), nil
	case "init", "switch":
		if len(arguments) >= 3 {
			f.branch = arguments[len(arguments)-1]
		}
		return []byte(""), nil
	case "branch":
		return []byte(f.branch + "\n"), nil
	default:
		return []byte(""), nil
	}
}

func TestCreateIntakeSnapshotsGitSourceAndEnvironment(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir(), WithGitRunner(&fakeGitRunner{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })

	session, err := application.CreateIntake(context.Background(), CreateIntakeRequest{
		URL: "https://github.com/systemeio/backend-test-task",
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Status != domain.IntakeAwaitingApproval {
		t.Fatalf("status=%s error=%s blockers=%v", session.Status, session.Error, session.Blockers)
	}
	if session.Source.Kind != "git" || session.Source.Digest == "" {
		t.Fatalf("source not snapshotted: %#v", session.Source)
	}
	if session.Environment.Runtime.Toolchains["php"] == "" {
		t.Fatalf("expected php toolchain: %#v", session.Environment.Runtime)
	}
	if session.Environment.Runtime.Image == "" {
		t.Fatal("expected runtime image from managed pack")
	}
	if !session.Coverage.Ready {
		t.Fatalf("coverage should be ready for catalog tools: %#v", session.Coverage)
	}
	if session.Brief == nil || session.Brief.Goal == "" {
		t.Fatal("expected draft brief")
	}
	if !strings.HasPrefix(session.Delivery.Branch, "point/") {
		t.Fatalf("branch=%q", session.Delivery.Branch)
	}
}

func TestRestrictProfileToLeaseDropsExtraTools(t *testing.T) {
	profile := domain.AgentProfile{AllowedTools: []string{"read_file", "propose_patch", "run_command", "db_exec"}}
	lease := domain.QuestToolLease{ToolNames: []string{"read_file", "run_command"}}
	restricted := restrictProfileToLease(profile, lease)
	raw, _ := json.Marshal(restricted.AllowedTools)
	if string(raw) != `["read_file","run_command"]` {
		t.Fatalf("tools=%s", raw)
	}
}

func TestFinalizeIntakeWritesEvidenceBundle(t *testing.T) {
	application, world := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	brief := domain.NormalizeTaskBrief(domain.TaskBrief{
		Version: 1, State: "ready", Mode: domain.TaskModeProject, Goal: "Ship endpoints", ResultKind: "workspace_change",
		Criteria: []domain.AcceptanceCriterion{
			{ID: "manual-result", Kind: "manual", Text: "User reviews the result"},
		},
		Permissions: domain.TaskPermissions{WriteFiles: true, ExecuteCommands: true},
		Budget:      domain.TaskBudget{Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxAttempts: 1},
	})
	quest := domain.Quest{
		ID: "quest-intake-1", WorkspaceID: world.ID, Title: brief.Goal, Status: domain.QuestCompleted,
		Brief: &brief, CreatedAt: now, UpdatedAt: now,
	}
	if err := application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	source := domain.SourceBundle{
		ID: "sourcebundle-1", PrimaryURL: "https://example.com/task", Kind: "git", Digest: "digest-source",
		Artifacts: []domain.SourceArtifact{{ID: "source-1", Kind: "git", CanonicalURL: "https://example.com/task"}},
		CreatedAt: now,
	}
	if err := application.store.SaveSourceBundle(ctx, source); err != nil {
		t.Fatal(err)
	}
	session := domain.IntakeSession{
		ID: "intake-1", WorkspaceID: world.ID, QuestID: quest.ID, URL: source.PrimaryURL,
		Status: domain.IntakeExecuting, Source: source, Brief: &brief,
		Environment: domain.EnvironmentPlan{ID: "env-1", Digest: "digest-env", Commands: []domain.EnvironmentCommand{
			{ID: "tests", Program: "php", Arguments: []string{"bin/phpunit"}},
		}},
		Delivery:  domain.DeliveryTarget{Kind: "local_branch", Branch: "point/task"},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := application.store.SaveIntakeSession(ctx, session); err != nil {
		t.Fatal(err)
	}

	application.finalizeIntakeAfterQuest(quest.ID, true)

	got, err := application.store.GetIntakeSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.IntakeNeedsReview {
		t.Fatalf("status=%s error=%s", got.Status, got.Error)
	}
	if got.Evidence == nil || got.Evidence.SourceDigest != source.Digest {
		t.Fatalf("evidence missing: %#v", got.Evidence)
	}
	if len(got.Evidence.ReproductionCommands) == 0 {
		t.Fatal("expected reproduction commands")
	}
}

func TestFinalizeIntakeUnverifiedManualNeedsReview(t *testing.T) {
	application, world := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	brief := domain.NormalizeTaskBrief(domain.TaskBrief{
		Version: 1, State: "ready", Mode: domain.TaskModeProject, Goal: "Ship", ResultKind: "workspace_change",
		Criteria:    []domain.AcceptanceCriterion{{ID: "m1", Kind: "manual", Text: "Review"}},
		Permissions: domain.TaskPermissions{WriteFiles: true, ExecuteCommands: true},
		Budget:      domain.TaskBudget{Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxAttempts: 1},
	})
	quest := domain.Quest{
		ID: "quest-intake-fail-manual", WorkspaceID: world.ID, Title: brief.Goal, Status: domain.QuestFailed,
		Brief: &brief, CreatedAt: now, UpdatedAt: now,
	}
	if err := application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	source := domain.SourceBundle{
		ID: "sb-fail-manual", PrimaryURL: "https://example.com/task", Kind: "git", Digest: "digest-fail",
		Artifacts: []domain.SourceArtifact{{ID: "s1", Kind: "git", CanonicalURL: "https://example.com/task"}},
		CreatedAt: now,
	}
	if err := application.store.SaveSourceBundle(ctx, source); err != nil {
		t.Fatal(err)
	}
	session := domain.IntakeSession{
		ID: "intake-fail-manual", WorkspaceID: world.ID, QuestID: quest.ID, URL: source.PrimaryURL,
		Status: domain.IntakeExecuting, Source: source, Brief: &brief,
		Environment: domain.EnvironmentPlan{ID: "env-1", Digest: "digest-env"},
		Delivery:    domain.DeliveryTarget{Kind: "local_branch", Branch: "point/task"},
		CreatedAt:   now, UpdatedAt: now,
	}
	if err := application.store.SaveIntakeSession(ctx, session); err != nil {
		t.Fatal(err)
	}

	application.finalizeIntakeAfterQuest(quest.ID, false)

	got, err := application.store.GetIntakeSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.IntakeNeedsReview {
		t.Fatalf("status=%s error=%s", got.Status, got.Error)
	}
}

func TestFinalizeIntakeFailedVerificationBlocked(t *testing.T) {
	application, world := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	exit := 0
	brief := domain.NormalizeTaskBrief(domain.TaskBrief{
		Version: 1, State: "ready", Mode: domain.TaskModeProject, Goal: "Ship", ResultKind: "workspace_change",
		Criteria: []domain.AcceptanceCriterion{
			{ID: "verify-1", Kind: "verification", Text: "phpunit", Tool: "run_command",
				Arguments: []byte(`{"command":"php bin/phpunit"}`), ExpectedExitCode: &exit},
		},
		Permissions: domain.TaskPermissions{WriteFiles: true, ExecuteCommands: true},
		Budget:      domain.TaskBudget{Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxAttempts: 1},
	})
	quest := domain.Quest{
		ID: "quest-intake-fail-verify", WorkspaceID: world.ID, Title: brief.Goal, Status: domain.QuestFailed,
		Brief: &brief, CreatedAt: now, UpdatedAt: now,
	}
	if err := application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	source := domain.SourceBundle{
		ID: "sb-fail-verify", PrimaryURL: "https://example.com/task", Kind: "git", Digest: "digest-v",
		Artifacts: []domain.SourceArtifact{{ID: "s1", Kind: "git", CanonicalURL: "https://example.com/task"}},
		CreatedAt: now,
	}
	if err := application.store.SaveSourceBundle(ctx, source); err != nil {
		t.Fatal(err)
	}
	session := domain.IntakeSession{
		ID: "intake-fail-verify", WorkspaceID: world.ID, QuestID: quest.ID, URL: source.PrimaryURL,
		Status: domain.IntakeExecuting, Source: source, Brief: &brief,
		Environment: domain.EnvironmentPlan{ID: "env-1", Digest: "digest-env"},
		Delivery:    domain.DeliveryTarget{Kind: "local_branch", Branch: "point/task"},
		CreatedAt:   now, UpdatedAt: now,
	}
	if err := application.store.SaveIntakeSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	application.finalizeIntakeAfterQuest(quest.ID, false)
	got, err := application.store.GetIntakeSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.IntakeBlocked {
		t.Fatalf("status=%s error=%s", got.Status, got.Error)
	}
}

func TestEvidenceBundleIncludesPolicyDecisions(t *testing.T) {
	application, world := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	brief := domain.NormalizeTaskBrief(domain.TaskBrief{
		Version: 1, State: "ready", Mode: domain.TaskModeProject, Goal: "Ship", ResultKind: "workspace_change",
		Criteria:    []domain.AcceptanceCriterion{{ID: "m1", Kind: "manual", Text: "Review"}},
		Permissions: domain.TaskPermissions{WriteFiles: true, ExecuteCommands: true, NetworkHosts: []string{"repo.packagist.org"}},
		Budget:      domain.TaskBudget{Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxAttempts: 1},
	})
	quest := domain.Quest{
		ID: "quest-policy-ev", WorkspaceID: world.ID, Title: brief.Goal, Status: domain.QuestCompleted,
		Brief: &brief, CreatedAt: now, UpdatedAt: now,
	}
	if err := application.store.SaveQuest(ctx, quest); err != nil {
		t.Fatal(err)
	}
	source := domain.SourceBundle{
		ID: "sb-policy-ev", PrimaryURL: "https://example.com/task", Kind: "git", Digest: "digest-policy",
		Artifacts: []domain.SourceArtifact{{ID: "s1", Kind: "git", CanonicalURL: "https://example.com/task"}},
		CreatedAt: now,
	}
	if err := application.store.SaveSourceBundle(ctx, source); err != nil {
		t.Fatal(err)
	}
	session := domain.IntakeSession{
		ID: "intake-policy-ev", WorkspaceID: world.ID, QuestID: quest.ID, URL: source.PrimaryURL,
		Status: domain.IntakeExecuting, Source: source, Brief: &brief,
		Environment: domain.EnvironmentPlan{ID: "env-1", Digest: "digest-env", NetworkHosts: []string{"repo.packagist.org"}},
		Delivery:    domain.DeliveryTarget{Kind: "local_branch", Branch: "point/task"},
		CreatedAt:   now, UpdatedAt: now,
	}
	if err := application.store.SaveIntakeSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := application.store.SaveEgressAsk(ctx, domain.EgressAsk{
		ID: "egress-1", WorkspaceID: world.ID, QuestID: quest.ID, Kind: domain.EgressAskNetworkHost,
		Target: "repo.packagist.org", Reason: "composer", Status: domain.EgressAskAllowedQuest, CreatedAt: now,
		ResolvedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := application.store.SaveEgressAsk(ctx, domain.EgressAsk{
		ID: "sup-1", WorkspaceID: world.ID, QuestID: quest.ID, RunID: "run-1", Kind: domain.EgressAskSupervision,
		Target: "duplicate_tool_plan:run-1", Reason: "repeated plan", Status: domain.EgressAskAllowedOnce, CreatedAt: now,
		ResolvedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	application.finalizeIntakeAfterQuest(quest.ID, true)
	got, err := application.store.GetIntakeSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Evidence == nil {
		t.Fatal("evidence missing")
	}
	if len(got.Evidence.NetworkDecisions) != 1 || got.Evidence.NetworkDecisions[0].Target != "repo.packagist.org" {
		t.Fatalf("network decisions=%#v", got.Evidence.NetworkDecisions)
	}
	if len(got.Evidence.SupervisionInterventions) != 1 || got.Evidence.SupervisionInterventions[0].Kind != string(domain.EgressAskSupervision) {
		t.Fatalf("supervision=%#v", got.Evidence.SupervisionInterventions)
	}
}

func TestExpandIntakeRequiresReapproval(t *testing.T) {
	application, world := outcomeWorld(t)
	ctx := context.Background()
	now := time.Now().UTC()
	brief := domain.NormalizeTaskBrief(domain.TaskBrief{
		Version: 1, State: "ready", Mode: domain.TaskModeProject, Goal: "Ship", ResultKind: "workspace_change",
		Criteria:    []domain.AcceptanceCriterion{{ID: "m1", Kind: "manual", Text: "Review"}},
		Permissions: domain.TaskPermissions{NetworkHosts: []string{"repo.packagist.org"}},
		Budget:      domain.TaskBudget{Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxAttempts: 1},
	})
	source := domain.SourceBundle{ID: "sb-expand", PrimaryURL: "https://example.com", Kind: "text", Digest: "d1", Artifacts: []domain.SourceArtifact{{ID: "s1", Kind: "text", CanonicalURL: "https://example.com"}}, CreatedAt: now}
	if err := application.store.SaveSourceBundle(ctx, source); err != nil {
		t.Fatal(err)
	}
	session := domain.IntakeSession{
		ID: "intake-expand", WorkspaceID: world.ID, URL: source.PrimaryURL, Status: domain.IntakeExecuting,
		Source: source, Brief: &brief, Environment: domain.EnvironmentPlan{NetworkHosts: []string{"repo.packagist.org"}},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := application.store.SaveIntakeSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	got, err := application.ExpandIntake(ctx, session.ID, ExpandIntakeRequest{
		ExpectedVersion: 1, NetworkHosts: []string{"example.com"}, Tokens: 5000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.IntakeAwaitingApproval {
		t.Fatalf("status=%s", got.Status)
	}
	if got.Brief.Version != 2 || got.Brief.ApprovedVersion != 0 {
		t.Fatalf("brief=%#v", got.Brief)
	}
	found := false
	for _, host := range got.Brief.Permissions.NetworkHosts {
		if host == "example.com" {
			found = true
		}
	}
	if !found {
		t.Fatalf("hosts=%v", got.Brief.Permissions.NetworkHosts)
	}
}
