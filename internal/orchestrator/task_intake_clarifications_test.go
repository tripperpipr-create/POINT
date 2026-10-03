package orchestrator

import (
	"context"
	"reflect"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

// Q10 (E1/E2): «незащищённые ветки» из второй реплики человека превратились
// в «не default branch» — до исполнителя дошли первый запрос и пересказ
// Мастера. Реплика, после которой задание переоформлено, хранится дословно.
func TestTaskIntakeKeepsHumanClarificationsVerbatim(t *testing.T) {
	store := newChatStoreStub()
	store.proposals = []domain.QuestProposal{{
		ID: "qp_deploy", WorkspaceID: "ws", Title: "Деплой", Status: "pending",
		Brief: &domain.TaskBrief{SourceRequest: "Добавь деплой на тестовые серверы", Mode: domain.TaskModePrecise, Goal: "Деплой", ResultKind: "code", Version: 1},
	}}
	brief := domain.TaskBrief{
		Mode: domain.TaskModePrecise, Goal: "Деплой на test4–7 из веток", ResultKind: "code",
		Criteria: []domain.AcceptanceCriterion{{ID: "c1", Text: "Пайплайн собирается", Kind: "manual"}},
	}
	model := &turnModel{rounds: []roundScript{{text: "Уточнил.", calls: []providers.ToolCall{proposeBriefCall("b1", "Деплой", "qp_deploy", brief)}}}}
	service := ChatService{Store: store, ModelFactory: model.factory()}
	request := ChatRequest{WorkspaceID: "ws", TaskIntake: true, ProposalID: "qp_deploy", Message: "Только из незащищённых веток", Config: domain.OrchestratorConfig{Provider: domain.ProviderOllama, Model: "model"}}
	response, err := service.Chat(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Proposal == nil || response.Proposal.Brief == nil {
		t.Fatal("задание не переоформлено")
	}
	got := response.Proposal.Brief
	if got.SourceRequest != "Добавь деплой на тестовые серверы" || !reflect.DeepEqual(got.Clarifications, []string{"Только из незащищённых веток"}) {
		t.Fatalf("слова человека потеряны: source=%q clarifications=%q", got.SourceRequest, got.Clarifications)
	}
}

func TestAppendClarificationSkipsRepeatsAndKeepsTheLatest(t *testing.T) {
	values := domain.AppendClarification("первый запрос", nil, "первый запрос")
	if len(values) != 0 {
		t.Fatalf("исходный запрос стал уточнением: %q", values)
	}
	for index := 0; index < 10; index++ {
		values = domain.AppendClarification("первый запрос", values, string(rune('a'+index)))
		values = domain.AppendClarification("первый запрос", values, string(rune('a'+index)))
	}
	if len(values) != 8 || values[0] != "c" || values[7] != "j" {
		t.Fatalf("уточнения: %q", values)
	}
}
