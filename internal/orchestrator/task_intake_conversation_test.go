package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

// conversationScopedStub — хранилище одной беседы: задания проекта общие, а
// своими беседа считает только названные в её переписке.
type conversationScopedStub struct {
	*chatStoreStub
	own []string
}

func (s conversationScopedStub) ConversationProposalIDs(context.Context, string) ([]string, error) {
	return s.own, nil
}

// Q02: во втором чате проекта Мастер видел в снимке задания первого и по их
// id переписывал чужое своим, а наряд второго чата начинал ссылаться на
// задание первого.
func TestTaskIntakeDoesNotSeeOrRewriteAnotherConversationsProposal(t *testing.T) {
	base := newChatStoreStub()
	foreignBrief := domain.TaskBrief{Mode: domain.TaskModePrecise, Goal: "Чужая работа", ResultKind: "code", Version: 1}
	base.proposals = []domain.QuestProposal{
		{ID: "qp_foreign", WorkspaceID: "ws", Title: "Задание первого чата", Status: "pending", Brief: &foreignBrief},
		{ID: "qp_own", WorkspaceID: "ws", Title: "Задание этого чата", Status: "pending"},
	}
	store := conversationScopedStub{chatStoreStub: base, own: []string{"qp_own"}}
	brief := domain.TaskBrief{
		Mode: domain.TaskModePrecise, Goal: "Собрать REST-эндпоинт", ResultKind: "code",
		Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Эндпоинт отвечает", Kind: "manual"}},
	}
	model := &turnModel{rounds: []roundScript{{text: "Готово", calls: []providers.ToolCall{proposeBriefCall("b1", "Эндпоинт", "qp_foreign", brief)}}}}
	service := ChatService{Store: store, ModelFactory: model.factory()}
	req := ChatRequest{
		WorkspaceID: "ws", TaskIntake: true, Message: "Сделай эндпоинт",
		Config: domain.OrchestratorConfig{Provider: domain.ProviderOllama, Model: "model"},
	}
	response, err := service.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(model.requests) == 0 {
		t.Fatal("модель не вызвана")
	}
	sent, _ := json.Marshal(model.requests[0].Messages)
	if strings.Contains(string(sent), "qp_foreign") {
		t.Fatal("в снимок Мастера попало задание другой беседы")
	}
	if !strings.Contains(string(sent), "qp_own") {
		t.Fatal("задание своей беседы пропало из снимка")
	}
	if response.Proposal == nil || response.Proposal.ID == "qp_foreign" {
		t.Fatalf("ход присвоил задание другой беседы: %#v", response.Proposal)
	}
	for _, proposal := range base.proposals {
		if proposal.ID == "qp_foreign" && (proposal.Title != "Задание первого чата" || proposal.Brief.Goal != "Чужая работа") {
			t.Fatalf("задание другой беседы переписано: %#v", proposal)
		}
	}

	// Человек сам открыл задание кнопкой «Обсудить»: это его выбор, и оно
	// обсуждается здесь, хотя родилось в другом чате.
	model.requests = nil
	req.ProposalID = "qp_foreign"
	response, err = service.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("выбранное человеком задание не нашлось: %v", err)
	}
	if response.Proposal == nil || response.Proposal.ID != "qp_foreign" {
		t.Fatalf("обсуждение выбранного человеком задания ушло в новое: %#v", response.Proposal)
	}
}
