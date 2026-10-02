package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/filepolicy"
	"local-agent-workbench/internal/providers"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/sandboxsync"
)

type volumeParityModel struct{ calls int }

func (m *volumeParityModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.calls++
	if m.calls == 1 {
		args, _ := json.Marshal(map[string]any{"command": "printf 'updated\\r\\n' > original.txt; rm deleted.txt; mv renamed.txt moved.txt; printf 'unicode' > 'данные.txt'; printf '\\000\\377' > payload.bin; head -c 1048576 /dev/zero > large.bin; printf '{\"kind\":\"result\"}' >&2; exit 7", "reason": "exercise exact workspace audit", "timeoutSeconds": 30})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "parity-command", Name: "run_command", Arguments: args}})
	}
	if m.calls == 2 {
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "parity-verifier", Name: "customtool_0123456789abcdef01234567", Arguments: json.RawMessage(`{"reason":"verify exact fixture"}`)}})
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Fixture finished."})
}

// This uses the model/approval/tool/audit path, rather than comparing transfer internals.
func TestVolumeModelIntegrationByteParity(t *testing.T) {
	if os.Getenv("POINT_VOLUME_INTEGRATION") != "1" {
		t.Skip("requires Docker and static sandboxd")
	}
	type result struct {
		digest  string
		patches []domain.PatchProposal
		audits  []json.RawMessage
		tools   []json.RawMessage
	}
	var results []result
	for _, mode := range []string{"bind", "volume"} {
		root := t.TempDir()
		source := filepath.Join(root, "project")
		if err := os.MkdirAll(source, 0755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"original.txt", "deleted.txt", "renamed.txt"} {
			if err := os.WriteFile(filepath.Join(source, name), []byte("original\r\n"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		backend := sandbox.NewContainerBackend(filepath.Join(root, "sandboxes"))
		if image := os.Getenv("POINT_SANDBOX_IMAGE"); image != "" {
			backend.Image = image
		}
		if err := backend.Probe(context.Background()); err != nil {
			t.Fatal(err)
		}
		record, err := backend.Create(context.Background(), sandbox.CreateRequest{WorkspacePath: source, WorkspaceID: "parity", ExecutionID: "parity", StorageMode: mode, FileRulesVersion: filepolicy.Current})
		if err != nil {
			t.Fatal(err)
		}
		defer backend.Close(context.Background(), record, source)
		repo := newMemoryRepo()
		engine := NewEngine(repo, nil)
		engine.SetProcessExecutor(backend)
		engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return &volumeParityModel{}, nil })
		profile := domain.DefaultProfile()
		custom := domain.CustomTool{ID: "customtool_0123456789abcdef01234567", Kind: domain.CustomToolProcess, DisplayName: "Verify fixture", Description: "Verify exact bytes", Program: "python3", Arguments: []string{"-c", `import pathlib; assert pathlib.Path('original.txt').read_bytes()==b'updated\r\n'; assert pathlib.Path('large.bin').stat().st_size==1048576; assert not pathlib.Path('deleted.txt').exists()`}, CWD: ".", TimeoutSeconds: 30, ProvidesVerification: true}
		profile.AllowedTools = []string{"run_command", custom.ID}
		profile.MaxDurationSeconds = 60
		run, err := engine.Start(StartInput{Configuration: domain.NewRunConfigurationSnapshot("test", profile, []domain.CustomTool{custom}, time.Now().UTC()), Workspace: domain.Workspace{ID: "parity", Path: source}, SandboxPath: record.Path, SandboxImage: sandbox.ExecutionImageForRecord(record), Task: "exercise the workspace fixture"})
		if err != nil {
			t.Fatal(err)
		}
		first := resolveNextApproval(t, engine, run.ID, "", true)
		resolveNextApproval(t, engine, run.ID, first, true)
		finished := waitForTerminalRun(t, repo, run.ID)
		if finished.Status != domain.RunCompleted {
			t.Fatalf("%s: %+v", mode, finished)
		}
		manifest, err := sandboxsync.Scan(context.Background(), record.Path, filepolicy.Current)
		if err != nil {
			t.Fatal(err)
		}
		got := result{digest: manifest.Digest}
		repo.mu.Lock()
		for _, patch := range repo.patches {
			patch.ID, patch.RunID, patch.ApprovalID = "", "", ""
			patch.CreatedAt = time.Time{}
			got.patches = append(got.patches, patch)
		}
		repo.mu.Unlock()
		sort.Slice(got.patches, func(i, j int) bool { return got.patches[i].Path < got.patches[j].Path })
		events, err := repo.ListByRun(context.Background(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			if event.Type == domain.EventToolFinished {
				var value struct {
					Tool   string            `json:"tool"`
					Result domain.ToolResult `json:"result"`
				}
				if err := json.Unmarshal(event.Data, &value); err != nil {
					t.Fatal(err)
				}
				var output map[string]any
				if err := json.Unmarshal(value.Result.Output, &output); err != nil {
					t.Fatal(err)
				}
				delete(output, "durationMs")
				if audit, ok := output["_pointWorkspaceAudit"].(map[string]any); ok {
					delete(audit, "approvalId")
				}
				value.Result.Output, _ = json.Marshal(output)
				data, _ := json.Marshal(value)
				got.tools = append(got.tools, data)
			}
			if event.Type == domain.EventWorkspaceChanged {
				var value map[string]any
				if err := json.Unmarshal(event.Data, &value); err != nil {
					t.Fatal(err)
				}
				delete(value, "approvalId")
				data, _ := json.Marshal(value)
				got.audits = append(got.audits, data)
			}
		}
		if len(got.patches) < 4 || len(got.audits) == 0 || len(got.tools) != 2 {
			t.Fatalf("%s: audit was not exercised: %+v", mode, got)
		}
		results = append(results, got)
	}
	if !reflect.DeepEqual(results[0], results[1]) {
		t.Fatalf("bind/volume byte parity differs:\nbind %+v\nvolume %+v", results[0], results[1])
	}
}
