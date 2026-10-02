package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

func gitAgentConfig() domain.OrchestratorConfig {
	return domain.OrchestratorConfig{Provider: "openai-compatible", Model: "archivist", BaseURL: "http://127.0.0.1:1"}
}

func TestGitAgentWritesCommitInRepositoryStyle(t *testing.T) {
	var seen providers.ModelRequest
	factory := func(providers.Config) (providers.Model, error) {
		return &scriptedModel{reply: "<think>смотрю историю</think>{\"subject\":\"feat: add exact flag lookup\\nlost\",\"body\":\"GET /flag/get-flag returns one flag.\"}", seen: &seen}, nil
	}
	text, err := ComposeCommit(context.Background(), gitAgentConfig(), GitAgentRequest{
		Goal: "Новый эндпоинт GET /flag/get-flag", RecentSubjects: []string{"fix: onboarding"}, Files: []string{"src/flags.ts"},
	}, factory)
	if err != nil {
		t.Fatal(err)
	}
	if text.Subject != "feat: add exact flag lookup" || text.Body != "GET /flag/get-flag returns one flag." {
		t.Fatalf("text = %+v", text)
	}
	user := seen.Messages[1].Content
	if !strings.Contains(user, "fix: onboarding") || !strings.Contains(user, "src/flags.ts") {
		t.Fatalf("git agent must see history and files: %s", user)
	}
	if len(seen.Tools) != 0 {
		t.Fatal("git agent gets no tools")
	}
}

func TestGitAgentBranchNameAndFailures(t *testing.T) {
	factory := func(providers.Config) (providers.Model, error) {
		return &scriptedModel{reply: `{"branch":"feat/flag-get-flag-endpoint"}`}, nil
	}
	name, err := ProposeBranchName(context.Background(), gitAgentConfig(), GitAgentRequest{Goal: "x"}, factory)
	if err != nil || name != "feat/flag-get-flag-endpoint" {
		t.Fatalf("branch = %q %v", name, err)
	}
	failing := func(providers.Config) (providers.Model, error) {
		return &scriptedModel{fail: errors.New("connection refused")}, nil
	}
	if _, err = ProposeBranchName(context.Background(), gitAgentConfig(), GitAgentRequest{Goal: "x"}, failing); err == nil {
		t.Fatal("model failure must surface so the caller uses the template")
	}
	if _, err = ComposeCommit(context.Background(), domain.OrchestratorConfig{}, GitAgentRequest{Goal: "x"}, factory); err == nil {
		t.Fatal("missing model must be an error")
	}
}
