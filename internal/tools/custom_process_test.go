package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/workspace"
)

func TestCustomProcessUsesTypedArgvWithoutShellExpansion(t *testing.T) {
	root := t.TempDir()
	program := buildArgumentHelper(t, root)
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	tool := CustomProcess{FS: fs, Config: domain.CustomTool{
		ID: "customtool_0123456789abcdef01234567", Kind: domain.CustomToolProcess,
		DisplayName: "Argument helper", Description: "Prints one validated argument",
		Program: program, Arguments: []string{"--value", "{{value}}", "--mode={{mode}}"},
		Parameters: []domain.CustomToolParameter{
			{Name: "value", DisplayName: "Value", Description: "Value to print", Type: domain.CustomToolParameterString, Required: true, MaxLength: 1024},
			{Name: "mode", DisplayName: "Mode", Description: "Optional output mode", Type: domain.CustomToolParameterEnum, EnumValues: []string{"short", "full"}, MaxLength: 16},
		},
		CWD: ".", TimeoutSeconds: 30,
	}}
	raw := json.RawMessage(`{"reason":"verify argv","value":"alpha & echo injected"}`)
	if invalid := tool.ValidateArguments(raw); invalid != nil {
		t.Fatalf("valid arguments rejected: %#v", invalid)
	}
	definition := tool.Definition()
	if !json.Valid(definition.InputSchema) || !strings.Contains(string(definition.InputSchema), `"additionalProperties":false`) || !strings.Contains(string(definition.InputSchema), `"value"`) {
		t.Fatalf("schema=%s", definition.InputSchema)
	}
	dryRun, err := tool.Preview(raw)
	if err != nil {
		t.Fatal(err)
	}
	if dryRun.Program == "" || len(dryRun.Arguments) != 2 || dryRun.Arguments[1] != "alpha & echo injected" || dryRun.ResolvedCWD != fs.Root() || !strings.Contains(dryRun.Command, "alpha & echo injected") {
		t.Fatalf("dry-run preview=%#v", dryRun)
	}
	preview := tool.ApprovalArguments(raw)
	var approval struct {
		Arguments  []string       `json:"arguments"`
		Parameters map[string]any `json:"parameters"`
	}
	if err = json.Unmarshal(preview, &approval); err != nil {
		t.Fatal(err)
	}
	if len(approval.Arguments) != 2 || approval.Arguments[1] != "alpha & echo injected" || approval.Parameters["value"] != "alpha & echo injected" {
		t.Fatalf("approval preview=%s", preview)
	}
	result := tool.Execute(context.Background(), raw)
	if !result.OK {
		t.Fatalf("process failed: %#v", result)
	}
	var output struct {
		Stdout   string `json:"stdout"`
		ExitCode int    `json:"exitCode"`
	}
	if err = json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if output.ExitCode != 0 || output.Stdout != "--value|alpha & echo injected" || strings.Contains(output.Stdout, "injected\r\n") {
		t.Fatalf("process output=%#v", output)
	}
}

func TestCustomProcessRejectsUnknownEnumAndEscapingWorkspacePath(t *testing.T) {
	root := t.TempDir()
	program := buildArgumentHelper(t, root)
	if err := os.WriteFile(filepath.Join(root, "safe.txt"), []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	tool := CustomProcess{FS: fs, Config: domain.CustomTool{
		ID: "customtool_0123456789abcdef01234567", Kind: domain.CustomToolProcess,
		DisplayName: "Path helper", Description: "Checks typed values", Program: program,
		Arguments: []string{"{{target}}", "{{mode}}"}, Parameters: []domain.CustomToolParameter{
			{Name: "target", DisplayName: "Target", Description: "Workspace path", Type: domain.CustomToolParameterWorkspacePath, Required: true, MaxLength: 1024},
			{Name: "mode", DisplayName: "Mode", Description: "Mode", Type: domain.CustomToolParameterEnum, Required: true, EnumValues: []string{"read", "check"}, MaxLength: 16},
		}, CWD: ".", TimeoutSeconds: 30,
	}}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"reason":"test","target":"../outside.txt","mode":"read"}`),
		json.RawMessage(`{"reason":"test","target":"safe.txt","mode":"delete"}`),
		json.RawMessage(`{"reason":"test","target":"safe.txt","mode":"read","extra":true}`),
	} {
		if invalid := tool.ValidateArguments(raw); invalid == nil || invalid.OK || invalid.Error == nil || invalid.Error.Code != "invalid_input" {
			t.Fatalf("unsafe arguments accepted: %s result=%#v", raw, invalid)
		}
	}
	valid := json.RawMessage(`{"reason":"test","target":"safe.txt","mode":"check"}`)
	if invalid := tool.ValidateArguments(valid); invalid != nil {
		t.Fatalf("safe arguments rejected: %#v", invalid)
	}
}

func buildArgumentHelper(t *testing.T, root string) string {
	t.Helper()
	source := `package main
import (
  "fmt"
  "os"
  "strings"
)
func main() { fmt.Print(strings.Join(os.Args[1:], "|")) }
`
	if err := os.WriteFile(filepath.Join(root, "helper.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	name := "argument-helper"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	command := exec.Command("go", "build", "-o", name, "helper.go")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build helper: %v: %s", err, output)
	}
	return "./" + name
}
