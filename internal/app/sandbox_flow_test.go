package app

import (
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/filepolicy"
	"testing"
)

func TestFlowSandboxChoiceSurvivesEnvironmentChange(t *testing.T) {
	t.Setenv("POINT_SANDBOX_WORKSPACE", "volume")
	selected, err := currentSandboxChoice()
	if err != nil {
		t.Fatal(err)
	}
	run := domain.FlowRun{Snapshot: map[string]any{"sandboxWorkspace": map[string]any{"storageMode": selected.StorageMode, "fileRulesVersion": selected.FileRulesVersion}}}
	t.Setenv("POINT_SANDBOX_WORKSPACE", "bind")
	got := flowSandboxOptions(run, domain.StageRoleImplement)
	if got.StorageMode != "volume" || got.FileRulesVersion != filepolicy.Current {
		t.Fatal("flow changed storage")
	}
	legacy := flowSandboxOptions(domain.FlowRun{}, domain.StageRoleAccept)
	if legacy.StorageMode != "bind" || legacy.FileRulesVersion != filepolicy.Legacy {
		t.Fatal("migrated flow changed rules")
	}
}
