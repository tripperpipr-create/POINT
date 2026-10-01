package agent

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

func prefixTestRound(step int) conversationRound {
	return conversationRound{
		Step:      step,
		Assistant: providers.Message{Role: "assistant", Content: fmt.Sprintf("step %d %s", step, strings.Repeat("a", 400))},
		Tools: []conversationToolTurn{{
			Call:    providers.ToolCall{ID: fmt.Sprintf("c%d", step), Name: "read_file"},
			Result:  domain.ToolResult{OK: true},
			Message: providers.Message{Role: "tool", ToolCallID: fmt.Sprintf("c%d", step), Content: strings.Repeat("r", 800)},
		}},
	}
}

// Когда разговор перестал влезать, он сжимается с запасом, и следующие ходы
// только дописывают в конец: запрос N+1 продолжает запрос N байт в байт, и
// провайдер берёт прежнее из кэша. Прежде вытеснялось по раунду на ход, и
// хвост после стабильного префикса менялся каждый раз.
func TestCompactionLeavesRoomSoNextTurnsExtendThePrefix(t *testing.T) {
	history := newConversationHistory([]providers.Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "task"}})
	const budget = 3000
	var previous []providers.Message
	compactions := 0
	extended := 0
	for step := 1; step <= 40; step++ {
		history.AppendRound(prefixTestRound(step))
		messages, report, err := history.Prepare(nil, budget)
		if err != nil {
			t.Fatal(err)
		}
		if report.Compacted() {
			compactions++
		} else if previous != nil && len(messages) > len(previous) && reflect.DeepEqual(messages[:len(previous)], previous) {
			extended++
		}
		previous = messages
	}
	if compactions == 0 {
		t.Fatal("the test never filled the budget")
	}
	if extended < compactions {
		t.Fatalf("after compaction the prefix kept moving: %d compactions, only %d turns extended the previous request", compactions, extended)
	}
}
