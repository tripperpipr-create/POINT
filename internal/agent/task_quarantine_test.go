package agent

import (
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

func TestQuarantinedBriefNeverExecutes(t *testing.T) {
	if err := refuseQuarantinedBrief(&domain.TaskBrief{Goal: "ok"}); err != nil {
		t.Fatalf("valid brief refused: %v", err)
	}
	err := refuseQuarantinedBrief(&domain.TaskBrief{Goal: "stale", Quarantine: "task brief approval does not match its version and content"})
	if err == nil || !strings.Contains(err.Error(), "утвердят заново") {
		t.Fatalf("quarantined brief was allowed to execute: %v", err)
	}
	if copied := copyExecutionBrief(&domain.TaskBrief{Quarantine: "x"}); copied.Quarantine != "" {
		t.Fatal("the guard must run before the JSON copy, which drops the marker")
	}
}
