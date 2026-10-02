package environment

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"local-agent-workbench/internal/domain"
)

var criterionCwd = regexp.MustCompile(`(?:^|&&\s*)cd\s+(?:"([^"]+)"|'([^']+)'|([^\s;&|]+))\s*&&`)

// DependencyPlanFor discovers the projects named by acceptance commands. It
// resolves their nearest manifest, rather than installing in an opened parent folder.
func DependencyPlanFor(root string, criteria []domain.AcceptanceCriterion) *domain.DependencyPlan {
	dirs := map[string]bool{}
	add := func(cwd string) {
		cwd = strings.TrimPrefix(strings.ReplaceAll(cwd, `\`, "/"), "./")
		if !domain.DependencyPathValid(cwd, true) {
			return
		}
		for {
			abs := filepath.Join(root, filepath.FromSlash(cwd))
			if exists(abs, "package.json") || exists(abs, "go.mod") || exists(abs, "composer.json") {
				dirs[cwd] = true
				return
			}
			if cwd == "" || cwd == "." {
				return
			}
			cwd = path.Dir(cwd)
			if cwd == "." {
				cwd = ""
			}
		}
	}
	for _, criterion := range criteria {
		if criterion.Kind == "manual" {
			continue
		}
		var args struct {
			Command string `json:"command"`
			Cwd     string `json:"cwd"`
		}
		if json.Unmarshal(criterion.Arguments, &args) != nil {
			continue
		}
		cwd := args.Cwd
		invalid := !domain.DependencyPathValid(cwd, true)
		for _, match := range criterionCwd.FindAllStringSubmatch(args.Command, -1) {
			for _, value := range match[1:] {
				if value != "" {
					if !domain.DependencyPathValid(value, true) {
						invalid = true
						break
					}
					cwd = path.Join(cwd, value)
					break
				}
			}
		}
		if !invalid {
			add(cwd)
		}
	}
	if len(dirs) == 0 {
		for _, dir := range manifestDirs(root) {
			add(strings.TrimSuffix(dir.prefix, "/"))
		}
	}
	keys := make([]string, 0, len(dirs))
	for dir := range dirs {
		keys = append(keys, dir)
	}
	sort.Strings(keys)
	plan := &domain.DependencyPlan{Version: "1"}
	for _, cwd := range keys {
		abs := filepath.Join(root, filepath.FromSlash(cwd))
		makeProject := func(manager, command string, manifests, expected []string) {
			for i := range manifests {
				manifests[i] = path.Join(cwd, manifests[i])
			}
			for i := range expected {
				expected[i] = path.Join(cwd, expected[i])
			}
			plan.Projects = append(plan.Projects, domain.DependencyProject{Cwd: cwd, Manager: manager,
				Commands: []domain.SetupCommand{{Command: command, TimeoutSeconds: 600}}, ManifestPaths: manifests, ExpectedPaths: expected})
		}
		if exists(abs, "package.json") {
			expected := []string(nil)
			var manifest struct {
				Dependencies    map[string]any
				DevDependencies map[string]any
			}
			if data, err := os.ReadFile(filepath.Join(abs, "package.json")); err == nil && json.Unmarshal(data, &manifest) == nil && len(manifest.Dependencies)+len(manifest.DevDependencies) > 0 {
				expected = []string{"node_modules"}
			}
			makeProject("npm", "npm ci --include=dev --no-audit --no-fund", []string{"package.json", "package-lock.json"}, expected)
		}
		if exists(abs, "go.mod") {
			manifests := []string{"go.mod"}
			if exists(abs, "go.sum") {
				manifests = append(manifests, "go.sum")
			}
			command := "go mod download && go mod verify"
			if exists(abs, "vendor/modules.txt") {
				command = "go mod download && go mod verify && go mod vendor"
			}
			makeProject("go", command, manifests, nil)
		}
		if exists(abs, "composer.json") {
			makeProject("composer", "composer install --no-interaction --prefer-dist", []string{"composer.json", "composer.lock"}, []string{"vendor/autoload.php"})
		}
	}
	if len(plan.Projects) == 0 {
		return nil
	}
	return plan
}
