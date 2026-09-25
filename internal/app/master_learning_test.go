package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/masterskills"
	"local-agent-workbench/internal/orchestrator"
	"local-agent-workbench/internal/providers"
)

func TestMasterLearningFullCycleAndRegression(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	defs, err := a.masterSkillDefinitions(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	var skill domain.SkillDefinition
	for _, s := range defs {
		if s.ID == masterskills.Explanation {
			skill = s
		}
	}
	revisions, _ := a.store.MasterSkillRevisions(ctx)
	var baseline string
	for _, r := range revisions {
		if r.Skill.ID == skill.ID {
			baseline = r.ID
		}
	}
	request := orchestrator.MasterSkillFixtures("explanation")[0]
	encoded, _ := json.Marshal(request)
	replayJSON, _ := json.Marshal(domain.MasterReplay{Format: domain.MasterReplayFormat, Request: encoded})
	for i := 0; i < 3; i++ {
		if err = a.store.SaveMasterOperation(ctx, domain.MasterOperation{ID: fmt.Sprintf("example-%d", i), WorkspaceID: "a", Phase: "explanation", Skills: []domain.SkillAttribution{domain.SkillDefinitionAttribution(skill)}, InputTokens: 1000000, Replay: string(replayJSON), CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	if err = a.store.QueueMasterLearning(ctx, "a", "explanation", skill.ID, baseline); err != nil {
		t.Fatal(err)
	}
	job, err := a.store.ClaimMasterLearning(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Messages []providers.Message `json:"messages"`
			Tools    []any               `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if len(body.Tools) > 0 {
			t.Error("replay exposed tools")
		}
		out := `{"reply":"Результат не подтверждён; нужна проверка критериев."}`
		if strings.Contains(body.Messages[0].Content, "Improve one Master methodology") {
			out = `{"instructions":"Начни с подтверждённого результата. Раздели доказанные факты, ограничения и следующий шаг. Не объявляй выполнение по одному сообщению исполнителя; назови непроведённые проверки явно."}`
		}
		if strings.Contains(body.Messages[0].Content, "Evaluate methodology") {
			out = `{"constraintsPreserved":true,"portable":true,"baselineQuality":4,"candidateQuality":5,"defectFixed":true}`
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": out}}}, "usage": map[string]int{"prompt_tokens": 20, "completion_tokens": 10}})
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer endpoint.Close()
	cfg := domain.OrchestratorConfig{Provider: domain.ProviderOpenAI, Model: "test-model", BaseURL: endpoint.URL}
	a.evaluateMasterJob(ctx, &job, cfg, "test-key-never-persisted")
	if job.Status != "canary" || len(job.Evaluations) != 6 || calls.Load() != 19 {
		t.Fatalf("job=%+v calls=%d", job, calls.Load())
	}
	if err = a.store.SaveMasterLearningJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	revisions, _ = a.store.MasterSkillRevisions(ctx)
	var candidate domain.MasterSkillRevision
	for _, r := range revisions {
		if r.ID == job.CandidateID {
			candidate = r
		}
	}
	attr := domain.SkillDefinitionAttribution(candidate.Skill)
	apply := func(ws string, n int, bad bool) {
		t.Helper()
		for i := 0; i < n; i++ {
			op := domain.MasterOperation{ID: domain.NewID("canary"), WorkspaceID: ws, Phase: "explanation", Skills: []domain.SkillAttribution{attr}, ContractError: bad, CreatedAt: time.Now().UTC()}
			if err = a.store.SaveMasterOperation(ctx, op); err != nil {
				t.Fatal(err)
			}
		}
		if err = a.updateMasterCanaries(ctx, ws); err != nil {
			t.Fatal(err)
		}
	}
	status := func() string {
		all, _ := a.store.MasterSkillRevisions(ctx)
		for _, r := range all {
			if r.ID == candidate.ID {
				return r.Status
			}
		}
		return ""
	}
	apply("a", 2, false)
	if status() != "canary" {
		t.Fatal("promoted with insufficient data")
	}
	apply("a", 1, false)
	if status() != "local" {
		t.Fatal("did not pass source canary")
	}
	foreign, _ := a.masterSkillDefinitions(ctx, "b")
	for _, s := range foreign {
		if s.ID == skill.ID && s.Instructions == candidate.Skill.Instructions {
			t.Fatal("leaked source trial into another project")
		}
	}
	if err = a.store.SetMasterSkillTrial(ctx, candidate.ID, "b", "canary"); err != nil {
		t.Fatal(err)
	}
	apply("b", 3, false)
	if status() != "shared" {
		t.Fatal("two independent projects did not promote")
	}
	apply("b", 1, true)
	if status() != "rolled_back" {
		t.Fatal("contract regression did not roll back")
	}
	active, _ := a.masterSkillDefinitions(ctx, "a")
	for _, s := range active {
		if s.ID == skill.ID && s.Instructions == candidate.Skill.Instructions {
			t.Fatal("rolled back revision still active")
		}
	}
	budget, _ := a.store.MasterLearningBudget(ctx, "a")
	if budget.SpentTokens != 19*30 || budget.ReservedTokens != 0 {
		t.Fatalf("budget=%+v", budget)
	}
	jobs, _ := a.store.MasterLearningJobs(ctx, "a")
	raw, _ := json.Marshal(jobs)
	if strings.Contains(string(raw), "test-key-never-persisted") {
		t.Fatal("key persisted")
	}
}

func TestMasterLearningDefersWithoutBudgetOrAuthorization(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	cfg := domain.OrchestratorConfig{Provider: domain.ProviderOpenAI, Model: "test"}
	job := domain.MasterLearningJob{WorkspaceID: "a"}
	a.evaluateMasterJob(ctx, &job, cfg, "")
	if job.Status != "deferred" || !strings.Contains(job.Reason, "авторизация") {
		t.Fatalf("job %+v", job)
	}
	model := &masterNoCallModel{}
	_, _, err := a.masterLearningCall(ctx, model, cfg, "a", providers.ModelRequest{MaxOutputTokens: 20})
	if err == nil || model.called {
		t.Fatal("learning called provider without budget")
	}
}

type masterNoCallModel struct{ called bool }

func (m *masterNoCallModel) Stream(context.Context, providers.ModelRequest, func(providers.ModelEvent) error) error {
	m.called = true
	return nil
}

func TestMasterDevelopmentIsolationAndManualRollback(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	defs, err := a.masterSkillDefinitions(ctx, a.currentWorldID())
	if err != nil {
		t.Fatal(err)
	}
	r := domain.MasterSkillRevision{ID: "private-revision", WorkspaceID: "other-project", Skill: defs[0], Status: "canary", Reason: "private-example", CreatedAt: time.Now().UTC()}
	if err = a.store.SaveMasterRevision(ctx, r); err != nil {
		t.Fatal(err)
	}
	view, err := a.MasterDevelopment(ctx)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(view)
	if strings.Contains(string(encoded), "private-example") {
		t.Fatal("foreign project details exposed")
	}
	if _, err = a.RollbackMasterSkill(ctx, r.ID); err == nil {
		t.Fatal("cross-project rollback allowed")
	}
	r.ID = "local-revision"
	r.WorkspaceID = a.currentWorldID()
	if err = a.store.SaveMasterRevision(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err = a.RollbackMasterSkill(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
}
