package app

import (
	"context"
	"encoding/json"
	"testing"

	"local-agent-workbench/internal/domain"
)

func TestStageAllowsLLMBypassOnlyForInheritedSerialStages(t *testing.T) {
	if !stageAllowsLLMBypass(domain.StageRoleIntegrate, "inherited") {
		t.Fatal("serial integrate should bypass LLM")
	}
	if !stageAllowsLLMBypass(domain.StageRoleImplReview, "inherited") {
		t.Fatal("serial impl_review should bypass LLM")
	}
	if stageAllowsLLMBypass(domain.StageRoleIntegrate, "merged_parallel_join") {
		t.Fatal("parallel merge integrate must keep LLM/merge agent")
	}
	if stageAllowsLLMBypass(domain.StageRoleImplement, "inherited") {
		t.Fatal("implement must never bypass LLM")
	}
	if stageAllowsLLMBypass(domain.StageRoleAccept, "inherited") {
		t.Fatal("accept verification must never bypass LLM via inheritance shortcut")
	}
}

func TestCriteriaSupportDeterministicAccept(t *testing.T) {
	exit := 0
	ok := &domain.TaskBrief{Criteria: []domain.AcceptanceCriterion{{
		ID: "v1", Kind: "verification", Tool: "run_command",
		Arguments: json.RawMessage(`{"command":"php bin/phpunit"}`), ExpectedExitCode: &exit,
	}}}
	if !criteriaSupportDeterministicAccept(ok) {
		t.Fatal("declared run_command criteria should be deterministic")
	}
	manual := &domain.TaskBrief{Criteria: []domain.AcceptanceCriterion{{ID: "m1", Kind: "manual", Text: "looks good"}}}
	if criteriaSupportDeterministicAccept(manual) {
		t.Fatal("manual criteria must keep LLM accept")
	}
}

func TestContinueAfterDeterministicStageTerminalStatuses(t *testing.T) {
	failed := domain.FlowRun{ID: "fr-fail", Status: domain.RunFailed}
	completed := domain.FlowRun{ID: "fr-ok", Status: domain.RunCompleted}
	// No quest id → finalize is a no-op; ensures the switch does not panic.
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	if err = application.continueAfterDeterministicStage(failed); err != nil {
		t.Fatal(err)
	}
	if err = application.continueAfterDeterministicStage(completed); err != nil {
		t.Fatal(err)
	}
}

func TestApplyStageExecutionBudgetPreservesImplementCeiling(t *testing.T) {
	profile := domain.DefaultProfile()
	profile.MaxOutputTokens = 65536
	profile.MaxSteps = 100
	profile.ReasoningEffort = "medium"
	applyStageExecutionBudget(&profile, domain.StageRoleImplement)
	if profile.MaxOutputTokens != 16384 {
		t.Fatalf("implement output cap=%d", profile.MaxOutputTokens)
	}
	if profile.MaxSteps != 100 {
		t.Fatalf("implement max steps must stay %d, got %d", 100, profile.MaxSteps)
	}
	if profile.ReasoningEffort != "low" {
		t.Fatalf("reasoning=%q", profile.ReasoningEffort)
	}

	review := domain.DefaultProfile()
	review.MaxOutputTokens = 65536
	review.MaxSteps = 100
	applyStageExecutionBudget(&review, domain.StageRoleImplReview)
	if review.MaxSteps != 16 || review.MaxOutputTokens != 4096 {
		t.Fatalf("review budget steps=%d output=%d", review.MaxSteps, review.MaxOutputTokens)
	}
}
