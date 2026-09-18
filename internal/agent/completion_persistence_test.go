package agent

import (
	"context"
	"errors"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
	"strings"
	"testing"
	"time"
)

type failedEvidenceRepo struct{ *memoryRepo }

func (r *failedEvidenceRepo) Append(ctx context.Context, event domain.Event) error {
	if event.Type == domain.EventCompletionChecked {
		return errors.New("evidence disk failure")
	}
	return r.memoryRepo.Append(ctx, event)
}

type finalAnswerModel struct{}

func (finalAnswerModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "func Answer() int { return 42 }"})
}
func TestEngineDoesNotCompleteWithoutDurableCriterionEvidence(t *testing.T) {
	repo := &failedEvidenceRepo{newMemoryRepo()}
	engine := NewEngine(repo, nil)
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return finalAnswerModel{}, nil })
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{Mode: domain.TaskModePrecise, Goal: "Return Answer", ResultKind: "code", Criteria: []domain.AcceptanceCriterion{{ID: "answer", Text: "Function returns 42", Kind: "manual"}}}))
	if err != nil {
		t.Fatal(err)
	}
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{}
	run, err := engine.Start(StartInput{Workspace: domain.Workspace{ID: "ws", Path: t.TempDir()}, Task: "Return Answer", TaskBrief: &brief, Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC())})
	if err != nil {
		t.Fatal(err)
	}
	finished := waitForTerminalRun(t, repo.memoryRepo, run.ID)
	if finished.Status != domain.RunFailed || !strings.Contains(finished.Error, "persist completion evidence") {
		t.Fatalf("false completion: %+v", finished)
	}
}
