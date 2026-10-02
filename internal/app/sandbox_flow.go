package app

import (
	"fmt"
	"os"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/filepolicy"
)

func currentSandboxChoice() (sandboxStageOptions, error) {
	mode := strings.TrimSpace(os.Getenv("POINT_SANDBOX_WORKSPACE"))
	if mode == "" {
		mode = "bind"
	}
	if mode != "bind" && mode != "volume" {
		return sandboxStageOptions{}, fmt.Errorf("POINT_SANDBOX_WORKSPACE must be bind or volume")
	}
	return sandboxStageOptions{StorageMode: mode, FileRulesVersion: filepolicy.Current}, nil
}
func flowSandboxOptions(run domain.FlowRun, role string) sandboxStageOptions {
	options := sandboxStageOptions{Role: role, StorageMode: "bind", FileRulesVersion: filepolicy.Legacy}
	if choice, ok := run.Snapshot["sandboxWorkspace"].(map[string]any); ok {
		if mode, ok := choice["storageMode"].(string); ok {
			options.StorageMode = mode
		}
		if rules, ok := choice["fileRulesVersion"].(string); ok {
			options.FileRulesVersion = rules
		}
	}
	return options
}
