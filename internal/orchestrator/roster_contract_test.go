package orchestrator

import (
	"local-agent-workbench/internal/domain"
	"strings"
	"testing"
)

func TestMasterRosterExposesSelectableIDs(t *testing.T) {
	w := masterWorldPrompt([]domain.ProjectAgent{{ID: "exact-id-712", Name: "Developer", AllowedTools: []string{"read_file"}}}, nil, nil, nil, 0, Situation{})
	for _, v := range []string{"exact-id-712", "Developer", "read_file"} {
		if !strings.Contains(w, v) {
			t.Fatalf("missing %s", v)
		}
	}
}
