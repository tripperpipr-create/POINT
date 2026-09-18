package orchestrator

import (
	"context"
	"errors"
	"sync"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

type reasoningThenAnswerModel struct {
	mu sync.Mutex
	// Чем кончилась каждая попытка: предел вывода и было ли погашено
	// размышление. По этому списку видно не только «повтор был», но и в каком
	// порядке ход лечили — а порядок здесь и есть решение.
	tries []providers.ModelRequest
	// answerAt — попытка, начиная с которой модель отвечает; ноль означает
	// «отвечает только с погашенным размышлением».
	answerAt int
}

func (m *reasoningThenAnswerModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	m.tries = append(m.tries, request)
	attempt := len(m.tries)
	m.mu.Unlock()
	if request.DisableThinking || (m.answerAt > 0 && attempt >= m.answerAt) {
		return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: `{"intent":"chat","reply":"ok"}`})
	}
	return errors.New("model returned no answer: the entire output budget of 2000 tokens went to reasoning (finish_reason=length)")
}

// Ход, в котором размышление съело весь вывод, лечится сначала местом для
// ответа. Это обычное поле запроса: оно ничего не стоит на бесплатном рантайме
// и не подменяет ответ модели тишиной.
func TestStreamMasterModelGrowsOutputBudgetFirst(t *testing.T) {
	model := &reasoningThenAnswerModel{answerAt: 2}
	var got string
	// Модель из справочника, которая размышляет: у Qwen3 потолок вывода 32768,
	// и восьми тысяч на мысль и ответ ей не хватило — ровно случай, с которого
	// это лечение и началось.
	err := streamMasterModel(context.Background(), model, providers.ModelRequest{Model: "qwen3.5:9b", MaxOutputTokens: 8192}, true, nil, func(event providers.ModelEvent) error {
		if event.Kind == providers.EventTextDelta {
			got += event.Delta
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	if len(model.tries) != 2 {
		t.Fatalf("попыток %d: %+v", len(model.tries), model.tries)
	}
	if model.tries[1].MaxOutputTokens != domain.MaxThinkingOutputTokens {
		t.Fatalf("предел вывода не вырос: %d", model.tries[1].MaxOutputTokens)
	}
	if model.tries[1].DisableThinking {
		t.Fatal("размышление погашено раньше, чем добавлено место для ответа")
	}
	if got == "" {
		t.Fatal("ответа после повтора нет")
	}
}

// Место кончилось — тогда тишина, и только на своём рантайме. Потолок здесь не
// общий, а семейства: у Qwen2.5 это 8192, и просить у провайдера больше — отказ
// на ровном месте вместо лечения.
func TestStreamMasterModelDisablesThinkingAfterBudgetCeiling(t *testing.T) {
	model := &reasoningThenAnswerModel{}
	var got string
	err := streamMasterModel(context.Background(), model, providers.ModelRequest{Model: "qwen2.5-coder", MaxOutputTokens: 8192}, true, nil, func(event providers.ModelEvent) error {
		if event.Kind == providers.EventTextDelta {
			got += event.Delta
		}
		return nil
	})
	if err != nil || got == "" {
		t.Fatalf("ход не вытащен: %v %q", err, got)
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	// Расти было некуда, поэтому лишнего хода к модели не случилось.
	if len(model.tries) != 2 || !model.tries[1].DisableThinking {
		t.Fatalf("попытки не те: %+v", model.tries)
	}
}

// У официального endpoint'а поля сверх спецификации нет: повтор с погашенным
// «размышлением» вернул бы 400 вместо настоящей причины, и человек прочитал бы
// ошибку формата вместо «весь вывод ушёл в размышление». Место для ответа ему
// добавить можно — это обычный предел вывода.
func TestStreamMasterModelKeepsReasoningErrorOnStrictEndpoint(t *testing.T) {
	model := &reasoningThenAnswerModel{}
	err := streamMasterModel(context.Background(), model, providers.ModelRequest{Model: "gpt", MaxOutputTokens: 2000}, false, nil, func(providers.ModelEvent) error {
		return nil
	})
	if err == nil {
		t.Fatal("строгий endpoint получил повтор с полем сверх спецификации")
	}
	if !providers.IsTruncatedReasoningError(err) {
		t.Fatalf("причина отказа подменена: %v", err)
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	for _, try := range model.tries {
		if try.DisableThinking {
			t.Fatalf("размышление погашено на чужом рантайме: %+v", model.tries)
		}
	}
	if len(model.tries) != 2 {
		t.Fatalf("попыток %d: %+v", len(model.tries), model.tries)
	}
}

// Вывод не может занимать больше половины окна: остальное нужно разговору.
func TestStreamMasterModelKeepsRoomForTheConversation(t *testing.T) {
	model := &reasoningThenAnswerModel{}
	_ = streamMasterModel(context.Background(), model, providers.ModelRequest{
		Model: "gpt", MaxOutputTokens: 8192, ContextWindowTokens: 16384,
	}, false, nil, func(providers.ModelEvent) error { return nil })
	model.mu.Lock()
	defer model.mu.Unlock()
	if len(model.tries) != 1 {
		t.Fatalf("окна не хватало, а повтор случился: %+v", model.tries)
	}
}
