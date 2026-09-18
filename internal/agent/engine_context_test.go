package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

type indexedScopeRepairModel struct {
	mu                    sync.Mutex
	calls                 int
	sawDigest             bool
	sawScopeGuardrail     bool
	maxSearchMessageBytes int
}

func (m *indexedScopeRepairModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	for _, message := range request.Messages {
		if message.Role != "tool" {
			continue
		}
		if strings.Contains(message.Content, `"fileSha256"`) {
			m.sawDigest = true
			if len(message.Content) > m.maxSearchMessageBytes {
				m.maxSearchMessageBytes = len(message.Content)
			}
		}
		if strings.Contains(message.Content, `"code":"inspection_scope_required"`) {
			m.sawScopeGuardrail = true
		}
	}
	firstEdit := map[string]string{
		"oldText": `func AlphaQuestMarker() string { return "sealed" }`,
		"newText": `func AlphaQuestMarker() string { return "opened" }`,
	}
	secondEdit := map[string]string{
		"oldText": `func OmegaQuestMarker() string { return "sealed" }`,
		"newText": `func OmegaQuestMarker() string { return "opened" }`,
	}
	switch m.calls {
	case 1:
		arguments, _ := json.Marshal(map[string]any{"query": "AlphaQuestMarker", "max_chunks": 1, "max_chars": 4096})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "search-alpha", Name: "search_code", Arguments: arguments}})
	case 2:
		arguments, _ := json.Marshal(map[string]any{"path": "generated.go", "reason": "update both quest markers", "edits": []map[string]string{firstEdit, secondEdit}})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "patch-unseen-omega", Name: "propose_patch", Arguments: arguments}})
	case 3:
		arguments, _ := json.Marshal(map[string]any{"query": "OmegaQuestMarker", "max_chunks": 1, "max_chars": 4096})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "search-omega", Name: "search_code", Arguments: arguments}})
	case 4:
		arguments, _ := json.Marshal(map[string]any{"path": "generated.go", "reason": "update both inspected quest markers", "edits": []map[string]string{firstEdit, secondEdit}})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "patch-inspected", Name: "propose_patch", Arguments: arguments}})
	default:
		return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Оба найденных участка обновлены."})
	}
}

func TestAgentRepairsIndexedEditScopeWithoutReadingLargeFile(t *testing.T) {
	root := t.TempDir()
	var source strings.Builder
	source.WriteString("package generated\n\n")
	source.WriteString(`func AlphaQuestMarker() string { return "sealed" }` + "\n")
	for index := 0; index < 8_000; index++ {
		fmt.Fprintf(&source, "// filler rune %05d keeps distant fragments separate\n", index)
	}
	source.WriteString(`func OmegaQuestMarker() string { return "sealed" }` + "\n")
	original := source.String()
	if len(original) < 256*1024 || len(original) >= 512*1024 {
		t.Fatalf("large fixture size=%d", len(original))
	}
	if err := os.WriteFile(filepath.Join(root, "generated.go"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &indexedScopeRepairModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"search_code", "propose_patch"}
	profile.MaxSteps = 8
	profile.MaxDurationSeconds = 60
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws-indexed", Path: root},
		Task:          "update both quest markers",
	})
	if err != nil {
		t.Fatal(err)
	}
	resolveNextApproval(t, engine, run.ID, "", true)
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted {
		t.Fatalf("run=%#v", finished)
	}
	if slices.Contains(finished.ToolsUsed, "read_file") || !slices.Contains(finished.ToolsUsed, "search_code") || !slices.Contains(finished.ToolsUsed, "propose_patch") {
		t.Fatalf("unexpected tools=%v", finished.ToolsUsed)
	}
	data, err := os.ReadFile(filepath.Join(root, "generated.go"))
	if err != nil {
		t.Fatal(err)
	}
	updated := string(data)
	if !strings.Contains(updated, `func AlphaQuestMarker() string { return "opened" }`) || !strings.Contains(updated, `func OmegaQuestMarker() string { return "opened" }`) {
		t.Fatalf("indexed edits were not applied")
	}
	if strings.Count(updated, "// filler rune") != 8_000 || len(updated) != len(original) {
		t.Fatal("unrelated large-file content changed")
	}
	model.mu.Lock()
	calls, sawDigest, sawScope, maxSearchBytes := model.calls, model.sawDigest, model.sawScopeGuardrail, model.maxSearchMessageBytes
	model.mu.Unlock()
	if calls != 5 || !sawDigest || !sawScope || maxSearchBytes == 0 || maxSearchBytes > 10*1024 {
		t.Fatalf("model calls=%d digest=%v scope=%v max search message=%d", calls, sawDigest, sawScope, maxSearchBytes)
	}
	codes := guardrailCodes(t, repo, run.ID)
	if len(codes) != 1 || codes[0] != "inspection_scope_required" {
		t.Fatalf("guardrail codes=%v", codes)
	}
}

type retrievalRefinementModel struct {
	mu               sync.Mutex
	calls            int
	sawTruncation    bool
	sawMatchedTokens bool
	maxSearchBytes   int
}

func (m *retrievalRefinementModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	for _, message := range request.Messages {
		if message.Role != "tool" || !strings.Contains(message.Content, `"fileSha256"`) {
			continue
		}
		if strings.Contains(message.Content, `"truncated":true`) && strings.Contains(message.Content, `"candidateChunks":`) {
			m.sawTruncation = true
		}
		if strings.Contains(message.Content, `"matchedTokens"`) {
			m.sawMatchedTokens = true
		}
		if len(message.Content) > m.maxSearchBytes {
			m.maxSearchBytes = len(message.Content)
		}
	}
	switch m.calls {
	case 1:
		arguments := json.RawMessage(`{"query":"quest marker","max_chunks":1,"max_chars":1200}`)
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "broad-search", Name: "search_code", Arguments: arguments}})
	case 2:
		arguments := json.RawMessage(`{"query":"FinalizeRareArtifact","max_chunks":1,"max_chars":2400}`)
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "refined-search", Name: "search_code", Arguments: arguments}})
	case 3:
		arguments, _ := json.Marshal(map[string]any{
			"path": "target.go", "reason": "update the exact declaration found by refined retrieval",
			"edits": []map[string]string{{
				"oldText": `func FinalizeRareArtifact() string { return "sealed" }`,
				"newText": `func FinalizeRareArtifact() string { return "opened" }`,
			}},
		})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "refined-patch", Name: "propose_patch", Arguments: arguments}})
	default:
		return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Точный символ найден и обновлён."})
	}
}

func TestAgentRefinesTruncatedSearchBeforeEditing(t *testing.T) {
	root := t.TempDir()
	var common strings.Builder
	common.WriteString("package common\n")
	for line := 0; line < 4_000; line++ {
		fmt.Fprintf(&common, "// quest marker common lore %04d\n", line)
	}
	if err := os.WriteFile(filepath.Join(root, "common.go"), []byte(common.String()), 0600); err != nil {
		t.Fatal(err)
	}
	target := "package target\n\n" + `func FinalizeRareArtifact() string { return "sealed" }` + "\n"
	if err := os.WriteFile(filepath.Join(root, "target.go"), []byte(target), 0600); err != nil {
		t.Fatal(err)
	}
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &retrievalRefinementModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"search_code", "propose_patch"}
	profile.MaxSteps = 7
	profile.MaxDurationSeconds = 60
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws-retrieval", Path: root},
		Task:          "Update FinalizeRareArtifact without loading unrelated lore",
	})
	if err != nil {
		t.Fatal(err)
	}
	resolveNextApproval(t, engine, run.ID, "", true)
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted || slices.Contains(finished.ToolsUsed, "read_file") {
		t.Fatalf("run=%#v", finished)
	}
	data, err := os.ReadFile(filepath.Join(root, "target.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != strings.Replace(target, `return "sealed"`, `return "opened"`, 1) {
		t.Fatalf("target=%q", data)
	}
	model.mu.Lock()
	calls, sawTruncation, sawMatched, maxSearchBytes := model.calls, model.sawTruncation, model.sawMatchedTokens, model.maxSearchBytes
	model.mu.Unlock()
	if calls != 4 || !sawTruncation || !sawMatched || maxSearchBytes <= 0 || maxSearchBytes > 5*1024 {
		t.Fatalf("calls=%d truncation=%v matched=%v maxSearchBytes=%d", calls, sawTruncation, sawMatched, maxSearchBytes)
	}
}

type dependencyAwareMultiFileModel struct {
	mu                  sync.Mutex
	calls               int
	sawAPIImporter      bool
	sawRelatedTest      bool
	relatedMetadataOnly bool
}

func (m *dependencyAwareMultiFileModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	for _, message := range request.Messages {
		if message.Role != "tool" {
			continue
		}
		var result domain.ToolResult
		if json.Unmarshal([]byte(message.Content), &result) != nil || !result.OK {
			continue
		}
		var output struct {
			RelatedFiles []map[string]any `json:"relatedFiles"`
		}
		if json.Unmarshal(result.Output, &output) != nil || len(output.RelatedFiles) == 0 {
			continue
		}
		m.relatedMetadataOnly = true
		for _, related := range output.RelatedFiles {
			path, _ := related["path"].(string)
			relation, _ := related["relation"].(string)
			if _, leaked := related["content"]; leaked {
				m.relatedMetadataOnly = false
			}
			if path == "src/api.ts" && relation == "imported_by" {
				m.sawAPIImporter = true
			}
			if path == "src/service.test.ts" && relation == "test" {
				m.sawRelatedTest = true
			}
		}
	}
	switch m.calls {
	case 1:
		arguments := json.RawMessage(`{"query":"CreateSession","max_chunks":1,"max_chars":2400,"include_related":true}`)
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "dependency-search", Name: "search_code", Arguments: arguments}})
	case 2:
		arguments := json.RawMessage(`{"query":"src/service.test.ts CreateSession","max_chunks":1,"max_chars":2400}`)
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "test-search", Name: "search_code", Arguments: arguments}})
	case 3:
		servicePatch, _ := json.Marshal(map[string]any{
			"path": "src/service.ts", "reason": "update the inspected implementation",
			"edits": []map[string]string{{
				"oldText": `export function CreateSession() { return 'ok' }`,
				"newText": `export function CreateSession() { return 'secure' }`,
			}},
		})
		if err := emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "service-patch", Name: "propose_patch", Arguments: servicePatch}}); err != nil {
			return err
		}
		testPatch, _ := json.Marshal(map[string]any{
			"path": "src/service.test.ts", "reason": "update the inspected related test",
			"edits": []map[string]string{{
				"oldText": `expect(CreateSession()).toBe('ok')`,
				"newText": `expect(CreateSession()).toBe('secure')`,
			}},
		})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: "test-patch", Name: "propose_patch", Arguments: testPatch}})
	default:
		return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Реализация и связанный тест обновлены."})
	}
}

func TestAgentUsesDependencyGraphForTwoInspectedFileEdits(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"src/service.ts":      "export function CreateSession() { return 'ok' }\n",
		"src/api.ts":          "import { CreateSession } from './service'\nexport const handler = CreateSession\n",
		"src/service.test.ts": "import { CreateSession } from './service'\ntest('session', () => { expect(CreateSession()).toBe('ok') })\n",
	}
	for relative, content := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &dependencyAwareMultiFileModel{}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"search_code", "propose_patch"}
	profile.MaxSteps = 7
	profile.MaxDurationSeconds = 60
	run, err := engine.Start(StartInput{
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
		Workspace:     domain.Workspace{ID: "ws-dependencies", Path: root},
		Task:          "Update CreateSession and its related test",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstApproval := resolveNextApproval(t, engine, run.ID, "", true)
	resolveNextApproval(t, engine, run.ID, firstApproval, true)
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted || slices.Contains(finished.ToolsUsed, "read_file") || len(finished.ChangedFiles) != 2 {
		t.Fatalf("run=%#v", finished)
	}
	service, err := os.ReadFile(filepath.Join(root, "src", "service.ts"))
	if err != nil || !strings.Contains(string(service), "return 'secure'") {
		t.Fatalf("service=%q err=%v", service, err)
	}
	testFile, err := os.ReadFile(filepath.Join(root, "src", "service.test.ts"))
	if err != nil || !strings.Contains(string(testFile), "toBe('secure')") {
		t.Fatalf("test=%q err=%v", testFile, err)
	}
	model.mu.Lock()
	calls, sawAPI, sawTest, metadataOnly := model.calls, model.sawAPIImporter, model.sawRelatedTest, model.relatedMetadataOnly
	model.mu.Unlock()
	if calls != 4 || !sawAPI || !sawTest || !metadataOnly {
		t.Fatalf("calls=%d api=%v test=%v metadataOnly=%v", calls, sawAPI, sawTest, metadataOnly)
	}
	if codes := guardrailCodes(t, repo, run.ID); len(codes) != 0 {
		t.Fatalf("unchanged second file was unnecessarily invalidated: %v", codes)
	}
}

type rollingContextModel struct {
	mu             sync.Mutex
	calls          int
	paths          []string
	budget         int
	maxInputTokens int
	overBudget     bool
	sawMemory      bool
}

func (m *rollingContextModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	estimated := EstimateModelInputTokens(request.Messages, request.Tools)
	if estimated > m.maxInputTokens {
		m.maxInputTokens = estimated
	}
	if estimated > m.budget {
		m.overBudget = true
	}
	for _, message := range request.Messages {
		if strings.Contains(message.Content, "<point_run_memory>") {
			m.sawMemory = true
		}
	}
	if m.calls <= len(m.paths) {
		if err := emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: strings.Repeat("evidence ", 180)}); err != nil {
			return err
		}
		arguments, _ := json.Marshal(map[string]string{"path": m.paths[m.calls-1]})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: fmt.Sprintf("rolling-%d", m.calls), Name: "read_file", Arguments: arguments}})
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Context stayed bounded and the repeated read was executed safely."})
}

func TestEngineRollsContextAndAllowsEvictedReadToRunAgain(t *testing.T) {
	root := t.TempDir()
	paths := make([]string, 0, 11)
	for index := 0; index < 10; index++ {
		name := fmt.Sprintf("file-%02d.txt", index)
		if err := os.WriteFile(filepath.Join(root, name), []byte(strings.Repeat(fmt.Sprintf("line-%02d evidence\n", index), 900)), 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, name)
	}
	paths = append(paths, paths[0])

	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	profile := domain.DefaultProfile()
	profile.AllowedTools = []string{"read_file"}
	profile.MaxSteps = 15
	profile.MaxDurationSeconds = 10
	profile.ContextWindowTokens = 4096
	profile.MaxOutputTokens = 1024
	budget := ModelInputBudgetTokens(profile)
	model := &rollingContextModel{paths: paths, budget: budget}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	configuration := domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC())
	run, err := engine.Start(StartInput{Configuration: configuration, Workspace: domain.Workspace{ID: "ws", Path: root}, Task: "inspect files with bounded memory"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		repo.mu.Lock()
		status := repo.runs[run.ID].Status
		repo.mu.Unlock()
		if status == domain.RunCompleted || status == domain.RunFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	repo.mu.Lock()
	finished := repo.runs[run.ID]
	repo.mu.Unlock()
	if finished.Status != domain.RunCompleted {
		t.Fatalf("run status=%s error=%q", finished.Status, finished.Error)
	}
	model.mu.Lock()
	overBudget, maxInput, sawMemory := model.overBudget, model.maxInputTokens, model.sawMemory
	model.mu.Unlock()
	if overBudget || maxInput > budget || !sawMemory {
		t.Fatalf("rolling context budget=%d max=%d over=%v memory=%v", budget, maxInput, overBudget, sawMemory)
	}
	eventsList, err := repo.ListByRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	compactions, successfulReads := 0, 0
	for _, event := range eventsList {
		if event.Type == domain.EventContextCompacted {
			compactions++
		}
		if event.Type == domain.EventToolFinished {
			var payload struct {
				Tool   string            `json:"tool"`
				Result domain.ToolResult `json:"result"`
			}
			_ = json.Unmarshal(event.Data, &payload)
			if payload.Tool == "read_file" && payload.Result.OK {
				successfulReads++
			}
		}
	}
	if compactions == 0 || successfulReads != len(paths) {
		t.Fatalf("compactions=%d successful reads=%d want=%d", compactions, successfulReads, len(paths))
	}
}
