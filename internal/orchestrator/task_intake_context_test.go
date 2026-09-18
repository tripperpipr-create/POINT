package orchestrator

import (
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
	"strings"
	"testing"
)

func TestIntakeContextPreservesSelectedContractAndBoundsUnrelatedTasks(t *testing.T) {
	brief := &domain.TaskBrief{SourceRequest: "original", Decisions: []domain.BriefDecision{{Topic: "output", Decision: "preserve", Source: "user"}}}
	proposals := []domain.QuestProposal{}
	for i := 0; i < 100; i++ {
		proposals = append(proposals, domain.QuestProposal{ID: strings.Repeat("x", i+1), Status: "pending", Task: strings.Repeat("unrelated", 10000)})
	}
	proposals = append(proposals, domain.QuestProposal{ID: "selected", Status: "pending", Brief: brief})
	got := intakeContextProposals(proposals, "selected")
	if len(got) != 9 || got[0].ID != "selected" || got[0].Brief != brief {
		t.Fatal("selected agreements lost or unrelated proposals unbounded")
	}
	for _, p := range got[1:] {
		if p.Task != "" || p.Brief != nil {
			t.Fatal("unrelated full task leaked into context")
		}
	}
	if proposals[0].Task == "" {
		t.Fatal("stored data mutated")
	}
}

func TestIntakeContextRejectsOversizedInputWithoutTruncation(t *testing.T) {
	// Оценка токенов ≈ len/4; окно 128K, ответ 4K → нужен текст заметно больше ~500 КиБ.
	oversized := strings.Repeat("agreement", 80_000) // 720_000 рун ASCII → ~180K токенов
	request := providers.ModelRequest{ContextWindowTokens: intakeContextWindowTokens, MaxOutputTokens: 4096, Messages: []providers.Message{{Role: "user", Content: oversized}}}
	if validateIntakeContext(request) == nil {
		t.Fatal("oversized contract accepted")
	}
	if len(request.Messages[0].Content) != len(oversized) {
		t.Fatal("agreement truncated")
	}
	request.Messages[0].Content = "complete precise request"
	if err := validateIntakeContext(request); err != nil {
		t.Fatal(err)
	}
}
