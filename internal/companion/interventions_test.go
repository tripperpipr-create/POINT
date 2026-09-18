package companion_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/companion"
	"local-agent-workbench/internal/diagnostics"
	"local-agent-workbench/internal/domain"
)

func TestCompanionInterventionsArePrioritizedAndNonBlocking(t *testing.T) {
	cfg, _ := companion.PresetDefaults("cautious")
	interventions := companion.BuildInterventions(
		cfg,
		nil,
		[]domain.ExecutionInstance{{ID: "waiting", Status: domain.RunWaiting}, {ID: "failed-1", Status: domain.RunFailed}, {ID: "failed-2", Status: domain.RunFailed}, {ID: "failed-3", Status: domain.RunInterrupted}},
		[]domain.ChangeSet{{ID: "conflict", Title: "Auth changes", Status: domain.ChangeSetConflict}},
		nil,
		nil,
	)
	if len(interventions) < 4 {
		t.Fatalf("interventions=%#v", interventions)
	}
	if interventions[0].Level != "critical" || interventions[0].RelatedID != "conflict" {
		t.Fatalf("critical intervention must be first: %#v", interventions)
	}
	for _, intervention := range interventions {
		if intervention.Level != "suggestion" && intervention.Level != "warning" && intervention.Level != "critical" {
			t.Fatalf("invalid intervention level: %#v", intervention)
		}
		if len(intervention.OccurrenceKey) != 16 {
			t.Fatalf("intervention occurrence key=%q", intervention.OccurrenceKey)
		}
	}
}

func TestUsageFailureInterventionProbesSelectedCompanionConnection(t *testing.T) {
	cfg, _ := companion.PresetDefaults("cautious")
	cfg.Provider = domain.ProviderOpenAI
	cfg.ProviderPreset = "llmux"
	cfg.BaseURL = "https://llmux.example.test/v1/"
	usage := []domain.UsageRecord{
		{ID: "1", Outcome: "error"}, {ID: "2", Outcome: "failed"},
		{ID: "3", Outcome: "ok"}, {ID: "4", Outcome: "ok"}, {ID: "5", Outcome: "ok"},
	}
	connections := []domain.Connection{
		{ID: "other-openai", Provider: domain.ProviderOpenAI, PresetID: "openai", BaseURL: "https://api.openai.com/v1", Status: domain.ConnectionConnected},
		{ID: "selected-llmux", Provider: domain.ProviderOpenAI, PresetID: "llmux", BaseURL: "https://llmux.example.test/v1", Status: domain.ConnectionError},
	}
	interventions := companion.BuildInterventions(cfg, []domain.ProjectAgent{{ID: "agent"}}, nil, nil, usage, connections)
	for _, intervention := range interventions {
		if intervention.ID != "usage-failure-rate" {
			continue
		}
		if intervention.ActionKind != domain.CompanionInterventionProbeConnection || intervention.RelatedID != "selected-llmux" || intervention.ActionLabel != "Проверить связь" {
			t.Fatalf("usage probe action=%#v", intervention)
		}
		withoutSelected := companion.BuildInterventions(cfg, []domain.ProjectAgent{{ID: "agent"}}, nil, nil, usage, connections[:1])
		for _, fallback := range withoutSelected {
			if fallback.ID == "usage-failure-rate" && (fallback.ActionKind != "" || fallback.RelatedID != "" || fallback.ActionLabel != "Открыть подключения") {
				t.Fatalf("usage probe selected the wrong gateway: %#v", fallback)
			}
		}
		return
	}
	t.Fatalf("usage failure intervention is missing: %#v", interventions)
}

func TestUsageFailureInterventionClearsAfterThreeSuccessfulCalls(t *testing.T) {
	now := time.Now().UTC()
	usage := []domain.UsageRecord{
		{Outcome: "completed", CreatedAt: now},
		{Outcome: "success", CreatedAt: now.Add(-time.Second)},
		{Outcome: "ok", CreatedAt: now.Add(-2 * time.Second)},
		{Outcome: "failed", CreatedAt: now.Add(-3 * time.Second)},
		{Outcome: "failed", CreatedAt: now.Add(-4 * time.Second)},
		{Outcome: "failed", CreatedAt: now.Add(-5 * time.Second)},
	}
	items := companion.BuildInterventions(domain.CompanionConfig{}, []domain.ProjectAgent{{ID: "agent"}}, nil, nil, usage, nil)
	for _, item := range items {
		if item.ID == "usage-failure-rate" {
			t.Fatalf("recovered provider must not keep a stale failure-rate warning: %#v", item)
		}
	}
}

func TestUsageFailureOccurrenceKeyIsStableAcrossCounterChanges(t *testing.T) {
	first := domain.CompanionIntervention{ID: "usage-failure-rate", Level: "warning", Title: "Высокая доля", Detail: "3 из 7"}
	second := domain.CompanionIntervention{ID: "usage-failure-rate", Level: "warning", Title: "Высокая доля", Detail: "3 из 8"}
	firstKey := companion.InterventionOccurrenceKey(first, nil)
	secondKey := companion.InterventionOccurrenceKey(second, nil)
	if firstKey != secondKey || len(firstKey) != 16 {
		t.Fatalf("aggregate warning key must be stable and valid: first=%q second=%q", firstKey, secondKey)
	}
}

func TestDismissedInterventionOnlyHidesTheObservedOccurrence(t *testing.T) {
	items := make([]domain.CompanionIntervention, 0, 10)
	for index := 0; index < 10; index++ {
		items = append(items, domain.CompanionIntervention{
			ID: fmt.Sprintf("risk-%d", index), Level: "suggestion", Title: "Risk", Detail: fmt.Sprintf("evidence=%d", index),
		})
	}
	merged := companion.MergeInterventions(items)
	if len(merged) != 10 || merged[0].OccurrenceKey == "" {
		t.Fatalf("merged interventions=%#v", merged)
	}
	visible, hidden := companion.VisibleInterventions(merged, map[string]bool{merged[0].OccurrenceKey: true}, 8)
	if hidden != 1 || len(visible) != 8 || visible[0].ID == merged[0].ID || visible[7].ID != merged[8].ID {
		t.Fatalf("visible=%#v hidden=%d", visible, hidden)
	}
	changed := companion.MergeInterventions([]domain.CompanionIntervention{{ID: merged[0].ID, Level: "warning", Title: "Risk", Detail: "new evidence"}})
	if changed[0].OccurrenceKey == merged[0].OccurrenceKey {
		t.Fatalf("changed evidence reused occurrence key %q", changed[0].OccurrenceKey)
	}
	visible, hidden = companion.VisibleInterventions(changed, map[string]bool{merged[0].OccurrenceKey: true}, 8)
	if hidden != 0 || len(visible) != 1 {
		t.Fatalf("changed occurrence stayed hidden: %#v hidden=%d", visible, hidden)
	}
}

func TestCompanionBudgetAndExecutionInterventions(t *testing.T) {
	cfg, _ := companion.PresetDefaults("cautious")
	now := time.Now().UTC()
	budget := companion.BuildBudgetInterventions(companion.BudgetSnapshot{
		DailyLimitCents: 100, DailyUsedCents: 82, MonthlyLimitCents: 1000, MonthlyUsedCents: 1000, HardStop: true,
	})
	if len(budget) != 2 || budget[0].Level != "warning" || budget[1].Level != "critical" || !strings.Contains(budget[1].Detail, "политикой бюджета") {
		t.Fatalf("budget interventions=%#v", budget)
	}
	run := domain.Run{
		ID: "run-risk", WorkspaceID: "ws", Status: domain.RunRunning, StartedAt: now.Add(-9 * time.Minute),
		ChangedFiles:          []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"},
		ContextItems:          []domain.RunContextItem{{ID: "context", Size: 13_000, TokenEstimate: 3_250}},
		ConfigurationSnapshot: domain.RunConfigurationSnapshot{Profile: domain.AgentProfile{ContextWindowTokens: 4_000, MaxDurationSeconds: 600}},
	}
	execution := domain.ExecutionInstance{ID: "execution-risk", RunID: run.ID, Status: domain.RunRunning}
	items := companion.BuildExecutionInterventions(cfg, []domain.ExecutionInstance{execution}, []domain.Run{run}, now)
	if len(items) != 3 {
		t.Fatalf("execution interventions=%#v", items)
	}
	merged := companion.MergeInterventions(budget, items)
	if len(merged) != 5 || merged[0].Level != "critical" {
		t.Fatalf("merged interventions=%#v", merged)
	}
}

func TestCompanionRunDiagnosticInterventionsOfferExplicitActions(t *testing.T) {
	cfg, _ := companion.PresetDefaults("cautious")
	execution := domain.ExecutionInstance{ID: "execution-live", RunID: "run-live", Status: domain.RunWaiting}
	diag := diagnostics.RunDiagnostics{
		RunID: "run-live", Health: diagnostics.HealthAttention,
		Tools:      diagnostics.ToolMetrics{Failed: 2, Items: []diagnostics.ToolMetric{{Name: "run_command", Failed: 2}}},
		Approvals:  diagnostics.ApprovalMetrics{Pending: 1, MaxWaitMs: 45_000},
		Context:    diagnostics.ContextMetrics{Compactions: 2, PeakInputTokens: 900, InputBudgetTokens: 1_000},
		Retrieval:  diagnostics.RetrievalMetrics{TruncatedSearches: 1},
		Completion: diagnostics.CompletionMetrics{RevisionRequests: 1},
		Model:      diagnostics.ModelMetrics{Retries: 2},
	}
	items := companion.BuildRunDiagnosticInterventions(cfg, []domain.ExecutionInstance{execution}, []diagnostics.RunDiagnostics{diag})
	if len(items) != 6 {
		t.Fatalf("diagnostic interventions=%#v", items)
	}
	byID := map[string]domain.CompanionIntervention{}
	for _, item := range items {
		byID[item.ID] = item
	}
	approval := byID["run-approval-run-live"]
	if approval.ActionKind != domain.CompanionInterventionOpenRun || approval.RelatedID != execution.ID {
		t.Fatalf("approval intervention=%#v", approval)
	}
	toolFailure := byID["run-tool-failures-run-live"]
	if toolFailure.ActionKind != domain.CompanionInterventionMessageRun || toolFailure.ActionMessage == "" || !strings.Contains(toolFailure.Detail, "run_command") {
		t.Fatalf("tool intervention=%#v", toolFailure)
	}
	merged := companion.MergeInterventions(items)
	if len(merged) != len(items) || merged[0].Level != "warning" || merged[0].OccurrenceKey == "" {
		t.Fatalf("merged diagnostic interventions=%#v", merged)
	}
}

func TestCompanionIDEInterventionsPrepareReviewedFixQuests(t *testing.T) {
	exitCode := 1
	items := companion.BuildIDEInterventions([]domain.IDEObservation{
		{ID: "diag-1", Kind: "diagnostic", Level: "error", Path: "main.go", Line: 12, Summary: "undefined: handler"},
		{ID: "terminal-1", Kind: "terminal", Level: "error", Command: "go test ./...", ExitCode: &exitCode, Detail: "FAIL auth"},
	})
	if len(items) != 2 {
		t.Fatalf("IDE interventions=%#v", items)
	}
	diagnosticsItem := items[0]
	if diagnosticsItem.ActionKind != domain.CompanionInterventionPrompt || diagnosticsItem.RelatedID != "diag-1" || diagnosticsItem.RelatedPath != "main.go" || diagnosticsItem.RelatedLine != 12 || !strings.Contains(diagnosticsItem.ActionMessage, "Problems") {
		t.Fatalf("diagnostic action=%#v", diagnosticsItem)
	}
	terminalItem := items[1]
	if terminalItem.ActionKind != domain.CompanionInterventionPrompt || terminalItem.RelatedID != "terminal-1" || !strings.Contains(terminalItem.ActionMessage, "go test ./...") {
		t.Fatalf("terminal action=%#v", terminalItem)
	}
}

func TestDiagnosticsRelatedPathPrefersFocus(t *testing.T) {
	now := time.Now().UTC()
	obs := []domain.IDEObservation{
		{ID: "d1", Kind: "diagnostic", Level: "error", Path: "a.go", Line: 1, Summary: "first", ObservedAt: now, FirstSeen: now, LastSeen: now, Count: 1},
		{ID: "d2", Kind: "diagnostic", Level: "error", Path: "b.go", Line: 9, Summary: "focused", ObservedAt: now, FirstSeen: now, LastSeen: now, Count: 1},
	}
	items := companion.BuildIDEInterventionsGated(obs, domain.CompanionConfig{Initiative: 55, QuestionStrictness: 40}, companion.InterveneContext{Now: now, FocusPath: "b.go"})
	if len(items) == 0 || items[0].ID != "ide-diagnostics" {
		t.Fatalf("expected diagnostics: %#v", items)
	}
	if items[0].RelatedPath != "b.go" || items[0].RelatedLine != 9 || items[0].RelatedID != "d2" {
		t.Fatalf("RelatedPath must prefer focus match: %#v", items[0])
	}
}

func TestSCMInterventionCopyIsHonest(t *testing.T) {
	now := time.Now().UTC()
	obs := []domain.IDEObservation{{
		ID: "s1", Kind: "scm", Level: "warning", Path: "pkg/x.go",
		Summary:    "9 в git · behind 1 · main",
		Detail:     "changed=5; staged=2; untracked=2; unsaved=1; branch=main; ahead=0; behind=1",
		ObservedAt: now, LastSeen: now,
	}}
	items := companion.BuildIDEInterventionsGated(obs, domain.CompanionConfig{Initiative: 80, QuestionStrictness: 40}, companion.InterveneContext{Now: now})
	if len(items) == 0 || items[0].ID != "ide-scm-dirty" {
		t.Fatalf("expected scm intervention: %#v", items)
	}
	item := items[0]
	if item.Level != "warning" {
		t.Fatalf("large/behind scm should be warning: %#v", item)
	}
	if !strings.Contains(item.Title, "отставание") && !strings.Contains(item.Title, "незакоммиченные") {
		t.Fatalf("title must describe git state honestly: %#v", item)
	}
	if !strings.Contains(item.Detail, "изменено 5") || !strings.Contains(item.Detail, "несохранённых буферов 1") {
		t.Fatalf("detail must separate git vs unsaved: %#v", item)
	}
	if item.RelatedPath != "pkg/x.go" {
		t.Fatalf("RelatedPath=%q", item.RelatedPath)
	}
	legacy := companion.BuildIDEInterventionsGated([]domain.IDEObservation{{
		ID: "legacy", Kind: "scm", Level: "info", Detail: "dirty=3; branch=dev", ObservedAt: now, LastSeen: now,
	}}, domain.CompanionConfig{Initiative: 80, QuestionStrictness: 40}, companion.InterveneContext{Now: now})
	if len(legacy) == 0 || !strings.Contains(legacy[0].Title, "несохранённые") {
		t.Fatalf("legacy dirty= should map to unsaved buffers: %#v", legacy)
	}
}
