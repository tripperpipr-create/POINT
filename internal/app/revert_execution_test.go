package app

import (
	"testing"

	"local-agent-workbench/internal/domain"
)

// Исполняющий агент читает задачу, а не рассказ о том, как его выбирали.
//
// Описание квеста уходит в контекст агента. Пока задаче не было места в
// предложении, туда попадал Rationale — «пресет conductor · отряд 1 · движком
// Point». Проверяем оба случая: с задачей и без неё (предложения, созданные до
// появления поля, обязаны сохранить прежнее поведение, а не остаться пустыми).
func TestQuestDescriptionCarriesTheTask(t *testing.T) {
	task := "почини вебхук биллинга: 500 на повторной доставке"
	withTask := domain.QuestProposal{
		Title: "Починить вебхук биллинга", Task: task,
		Rationale: "пресет conductor · отряд 1 · движком Point",
	}
	if got := questDescription(withTask); got != task {
		t.Fatalf("в описание квеста ушла не задача: %q", got)
	}

	legacy := domain.QuestProposal{
		Title:     "Починить вебхук биллинга",
		Rationale: "пресет conductor · отряд 1 · движком Point",
	}
	if got := questDescription(legacy); got != legacy.Rationale {
		t.Fatalf("старое предложение осталось без описания: %q", got)
	}
}
