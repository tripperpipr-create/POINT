package environment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/tools"
)

type manifestRule struct {
	Path      string
	Toolchain string
	Version   string
	Registry  []string
	Install   *domain.EnvironmentCommand
	Test      *domain.EnvironmentCommand
}

var manifestRules = []manifestRule{
	// Composer metadata is Packagist; prefer-dist zipballs often come from api.github.com /
	// codeload.github.com / objects.githubusercontent.com (not covered by github.com alone).
	{Path: "composer.json", Toolchain: "php", Version: "8.3", Registry: []string{"repo.packagist.org", "packagist.org", "github.com", "api.github.com", "codeload.github.com", "objects.githubusercontent.com"}, Install: command("dependencies", "Install Composer dependencies", "composer", "install", "--no-interaction", "--prefer-dist"), Test: nil},
	{Path: "package.json", Toolchain: "node", Version: "24", Registry: []string{"registry.npmjs.org"}, Install: command("dependencies", "Install Node dependencies", "npm", "ci"), Test: command("tests", "Run Node tests", "npm", "test")},
	{Path: "go.mod", Toolchain: "go", Version: "1.26", Registry: []string{"proxy.golang.org", "sum.golang.org"}, Test: command("tests", "Run Go tests", "go", "test", "./...")},
	{Path: "pyproject.toml", Toolchain: "python", Version: "3.13", Registry: []string{"pypi.org", "files.pythonhosted.org"}, Install: command("dependencies", "Install Python project", "python", "-m", "pip", "install", "."), Test: command("tests", "Run Python tests", "python", "-m", "pytest")},
	{Path: "requirements.txt", Toolchain: "python", Version: "3.13", Registry: []string{"pypi.org", "files.pythonhosted.org"}, Install: command("dependencies", "Install Python requirements", "python", "-m", "pip", "install", "-r", "requirements.txt"), Test: command("tests", "Run Python tests", "python", "-m", "pytest")},
	{Path: "Cargo.toml", Toolchain: "rust", Version: "stable", Registry: []string{"crates.io", "index.crates.io", "static.crates.io"}, Test: command("tests", "Run Rust tests", "cargo", "test")},
	{Path: "pom.xml", Toolchain: "java", Version: "21", Registry: []string{"repo.maven.apache.org"}, Test: command("tests", "Run Maven tests", "mvn", "test")},
	{Path: "build.gradle", Toolchain: "java", Version: "21", Registry: []string{"plugins.gradle.org", "repo.maven.apache.org"}, Test: command("tests", "Run Gradle tests", "gradle", "test")},
	{Path: "global.json", Toolchain: "dotnet", Version: "8", Registry: []string{"api.nuget.org"}, Test: command("tests", "Run .NET tests", "dotnet", "test")},
}

// RegistryHosts returns the package registries of a toolchain from the same
// table the manifest analyzer reads, so a greenfield brief that picks a
// language gets the network an existing project with that manifest would get.
func RegistryHosts(toolchain string) []string {
	seen := map[string]bool{}
	hosts := []string{}
	for _, rule := range manifestRules {
		if rule.Toolchain != toolchain {
			continue
		}
		for _, host := range rule.Registry {
			if !seen[host] {
				seen[host] = true
				hosts = append(hosts, host)
			}
		}
	}
	sort.Strings(hosts)
	return hosts
}

// ManifestToolchains lists the toolchains whose manifests exist under root, or,
// when root has none, under the Git projects nested in it.
func ManifestToolchains(root string) []string {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	seen := map[string]bool{}
	toolchains := []string{}
	for _, dir := range manifestDirs(root) {
		for _, rule := range manifestRules {
			if !seen[rule.Toolchain] && exists(dir.path, rule.Path) {
				seen[rule.Toolchain] = true
				toolchains = append(toolchains, rule.Toolchain)
			}
		}
		if !seen["dotnet"] && hasCSProj(dir.path) {
			seen["dotnet"] = true
			toolchains = append(toolchains, "dotnet")
		}
	}
	sort.Strings(toolchains)
	return toolchains
}

type manifestDir struct {
	path   string
	prefix string // "" for the root, "cf-vue-apps/" for a nested project
}

// manifestDirs — где искать манифесты. Человек открывает папку вроде «фронт
// cf», в которой живут отдельные Git-проекты (cf-pages, cf-vue-apps), а сама
// она ни манифеста, ни CI не несёт. Анализ одного корня отдавал такой папке
// образ по умолчанию с Node 24 и npm 12, хотя CI проекта требует node:20 и
// npm 10 (E1/E2, Q04). Вложенные проекты читаются, только когда корень пуст:
// у обычного проекта результат прежний.
func manifestDirs(root string) []manifestDir {
	if rootHasManifest(root) {
		return []manifestDir{{path: root}}
	}
	dirs := []manifestDir{}
	for _, repo := range tools.DiscoverGitRepos(root) {
		if repo == "." {
			continue
		}
		dirs = append(dirs, manifestDir{path: filepath.Join(root, filepath.FromSlash(repo)), prefix: repo + "/"})
	}
	if len(dirs) == 0 {
		return []manifestDir{{path: root}}
	}
	return dirs
}

func rootHasManifest(root string) bool {
	for _, rule := range manifestRules {
		if exists(root, rule.Path) {
			return true
		}
	}
	for _, name := range append(projectRuntimeFiles, ".nvmrc", ".node-version", ".python-version", ".tool-versions") {
		if exists(root, name) {
			return true
		}
	}
	return hasCSProj(root)
}

var projectRuntimeFiles = []string{"Dockerfile", "docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml", ".devcontainer/devcontainer.json"}

func command(id, purpose, program string, arguments ...string) *domain.EnvironmentCommand {
	return &domain.EnvironmentCommand{ID: id, Purpose: purpose, Program: program, Arguments: arguments, ProvidesVerification: id == "tests"}
}

func Analyze(root, workspaceID string) domain.EnvironmentPlan {
	now := time.Now().UTC()
	plan := domain.EnvironmentPlan{ID: domain.NewID("env"), WorkspaceID: workspaceID, Strategy: "managed", CreatedAt: now}
	plan.Runtime = domain.RuntimeSpec{ID: domain.NewID("runtime"), Kind: "managed", Toolchains: map[string]string{}, VersionSources: map[string]string{}}
	seenCommands, hosts := map[string]bool{}, map[string]bool{}
	dirs := manifestDirs(root)
	for _, dir := range dirs {
		analyzeManifestDir(&plan, dir, hosts, seenCommands)
	}
	for host := range hosts {
		plan.NetworkHosts = append(plan.NetworkHosts, host)
	}
	sort.Strings(plan.NetworkHosts)
	sort.Strings(plan.Runtime.ManifestPaths)
	for _, dir := range dirs {
		applyDetectedVersions(dir.path, dir.prefix, &plan.Runtime)
	}
	if len(plan.Runtime.Toolchains) == 0 && len(plan.Services) == 0 {
		plan.Strategy, plan.Runtime.Kind = "generated", "generated"
		plan.Blockers = append(plan.Blockers, "No recognized project runtime; generate and probe a quest-scoped RuntimeSpec before execution")
	}
	plan.Runtime.DependencyLock = dependencyDigest(dirs)
	ApplyManagedRuntimePack(&plan)
	return plan
}

// analyzeManifestDir добавляет в план манифесты одного каталога. У вложенного
// проекта команды установки и проверки не берутся: у EnvironmentCommand нет
// рабочего каталога, и `npm ci` в корне открытой папки упал бы. Проверки для
// такой папки приходят из наряда. Dockerfile и compose вложенного проекта тоже
// не переключают стратегию: сервис описан путём от его собственного корня.
func analyzeManifestDir(plan *domain.EnvironmentPlan, dir manifestDir, hosts, seenCommands map[string]bool) {
	nested := dir.prefix != ""
	for _, rule := range manifestRules {
		if !exists(dir.path, rule.Path) {
			continue
		}
		plan.Runtime.ManifestPaths = append(plan.Runtime.ManifestPaths, dir.prefix+rule.Path)
		// Умолчание не перебивает версию, уже выбранную соседним проектом:
		// это не требование проекта, а догадка Point.
		if _, known := plan.Runtime.Toolchains[rule.Toolchain]; !known || !nested {
			plan.Runtime.Toolchains[rule.Toolchain] = rule.Version
			plan.Runtime.VersionSources[rule.Toolchain] = "Point default"
		}
		for _, host := range rule.Registry {
			hosts[host] = true
		}
		if nested {
			continue
		}
		for _, item := range []*domain.EnvironmentCommand{rule.Install, rule.Test} {
			if rule.Path == "composer.json" && item == rule.Test {
				item = phpVerificationCommand(dir.path)
			}
			if item != nil && !seenCommands[item.ID+"\x00"+item.Program] {
				plan.Commands = append(plan.Commands, *item)
				seenCommands[item.ID+"\x00"+item.Program] = true
			}
		}
	}
	if !nested {
		for _, name := range projectRuntimeFiles {
			if exists(dir.path, name) {
				plan.Strategy, plan.Runtime.Kind = "project", "project"
				plan.Runtime.ManifestPaths = append(plan.Runtime.ManifestPaths, name)
				if strings.Contains(name, "compose") {
					plan.Services = append(plan.Services, domain.EnvironmentService{ID: "project", Kind: "compose", ComposeFile: name})
				}
			}
		}
		if exists(dir.path, "Makefile") {
			plan.Runtime.ManifestPaths = append(plan.Runtime.ManifestPaths, "Makefile")
		}
	}
	if plan.Runtime.Toolchains["dotnet"] == "" && hasCSProj(dir.path) {
		plan.Runtime.Toolchains["dotnet"] = "8"
		plan.Runtime.VersionSources["dotnet"] = "Point default"
		plan.Runtime.ManifestPaths = append(plan.Runtime.ManifestPaths, dir.prefix+"*.csproj")
		hosts["api.nuget.org"] = true
		if !nested && !seenCommands["tests\x00dotnet"] {
			plan.Commands = append(plan.Commands, *command("tests", "Run .NET tests", "dotnet", "test"))
			seenCommands["tests\x00dotnet"] = true
		}
	}
}

func exists(root, relative string) bool {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative)))
	return err == nil && !info.IsDir()
}

func hasCSProj(root string) bool {
	matches, err := filepath.Glob(filepath.Join(root, "*.csproj"))
	return err == nil && len(matches) > 0
}

func dependencyDigest(dirs []manifestDir) string {
	h := sha256.New()
	for _, dir := range dirs {
		for _, name := range []string{"composer.lock", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "go.sum", "Cargo.lock", "poetry.lock", "requirements.txt"} {
			data, err := os.ReadFile(filepath.Join(dir.path, name))
			if err == nil {
				h.Write([]byte(dir.prefix + name))
				h.Write(data)
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func digest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
