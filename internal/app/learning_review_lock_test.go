package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// Рецензент работает минутами. Пока он думает, финализация прогона, канарейка
// и откат не должны ждать его блокировку; а если за это время агент изменился,
// отзыв, написанный против прежнего состояния, не применяется.
func TestAgentRunReviewReleasesLockAndSkipsWhenStateChanged(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var agentID atomic.Value
	var lockFree atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if application.learningReviewMu.TryLock() {
			lockFree.Store(true)
			application.learningReviewMu.Unlock()
		}
		if id, ok := agentID.Load().(string); ok {
			agent, getErr := application.store.GetProjectAgent(context.Background(), id)
			if getErr == nil {
				agent.Rules = append(agent.Rules, "Правило, добавленное человеком во время разбора.")
				_ = application.store.SaveProjectAgent(context.Background(), agent)
			}
		}
		http.Error(w, "controlled reviewer outage", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{
		WorkspaceID: view.Workspace.ID, Name: "Backend", Provider: domain.ProviderOllama, BaseURL: server.URL, PrimaryModel: "qwen2.5-coder:7b",
		AllowedTools: []string{"project_map", "list_files", "search_code", "read_file", "search_text"},
	})
	if err != nil {
		t.Fatal(err)
	}
	agentID.Store(agent.ID)
	run := saveLearningRun(t, application, view.Workspace.ID, agent, "run-lock-review", time.Now().UTC())
	item, err := application.reviewAgentRun(ctx, run, agent.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !lockFree.Load() {
		t.Fatal("learning mutex was held while the reviewer model ran")
	}
	if item.Status != "skipped" || !strings.Contains(item.Failure, "changed while the reviewer was running") {
		t.Fatalf("stale review was applied: status=%s failure=%q", item.Status, item.Failure)
	}
	stored, err := application.store.GetProjectAgent(ctx, agent.ID)
	if err != nil || len(stored.SkillIDs) != 0 {
		t.Fatalf("stale review mutated the agent: %#v err=%v", stored.SkillIDs, err)
	}
}

// Рецензент получает наблюдения ядра по своему прогону, а не только
// помечает их прочитанными.
func TestReviewerSeesRunLearningSignals(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var body atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		raw, _ := io.ReadAll(request.Body)
		if body.Load() == nil {
			body.Store(string(raw))
		}
		http.Error(w, "controlled reviewer outage", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{
		WorkspaceID: view.Workspace.ID, Name: "Backend", Provider: domain.ProviderOllama, BaseURL: server.URL, PrimaryModel: "qwen2.5-coder:7b",
		AllowedTools: []string{"project_map", "list_files", "search_code", "read_file", "search_text"},
	})
	if err != nil {
		t.Fatal(err)
	}
	run := saveLearningRun(t, application, view.Workspace.ID, agent, "run-signal-review", time.Now().UTC())
	if err = application.store.SaveLearningPrinciple(ctx, domain.LearningPrinciple{ID: "principle-known", WorkspaceID: view.Workspace.ID, ProjectAgentID: agent.ID,
		Kind: "failure", Key: "verify-before-report", Content: "Record the verifier result before reporting.", Signature: "sig-known", SourceRunID: "old-run", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveLearningSignal(ctx, domain.LearningSignal{
		ID: "signal-review", WorkspaceID: view.Workspace.ID, ProjectAgentID: agent.ID, RunID: run.ID, Kind: domain.LearningSignalKind("tool_failure"),
		Status: "observed", Summary: "search_code вернул ошибку дважды подряд", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = application.reviewAgentRun(ctx, run, agent.ID, ""); err != nil {
		t.Fatal(err)
	}
	sent, _ := body.Load().(string)
	if !strings.Contains(sent, "observedSignals") || !strings.Contains(sent, "search_code") || !strings.Contains(sent, "verify-before-report") {
		t.Fatalf("reviewer request lacks run signals: %.400s", sent)
	}
}
