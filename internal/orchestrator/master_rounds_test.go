package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

// Замер 25.09–02.10: круг Мастера ~40 с, почти всё — размышление, и каждый из
// 112 кругов звал один инструмент. Круг после успешных чтений идёт без
// размышления, а ответ пишет только размышляющий круг.
func TestMasterReadRoundsSkipThinkingButAnswerThinks(t *testing.T) {
	model := &turnModel{rounds: []roundScript{
		{calls: []providers.ToolCall{toolCall("r1", "read_file", map[string]string{"path": "a.go"})}},
		{calls: []providers.ToolCall{toolCall("r2", "read_file", map[string]string{"path": "b.go"})}},
		{text: "черновик без раздумий"},
		{text: "Ответ по a.go и b.go."},
	}}
	var replies, rounds []string
	service := ChatService{Store: newChatStoreStub(), ReadTools: readingToolsStub{}, ModelFactory: model.factory(),
		OnProgress: func(kind, text, detail string) {
			switch kind {
			case "reply":
				replies = append(replies, text)
			case "round":
				rounds = append(rounds, detail)
			}
		}}
	response, err := service.Chat(context.Background(), intakeRequest("Как устроен a.go?"))
	if err != nil {
		t.Fatal(err)
	}
	want := []bool{false, true, true, false}
	if len(model.requests) != len(want) {
		t.Fatalf("кругов %d вместо %d", len(model.requests), len(want))
	}
	for index, disabled := range want {
		if model.requests[index].DisableThinking != disabled {
			t.Fatalf("круг %d: DisableThinking=%v, ждали %v", index+1, model.requests[index].DisableThinking, disabled)
		}
	}
	if response.Reply != "Ответ по a.go и b.go." {
		t.Fatalf("ответ = %q", response.Reply)
	}
	for _, reply := range replies {
		if strings.Contains(reply, "черновик") {
			t.Fatalf("текст быстрого круга дошёл до человека: %q", reply)
		}
	}
	if len(rounds) != 4 || !strings.Contains(rounds[1], `"thinking":"off"`) || !strings.Contains(rounds[3], `"rerun":true`) {
		t.Fatalf("замер кругов: %q", rounds)
	}
}

// Задание оформляет только круг с размышлением: propose_brief из быстрого
// круга не исполняется.
func TestMasterFastRoundDoesNotProposeBrief(t *testing.T) {
	model := &turnModel{rounds: []roundScript{
		{calls: []providers.ToolCall{toolCall("r1", "read_file", map[string]string{"path": "a.go"})}},
		{calls: []providers.ToolCall{proposeBriefCall("b1", "Наспех", "", validBrief())}},
		{text: "Разобрался: менять ничего не нужно."},
	}}
	var started []string
	service := ChatService{Store: newChatStoreStub(), ReadTools: readingToolsStub{}, ModelFactory: model.factory(),
		OnProgress: func(kind, text, _ string) {
			if kind == "tools" {
				started = append(started, text)
			}
		}}
	if _, err := service.Chat(context.Background(), intakeRequest("Нужно ли менять a.go?")); err != nil {
		t.Fatal(err)
	}
	for _, name := range started {
		if name == masterActionProposeBrief {
			t.Fatal("задание быстрого круга исполнено")
		}
	}
	if len(model.requests) != 3 || !model.requests[1].DisableThinking || model.requests[2].DisableThinking {
		t.Fatalf("переигровка с размышлением не случилась: %d кругов", len(model.requests))
	}
}

// Платный рантайм гасит размышление сам; быстрых кругов и переигровок нет.
func TestMasterPaidRuntimeKeepsRoundsAsBefore(t *testing.T) {
	model := &turnModel{rounds: []roundScript{
		{calls: []providers.ToolCall{toolCall("r1", "read_file", map[string]string{"path": "a.go"})}},
		{text: "Ответ."},
	}}
	request := intakeRequest("Как устроен a.go?")
	request.Config = domain.OrchestratorConfig{Provider: domain.ProviderOpenAI, ProviderPreset: "openai", Model: "model"}
	service := ChatService{Store: newChatStoreStub(), ReadTools: readingToolsStub{}, ModelFactory: model.factory()}
	if _, err := service.Chat(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 2 || model.requests[1].DisableThinking {
		t.Fatalf("платный рантайм получил быстрые круги: %d кругов", len(model.requests))
	}
}

type slowReadingTools struct {
	running, peak atomic.Int32
	mu            sync.Mutex
}

func (*slowReadingTools) Definitions() []domain.ToolDefinition {
	return []domain.ToolDefinition{{Name: "read_file", Description: "read"}}
}

func (s *slowReadingTools) Execute(_ context.Context, _ string, raw json.RawMessage) domain.ToolResult {
	now := s.running.Add(1)
	s.mu.Lock()
	if now > s.peak.Load() {
		s.peak.Store(now)
	}
	s.mu.Unlock()
	time.Sleep(200 * time.Millisecond)
	s.running.Add(-1)
	var input struct{ Path string }
	_ = json.Unmarshal(raw, &input)
	output, _ := json.Marshal("content of " + input.Path)
	return domain.ToolResult{OK: true, Output: output}
}

// Независимые чтения круга идут разом, а результаты — в порядке вызовов;
// повтор того же вызова в круге получает отказ-дубликат.
func TestMasterReadsOfOneRoundRunTogetherInOrder(t *testing.T) {
	model := &turnModel{rounds: []roundScript{
		{calls: []providers.ToolCall{
			toolCall("r1", "read_file", map[string]string{"path": "a.go"}),
			toolCall("r2", "read_file", map[string]string{"path": "b.go"}),
			toolCall("r3", "read_file", map[string]string{"path": "c.go"}),
			toolCall("r4", "read_file", map[string]string{"path": "a.go"}),
		}},
		{text: "Ответ."},
	}}
	tools := &slowReadingTools{}
	service := ChatService{Store: newChatStoreStub(), ReadTools: tools, ModelFactory: model.factory()}
	started := time.Now()
	if _, err := service.Chat(context.Background(), intakeRequest("Что в a, b и c?")); err != nil {
		t.Fatal(err)
	}
	if tools.peak.Load() != 3 || time.Since(started) > 550*time.Millisecond {
		t.Fatalf("чтения шли по очереди: одновременно %d, всего %s", tools.peak.Load(), time.Since(started))
	}
	var results []providers.Message
	for _, message := range model.requests[1].Messages {
		if message.Role == "tool" {
			results = append(results, message)
		}
	}
	if len(results) != 4 {
		t.Fatalf("результатов %d", len(results))
	}
	for index, path := range []string{"a.go", "b.go", "c.go"} {
		if results[index].ToolCallID != "r"+string(rune('1'+index)) || !strings.Contains(results[index].Content, path) {
			t.Fatalf("результат %d не на своём месте: %+v", index, results[index])
		}
	}
	if !strings.Contains(results[3].Content, "duplicate_tool_call") {
		t.Fatalf("повтор в круге не отклонён: %s", results[3].Content)
	}
	// После отказа-дубликата следующий круг думает.
	if model.requests[1].DisableThinking {
		t.Fatal("после отказа круг должен идти с размышлением")
	}
}
