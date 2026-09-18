package orchestrator

import (
	"fmt"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

func TestMasterModelHistorySummarizesOlderTurns(t *testing.T) {
	history := make([]domain.CompanionMessage, 0, 28)
	for index := 0; index < 28; index++ {
		role := "user"
		if index%2 == 1 {
			role = "assistant"
		}
		history = append(history, domain.CompanionMessage{Role: role, Content: fmt.Sprintf("master-turn-%02d", index)})
	}
	messages := masterModelHistory(history)
	if len(messages) != maxMasterHistoryTurn+1 {
		t.Fatalf("summary + recent turns=%d", len(messages))
	}
	if !strings.Contains(messages[0].Content, "master-turn-00") || !strings.Contains(messages[0].Content, "резюме") {
		t.Fatalf("old turns not summarized: %#v", messages[0])
	}
	if got := messages[len(messages)-1].Content; got != "master-turn-27" {
		t.Fatalf("recent tail lost: %q", got)
	}
}
