package environment

import (
	"sort"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
)

// Managed sandbox images for recognized toolchains. Digests are resolved at
// probe time by the Docker host controller; tags are stable pack identifiers.
const (
	DefaultManagedImage = "point-agent-sandbox:1.2.2"
	PHPManagedImage     = "point-agent-sandbox-php:1.3.1"
	NodeManagedImage    = "point-agent-sandbox:1.2.2"
	PythonManagedImage  = "point-agent-sandbox:1.2.2"
	GoManagedImage      = "point-agent-sandbox:1.2.2"
	JavaManagedImage    = "point-agent-sandbox:1.2.2"
	DotNetManagedImage  = "point-agent-sandbox:1.2.2"
	RustManagedImage    = "point-agent-sandbox:1.2.2"
	GenericManagedImage = "point-agent-sandbox:1.2.2"
)

var toolchainImages = map[string]string{
	"php":    PHPManagedImage,
	"node":   NodeManagedImage,
	"python": PythonManagedImage,
	"go":     GoManagedImage,
	"java":   JavaManagedImage,
	"dotnet": DotNetManagedImage,
	"rust":   RustManagedImage,
}

type runtimeToolDefinition struct {
	Commands        []string
	Packages        []string
	CandidateImages []string
}

var phpRuntimePackages = []string{
	"php-8.3=8.3.33-r3", "php-8.3-config=8.3.33-r3",
	"php-8.3-ctype=8.3.33-r3", "php-8.3-ctype-config=8.3.33-r3",
	"php-8.3-curl=8.3.33-r3", "php-8.3-curl-config=8.3.33-r3",
	"php-8.3-dom=8.3.33-r3", "php-8.3-dom-config=8.3.33-r3",
	"php-8.3-fileinfo=8.3.33-r3", "php-8.3-fileinfo-config=8.3.33-r3",
	"php-8.3-iconv=8.3.33-r3", "php-8.3-iconv-config=8.3.33-r3",
	"php-8.3-intl=8.3.33-r3", "php-8.3-intl-config=8.3.33-r3",
	"php-8.3-mbstring=8.3.33-r3", "php-8.3-mbstring-config=8.3.33-r3",
	"php-8.3-openssl=8.3.33-r3", "php-8.3-openssl-config=8.3.33-r3",
	"php-8.3-phar=8.3.33-r3", "php-8.3-phar-config=8.3.33-r3",
	"php-8.3-pdo=8.3.33-r3", "php-8.3-pdo-config=8.3.33-r3",
	"php-8.3-pdo_sqlite=8.3.33-r3", "php-8.3-pdo_sqlite-config=8.3.33-r3",
	"php-8.3-simplexml=8.3.33-r3", "php-8.3-simplexml-config=8.3.33-r3",
	"php-8.3-xml=8.3.33-r3", "php-8.3-xml-config=8.3.33-r3",
	"php-8.3-xmlwriter=8.3.33-r3", "php-8.3-xmlwriter-config=8.3.33-r3",
	"php-8.3-zip=8.3.33-r3", "php-8.3-zip-config=8.3.33-r3",
	"composer=2.10.3-r0",
}

// runtimeToolCatalog is the server-owned package allowlist. Work orders and
// models can cause a known tool to be selected, but cannot inject image names,
// package names, Dockerfile instructions, or root commands.
var runtimeToolCatalog = map[string]runtimeToolDefinition{
	"composer": {Commands: []string{"composer", "php"}, Packages: phpRuntimePackages, CandidateImages: []string{PHPManagedImage}},
	"php":      {Commands: []string{"php"}, Packages: phpRuntimePackages, CandidateImages: []string{PHPManagedImage}},
	"node":     {Commands: []string{"node"}},
	"npm":      {Commands: []string{"npm", "node"}},
	"npx":      {Commands: []string{"npx", "node"}},
	"python":   {Commands: []string{"python3"}},
	"python3":  {Commands: []string{"python3"}},
	"pip":      {Commands: []string{"pip", "python3"}, Packages: []string{"py3.13-pip=26.2.1-r1"}},
	"pip3":     {Commands: []string{"pip3", "python3"}, Packages: []string{"py3.13-pip=26.2.1-r1"}},
	"go":       {Commands: []string{"go"}},
	"git":      {Commands: []string{"git"}},
	"rg":       {Commands: []string{"rg"}},
	"cargo":    {Commands: []string{"cargo", "rustc"}, Packages: []string{"rust-1.98=1.98.1-r0"}},
	"rustc":    {Commands: []string{"rustc"}, Packages: []string{"rust-1.98=1.98.1-r0"}},
	"java":     {Commands: []string{"java"}, Packages: []string{"openjdk-21-default-jdk=21.0.12.1-r2"}},
	"javac":    {Commands: []string{"javac", "java"}, Packages: []string{"openjdk-21-default-jdk=21.0.12.1-r2"}},
	"mvn":      {Commands: []string{"mvn", "java"}, Packages: []string{"openjdk-21-default-jdk=21.0.12.1-r2", "maven-3.9=3.9.16-r2"}},
	"gradle":   {Commands: []string{"gradle", "java"}, Packages: []string{"openjdk-21-default-jdk=21.0.12.1-r2", "gradle-8=8.14.3-r4"}},
	"dotnet":   {Commands: []string{"dotnet"}, Packages: []string{"dotnet-8-sdk=8.0.127-r0"}},
}

var stackRuntimeTools = map[string][]string{
	"php-symfony-7": {"composer", "php"},
}

// RuntimeRequirementsForWorkOrder translates an approved work contract into
// trusted runtime capabilities. Setup and completion commands are inspected
// only for names present in runtimeToolCatalog; arbitrary model text never
// becomes a package or image reference.
func RuntimeRequirementsForWorkOrder(order *domain.WorkOrder) sandbox.RuntimeRequirements {
	if order == nil {
		return sandbox.RuntimeRequirements{}
	}
	return runtimeRequirements(order.Stack, order.Setup, order.Completion)
}

func RuntimeRequirementsForExecutionContract(contract *domain.WorkOrderExecutionContract) sandbox.RuntimeRequirements {
	if contract == nil {
		return sandbox.RuntimeRequirements{}
	}
	return runtimeRequirements(contract.Stack, contract.Setup, contract.Completion)
}

func runtimeRequirements(stack domain.StackPresetRef, setup domain.SetupPlan, completion domain.CompletionProfile) sandbox.RuntimeRequirements {
	selected := map[string]bool{}
	for _, tool := range stackRuntimeTools[strings.ToLower(strings.TrimSpace(stack.ID))] {
		selected[tool] = true
	}
	for _, command := range setup.Commands {
		collectApprovedRuntimeTools(command.Command, selected)
	}
	for _, check := range completion.Checks {
		collectApprovedRuntimeTools(check.Command, selected)
	}
	if len(selected) == 0 {
		return sandbox.RuntimeRequirements{}
	}
	tools := make([]string, 0, len(selected))
	for tool := range selected {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	commands, packages, candidates := []string{}, []string{}, []string{}
	for _, tool := range tools {
		definition := runtimeToolCatalog[tool]
		commands = append(commands, definition.Commands...)
		packages = append(packages, definition.Packages...)
		candidates = append(candidates, definition.CandidateImages...)
	}
	return sandbox.RuntimeRequirements{
		ID: strings.TrimSpace(stack.ID), Version: strings.TrimSpace(stack.Version),
		RequiredCommands: commands, Packages: packages, CandidateImages: candidates,
	}
}

func collectApprovedRuntimeTools(command string, selected map[string]bool) {
	for _, token := range strings.FieldsFunc(strings.ToLower(command), func(r rune) bool {
		switch r {
		case ' ', '\t', '\r', '\n', ';', '&', '|', '(', ')', '<', '>', '"', '\'':
			return true
		default:
			return false
		}
	}) {
		token = strings.TrimSpace(token)
		if _, ok := runtimeToolCatalog[token]; ok {
			selected[token] = true
		}
	}
}

// ApplyManagedRuntimePack selects a trusted base image for the plan. Project
// Dockerfiles keep strategy "project"; managed/generated packs pin an image tag.
func ApplyManagedRuntimePack(plan *domain.EnvironmentPlan) {
	if plan == nil {
		return
	}
	if plan.Runtime.Toolchains == nil {
		plan.Runtime.Toolchains = map[string]string{}
	}
	switch plan.Strategy {
	case "project":
		// Project strategy keeps compose/Dockerfile ownership, but the agent
		// still executes inside a managed Point image. Prefer the toolchain pack.
		plan.Runtime.Image = selectManagedImage(plan.Runtime.Toolchains)
		if strings.TrimSpace(plan.Runtime.Image) == "" {
			plan.Runtime.Image = DefaultManagedImage
		}
	case "managed":
		plan.Runtime.Kind = "managed"
		plan.Runtime.Image = selectManagedImage(plan.Runtime.Toolchains)
	case "generated":
		plan.Runtime.Kind = "generated"
		plan.Runtime.Image = GenericManagedImage
		if !hasBlocker(plan.Blockers, "probe") {
			plan.Blockers = append(plan.Blockers, "Generated RuntimeSpec must be probed in the managed image before execution")
		}
	default:
		if strings.TrimSpace(plan.Runtime.Image) == "" {
			plan.Runtime.Image = DefaultManagedImage
		}
	}
	plan.Digest = digest(*plan)
}

func selectManagedImage(toolchains map[string]string) string {
	if len(toolchains) == 0 {
		return GenericManagedImage
	}
	names := make([]string, 0, len(toolchains))
	for name := range toolchains {
		names = append(names, name)
	}
	sort.Strings(names)
	// Prefer PHP when present: Symfony/Composer packs are the primary vertical.
	for _, name := range names {
		if name == "php" {
			return toolchainImages["php"]
		}
	}
	if image, ok := toolchainImages[names[0]]; ok {
		return image
	}
	return GenericManagedImage
}

func hasBlocker(blockers []string, needle string) bool {
	needle = strings.ToLower(needle)
	for _, item := range blockers {
		if strings.Contains(strings.ToLower(item), needle) {
			return true
		}
	}
	return false
}
