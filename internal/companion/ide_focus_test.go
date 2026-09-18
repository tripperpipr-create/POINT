package companion_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/companion"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
	"local-agent-workbench/internal/storage"
)

func TestModelPromptIncludesCurrentIDEFocus(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "companion-focus-prompt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	workspaceID := "ws-focus-prompt"
	now := time.Now().UTC()
	if err = store.SaveWorkspace(ctx, domain.Workspace{ID: workspaceID, Path: t.TempDir(), Name: "focus-prompt"}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := companion.PresetDefaults("balanced")
	cfg.ID, cfg.WorkspaceID = "companion-focus-prompt", workspaceID
	cfg.Provider, cfg.ProviderPreset, cfg.BaseURL, cfg.Model = domain.ProviderOpenAI, "openai", "https://example.test/v1", "planner"
	cfg.Temperature, cfg.MaxOutputTokens, cfg.CreatedAt, cfg.UpdatedAt = 0.2, 1200, now, now
	if err = store.SaveCompanionConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	model := &recordingCompanionModel{contents: []string{`{"reply":"Saw the open file","level":"suggestion","questions":[],"proposal":null}`}}
	svc := companion.Service{Store: store, ModelFactory: func(providers.Config) (providers.Model, error) { return model, nil }}
	if _, err = svc.Chat(ctx, companion.ChatRequest{
		WorkspaceID: workspaceID,
		Message:     "Что не так здесь?",
		Focus:       companion.ChatFocus{File: "auth.go", Line: 40, Language: "go", Snippet: "func Login() {}"},
	}); err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 1 {
		t.Fatalf("model requests=%d", len(model.requests))
	}
	encoded, err := json.Marshal(model.requests[0].Messages)
	if err != nil {
		t.Fatal(err)
	}
	prompt := string(encoded)
	for _, expected := range []string{"CURRENT IDE FOCUS", "auth.go", "40", "func Login()"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("model prompt missing %q: %s", expected, prompt)
		}
	}
}

func TestCompanionExplainsIDESignalsAndGroundsFixQuest(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "companion-ide.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	workspaceID := "ws-ide"
	now := time.Now().UTC()
	if err = store.SaveWorkspace(ctx, domain.Workspace{ID: workspaceID, Path: t.TempDir(), Name: "ide"}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveProjectAgent(ctx, domain.ProjectAgent{
		ID: "fixer", WorkspaceID: workspaceID, Name: "Fixer", RoleDescription: "Debug and test failures",
		SystemPrompt: "fix", Provider: domain.ProviderOllama, PrimaryModel: "local", AllowedTools: []string{"read_file", "run_command"},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	exitCode := 1
	if err = store.SaveIDEObservationBatch(ctx, workspaceID, "diagnostic", true, []domain.IDEObservation{{
		ID: "diag", WorkspaceID: workspaceID, Kind: "diagnostic", Source: "gopls", Level: "error",
		Summary: "undefined: handler", Path: "main.go", Line: 12, ObservedAt: now,
	}}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveIDEObservationBatch(ctx, workspaceID, "terminal", false, []domain.IDEObservation{{
		ID: "terminal", WorkspaceID: workspaceID, Kind: "terminal", Source: "Point", Level: "error",
		Summary: "tests failed", Detail: "FAIL auth", Command: "go test ./...", ExitCode: &exitCode, ObservedAt: now.Add(time.Second),
	}}); err != nil {
		t.Fatal(err)
	}
	svc := companion.Service{Store: store}
	status, err := svc.Chat(ctx, companion.ChatRequest{WorkspaceID: workspaceID, Message: "Какие ошибки сейчас?"})
	if err != nil {
		t.Fatal(err)
	}
	if status.Proposal != nil || !strings.Contains(status.Reply, "main.go:12") || !strings.Contains(status.Reply, "go test ./...") {
		t.Fatalf("IDE status response=%#v", status)
	}
	fix, err := svc.Chat(ctx, companion.ChatRequest{WorkspaceID: workspaceID, Message: "Исправь текущие ошибки"})
	if err != nil {
		t.Fatal(err)
	}
	if fix.Proposal == nil || fix.Proposal.Importance != domain.QuestImportant {
		t.Fatalf("fix response=%#v", fix)
	}
	joinedObjectives := strings.Join(fix.Proposal.Objectives, " ")
	joinedDoD := strings.Join(fix.Proposal.DefinitionOfDone, " ")
	if !strings.Contains(joinedObjectives, "main.go:12") || !strings.Contains(joinedObjectives, "go test ./...") || !strings.Contains(joinedDoD, "Problems") || !strings.Contains(joinedDoD, "кодом 0") {
		t.Fatalf("IDE-grounded proposal=%#v", fix.Proposal)
	}
	quests, err := store.ListQuests(ctx, workspaceID)
	if err != nil || len(quests) != 0 {
		t.Fatalf("fix proposal started work without confirmation: %#v err=%v", quests, err)
	}
}

func TestCompanionGroundsOrdinaryChatInLiveFocus(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "companion-focus.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	workspaceID := "ws-focus"
	now := time.Now().UTC()
	if err = store.SaveWorkspace(ctx, domain.Workspace{ID: workspaceID, Path: t.TempDir(), Name: "focus"}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveProjectAgent(ctx, domain.ProjectAgent{
		ID: "fixer", WorkspaceID: workspaceID, Name: "Fixer", RoleDescription: "Debug",
		SystemPrompt: "fix", Provider: domain.ProviderOllama, PrimaryModel: "local", AllowedTools: []string{"read_file"},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	svc := companion.Service{Store: store}
	status, err := svc.Chat(ctx, companion.ChatRequest{
		WorkspaceID: workspaceID,
		Message:     "Что здесь не так?",
		Focus: companion.ChatFocus{
			File: "auth.go", Line: 40, Language: "go", Dirty: true, Diagnostics: 1,
			Run: "go test ./...", Snippet: "func Login() {\n\treturn nil\n}",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.Proposal != nil || !strings.Contains(status.Reply, "auth.go:40") || !strings.Contains(status.Reply, "go test ./...") {
		t.Fatalf("focus status response=%#v", status)
	}
	if !containsFact(status.FactsUsed, "ideFocus=auth.go:40") || !containsFact(status.FactsUsed, "ideRun=go test ./...") {
		t.Fatalf("focus facts=%#v", status.FactsUsed)
	}
	joinedQuestions := strings.Join(status.Questions, " ")
	if !strings.Contains(joinedQuestions, "Исправь ошибки в auth.go:40") || strings.Contains(joinedQuestions, "Разбери цель запуска") {
		t.Fatalf("focus follow-ups=%#v", status.Questions)
	}
	here, err := svc.Chat(ctx, companion.ChatRequest{
		WorkspaceID: workspaceID,
		Message:     "Посмотри этот код",
		Focus:       companion.ChatFocus{File: "auth.go", Line: 40, Selection: true, Snippet: "func Login() {}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if here.Proposal != nil || !strings.Contains(here.Reply, "auth.go:40") || !strings.Contains(here.Reply, "выделение") {
		t.Fatalf("selection focus response=%#v", here)
	}
	if err = store.SaveIDEObservationBatch(ctx, workspaceID, "diagnostic", true, []domain.IDEObservation{
		{ID: "other", WorkspaceID: workspaceID, Kind: "diagnostic", Source: "gopls", Level: "error", Summary: "unused import", Path: "other.go", Line: 2, ObservedAt: now},
		{ID: "auth", WorkspaceID: workspaceID, Kind: "diagnostic", Source: "gopls", Level: "error", Summary: "undefined: token", Path: "auth.go", Line: 40, ObservedAt: now},
	}); err != nil {
		t.Fatal(err)
	}
	priority, err := svc.Chat(ctx, companion.ChatRequest{
		WorkspaceID: workspaceID,
		Message:     "Какие ошибки сейчас?",
		Focus:       companion.ChatFocus{File: "auth.go", Line: 40},
	})
	if err != nil {
		t.Fatal(err)
	}
	authAt, otherAt := strings.Index(priority.Reply, "auth.go:40"), strings.Index(priority.Reply, "other.go:2")
	if authAt < 0 || otherAt < 0 || authAt > otherAt {
		t.Fatalf("focused diagnostic was not first: %q", priority.Reply)
	}
	fix, err := svc.Chat(ctx, companion.ChatRequest{
		WorkspaceID: workspaceID,
		Message:     "Исправь этот файл",
		Focus:       companion.ChatFocus{File: "auth.go", Line: 40, Failure: "go test ./..."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fix.Proposal == nil {
		t.Fatalf("focus fix response=%#v", fix)
	}
	joined := strings.Join(fix.Proposal.Objectives, " ")
	if !strings.Contains(joined, "auth.go:40") || !strings.Contains(joined, "go test ./...") {
		t.Fatalf("focus-grounded proposal=%#v", fix.Proposal)
	}
	quests, err := store.ListQuests(ctx, workspaceID)
	if err != nil || len(quests) != 0 {
		t.Fatalf("focus fix started work: %#v err=%v", quests, err)
	}
}

func TestCompanionFocusKeepsDotDotFilenameAndDropsParentPaths(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "companion-path.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	workspaceID := "ws-path"
	if err = store.SaveWorkspace(ctx, domain.Workspace{ID: workspaceID, Path: t.TempDir(), Name: "path"}); err != nil {
		t.Fatal(err)
	}
	svc := companion.Service{Store: store}
	kept, err := svc.Chat(ctx, companion.ChatRequest{
		WorkspaceID: workspaceID,
		Message:     "Что здесь не так?",
		Focus:       companion.ChatFocus{File: "notes..md", Line: 2, Snippet: "TODO: check auth"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(kept.Reply, "notes..md") {
		t.Fatalf("filename with dots was stripped: %q", kept.Reply)
	}
	escaped, err := svc.Chat(ctx, companion.ChatRequest{
		WorkspaceID: workspaceID,
		Message:     "Что здесь не так?",
		Focus:       companion.ChatFocus{File: "../secret.go", Line: 1, Snippet: "package secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(escaped.Reply, "secret.go") {
		t.Fatalf("parent path leaked into reply: %q", escaped.Reply)
	}
	if !strings.Contains(escaped.Reply, "фрагмент") {
		t.Fatalf("snippet focus was dropped with parent path: %q", escaped.Reply)
	}
}
