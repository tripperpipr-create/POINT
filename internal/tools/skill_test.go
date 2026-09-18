package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

func TestReadSkillResolvesNameAndRejectsUnknown(t *testing.T) {
	tool := ReadSkill{Skills: []domain.SkillRuntime{{
		ID: "skill-code-review", Name: "Code Review", Description: "Review diffs",
		Instructions: "Inspect the change set.", RequiredTools: []string{"read_file"},
		References: []string{"docs/review.md"},
	}}}
	result := tool.Execute(t.Context(), json.RawMessage(`{"name":"code review"}`))
	if !result.OK {
		t.Fatalf("name lookup failed: %#v", result.Error)
	}
	var output struct {
		ID           string   `json:"id"`
		Instructions string   `json:"instructions"`
		References   []string `json:"references"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if output.ID != "skill-code-review" || output.Instructions != "Inspect the change set." || len(output.References) != 1 {
		t.Fatalf("output=%#v", output)
	}
	unknown := tool.Execute(t.Context(), json.RawMessage(`{"id":"skill-missing"}`))
	if unknown.OK || unknown.Error == nil || unknown.Error.Code != "skill_not_equipped" || !strings.Contains(unknown.Error.Hint, "Code Review") {
		t.Fatalf("unknown=%#v", unknown)
	}
}
