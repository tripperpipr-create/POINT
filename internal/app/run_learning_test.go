package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestRunLearningEvidenceCapturesFailureFeedbackAndExactSkillVersion(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Backend", RoleDescription: "Backend engineer", Provider: domain.ProviderOllama, BaseURL: "http://127.0.0.1:11434",
		PrimaryModel: "qwen2.5-coder:7b", AllowedTools: []string{"read_file", "propose_patch", "run_command"},
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	finished := started.Add(4 * time.Second)
	profile := domain.AgentProfile{
		ID: agent.ID, Provider: agent.Provider, Model: agent.PrimaryModel,
		AllowedTools: append([]string(nil), agent.AllowedTools...),
		EquippedSkills: []domain.SkillRuntime{{
			ID: "skill-api", Name: "API workflow", Instructions: "Inspect, edit, verify.",
			RequiredTools: []string{"read_file", "run_command"},
			Configuration: map[string]any{"revision": 3, "promotionStatus": "candidate"},
		}},
	}
	run := domain.Run{
		ID: "run-learning-failed", AgentID: agent.ID, ProfileID: agent.ID, WorkspaceID: view.Workspace.ID,
		Task: "Fix the implementation and verify it", Status: domain.RunFailed,
		Error: "provider failed with sk-123456789012345678901234", ChangedFiles: []string{"main.go"},
		StartedAt: started, FinishedAt: &finished, DurationMs: 4000,
		ConfigurationSnapshot: domain.NewRunConfigurationSnapshot(Version, profile, nil, started),
	}
	ctx := context.Background()
	if err = application.store.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	appendEvent := func(id string, kind domain.EventType, step int, payload any) {
		t.Helper()
		data, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if appendErr := application.store.Append(ctx, domain.Event{
			ID: id, RunID: run.ID, AgentID: agent.ID, Type: kind, Step: step,
			Actor: "agent", Data: data, CreatedAt: started.Add(time.Duration(step) * time.Second),
		}); appendErr != nil {
			t.Fatal(appendErr)
		}
	}
	appendEvent("requested", domain.EventToolRequested, 1, map[string]any{"tool": "read_file"})
	appendEvent("failed", domain.EventToolFinished, 2, map[string]any{
		"tool": "read_file", "result": domain.ToolResult{OK: false, Error: &domain.ToolError{Code: "read_failed", Message: "not found"}},
	})
	appendEvent("feedback", domain.EventRunMessageInjected, 3, map[string]any{"content": "First inspect the route; token sk-123456789012345678901234"})
	appendEvent("revision", domain.EventCompletionChecked, 4, map[string]any{"status": "revision_required"})
	if err = application.store.SaveApproval(ctx, domain.Approval{
		ID: "approval-denied", RunID: run.ID, AgentID: agent.ID, ToolName: "run_command",
		Status: domain.ApprovalDenied, CreatedAt: started.Add(time.Second), ResolvedAt: &finished,
	}); err != nil {
		t.Fatal(err)
	}

	if err = application.recordRunLearningEvidence(ctx, run, agent.ID); err != nil {
		t.Fatal(err)
	}
	// Recording is idempotent for callbacks retried during shutdown/recovery.
	if err = application.recordRunLearningEvidence(ctx, run, agent.ID); err != nil {
		t.Fatal(err)
	}
	signals, err := application.store.ListLearningSignalsForRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := map[domain.LearningSignalKind]bool{
		domain.LearningSignalRunFailure: true, domain.LearningSignalUserFeedback: true,
		domain.LearningSignalToolFailure: true, domain.LearningSignalApprovalDenied: true,
		domain.LearningSignalVerificationGap: true, domain.LearningSignalCompletionRevised: true,
	}
	if len(signals) != len(want) {
		t.Fatalf("signals=%#v", signals)
	}
	for _, signal := range signals {
		if !want[signal.Kind] || signal.Status != "observed" || len(signal.SkillAttributions) != 1 {
			t.Fatalf("signal=%#v", signal)
		}
		if strings.Contains(strings.Join(signal.Evidence, " "), "sk-123456") {
			t.Fatalf("signal leaked secret: %#v", signal.Evidence)
		}
	}
	outcomes, err := application.store.ListSkillOutcomesForRun(ctx, run.ID)
	if err != nil || len(outcomes) != 1 {
		t.Fatalf("outcomes=%#v err=%v", outcomes, err)
	}
	outcome := outcomes[0]
	if outcome.SkillID != "skill-api" || outcome.SkillRevision != 3 || len(outcome.SkillDigest) != 64 || outcome.RunStatus != domain.RunFailed || outcome.Health != "failed" || outcome.ToolFailures != 1 || outcome.FeedbackCount != 1 || outcome.ApprovalDenied != 1 || outcome.CompletionRevisions != 1 || !outcome.VerificationRequired || outcome.VerificationRecorded {
		t.Fatalf("outcome=%#v", outcome)
	}
}

func TestRunLearningEvidenceMarksOnlyEligibleVerifiedSuccess(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "QA", Provider: domain.ProviderOllama, BaseURL: "http://127.0.0.1:11434", PrimaryModel: "qwen2.5-coder:7b",
		AllowedTools: []string{"project_map", "list_files", "search_code", "read_file", "search_text"},
	})
	if err != nil {
		t.Fatal(err)
	}
	run := saveLearningRun(t, application, view.Workspace.ID, agent, "run-learning-signal", time.Now().UTC())
	if err = application.recordRunLearningEvidence(context.Background(), run, agent.ID); err != nil {
		t.Fatal(err)
	}
	signals, err := application.store.ListLearningSignalsForRun(context.Background(), run.ID)
	if err != nil || len(signals) != 1 || signals[0].Kind != domain.LearningSignalVerifiedSuccess {
		t.Fatalf("signals=%#v err=%v", signals, err)
	}
}
