package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDependencyPlanPathsAndCompatibility(t *testing.T) {
	for _, p := range []string{"../x", "/tmp/x", "C:/x", `a\..\x`, "a//x", "a/../../x"} {
		if DependencyPathValid(strings.ReplaceAll(p, `\`, "/"), false) {
			t.Errorf("accepted %q", p)
		}
	}
	data, _ := json.Marshal(WorkOrder{})
	if strings.Contains(string(data), "dependencyPlan") {
		t.Fatal("legacy digest changed")
	}
	plan := NormalizeDependencyPlan(&DependencyPlan{Version: "1", Projects: []DependencyProject{{Cwd: ".", Manager: "npm", Commands: []SetupCommand{{Command: "npm ci"}}, ManifestPaths: []string{"package.json", "package-lock.json"}}}})
	if err := ValidateDependencyPlan(plan); err != nil {
		t.Fatal(err)
	}
	duplicate := *plan
	duplicate.Projects = append([]DependencyProject(nil), plan.Projects[0], plan.Projects[0])
	duplicate.Projects[0].Cwd = "a/./b"
	duplicate.Projects[1].Cwd = "a/b"
	if ValidateDependencyPlan(&duplicate) == nil {
		t.Fatal("directory alias installed twice")
	}
	plan.Projects[0].Commands[0].Cwd = "../escape"
	if ValidateDependencyPlan(plan) == nil {
		t.Fatal("unsafe command cwd accepted")
	}
}
