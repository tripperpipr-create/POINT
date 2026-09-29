package app

import (
	"encoding/json"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

func attemptEvent(kind domain.EventType, payload any) domain.Event {
	data, _ := json.Marshal(payload)
	return domain.Event{Type: kind, Data: data}
}

// E6: интегратор нашёл блокер и начал синхронизацию lock-файла, когда обрыв
// llmux уронил прогон. Отчёт несёт находку, исходы действий и действие в полёте.
func TestPreviousAttemptSummaryKeepsFindingsAndInFlightAction(t *testing.T) {
	events := []domain.Event{
		attemptEvent(domain.EventToolRequested, map[string]any{"tool": "run_command", "arguments": map[string]any{"command": "npm ci"}, "callId": "c1"}),
		attemptEvent(domain.EventToolFinished, map[string]any{"tool": "run_command", "callId": "c1", "result": domain.ToolResult{OK: true}}),
		attemptEvent(domain.EventModelResponded, map[string]any{"content": "Блокер: package-lock.json не содержит ssh2, npm ci упадёт. Синхронизирую lock."}),
		attemptEvent(domain.EventToolRequested, map[string]any{"tool": "run_command", "arguments": map[string]any{"command": "npm install --package-lock-only"}, "callId": "c2"}),
	}
	summary := summarizePreviousAttempt(1, "wsarecv: An established connection was aborted", events, []string{"cf-vue-apps/package.json"})
	for _, want := range []string{"Попытка 1", "wsarecv", "ssh2", "npm ci → ok", "npm install --package-lock-only → в полёте", "cf-vue-apps/package.json", "Продолжай с найденного"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary lacks %q:\n%s", want, summary)
		}
	}
	if len([]rune(summary)) > previousAttemptMaxRunes {
		t.Fatalf("summary is too long: %d runes", len([]rune(summary)))
	}
}

func TestPreviousAttemptContextOnlyWhenRecorded(t *testing.T) {
	run := domain.FlowRun{NodeStates: map[string]domain.FlowNodeState{"work": {Output: map[string]any{}}}}
	if _, ok := previousAttemptContext(run, "work"); ok {
		t.Fatal("first attempt must not carry a previous attempt report")
	}
	run.NodeStates["work"].Output["previousAttempt"] = "Попытка 1 этого этапа прервана: boom"
	if item, ok := previousAttemptContext(run, "work"); !ok || item.Kind != domain.ContextText {
		t.Fatalf("item=%#v ok=%v", item, ok)
	}
}
