package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestCustomToolPreviewValidatesUnsavedArgvWithoutExecutionOrPersistence(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	workspaceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "safe.txt"), []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	source := `package main
import "os"
func main() { _ = os.WriteFile("preview-executed", []byte("unexpected"), 0600) }
`
	if err := os.WriteFile(filepath.Join(workspaceRoot, "preview-side-effect.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(workspaceRoot); err != nil {
		t.Fatal(err)
	}
	before, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	tool := domain.CustomTool{
		Kind: domain.CustomToolProcess, DisplayName: "Preview only", Description: "Validates an unsaved process tool",
		Program: "go", Arguments: []string{"run", "preview-side-effect.go", "{{target}}"},
		Parameters: []domain.CustomToolParameter{{Name: "target", DisplayName: "Target", Description: "Existing workspace path", Type: domain.CustomToolParameterWorkspacePath, Required: true, MaxLength: 1024}},
		CWD:        ".", TimeoutSeconds: 30,
	}
	preview, err := application.PreviewCustomTool(CustomToolPreviewRequest{Tool: tool, Arguments: json.RawMessage(`{"reason":"dry run","target":"safe.txt"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Program != "go" || len(preview.Arguments) != 3 || preview.Arguments[2] != "safe.txt" || preview.ResolvedCWD != workspaceRoot || !strings.Contains(string(preview.Definition.InputSchema), `"additionalProperties":false`) {
		t.Fatalf("preview=%#v", preview)
	}
	if _, err = os.Stat(filepath.Join(workspaceRoot, "preview-executed")); !os.IsNotExist(err) {
		t.Fatal("preview unexpectedly executed the configured process")
	}
	after, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if len(after.CustomTools) != len(before.CustomTools) {
		t.Fatalf("preview persisted a custom tool: before=%d after=%d", len(before.CustomTools), len(after.CustomTools))
	}
	if _, err = application.PreviewCustomTool(CustomToolPreviewRequest{Tool: tool, Arguments: json.RawMessage(`{"reason":"dry run","target":"../outside.txt"}`)}); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("escaping workspace path accepted: %v", err)
	}
}

func TestCustomCommandToolUsesFixedCommandAndApproval(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	workspaceRoot := t.TempDir()
	command := "printf custom-tool-ok"
	modelCommand := "printf model-overrode"
	if runtime.GOOS == "windows" {
		command = "echo custom-tool-ok"
		modelCommand = "echo model-overrode"
	}
	var mu sync.Mutex
	requestNumber := 0
	var customToolID string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestNumber++
		current := requestNumber
		mu.Unlock()
		w.Header().Set("Content-Type", "application/x-ndjson")
		if current == 1 {
			arguments := map[string]any{"reason": "Проверить готовность API", "command": modelCommand}
			_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"function": map[string]any{"name": customToolID, "arguments": arguments}}}}, "done": true})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "content": "Проверка завершена."}, "done": true})
	}))
	defer provider.Close()

	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(workspaceRoot); err != nil {
		t.Fatal(err)
	}
	custom, err := application.SaveCustomTool(domain.CustomTool{
		Kind: domain.CustomToolCommand, DisplayName: "Проверить API", Description: "Запускает фиксированную проверку API",
		Command: command, ProvidesVerification: true, CWD: ".", TimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	customToolID = custom.ID
	profile := domain.DefaultProfile()
	profile.ID, profile.Name, profile.BaseURL, profile.Model = "custom-tool-profile", "Custom tool agent", provider.URL, "scripted"
	profile.SystemPrompt = "captured custom tool prompt"
	profile.AllowedTools = []string{custom.ID}
	if _, err = application.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	task := "Run tests with the custom verification tool"
	preview, err := application.PreviewAgentRun(AgentRunPreviewRequest{ProfileID: profile.ID, Task: task})
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Completion.ExplicitVerification || !preview.Completion.VerificationToolAvailable || preview.Completion.BlockingConfigurationIssue || len(preview.Tools) != 1 || !preview.Tools[0].ProvidesVerification {
		t.Fatalf("custom verification preflight=%#v tools=%#v", preview.Completion, preview.Tools)
	}
	run, err := application.StartRun(StartRunRequest{ProfileID: profile.ID, Task: task, PreflightFingerprint: preview.Fingerprint})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	resolved := false
	for time.Now().Before(deadline) {
		details, detailsErr := application.RunDetails(run.ID)
		if detailsErr != nil {
			t.Fatal(detailsErr)
		}
		for _, approval := range details.Approvals {
			if approval.Status != domain.ApprovalPending || resolved {
				continue
			}
			arguments := string(approval.Arguments)
			if !strings.Contains(arguments, command) || strings.Contains(arguments, modelCommand) {
				t.Fatalf("approval does not show the fixed command: %s", arguments)
			}
			if err = application.ResolveApproval(approval.ID, true); err != nil {
				t.Fatal(err)
			}
			resolved = true
		}
		if details.Run.Status == domain.RunCompleted {
			break
		}
		if details.Run.Status == domain.RunFailed {
			t.Fatalf("custom tool run failed: %s", details.Run.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	details, err := application.RunDetails(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved || details.Run.Status != domain.RunCompleted || fmt.Sprint(details.Run.ToolsUsed) != fmt.Sprintf("[%s]", custom.ID) || len(details.Run.ConfigurationSnapshot.CustomTools) != 1 || !details.Run.ConfigurationSnapshot.CustomTools[0].ProvidesVerification {
		t.Fatalf("custom tool run=%#v resolved=%v", details.Run, resolved)
	}
	foundOutput := false
	for _, event := range details.Events {
		if event.Type == domain.EventToolFinished && strings.Contains(string(event.Data), "custom-tool-ok") && !strings.Contains(string(event.Data), "model-overrode") {
			foundOutput = true
		}
	}
	if !foundOutput {
		t.Fatal("fixed custom command output was not retained")
	}
	if err = application.DeleteCustomTool(custom.ID); err == nil {
		t.Fatal("tool used by a profile was deleted")
	}
	profile.AllowedTools = nil
	profile.SystemPrompt = "mutated after run"
	if _, err = application.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	if err = application.DeleteCustomTool(custom.ID); err != nil {
		t.Fatal(err)
	}
	details, err = application.RunDetails(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := details.Run.ConfigurationSnapshot
	// Проверяем свойство, а не буквальный текст: снимок обязан держать то, что
	// было на момент запуска, и не замечать позднейших правок. Промпт профиля
	// теперь всегда скомпилирован (IDENTITY/ROLE/ADDITIONAL INSTRUCTIONS) —
	// профиль выводится из чертежа, а не хранится отдельной таблицей, поэтому
	// сравнивать с сырой строкой больше нельзя.
	if snapshot.SchemaVersion != 3 || snapshot.ApplicationVersion != Version ||
		snapshot.ProfileDigest == "" || snapshot.ConfigurationDigest == "" || snapshot.EgressPolicyDigest == "" ||
		!strings.Contains(snapshot.Profile.SystemPrompt, "captured custom tool prompt") ||
		strings.Contains(snapshot.Profile.SystemPrompt, "mutated after run") ||
		len(snapshot.CustomTools) != 1 || snapshot.CustomTools[0].Command != command {
		t.Fatalf("run configuration snapshot changed with live profile/tool: %#v", snapshot)
	}
}

func TestParameterizedProcessToolUsesValidatedArgvAndSnapshot(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	workspaceRoot := t.TempDir()
	helper := `package main
import (
  "fmt"
  "os"
  "strings"
)
func main() { fmt.Print(strings.Join(os.Args[1:], "|")) }
`
	if err := os.WriteFile(filepath.Join(workspaceRoot, "echoargs.go"), []byte(helper), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	requestNumber := 0
	var customToolID string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requestNumber++
		current := requestNumber
		mu.Unlock()
		w.Header().Set("Content-Type", "application/x-ndjson")
		if current == 1 {
			arguments := map[string]any{"reason": "Проверить безопасную передачу аргумента", "value": "alpha & echo injected"}
			_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"function": map[string]any{"name": customToolID, "arguments": arguments}}}}, "done": true})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "content": "Process tool завершён."}, "done": true})
	}))
	defer provider.Close()
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(workspaceRoot); err != nil {
		t.Fatal(err)
	}
	custom, err := application.SaveCustomTool(domain.CustomTool{
		Kind: domain.CustomToolProcess, DisplayName: "Echo argv", Description: "Проверяет один аргумент без shell",
		Program: "go", Arguments: []string{"run", "echoargs.go", "{{value}}"}, Parameters: []domain.CustomToolParameter{
			{Name: "value", DisplayName: "Значение", Description: "Строка для безопасной передачи процессу", Type: domain.CustomToolParameterString, Required: true, MaxLength: 1024},
		}, CWD: ".", TimeoutSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	customToolID = custom.ID
	profile := domain.DefaultProfile()
	profile.ID, profile.BaseURL, profile.Model = "process-tool-profile", provider.URL, "scripted"
	profile.AllowedTools = []string{custom.ID}
	if _, err = application.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	run, err := application.StartRun(StartRunRequest{ProfileID: profile.ID, Task: "Проверь параметризованный процесс"})
	if err != nil {
		t.Fatal(err)
	}
	resolved := false
	// Ожидание должно переживать то, чего ждёт. Инструмент запускает `go run`
	// со своим потолком в 60 секунд, а внешний срок стоял на пятнадцати: под
	// нагрузкой полного прогона пакета компиляция не укладывалась, и тест падал
	// не по существу, а по будильнику.
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		details, detailsErr := application.RunDetails(run.ID)
		if detailsErr != nil {
			t.Fatal(detailsErr)
		}
		for _, approval := range details.Approvals {
			if approval.Status != domain.ApprovalPending || resolved {
				continue
			}
			var preview struct {
				Program   string   `json:"program"`
				Arguments []string `json:"arguments"`
			}
			if err = json.Unmarshal(approval.Arguments, &preview); err != nil {
				t.Fatal(err)
			}
			if preview.Program != "go" || len(preview.Arguments) != 3 || preview.Arguments[0] != "run" || preview.Arguments[1] != "echoargs.go" || preview.Arguments[2] != "alpha & echo injected" {
				t.Fatalf("approval does not expose exact argv: %s", approval.Arguments)
			}
			if err = application.ResolveApproval(approval.ID, true); err != nil {
				t.Fatal(err)
			}
			resolved = true
		}
		if details.Run.Status == domain.RunCompleted {
			break
		}
		if details.Run.Status == domain.RunFailed {
			t.Fatalf("process tool run failed: %s", details.Run.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	details, err := application.RunDetails(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved || details.Run.Status != domain.RunCompleted || len(details.Run.ConfigurationSnapshot.CustomTools) != 1 || details.Run.ConfigurationSnapshot.CustomTools[0].Program != "go" || len(details.Run.ConfigurationSnapshot.CustomTools[0].Parameters) != 1 {
		t.Fatalf("process tool snapshot/run=%#v resolved=%v", details.Run, resolved)
	}
	foundOutput := false
	for _, event := range details.Events {
		if event.Type == domain.EventToolFinished {
			var payload struct {
				Result struct {
					Output struct {
						Stdout string `json:"stdout"`
					} `json:"output"`
				} `json:"result"`
			}
			if json.Unmarshal(event.Data, &payload) == nil && payload.Result.Output.Stdout == "alpha & echo injected" {
				foundOutput = true
			}
		}
	}
	if !foundOutput {
		t.Fatal("process argv output was not retained")
	}
}

func TestExecutableToolChangesAreJournaledRedactedAndRevertible(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	workspaceRoot := t.TempDir()
	fixtures := map[string]string{
		"modify.txt": "before sk-original-abcdefghijklmnop",
		"delete.txt": "delete me",
		".env":       "TOKEN=before-sensitive-value",
		"mutate.go": `package main
import "os"
func main() {
  _ = os.WriteFile("modify.txt", []byte("after sk-updated-abcdefghijklmnop"), 0600)
  _ = os.WriteFile("create.txt", []byte("created"), 0600)
  _ = os.WriteFile(".env", []byte("TOKEN=after-sensitive-value"), 0600)
  _ = os.Remove("delete.txt")
}

`,
	}
	for name, content := range fixtures {
		if err := os.WriteFile(filepath.Join(workspaceRoot, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	requestNumber := 0
	var customToolID string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requestNumber++
		current := requestNumber
		mu.Unlock()
		w.Header().Set("Content-Type", "application/x-ndjson")
		if current == 1 {
			arguments := map[string]any{"reason": "apply the requested fixture changes"}
			_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"function": map[string]any{"name": customToolID, "arguments": arguments}}}}, "done": true})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "content": "Workspace changes completed."}, "done": true})
	}))
	defer provider.Close()

	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(workspaceRoot); err != nil {
		t.Fatal(err)
	}
	if _, err = application.RebuildProjectIndex(); err != nil {
		t.Fatal(err)
	}
	custom, err := application.SaveCustomTool(domain.CustomTool{
		Kind: domain.CustomToolProcess, DisplayName: "Mutate fixtures", Description: "Creates, modifies, and deletes test files",
		Program: "go", Arguments: []string{"run", "mutate.go"}, CWD: ".", TimeoutSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	customToolID = custom.ID
	profile := domain.DefaultProfile()
	profile.ID, profile.Name, profile.BaseURL, profile.Model = "mutation-audit-profile", "Mutation audit", provider.URL, "scripted"
	profile.AllowedTools = []string{custom.ID}
	if _, err = application.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	run, err := application.StartRun(StartRunRequest{ProfileID: profile.ID, Task: "Apply the fixture changes"})
	if err != nil {
		t.Fatal(err)
	}

	resolved := false
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		details, detailsErr := application.RunDetails(run.ID)
		if detailsErr != nil {
			t.Fatal(detailsErr)
		}
		for _, approval := range details.Approvals {
			if approval.Status == domain.ApprovalPending && !resolved {
				if err = application.ResolveApproval(approval.ID, true); err != nil {
					t.Fatal(err)
				}
				resolved = true
			}
		}
		if details.Run.Status == domain.RunCompleted {
			break
		}
		if details.Run.Status == domain.RunFailed {
			t.Fatalf("mutation run failed: %s", details.Run.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	details, err := application.RunDetails(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved || details.Run.Status != domain.RunCompleted || len(details.Patches) != 3 {
		t.Fatalf("run=%#v patches=%#v resolved=%v", details.Run, details.Patches, resolved)
	}
	if len(details.Run.ChangedFiles) != 4 || details.Diagnostics.Workspace.Audits != 1 || details.Diagnostics.Workspace.RecordedChanges != 3 || details.Diagnostics.Workspace.NonRevertibleChanges != 1 {
		t.Fatalf("changed files=%v workspace diagnostics=%#v", details.Run.ChangedFiles, details.Diagnostics.Workspace)
	}
	foundNonRevertibleSignal := false
	for _, signal := range details.Diagnostics.Signals {
		if signal.Code == "workspace_changes_non_revertible" && signal.Value == 1 {
			foundNonRevertibleSignal = true
		}
	}
	if !foundNonRevertibleSignal {
		t.Fatalf("missing non-revertible diagnostic signal: %#v", details.Diagnostics.Signals)
	}
	for _, change := range details.Patches {
		if change.SourceTool != custom.ID || change.ApprovalID == "" || change.Status != "applied" || change.Original != "" || change.Proposed != "" {
			t.Fatalf("public change=%#v", change)
		}
		if strings.Contains(change.Diff, "sk-original-") || strings.Contains(change.Diff, "sk-updated-") {
			t.Fatalf("public diff leaked a secret: %s", change.Diff)
		}
	}
	workspaceEvent := false
	for _, event := range details.Events {
		if strings.Contains(string(event.Data), "sk-original-") || strings.Contains(string(event.Data), "sk-updated-") {
			t.Fatalf("event leaked exact file content: %s", event.Data)
		}
		if event.Type == domain.EventWorkspaceChanged && strings.Contains(string(event.Data), `"recordedChanges":3`) && strings.Contains(string(event.Data), `"nonRevertibleChanges":1`) {
			workspaceEvent = true
		}
	}
	if !workspaceEvent {
		t.Fatal("workspace change audit event was not retained")
	}
	boot, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	// Sandboxed executions mutate an isolated copy; the live project index stays
	// ready until Change Sets are applied to the workspace.
	if boot.IndexStatus.State != "ready" && boot.IndexStatus.State != "stale" {
		t.Fatalf("index status=%#v", boot.IndexStatus)
	}
	if err = waitAndApplyPendingChangeSets(t, application); err != nil {
		t.Fatal(err)
	}
	boot, err = application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if boot.IndexStatus.State != "stale" {
		t.Fatalf("index status after apply=%#v, want stale", boot.IndexStatus)
	}
	var modifiedPatchID string
	for _, change := range details.Patches {
		if change.Path == "modify.txt" {
			modifiedPatchID = change.ID
		}
	}
	if modifiedPatchID == "" {
		t.Fatal("modified file was not present in the command journal")
	}
	if err = os.WriteFile(filepath.Join(workspaceRoot, "modify.txt"), []byte("later user edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = application.RevertPatch(modifiedPatchID); err == nil || !strings.Contains(err.Error(), "changed after") {
		t.Fatalf("rollback did not refuse a later user edit: %v", err)
	}
	if err = os.WriteFile(filepath.Join(workspaceRoot, "modify.txt"), []byte("after sk-updated-abcdefghijklmnop"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, change := range details.Patches {
		if _, err = application.RevertPatch(change.ID); err != nil {
			t.Fatalf("revert %s: %v", change.Path, err)
		}
	}
	assertFileContent := func(name, want string) {
		t.Helper()
		data, readErr := os.ReadFile(filepath.Join(workspaceRoot, name))
		if readErr != nil || string(data) != want {
			t.Fatalf("%s=%q err=%v want=%q", name, data, readErr, want)
		}
	}
	assertFileContent("modify.txt", fixtures["modify.txt"])
	assertFileContent("delete.txt", fixtures["delete.txt"])
	// Sensitive files are not copied into the sandbox or represented by a
	// ChangeSet, so a command cannot publish their sandbox-local value.
	assertFileContent(".env", fixtures[".env"])
	if _, err = os.Stat(filepath.Join(workspaceRoot, "create.txt")); !os.IsNotExist(err) {
		t.Fatalf("created file still exists after rollback: %v", err)
	}
}

func TestRunCommandMutationAdvancesRevisionAndRetainsVerificationEvidence(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	workspaceRoot := t.TempDir()
	files := map[string]string{
		"go.mod": "module fixture.local/command-audit\n\ngo 1.25.0\n",
		"mutation_test.go": `package commandaudit
import (
  "os"
  "testing"
)
func TestMutation(t *testing.T) {
  if err := os.WriteFile("generated.txt", []byte("verified"), 0600); err != nil { t.Fatal(err) }
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(workspaceRoot, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	requests := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		current := requests
		mu.Unlock()
		w.Header().Set("Content-Type", "application/x-ndjson")
		if current == 1 {
			arguments := map[string]any{"command": "go test ./...", "cwd": ".", "reason": "verify behavior and generate the requested artifact", "timeoutSeconds": 60}
			_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"function": map[string]any{"name": "run_command", "arguments": arguments}}}}, "done": true})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "content": "Verification passed and the artifact was generated."}, "done": true})
	}))
	defer provider.Close()
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(workspaceRoot); err != nil {
		t.Fatal(err)
	}
	profile := domain.DefaultProfile()
	profile.ID, profile.Name, profile.BaseURL, profile.Model = "run-command-audit", "Run command audit", provider.URL, "scripted"
	profile.AllowedTools = []string{"run_command"}
	if _, err = application.SaveProfile(profile); err != nil {
		t.Fatal(err)
	}
	run, err := application.StartRun(StartRunRequest{ProfileID: profile.ID, Task: "Run the tests and generate the verified artifact"})
	if err != nil {
		t.Fatal(err)
	}
	resolved := false
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		details, detailsErr := application.RunDetails(run.ID)
		if detailsErr != nil {
			t.Fatal(detailsErr)
		}
		for _, approval := range details.Approvals {
			if approval.Status == domain.ApprovalPending && !resolved {
				if err = application.ResolveApproval(approval.ID, true); err != nil {
					t.Fatal(err)
				}
				resolved = true
			}
		}
		if details.Run.Status == domain.RunCompleted {
			break
		}
		if details.Run.Status == domain.RunFailed {
			t.Fatalf("run command audit failed: %s", details.Run.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	details, err := application.RunDetails(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved || details.Run.Status != domain.RunCompleted || requests != 2 || len(details.Patches) != 1 || details.Patches[0].SourceTool != "run_command" {
		t.Fatalf("run=%#v requests=%d patches=%#v resolved=%v", details.Run, requests, details.Patches, resolved)
	}
	if !details.Diagnostics.Verification.Recorded || details.Diagnostics.Workspace.RecordedChanges != 1 || details.Diagnostics.Completion.RevisionRequests != 0 {
		t.Fatalf("diagnostics=%#v", details.Diagnostics)
	}
	if err = waitAndApplyPendingChangeSets(t, application); err != nil {
		t.Fatal(err)
	}
	if _, err = application.RevertPatch(details.Patches[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(workspaceRoot, "generated.txt")); !os.IsNotExist(err) {
		t.Fatalf("generated artifact still exists after rollback: %v", err)
	}
}
