package providers

import (
	"context"
	"fmt"
	"os/exec"

	"local-agent-workbench/internal/osproc"
	"strings"
)

// discoverHeadlessCLI verifies that a local autonomous runtime is installed.
// Model discovery is deliberately conservative: both Cursor and Codex can
// choose a subscription-backed model dynamically, so "auto" is the only model
// Point advertises until a runtime-specific catalog probe is implemented.
func discoverHeadlessCLI(ctx context.Context, executable, defaultModel string) ([]ModelInfo, error) {
	path, err := exec.LookPath(executable)
	if err != nil {
		return nil, fmt.Errorf("%s CLI is not available: %w", executable, err)
	}
	command := osproc.CommandContext(ctx, path, "--version")
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s CLI version probe failed: %s", executable, strings.TrimSpace(string(output)))
	}
	model := strings.TrimSpace(defaultModel)
	if model == "" {
		model = "auto"
	}
	return []ModelInfo{{ID: model, DisplayName: model, OwnedBy: executable + " CLI"}}, nil
}
