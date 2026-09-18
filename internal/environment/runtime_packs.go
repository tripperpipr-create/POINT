package environment

import (
	"sort"
	"strings"

	"local-agent-workbench/internal/domain"
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
