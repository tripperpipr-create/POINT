package environment

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

// Q04, форма E1/E2: открыта папка «фронт cf» без своего манифеста, внутри
// живут отдельные Git-проекты. Анализ одного корня отдавал ей образ по
// умолчанию с Node 24 и npm 12, хотя CI проекта требует node:20.

func writeNestedFileForTest(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func nestedRepoForTest(t *testing.T, root, name, ci, packageJSON string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, name, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeNestedFileForTest(t, root, name+"/package.json", packageJSON)
	if ci != "" {
		writeNestedFileForTest(t, root, name+"/.gitlab-ci.yml", ci)
	}
}

func TestAnalyzeReadsNestedGitProjectsWhenRootHasNoManifest(t *testing.T) {
	root := t.TempDir()
	nestedRepoForTest(t, root, "cf-vue-apps", "verify:\n  image: node:20\n  script: npm ci\n", `{"name":"cf-vue-apps","engines":{"npm":"10"}}`)
	plan := Analyze(root, "ws-1")
	if plan.Runtime.Toolchains["node"] != "20" || plan.Runtime.VersionSources["node"] != "cf-vue-apps/.gitlab-ci.yml" {
		t.Fatalf("версия node вложенного проекта не найдена: %#v %#v", plan.Runtime.Toolchains, plan.Runtime.VersionSources)
	}
	if plan.Runtime.Toolchains["npm"] != "10" {
		t.Fatalf("npm из engines вложенного проекта потерян: %#v", plan.Runtime.Toolchains)
	}
	if plan.Runtime.Image != Node20ManagedImage {
		t.Fatalf("папка с проектом на node:20 получила образ %q", plan.Runtime.Image)
	}
	if plan.Strategy != "managed" || !slices.Contains(plan.Runtime.ManifestPaths, "cf-vue-apps/package.json") {
		t.Fatalf("strategy=%s manifests=%v", plan.Strategy, plan.Runtime.ManifestPaths)
	}
	if !slices.Contains(plan.NetworkHosts, "registry.npmjs.org") {
		t.Fatalf("реестр npm не попал в сеть песочницы: %v", plan.NetworkHosts)
	}
	// `npm ci` в корне открытой папки упал бы: у вложенного проекта команды
	// берутся из наряда.
	if len(plan.Commands) != 0 {
		t.Fatalf("команды вложенного проекта запускались бы из корня: %#v", plan.Commands)
	}
	if len(plan.Runtime.VersionConflicts) != 0 {
		t.Fatalf("лишний конфликт: %v", plan.Runtime.VersionConflicts)
	}
	requirements := RuntimeRequirementsForWorkOrder(&domain.WorkOrder{Sandbox: plan.Runtime})
	if requirements.ToolVersions["node"] != "20" || requirements.ToolVersions["npm"] != "10" || !slices.Contains(requirements.CandidateImages, Node20ManagedImage) {
		t.Fatalf("требования песочницы: %#v", requirements)
	}
}

func TestAnalyzeRecordsVersionConflictBetweenNestedProjects(t *testing.T) {
	root := t.TempDir()
	nestedRepoForTest(t, root, "cf-pages", "build:\n  image: node:22\n", `{"name":"cf-pages"}`)
	nestedRepoForTest(t, root, "cf-vue-apps", "verify:\n  image: node:20\n", `{"name":"cf-vue-apps"}`)
	plan := Analyze(root, "ws-1")
	if len(plan.Runtime.VersionConflicts) != 1 || !strings.HasPrefix(plan.Runtime.VersionConflicts[0], "node: ") ||
		!strings.Contains(plan.Runtime.VersionConflicts[0], "cf-pages/.gitlab-ci.yml") || !strings.Contains(plan.Runtime.VersionConflicts[0], "cf-vue-apps/.gitlab-ci.yml") {
		t.Fatalf("расхождение CI двух проектов не записано: %v", plan.Runtime.VersionConflicts)
	}
	requirements := RuntimeRequirementsForWorkOrder(&domain.WorkOrder{Sandbox: plan.Runtime})
	if len(requirements.VersionConflicts) != 1 {
		t.Fatalf("неразрешённый конфликт не дошёл до песочницы: %#v", requirements)
	}
	// Выбор человека в ревизии наряда снимает конфликт.
	spec := plan.Runtime
	spec.VersionSources = map[string]string{"node": UserSelectedVersion}
	if got := RuntimeRequirementsForWorkOrder(&domain.WorkOrder{Sandbox: spec}); len(got.VersionConflicts) != 0 {
		t.Fatalf("выбор человека не снял конфликт: %v", got.VersionConflicts)
	}
	if tools := ConflictedTools(plan.Runtime); !tools["node"] || len(tools) != 1 {
		t.Fatalf("ConflictedTools=%v", tools)
	}
}

// Умолчание Point не спорит с требованием соседнего проекта: проект без CI
// не создаёт конфликта и не перебивает node:20 догадкой «24».
func TestNestedDefaultVersionDoesNotOverrideSiblingRequirement(t *testing.T) {
	root := t.TempDir()
	nestedRepoForTest(t, root, "a-tools", "", `{"name":"tools"}`)
	nestedRepoForTest(t, root, "cf-vue-apps", "verify:\n  image: node:20\n", `{"name":"cf-vue-apps"}`)
	plan := Analyze(root, "ws-1")
	if plan.Runtime.Toolchains["node"] != "20" || len(plan.Runtime.VersionConflicts) != 0 {
		t.Fatalf("toolchains=%v conflicts=%v", plan.Runtime.Toolchains, plan.Runtime.VersionConflicts)
	}
}

func TestAnalyzeKeepsRootProjectWhenRootHasManifest(t *testing.T) {
	root := t.TempDir()
	writeNestedFileForTest(t, root, "package.json", `{"name":"root"}`)
	nestedRepoForTest(t, root, "vendor-copy", "verify:\n  image: node:20\n", `{"name":"copy"}`)
	plan := Analyze(root, "ws-1")
	if plan.Runtime.Toolchains["node"] != "24" || slices.Contains(plan.Runtime.ManifestPaths, "vendor-copy/package.json") {
		t.Fatalf("корневой проект читает вложенный: %#v %v", plan.Runtime.Toolchains, plan.Runtime.ManifestPaths)
	}
	if len(plan.Commands) == 0 {
		t.Fatal("команды корневого проекта пропали")
	}
	if got := ManifestToolchains(root); !slices.Equal(got, []string{"node"}) {
		t.Fatalf("ManifestToolchains=%v", got)
	}
}

func TestManifestToolchainsSeeNestedProjects(t *testing.T) {
	root := t.TempDir()
	nestedRepoForTest(t, root, "cf-vue-apps", "", `{"name":"cf-vue-apps"}`)
	if err := os.MkdirAll(filepath.Join(root, "api", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeNestedFileForTest(t, root, "api/go.mod", "module api\n\ngo 1.26\n")
	if got := ManifestToolchains(root); !slices.Equal(got, []string{"go", "node"}) {
		t.Fatalf("ManifestToolchains=%v", got)
	}
}

func TestNPM10WithoutNodeVersionOffersNodePacks(t *testing.T) {
	got := RuntimeRequirementsForWorkOrder(&domain.WorkOrder{Sandbox: domain.RuntimeSpec{Toolchains: map[string]string{"npm": "10"}}})
	if !slices.Contains(got.CandidateImages, Node20ManagedImage) || !slices.Contains(got.CandidateImages, Node22ManagedImage) {
		t.Fatalf("npm 10 без ноды не получил пакетов с npm 10: %v", got.CandidateImages)
	}
	if other := RuntimeRequirementsForWorkOrder(&domain.WorkOrder{Sandbox: domain.RuntimeSpec{Toolchains: map[string]string{"npm": "12"}}}); slices.Contains(other.CandidateImages, Node20ManagedImage) {
		t.Fatalf("npm 12 получил образ с npm 10: %v", other.CandidateImages)
	}
}
