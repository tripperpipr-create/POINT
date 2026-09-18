package companion_test

import (
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/companion"
	"local-agent-workbench/internal/domain"
)

func TestShouldInterveneRespectsInitiativeAndStaleSignals(t *testing.T) {
	now := time.Now().UTC()
	obs := []domain.IDEObservation{{
		ID: "diag", Kind: "diagnostic", Level: "error", Path: "main.go", Summary: "boom",
		ObservedAt: now.Add(-2 * time.Hour), LastSeen: now.Add(-2 * time.Hour),
	}}
	critical := domain.CompanionIntervention{ID: "ide-diagnostics", Level: "critical", RelatedPath: "main.go"}
	warning := domain.CompanionIntervention{ID: "ide-command-failed-x", Level: "warning"}
	suggestion := domain.CompanionIntervention{ID: "ide-run-active", Level: "suggestion"}

	quiet := domain.CompanionConfig{Initiative: 20, QuestionStrictness: 80}
	if companion.ShouldIntervene(quiet, critical, obs, companion.InterveneContext{Now: now}) {
		t.Fatalf("stale critical must stay quiet when initiative < 40")
	}
	if companion.ShouldIntervene(quiet, warning, obs, companion.InterveneContext{Now: now}) {
		t.Fatalf("warning must stay quiet when initiative < 35")
	}
	if companion.ShouldIntervene(quiet, suggestion, obs, companion.InterveneContext{Now: now}) {
		t.Fatalf("suggestion must stay quiet when initiative < 70")
	}

	loud := domain.CompanionConfig{Initiative: 80, QuestionStrictness: 40}
	freshObs := []domain.IDEObservation{{
		ID: "diag", Kind: "diagnostic", Level: "error", Path: "main.go", Summary: "boom",
		ObservedAt: now, LastSeen: now,
	}}
	if !companion.ShouldIntervene(loud, critical, freshObs, companion.InterveneContext{Now: now, FocusPath: "main.go"}) {
		t.Fatalf("fresh critical must speak")
	}
	if !companion.ShouldIntervene(loud, suggestion, freshObs, companion.InterveneContext{Now: now}) {
		t.Fatalf("suggestion must speak when initiative high")
	}
}

func TestGateIDEInterventionsCapsByInitiative(t *testing.T) {
	now := time.Now().UTC()
	exit := 1
	obs := []domain.IDEObservation{
		{ID: "d1", Kind: "diagnostic", Level: "error", Path: "a.go", Summary: "err", ObservedAt: now.Add(-30 * time.Minute), FirstSeen: now.Add(-30 * time.Minute), LastSeen: now.Add(-30 * time.Minute), Count: 8},
		{ID: "t1", Kind: "terminal", Level: "error", Command: "go test", ExitCode: &exit, ObservedAt: now, LastSeen: now},
	}
	raw := companion.BuildIDEInterventionsGated(obs, domain.CompanionConfig{Initiative: 50, QuestionStrictness: 70}, companion.InterveneContext{Now: now, FocusPath: "a.go"})
	if len(raw) == 0 {
		t.Fatalf("expected gated interventions")
	}
	// Fresh focused errors promote to critical; quiet initiative keeps only that.
	minimal := companion.BuildIDEInterventionsGated(obs, domain.CompanionConfig{Initiative: 20, QuestionStrictness: 70}, companion.InterveneContext{Now: now, FocusPath: "a.go"})
	if len(minimal) > 1 {
		t.Fatalf("minimal initiative must cap IDE nudges: %#v", minimal)
	}
	if len(minimal) == 1 && minimal[0].Level != "critical" {
		t.Fatalf("minimal should keep critical first: %#v", minimal)
	}
	proactive := companion.BuildIDEInterventionsGated(obs, domain.CompanionConfig{Initiative: 85, QuestionStrictness: 40}, companion.InterveneContext{Now: now, FocusPath: "a.go"})
	if len(proactive) < len(minimal) {
		t.Fatalf("proactive should allow more than minimal: minimal=%d proactive=%d", len(minimal), len(proactive))
	}
}

func TestGatePrefersFocusMatchedDiagnostics(t *testing.T) {
	now := time.Now().UTC()
	obs := []domain.IDEObservation{
		{ID: "d1", Kind: "diagnostic", Level: "warning", Path: "other.go", Summary: "warn", ObservedAt: now, LastSeen: now},
	}
	cfg := domain.CompanionConfig{Initiative: 45, QuestionStrictness: 70}
	items := companion.BuildIDEInterventionsGated(obs, cfg, companion.InterveneContext{Now: now, FocusPath: "main.go"})
	for _, item := range items {
		if item.ID == "ide-diagnostics" && item.Level == "warning" {
			t.Fatalf("unfocused warning diagnostics should be gated at initiative 45: %#v", items)
		}
	}
	focused := companion.BuildIDEInterventionsGated(obs, cfg, companion.InterveneContext{Now: now, FocusPath: "other.go"})
	if len(focused) == 0 {
		t.Fatalf("focused warning diagnostics should surface")
	}
	if !strings.Contains(focused[0].Detail, "other.go") {
		t.Fatalf("focused detail=%#v", focused[0])
	}
}

func TestApplySpeakCooldownHidesSoftRepeatsKeepsCritical(t *testing.T) {
	now := time.Now().UTC()
	cfg := domain.CompanionConfig{Initiative: 80, QuestionStrictness: 40}
	items := []domain.CompanionIntervention{
		{ID: "ide-diagnostics", Level: "critical", Title: "diag", OccurrenceKey: "aaaaaaaaaaaaaaaa"},
		{ID: "ide-scm-dirty", Level: "suggestion", Title: "scm", OccurrenceKey: "bbbbbbbbbbbbbbbb"},
	}
	memory := companion.SpeakMemory{ByID: map[string]companion.SpeakRecord{
		"ide-diagnostics": {OccurrenceKey: "aaaaaaaaaaaaaaaa", SpokenAt: now.Add(-2 * time.Minute)},
		"ide-scm-dirty":   {OccurrenceKey: "bbbbbbbbbbbbbbbb", SpokenAt: now.Add(-2 * time.Minute)},
	}}
	out, next := companion.ApplySpeakCooldown(cfg, items, nil, companion.InterveneContext{Now: now}, memory)
	if len(out) != 1 || out[0].ID != "ide-diagnostics" {
		t.Fatalf("expected only critical IDE signal during soft cooldown: %#v", out)
	}
	if next.ByID["ide-diagnostics"].SpokenAt.Equal(now) {
		t.Fatalf("critical cooldown should keep original spokenAt")
	}
}

func TestDiagnosticsStayWarningUnlessFocusOrFresh(t *testing.T) {
	now := time.Now().UTC()
	stale := []domain.IDEObservation{{
		ID: "d1", Kind: "diagnostic", Level: "error", Path: "lib.go", Summary: "err",
		ObservedAt: now.Add(-40 * time.Minute), FirstSeen: now.Add(-40 * time.Minute), LastSeen: now.Add(-10 * time.Minute), Count: 4,
	}}
	cfg := domain.CompanionConfig{Initiative: 50, QuestionStrictness: 70}
	soft := companion.BuildIDEInterventionsGated(stale, cfg, companion.InterveneContext{Now: now})
	if len(soft) != 1 || soft[0].Level != "warning" {
		t.Fatalf("stale unfocused errors should be warning: %#v", soft)
	}
	focused := companion.BuildIDEInterventionsGated(stale, cfg, companion.InterveneContext{Now: now, FocusPath: "lib.go"})
	if len(focused) != 1 || focused[0].Level != "critical" {
		t.Fatalf("focused errors should promote to critical: %#v", focused)
	}
	fresh := []domain.IDEObservation{{
		ID: "d2", Kind: "diagnostic", Level: "error", Path: "new.go", Summary: "boom",
		ObservedAt: now, FirstSeen: now, LastSeen: now, Count: 1,
	}}
	novel := companion.BuildIDEInterventionsGated(fresh, cfg, companion.InterveneContext{Now: now})
	if len(novel) != 1 || novel[0].Level != "critical" {
		t.Fatalf("fresh novelty should be critical: %#v", novel)
	}
}

func TestNoveltyOccurrenceKeyStickyAcrossDetailThrash(t *testing.T) {
	now := time.Now().UTC()
	obs := []domain.IDEObservation{{
		ID: "d1", Kind: "diagnostic", Level: "error", Path: "main.go", Summary: "undefined", Line: 12,
		ObservedAt: now, FirstSeen: now, LastSeen: now, Count: 1,
		NoveltyHash: companion.ObservationNoveltyHash(domain.IDEObservation{
			Kind: "diagnostic", Level: "error", Path: "main.go", Line: 12, Summary: "undefined",
		}),
	}}
	cfg := domain.CompanionConfig{Initiative: 55, QuestionStrictness: 40}
	first := companion.BuildIDEInterventionsGated(obs, cfg, companion.InterveneContext{Now: now, FocusPath: "main.go"})
	if len(first) != 1 || first[0].OccurrenceKey == "" {
		t.Fatalf("expected diagnostics with occurrence key: %#v", first)
	}
	thrashed := first[0]
	thrashed.Detail = first[0].Detail + " · extra:main.go:99 · noise"
	thrashed.OccurrenceKey = ""
	if got := companion.InterventionOccurrenceKey(thrashed, obs); got != first[0].OccurrenceKey {
		t.Fatalf("occurrence key must ignore Detail thrash: %q vs %q", first[0].OccurrenceKey, got)
	}
	// Soft scm cools on same novelty, speaks again when novelty changes.
	scmObs := []domain.IDEObservation{{ID: "s1", Kind: "scm", Level: "info", Summary: "dirty", NoveltyHash: "scmhash01"}}
	scm := domain.CompanionIntervention{ID: "ide-scm-dirty", Level: "suggestion", Title: "scm"}
	scm.OccurrenceKey = companion.InterventionOccurrenceKey(scm, scmObs)
	cooled, _ := companion.ApplySpeakCooldown(cfg, []domain.CompanionIntervention{scm}, scmObs, companion.InterveneContext{Now: now}, companion.SpeakMemory{
		ByID: map[string]companion.SpeakRecord{"ide-scm-dirty": {OccurrenceKey: scm.OccurrenceKey, SpokenAt: now.Add(-time.Minute)}},
	})
	if len(cooled) != 0 {
		t.Fatalf("soft scm must hide during cooldown: %#v", cooled)
	}
	scmObs[0].NoveltyHash = "scmhash02"
	scm.OccurrenceKey = companion.InterventionOccurrenceKey(domain.CompanionIntervention{ID: "ide-scm-dirty", Level: "suggestion"}, scmObs)
	spoken, _ := companion.ApplySpeakCooldown(cfg, []domain.CompanionIntervention{scm}, scmObs, companion.InterveneContext{Now: now}, companion.SpeakMemory{
		ByID: map[string]companion.SpeakRecord{"ide-scm-dirty": {OccurrenceKey: companion.InterventionOccurrenceKey(domain.CompanionIntervention{ID: "ide-scm-dirty", Level: "suggestion"}, []domain.IDEObservation{{ID: "s1", Kind: "scm", NoveltyHash: "scmhash01"}}), SpokenAt: now.Add(-time.Minute)}},
	})
	if len(spoken) != 1 {
		t.Fatalf("changed novelty must speak again: %#v", spoken)
	}
}

func TestGateMergedInterventionsFiltersHubSuggestions(t *testing.T) {
	now := time.Now().UTC()
	cfg := domain.CompanionConfig{Initiative: 50, QuestionStrictness: 70}
	items := []domain.CompanionIntervention{
		{ID: "executions-waiting", Level: "suggestion", Title: "waiting", OccurrenceKey: "hubsuggest000001"},
		{ID: "changeset-conflict-x", Level: "critical", Title: "conflict", OccurrenceKey: "hubcritical00001"},
		{ID: "executions-failing", Level: "warning", Title: "failing", OccurrenceKey: "hubwarning000001"},
	}
	out, _ := companion.GateMergedInterventions(cfg, items, nil, companion.InterveneContext{Now: now}, companion.SpeakMemory{})
	ids := map[string]bool{}
	for _, item := range out {
		ids[item.ID] = true
	}
	if !ids["executions-waiting"] || !ids["changeset-conflict-x"] || !ids["executions-failing"] {
		t.Fatalf("default initiative keeps hub suggestion+warning+critical: %#v", out)
	}
	quiet := domain.CompanionConfig{Initiative: 25, QuestionStrictness: 70}
	quietOut, _ := companion.GateMergedInterventions(quiet, items, nil, companion.InterveneContext{Now: now}, companion.SpeakMemory{})
	for _, item := range quietOut {
		if item.ID == "executions-waiting" {
			t.Fatalf("hub suggestions must hide at initiative ≤30: %#v", quietOut)
		}
	}
	// Soft hub warning cools down; critical stays.
	cooled, _ := companion.GateMergedInterventions(cfg, items, nil, companion.InterveneContext{Now: now}, companion.SpeakMemory{
		ByID: map[string]companion.SpeakRecord{
			"executions-failing":   {OccurrenceKey: "hubwarning000001", SpokenAt: now.Add(-time.Minute)},
			"executions-waiting":   {OccurrenceKey: "hubsuggest000001", SpokenAt: now.Add(-time.Minute)},
			"changeset-conflict-x": {OccurrenceKey: "hubcritical00001", SpokenAt: now.Add(-time.Minute)},
		},
	})
	cooledIDs := map[string]bool{}
	for _, item := range cooled {
		cooledIDs[item.ID] = true
	}
	if cooledIDs["executions-failing"] || cooledIDs["executions-waiting"] {
		t.Fatalf("soft hub signals should cool down: %#v", cooled)
	}
	if !cooledIDs["changeset-conflict-x"] {
		t.Fatalf("critical hub signal must stay visible: %#v", cooled)
	}
}

func TestLatestObservationFocus(t *testing.T) {
	now := time.Now().UTC()
	focus := companion.LatestObservationFocus([]domain.IDEObservation{
		{FocusPath: "old.go", LastSeen: now.Add(-time.Hour)},
		{FocusPath: "new.go", LastSeen: now},
		{FocusPath: "", LastSeen: now.Add(time.Minute)},
	})
	if focus != "new.go" {
		t.Fatalf("focus=%q", focus)
	}
}

func TestSpeakMemoryTTLSurvivesTransientClear(t *testing.T) {
	now := time.Now().UTC()
	cfg := domain.CompanionConfig{Initiative: 80, QuestionStrictness: 40}
	scm := domain.CompanionIntervention{
		ID: "ide-scm-dirty", Level: "suggestion", Title: "scm", OccurrenceKey: "scmkey0000000001",
	}
	spoken := companion.SpeakMemory{ByID: map[string]companion.SpeakRecord{
		"ide-scm-dirty": {OccurrenceKey: "scmkey0000000001", SpokenAt: now.Add(-2 * time.Minute), Level: "suggestion"},
	}}
	// Transient clear: no active items, memory must survive TTL prune.
	_, cleared := companion.ApplySpeakCooldown(cfg, nil, nil, companion.InterveneContext{Now: now}, spoken)
	if _, ok := cleared.ByID["ide-scm-dirty"]; !ok {
		t.Fatalf("speak memory must survive transient clear within TTL: %#v", cleared)
	}
	// Same occurrence after flicker must stay quiet.
	out, _ := companion.ApplySpeakCooldown(cfg, []domain.CompanionIntervention{scm}, nil, companion.InterveneContext{Now: now}, cleared)
	if len(out) != 0 {
		t.Fatalf("soft scm must stay cooled after flicker: %#v", out)
	}
	// After 2× cooldown, inactive stamp may be pruned.
	expiredNow := now.Add(80 * time.Minute)
	_, pruned := companion.ApplySpeakCooldown(cfg, nil, nil, companion.InterveneContext{Now: expiredNow}, cleared)
	if _, ok := pruned.ByID["ide-scm-dirty"]; ok {
		t.Fatalf("expired speak memory must prune: %#v", pruned)
	}
}

func TestSCMFocusDoesNotMatchBatchFocusPathAlone(t *testing.T) {
	now := time.Now().UTC()
	cfg := domain.CompanionConfig{Initiative: 50, QuestionStrictness: 70}
	obs := []domain.IDEObservation{{
		ID: "s1", Kind: "scm", Level: "warning", Summary: "3 в git",
		Detail:    "changed=3; staged=0; untracked=0; unsaved=0; branch=main; ahead=0; behind=0",
		FocusPath: "main.go", ObservedAt: now, LastSeen: now,
	}}
	items := companion.BuildIDEInterventionsGated(obs, cfg, companion.InterveneContext{Now: now, FocusPath: "main.go"})
	for _, item := range items {
		if item.ID == "ide-scm-dirty" {
			t.Fatalf("SCM without RelatedPath must not pass focus gate via batch FocusPath: %#v", items)
		}
	}
	obs[0].Path = "main.go"
	focused := companion.BuildIDEInterventionsGated(obs, cfg, companion.InterveneContext{Now: now, FocusPath: "main.go"})
	if len(focused) == 0 || focused[0].ID != "ide-scm-dirty" {
		t.Fatalf("SCM with matching Path should surface: %#v", focused)
	}
}
