package environment

import (
	"encoding/json"
	"local-agent-workbench/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDependencyPlanForProjects(t *testing.T) {
	for _, tc := range []struct{ name, cwd, manifest, lock, manager, want string }{
		{"node", "", "package.json", "package-lock.json", "npm", "npm ci --include=dev"},
		{"nested", "lk-backend/source", "package.json", "package-lock.json", "npm", "npm ci --include=dev"},
		{"go", "", "go.mod", "go.sum", "go", "go mod download && go mod verify"},
		{"vendor", "", "go.mod", "vendor/modules.txt", "go", "go mod vendor"},
		{"composer", "php/source", "composer.json", "composer.lock", "composer", "composer install"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, f := range []string{tc.manifest, tc.lock} {
				p := filepath.Join(root, tc.cwd, f)
				os.MkdirAll(filepath.Dir(p), 0755)
				os.WriteFile(p, []byte("{}"), 0600)
			}
			command := "verify"
			if tc.cwd != "" {
				command = "cd " + tc.cwd + " && verify"
			}
			args, _ := json.Marshal(map[string]string{"command": command})
			plan := DependencyPlanFor(root, []domain.AcceptanceCriterion{{Arguments: args}})
			if plan == nil || len(plan.Projects) != 1 || plan.Projects[0].Manager != tc.manager || plan.Projects[0].Cwd != tc.cwd || !strings.Contains(plan.Projects[0].Commands[0].Command, tc.want) {
				t.Fatalf("unexpected plan: %+v", plan)
			}
			if err := domain.ValidateDependencyPlan(plan); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestDependencyPlanNeverScaffolds(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "composer.json"), []byte("{}"), 0600)
	p := DependencyPlanFor(root, nil)
	if p == nil || strings.Contains(p.Projects[0].Commands[0].Command, "create-project") {
		t.Fatal(p)
	}
}
