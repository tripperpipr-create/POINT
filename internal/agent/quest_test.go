package agent

import "testing"

func TestComposeQuestTaskLegacyPassthrough(t *testing.T) {
	if got := ComposeQuestTask("  Explain architecture  ", "", nil, nil); got != "Explain architecture" {
		t.Fatalf("got %q", got)
	}
}

func TestComposeQuestTaskStructuredSections(t *testing.T) {
	got := ComposeQuestTask("Fix flaky test", "Stable CI", []string{"go test ./... passes", "", "no flake"}, []string{"do not change API"})
	want := "ЗАДАЧА:\nFix flaky test\n\nЦЕЛЬ:\nStable CI\n\nКРИТЕРИИ ГОТОВНОСТИ:\n- go test ./... passes\n- no flake\n\nОГРАНИЧЕНИЯ:\n- do not change API"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
