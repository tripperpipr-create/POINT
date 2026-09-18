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
	{Path: "composer.json", Toolchain: "php", Version: ">=8.3", Registry: []string{"repo.packagist.org", "packagist.org", "github.com", "api.github.com", "codeload.github.com", "objects.githubusercontent.com"}, Install: command("dependencies", "Install Composer dependencies", "composer", "install", "--no-interaction", "--prefer-dist"), Test: nil},
	{Path: "package.json", Toolchain: "node", Version: "24", Registry: []string{"registry.npmjs.org"}, Install: command("dependencies", "Install Node dependencies", "npm", "ci"), Test: command("tests", "Run Node tests", "npm", "test")},
	{Path: "go.mod", Toolchain: "go", Version: "1.25", Registry: []string{"proxy.golang.org", "sum.golang.org"}, Test: command("tests", "Run Go tests", "go", "test", "./...")},
	{Path: "pyproject.toml", Toolchain: "python", Version: "3.13", Registry: []string{"pypi.org", "files.pythonhosted.org"}, Install: command("dependencies", "Install Python project", "python", "-m", "pip", "install", "."), Test: command("tests", "Run Python tests", "python", "-m", "pytest")},
	{Path: "requirements.txt", Toolchain: "python", Version: "3.13", Registry: []string{"pypi.org", "files.pythonhosted.org"}, Install: command("dependencies", "Install Python requirements", "python", "-m", "pip", "install", "-r", "requirements.txt"), Test: command("tests", "Run Python tests", "python", "-m", "pytest")},
	{Path: "Cargo.toml", Toolchain: "rust", Version: "stable", Registry: []string{"crates.io", "index.crates.io", "static.crates.io"}, Test: command("tests", "Run Rust tests", "cargo", "test")},
	{Path: "pom.xml", Toolchain: "java", Version: "21", Registry: []string{"repo.maven.apache.org"}, Test: command("tests", "Run Maven tests", "mvn", "test")},
	{Path: "build.gradle", Toolchain: "java", Version: "21", Registry: []string{"plugins.gradle.org", "repo.maven.apache.org"}, Test: command("tests", "Run Gradle tests", "gradle", "test")},
	{Path: "global.json", Toolchain: "dotnet", Version: "8", Registry: []string{"api.nuget.org"}, Test: command("tests", "Run .NET tests", "dotnet", "test")},
}

func command(id, purpose, program string, arguments ...string) *domain.EnvironmentCommand {
	return &domain.EnvironmentCommand{ID: id, Purpose: purpose, Program: program, Arguments: arguments, ProvidesVerification: id == "tests"}
}

func Analyze(root, workspaceID string) domain.EnvironmentPlan {
	now := time.Now().UTC()
	plan := domain.EnvironmentPlan{ID: domain.NewID("env"), WorkspaceID: workspaceID, Strategy: "managed", CreatedAt: now}
	plan.Runtime = domain.RuntimeSpec{ID: domain.NewID("runtime"), Kind: "managed", Toolchains: map[string]string{}}
	seenCommands, hosts := map[string]bool{}, map[string]bool{}
	for _, rule := range manifestRules {
		if !exists(root, rule.Path) {
			continue
		}
		plan.Runtime.ManifestPaths = append(plan.Runtime.ManifestPaths, rule.Path)
		plan.Runtime.Toolchains[rule.Toolchain] = rule.Version
		for _, host := range rule.Registry {
			hosts[host] = true
		}
		for _, item := range []*domain.EnvironmentCommand{rule.Install, rule.Test} {
			if rule.Path == "composer.json" && item == rule.Test {
				item = phpVerificationCommand(root)
			}
			if item != nil && !seenCommands[item.ID+"\x00"+item.Program] {
				plan.Commands = append(plan.Commands, *item)
				seenCommands[item.ID+"\x00"+item.Program] = true
			}
		}
	}
	for _, name := range []string{"Dockerfile", "docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml", ".devcontainer/devcontainer.json"} {
		if exists(root, name) {
			plan.Strategy, plan.Runtime.Kind = "project", "project"
			plan.Runtime.ManifestPaths = append(plan.Runtime.ManifestPaths, name)
			if strings.Contains(name, "compose") {
				plan.Services = append(plan.Services, domain.EnvironmentService{ID: "project", Kind: "compose", ComposeFile: name})
			}
		}
	}
	if exists(root, "Makefile") {
		plan.Runtime.ManifestPaths = append(plan.Runtime.ManifestPaths, "Makefile")
	}
	if plan.Runtime.Toolchains["dotnet"] == "" && hasCSProj(root) {
		plan.Runtime.Toolchains["dotnet"] = "8"
		plan.Runtime.ManifestPaths = append(plan.Runtime.ManifestPaths, "*.csproj")
		hosts["api.nuget.org"] = true
		if !seenCommands["tests\x00dotnet"] {
			plan.Commands = append(plan.Commands, *command("tests", "Run .NET tests", "dotnet", "test"))
			seenCommands["tests\x00dotnet"] = true
		}
	}
	for host := range hosts {
		plan.NetworkHosts = append(plan.NetworkHosts, host)
	}
	sort.Strings(plan.NetworkHosts)
	sort.Strings(plan.Runtime.ManifestPaths)
	if len(plan.Runtime.Toolchains) == 0 && len(plan.Services) == 0 {
		plan.Strategy, plan.Runtime.Kind = "generated", "generated"
		plan.Blockers = append(plan.Blockers, "No recognized project runtime; generate and probe a quest-scoped RuntimeSpec before execution")
	}
	plan.Runtime.DependencyLock = dependencyDigest(root)
	ApplyManagedRuntimePack(&plan)
	return plan
}

func exists(root, relative string) bool {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative)))
	return err == nil && !info.IsDir()
}

func hasCSProj(root string) bool {
	matches, err := filepath.Glob(filepath.Join(root, "*.csproj"))
	return err == nil && len(matches) > 0
}

func dependencyDigest(root string) string {
	h := sha256.New()
	for _, name := range []string{"composer.lock", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "go.sum", "Cargo.lock", "poetry.lock", "requirements.txt"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err == nil {
			h.Write([]byte(name))
			h.Write(data)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func digest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
