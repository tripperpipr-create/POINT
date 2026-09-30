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
		// Задание открывает дефект: человек отметил ответ «Не помогло».
		feedback := map[bool]string{true: "down"}[i == 0]
		if err = a.store.SaveMasterOperation(ctx, domain.MasterOperation{ID: fmt.Sprintf("example-%d", i), WorkspaceID: "a", Phase: "explanation", Skills: []domain.SkillAttribution{domain.SkillDefinitionAttribution(skill)}, InputTokens: 1000000, Feedback: feedback, Replay: string(replayJSON), CreatedAt: time.Now().UTC()}); err != nil {
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
	uses := func(ws string) bool {
		skills, _ := a.masterSkillDefinitions(ctx, ws)
		for _, s := range skills {
			if s.ID == skill.ID && s.Instructions == candidate.Skill.Instructions {
				return true
			}
		}
		return false
	}
	// Сбой во втором проекте выключает общую ревизию только там.
	apply("b", 1, true)
	if status() != "shared" || uses("b") || !uses("a") {
		t.Fatalf("foreign regression: status=%s b=%t a=%t", status(), uses("b"), uses("a"))
	}
	// Сбой в проекте-источнике откатывает всю линию.
	apply("a", 1, true)
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

// Исправление хода — частый спутник приёма задания. Одно исправление из трёх
// применений ревизию не снимает; большинство исправленных ходов без базы — снимает.
func TestMasterCanaryToleratesSingleRepair(t *testing.T) {
	for _, tc := range []struct {
		use  masterRevisionUse
		want bool
	}{
		{masterRevisionUse{uses: 3, repaired: 1}, false},
		{masterRevisionUse{uses: 3, repaired: 2}, true},
		{masterRevisionUse{uses: 4, repaired: 2, baseline: 4, baselineRepaired: 2}, false},
		{masterRevisionUse{uses: 4, repaired: 3, baseline: 4, baselineRepaired: 1}, true},
		{masterRevisionUse{uses: 5, broken: true}, true},
	} {
		if got := tc.use.regression() != ""; got != tc.want {
			t.Errorf("%+v: regression=%t want %t", tc.use, got, tc.want)
		}
	}
}

// Проба навыка, который две недели не применялся, снимается: доказать ей
// нечего, а живое задание пробы не пускает в очередь новое.
func TestIdleMasterCanaryExpires(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	builtin := masterskills.Builtins()[0]
	revision := domain.MasterSkillRevision{ID: "idle-canary", Skill: builtin, Digest: "idle-digest", WorkspaceID: "a", Status: "canary", CreatedAt: time.Now().UTC().Add(-15 * 24 * time.Hour)}
	fresh := revision
	fresh.ID, fresh.Digest, fresh.CreatedAt = "fresh-canary", "fresh-digest", time.Now().UTC()
	for _, r := range []domain.MasterSkillRevision{revision, fresh} {
		if err := a.store.SaveMasterRevision(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.updateMasterCanaries(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	all, _ := a.store.MasterSkillRevisions(ctx)
	statuses := map[string]string{}
	for _, r := range all {
		statuses[r.ID] = r.Status
	}
	if statuses["idle-canary"] != "rolled_back" || statuses["fresh-canary"] != "canary" {
		t.Fatalf("statuses=%v", statuses)
	}
}

// Наряд, который человек переписал после Мастера, засчитывается ревизии
// приёма как исправленный ход: два переписанных наряда из трёх снимают пробу.
func TestUserRevisedWorkOrdersCountAgainstIntakeRevision(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	builtin := masterskills.Builtins()[0]
	revision := domain.MasterSkillRevision{ID: "revised-canary", Skill: builtin, Digest: "revised-digest", WorkspaceID: "a", Status: "canary", CreatedAt: time.Now().UTC()}
	if err := a.store.SaveMasterRevision(ctx, revision); err != nil {
		t.Fatal(err)
	}
	attribution := domain.SkillAttribution{SkillID: builtin.ID, Digest: revision.Digest}
	for i := 0; i < 3; i++ {
		proposal := fmt.Sprintf("proposal-%d", i)
		if err := a.store.SaveMasterOperation(ctx, domain.MasterOperation{ID: fmt.Sprintf("revised-op-%d", i), WorkspaceID: "a", Phase: "intake", ProposalID: proposal,
			Skills: []domain.SkillAttribution{attribution}, CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			a.recordMasterEvidence(ctx, domain.WorkOrder{ID: "order-" + proposal, Version: 1, WorkspaceID: "a", ProposalID: proposal}, "", "revision", "user_revised")
		}
	}
	if err := a.updateMasterCanaries(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	all, _ := a.store.MasterSkillRevisions(ctx)
	for _, r := range all {
		if r.ID == revision.ID && r.Status != "rolled_back" {
			t.Fatalf("revision kept after two of three revised orders: %s", r.Status)
		}
	}
}

// Каждое решение по критерию — своя улика с заметкой человека.
func TestManualCriterionDecisionsAreSeparateEvidence(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	order := domain.WorkOrder{ID: "order-manual", Version: 1, WorkspaceID: "a", ProposalID: "proposal-manual"}
	a.recordMasterEvidenceDetail(ctx, order, "quest-manual", "manual_review", "rejected", "c1", "c1: окно не открывается")
	a.recordMasterEvidenceDetail(ctx, order, "quest-manual", "manual_review", "rejected", "c2", "c2: текст обрезан")
	signals, err := a.store.MasterEvidence(ctx, domain.MasterOperation{WorkspaceID: "a", ProposalID: "proposal-manual"})
	if err != nil || len(signals) != 2 || signals[0].Detail == "" {
		t.Fatalf("signals=%#v err=%v", signals, err)
	}
}

// Правка наряда человеком доходит до Мастера один раз и только в беседе,
// где этот наряд обсуждался.
func TestRevisionNoteReachesMasterOnceInItsConversation(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	order := domain.WorkOrder{ID: "order-noted", Version: 2, WorkspaceID: "w", ProposalID: "proposal-noted", ConversationID: "chat-a"}
	a.saveMasterEvidenceSignal(ctx, order, "", "revision", "user_revised", "", "человек изменил поля наряда: criteria", order.ConversationID)
	if notes := a.masterRevisionNotes(ctx, "w", "chat-b"); len(notes) != 0 {
		t.Fatalf("note leaked into another conversation: %v", notes)
	}
	notes := a.masterRevisionNotes(ctx, "w", "chat-a")
	if len(notes) != 1 || !strings.Contains(notes[0], "criteria") {
		t.Fatalf("notes=%v", notes)
	}
	if again := a.masterRevisionNotes(ctx, "w", "chat-a"); len(again) != 0 {
		t.Fatalf("note repeated: %v", again)
	}
	revised, _ := a.store.MasterEvidenceProposals(ctx, "w", "revision", "user_revised")
	if !revised["proposal-noted"] {
		t.Fatal("the note marker hid the revision from the canary")
	}
}
