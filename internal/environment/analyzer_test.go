package environment

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

func TestAnalyzePHPProjectSelectsManagedPack(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{"name":"demo/app"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := Analyze(root, "ws-1")
	if plan.Runtime.Toolchains["php"] == "" {
		t.Fatalf("expected php toolchain, got %#v", plan.Runtime.Toolchains)
	}
	if plan.Runtime.Image != PHPManagedImage {
		t.Fatalf("expected php managed image %q, got %q", PHPManagedImage, plan.Runtime.Image)
	}
	if len(plan.NetworkHosts) == 0 {
		t.Fatal("expected packagist network hosts")
	}
	joined := strings.Join(plan.NetworkHosts, ",")
	for _, need := range []string{"repo.packagist.org", "api.github.com", "codeload.github.com"} {
		if !strings.Contains(joined, need) {
			t.Fatalf("missing composer egress host %s in %v", need, plan.NetworkHosts)
		}
	}
}

func TestRuntimeRequirementsUseOnlyTrustedToolCatalog(t *testing.T) {
	order := domain.WorkOrder{
		Stack: domain.StackPresetRef{ID: "php-symfony-7", Version: "1"},
		Setup: domain.SetupPlan{Commands: []domain.SetupCommand{{Command: "composer install && evil.example/arbitrary:latest"}}},
	}
	got := RuntimeRequirementsForWorkOrder(&order)
	if !slices.Contains(got.RequiredCommands, "composer") || !slices.Contains(got.RequiredCommands, "php") || !slices.Contains(got.CandidateImages, PHPManagedImage) {
		t.Fatalf("php runtime requirements=%#v", got)
	}
	for _, value := range append(append([]string(nil), got.Packages...), got.CandidateImages...) {
		if strings.Contains(value, "evil.example") {
			t.Fatalf("untrusted command text entered runtime policy: %#v", got)
		}
	}
}

func TestRuntimeRequirementsAreInferredFromApprovedCommands(t *testing.T) {
	order := domain.WorkOrder{
		Stack: domain.StackPresetRef{ID: "custom-service", Version: "3"},
		Setup: domain.SetupPlan{Commands: []domain.SetupCommand{
			{Command: "python3 -m pip install -r requirements.txt"},
			{Command: "npm install"},
		}},
		Completion: domain.CompletionProfile{Checks: []domain.CompletionCheck{{Command: "mvn test"}}},
	}
	got := RuntimeRequirementsForWorkOrder(&order)
	for _, command := range []string{"python3", "pip", "node", "npm", "java", "mvn"} {
		if !slices.Contains(got.RequiredCommands, command) {
			t.Fatalf("missing inferred command %q in %#v", command, got)
		}
	}
	for _, pkg := range []string{"py3.13-pip=26.2.1-r1", "openjdk-21-default-jdk=21.0.12.1-r2", "maven-3.9=3.9.16-r2"} {
		if !slices.Contains(got.Packages, pkg) {
			t.Fatalf("missing approved package %q in %#v", pkg, got)
		}
	}
}

func TestAnalyzePHPProjectWithDockerfileUsesPHPPack(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{"name":"demo/app","require":{"php":">=8.3"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM php:8.3-cli\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte("services:\n  app:\n    build: .\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := Analyze(root, "ws-php-project")
	if plan.Strategy != "project" {
		t.Fatalf("expected project strategy, got %q", plan.Strategy)
	}
	if plan.Runtime.Image != PHPManagedImage {
		t.Fatalf("expected php managed image %q, got %q", PHPManagedImage, plan.Runtime.Image)
	}
}

func TestAnalyzeGeneratedStackGetsGenericImage(t *testing.T) {
	root := t.TempDir()
	plan := Analyze(root, "ws-2")
	if plan.Strategy != "generated" {
		t.Fatalf("expected generated strategy, got %q", plan.Strategy)
	}
	if plan.Runtime.Image != GenericManagedImage {
		t.Fatalf("expected generic image, got %q", plan.Runtime.Image)
	}
	if len(plan.Blockers) == 0 {
		t.Fatal("expected probe blocker")
	}
}

func TestAnalyzeGoProject(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := Analyze(root, "ws-3")
	if plan.Runtime.Toolchains["go"] == "" {
		t.Fatal("expected go toolchain")
	}
	if plan.Runtime.Image == "" {
		t.Fatal("expected managed image")
	}
}

func TestAnalyzePHPWithoutPHPUnitUsesRequestsHTTPSmoke(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{"name":"demo/app","require":{"php":">=8.3"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "requests.http"), []byte("### Calc\nPOST http://127.0.0.1:8337/calculate-price\n\n{\"product\":1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := Analyze(root, "ws-php-smoke")
	var testCmd *domain.EnvironmentCommand
	for i := range plan.Commands {
		if plan.Commands[i].ID == "tests" {
			testCmd = &plan.Commands[i]
			break
		}
	}
	if testCmd == nil || testCmd.Program != PHPRequestsHTTPSmoke {
		t.Fatalf("expected requests.http smoke command, got %#v", plan.Commands)
	}
}

func TestAnalyzePHPWithRequireDevPHPUnit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "composer.json"), []byte(`{"name":"demo/app","require-dev":{"phpunit/phpunit":"^11"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := Analyze(root, "ws-php-unit")
	var testCmd *domain.EnvironmentCommand
	for i := range plan.Commands {
		if plan.Commands[i].ID == "tests" {
			testCmd = &plan.Commands[i]
			break
		}
	}
	if testCmd == nil || testCmd.Program != "php" || len(testCmd.Arguments) != 1 || testCmd.Arguments[0] != "vendor/bin/phpunit" {
		t.Fatalf("expected vendor/bin/phpunit, got %#v", testCmd)
	}
}
