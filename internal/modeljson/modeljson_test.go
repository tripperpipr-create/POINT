package modeljson

import (
	"errors"
	"testing"
)

func TestPayloadStripsReasoningBeforeJSON(t *testing.T) {
	// Ровно тот случай, ради которого пакет и заведён: рассуждающая модель
	// показывает ход мысли, а внутри мысли встречается фигурная скобка.
	raw := "<think>Сначала проверю, есть ли {структура} в ответе</think>\n{\"reply\":\"готово\"}"
	text, err := Payload(raw)
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if text != `{"reply":"готово"}` {
		t.Fatalf("размышление не снято: %q", text)
	}
	narrowed, ok := Braces(text)
	if !ok || narrowed != `{"reply":"готово"}` {
		t.Fatalf("сужение по скобкам: %q ok=%v", narrowed, ok)
	}
}

func TestPayloadStripsFence(t *testing.T) {
	text, err := Payload("```json\n{\"a\":1}\n```")
	if err != nil || text != `{"a":1}` {
		t.Fatalf("ограда не снята: %q err=%v", text, err)
	}
	if _, err := Payload("```json\n{\"a\":1}"); !errors.Is(err, ErrFence) {
		t.Fatalf("незакрытая ограда обязана называться отдельно: %v", err)
	}
}

func TestPayloadLeavesPlainTextAlone(t *testing.T) {
	text, err := Payload("  обычный ответ прозой  ")
	if err != nil || text != "обычный ответ прозой" {
		t.Fatalf("текст без разметки не трогаем: %q err=%v", text, err)
	}
	if _, ok := Braces("обычный ответ прозой"); ok {
		t.Fatal("скобок нет — сужать нечего")
	}
}
