package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/masterskills"
	"local-agent-workbench/internal/providers"
)

func TestMasterSkillsRequiredOnlyPinnedAndReadWithoutWorkspace(t *testing.T) {
	defs := masterskills.Builtins()
	s := NewMasterSkillSession("intake", defs)
	prompt := s.Prompt(nil, true)
	if len(s.Operation.Skills) != 2 || strings.Contains(prompt, "<master_skill id=\"master-flow\"") {
		t.Fatal("loaded irrelevant instructions")
	}
	defs[1].Instructions = "mutated after turn start"
	defs[1].Configuration["revision"] = 999
	tools := masterSkillTools{session: s}
	result := tools.Execute(context.Background(), "read_skill", json.RawMessage(`{"id":"master-intake"}`))
	if !result.OK || strings.Contains(string(result.Output), "mutated") || strings.Contains(string(result.Output), "999") {
		t.Fatalf("snapshot changed: %s", result.Output)
	}
	result = tools.Execute(context.Background(), "read_skill", json.RawMessage(`{"id":"MASTER-FLOW"}`))
	if !result.OK || len(s.Operation.Skills) != 3 {
		t.Fatalf("read_skill was not attributed: %+v", s.Operation)
	}
	result = tools.Execute(context.Background(), "run_command", json.RawMessage(`{"command":"echo unsafe"}`))
	if result.OK {
		t.Fatal("skills granted a tool")
	}
}

func TestMasterPromptSizeMeasurement(t *testing.T) {
	legacy, err := os.ReadFile("testdata/master-legacy-intake.txt")
	if err != nil {
		t.Fatal(err)
	}
	oldRunes := utf8.RuneCount(legacy)
	core := utf8.RuneCountInString(taskIntakePrompt)
	full := taskIntakePrompt + NewMasterSkillSession("intake", nil).Prompt(nil, true) + masterConversationPrompt(ChatRequest{WorkMode: "discuss"})
	if core*100 > oldRunes*60 {
		t.Fatalf("core reduction below 40%%: old=%d new=%d", oldRunes, core)
	}
	t.Logf("instruction characters: legacy=%d invariant=%d (-%.1f%%), with mandatory skills/catalog/mode=%d; conversation tool schemas separately=%d; actual provider token totals recorded per operation", oldRunes, core, 100*(1-float64(core)/float64(oldRunes)), utf8.RuneCountInString(full), utf8.RuneCount(masterActionSchemas()))
}

type masterSkillTestModel struct{ request *providers.ModelRequest }

func (m masterSkillTestModel) Stream(_ context.Context, req providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	*m.request = req
	return emit(providers.ModelEvent{Kind: providers.EventUsage, InputTokens: 31, OutputTokens: 7})
}
func TestMasterAttributionAndSecretRedaction(t *testing.T) {
	s := NewMasterSkillSession("intake", nil)
	prompt := s.Prompt(nil, false)
	var got providers.ModelRequest
	factory := s.Factory(func(providers.Config) (providers.Model, error) { return masterSkillTestModel{&got}, nil })
	m, err := factory(providers.Config{APIKey: "literal-test-credential"})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Stream(context.Background(), providers.ModelRequest{Messages: []providers.Message{{Role: "system", Content: prompt}, {Role: "user", Content: "literal-test-credential", Images: []providers.ImageContent{{DataBase64: "private-image"}}}}}, func(providers.ModelEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if s.Operation.InputTokens != 31 || s.Operation.OutputTokens != 7 {
		t.Fatal("usage missing")
	}
	if strings.Contains(s.Operation.Replay, "literal-test-credential") || strings.Contains(s.Operation.Replay, "private-image") {
		t.Fatal("secret or image persisted")
	}
	if !strings.Contains(s.Operation.Replay, "REDACTED") {
		t.Fatal("redaction missing")
	}
	for _, attr := range s.Operation.Skills {
		if attr.Digest == "" || attr.Revision != 1 {
			t.Fatalf("invalid attribution %+v", attr)
		}
	}
}

func TestMasterReplayDoesNotAcceptInvalidContracts(t *testing.T) {
	if _, err := ReplayScore("intake", `{"reply":"done","actions":[{"name":"propose_brief","arguments":{"title":"x","brief":{"mode":"nonsense"}}}]}`); err == nil {
		t.Fatal("accepted invalid brief")
	}
	if _, err := ReplayScore("intake", `{"reply":"done","actions":[{"name":"run_command","arguments":{"command":"echo"}}]}`); err == nil {
		t.Fatal("accepted a project tool call")
	}
	if _, err := ReplayRequestScore("planning", MasterSkillFixtures("planning")[0], `{"agentIds":["foreign"],"rationale":"ok","stages":[{"name":"read","agentId":"foreign","instruction":"read","phase":1}]}`); err == nil {
		t.Fatal("accepted invented agent")
	}
	for _, phase := range []string{"intake", "planning", "explanation", "recovery"} {
		for _, req := range MasterSkillFixtures(phase) {
			for _, tool := range req.Tools {
				if !IsMasterActionTool(tool.Name) {
					t.Fatalf("fixture has executable tool %s", tool.Name)
				}
			}
		}
	}
}

func TestMasterBuiltinsHaveNoPermissionOrToolDelta(t *testing.T) {
	for _, s := range masterskills.Builtins() {
		if len(s.RequiredTools)+len(s.PermissionDelta)+len(s.Scripts) > 0 {
			t.Fatalf("skill grants authority: %s", s.ID)
		}
		if domain.SkillDefinitionAttribution(s).Digest != domain.SkillRuntimeAttribution(masterskills.Runtime(s)).Digest {
			t.Fatal("definition/runtime identities differ")
		}
	}
}
