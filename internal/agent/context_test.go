package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

func TestConversationCompactionPreservesStablePrefixAndCompleteToolRounds(t *testing.T) {
	stable := []providers.Message{{Role: "system", Content: "fixed-system"}, {Role: "user", Content: "fixed-task"}}
	history := newConversationHistory(stable)
	for step := 1; step <= 6; step++ {
		call := providers.ToolCall{ID: fmt.Sprintf("call-%d", step), Name: "read_file", Arguments: json.RawMessage(fmt.Sprintf(`{"path":"file-%d.go"}`, step))}
		result := domain.ToolResult{OK: true, Output: json.RawMessage(`{"content":"` + strings.Repeat("данные", 700) + `"}`)}
		payload, _ := json.Marshal(result)
		history.AppendRound(conversationRound{
			Step:      step,
			Assistant: providers.Message{Role: "assistant", Content: fmt.Sprintf("inspect %d", step), ToolCalls: []providers.ToolCall{call}},
			Tools:     []conversationToolTurn{{Call: call, Result: result, Message: providers.Message{Role: "tool", ToolCallID: call.ID, Content: string(payload)}, ExecutionKey: fmt.Sprintf("key-%d", step), Replayable: true}},
		})
	}
	tools := []domain.ToolDefinition{{Name: "read_file", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	messages, report, err := history.Prepare(tools, 2600)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Compacted() || report.AfterTokens > report.BudgetTokens || report.RemovedRounds == 0 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if len(messages) < 2 || messages[0].Role != stable[0].Role || messages[0].Content != stable[0].Content || messages[1].Role != stable[1].Role || messages[1].Content != stable[1].Content {
		t.Fatalf("stable prefix changed: %#v", messages)
	}
	if !containsMessageText(messages, "<point_run_memory>") {
		t.Fatalf("deterministic memory missing: %#v", messages)
	}
	if !containsToolCall(messages, "call-6") {
		t.Fatalf("latest complete round was not retained: %#v", messages)
	}
	assertCompleteToolPairs(t, messages)
	if len(report.ReleasedReplayableKeys) == 0 {
		t.Fatalf("evicted read calls were not released: %#v", report)
	}
}

func TestConversationCompactsOneOversizedToolResultWithoutBreakingJSON(t *testing.T) {
	history := newConversationHistory([]providers.Message{{Role: "system", Content: "system"}, {Role: "user", Content: "task"}})
	call := providers.ToolCall{ID: "huge", Name: "read_file", Arguments: json.RawMessage(`{"path":"large.go"}`)}
	result := domain.ToolResult{OK: true, Output: json.RawMessage(`{"content":"` + strings.Repeat("λ", 70_000) + `"}`)}
	payload, _ := json.Marshal(result)
	history.AppendRound(conversationRound{
		Step:      1,
		Assistant: providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{call}},
		Tools:     []conversationToolTurn{{Call: call, Result: result, Message: providers.Message{Role: "tool", ToolCallID: call.ID, Content: string(payload)}, ExecutionKey: "huge-read", Replayable: true}},
	})

	messages, report, err := history.Prepare(nil, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if report.ReducedToolMessages != 1 || report.AfterTokens > 2048 {
		t.Fatalf("oversized result was not compacted: %#v", report)
	}
	if len(report.ReleasedReplayableKeys) != 1 || report.ReleasedReplayableKeys[0] != "huge-read" {
		t.Fatalf("compacted read was not released for refresh: %#v", report)
	}
	for _, message := range messages {
		if message.Role != "tool" {
			continue
		}
		if !json.Valid([]byte(message.Content)) || !strings.Contains(message.Content, `"contextCompacted":true`) {
			t.Fatalf("compacted tool payload is invalid: %s", message.Content)
		}
		var compacted struct {
			Output struct {
				OriginalBytes int `json:"originalBytes"`
			} `json:"output"`
		}
		if err := json.Unmarshal([]byte(message.Content), &compacted); err != nil || compacted.Output.OriginalBytes != len(payload) {
			t.Fatalf("original payload evidence was lost: %#v err=%v want=%d", compacted, err, len(payload))
		}
		if !utf8.ValidString(message.Content) {
			t.Fatal("compacted tool payload is not valid UTF-8")
		}
	}
}

func TestConversationOnlyReleasesEvictedReadCompletions(t *testing.T) {
	history := newConversationHistory([]providers.Message{{Role: "system", Content: "system"}, {Role: "user", Content: "task"}})
	for step, name := range []string{"read_file", "propose_patch", "search_code", "run_command"} {
		call := providers.ToolCall{ID: fmt.Sprintf("c-%d", step), Name: name, Arguments: json.RawMessage(`{}`)}
		result := domain.ToolResult{OK: true, Output: json.RawMessage(`{"value":"` + strings.Repeat("x", 5000) + `"}`)}
		payload, _ := json.Marshal(result)
		history.AppendRound(conversationRound{
			Step:      step + 1,
			Assistant: providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{call}},
			Tools:     []conversationToolTurn{{Call: call, Result: result, Message: providers.Message{Role: "tool", ToolCallID: call.ID, Content: string(payload)}, ExecutionKey: name, Replayable: isReplayableReadTool(name)}},
		})
	}
	_, report, err := history.Prepare(nil, 1400)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range report.ReleasedReplayableKeys {
		if key == "propose_patch" || key == "run_command" {
			t.Fatalf("side-effect completion was released: %#v", report)
		}
	}
}

func TestConversationRejectsImmutablePrefixOverBudget(t *testing.T) {
	history := newConversationHistory([]providers.Message{{Role: "system", Content: strings.Repeat("x", 20_000)}, {Role: "user", Content: "task"}})
	if _, _, err := history.Prepare(nil, 1024); err == nil || !strings.Contains(err.Error(), "immutable task context") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBoundedEvidenceIsDeterministicAndUTF8Safe(t *testing.T) {
	value := strings.Repeat("мир🌍", 1000)
	first := boundedEvidence(value, 137)
	second := boundedEvidence(value, 137)
	if first != second || !utf8.ValidString(first) || len(first) > 137 {
		t.Fatalf("invalid bounded evidence: len=%d valid=%v deterministic=%v", len(first), utf8.ValidString(first), first == second)
	}
}

func assertCompleteToolPairs(t *testing.T, messages []providers.Message) {
	t.Helper()
	known := make(map[string]struct{})
	for _, message := range messages {
		if message.Role == "assistant" {
			for _, call := range message.ToolCalls {
				known[call.ID] = struct{}{}
			}
		}
		if message.Role == "tool" {
			if _, ok := known[message.ToolCallID]; !ok {
				t.Fatalf("orphan tool message %q in %#v", message.ToolCallID, messages)
			}
		}
	}
}

func containsMessageText(messages []providers.Message, needle string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, needle) {
			return true
		}
	}
	return false
}

func containsToolCall(messages []providers.Message, id string) bool {
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if call.ID == id {
				return true
			}
		}
	}
	return false
}
