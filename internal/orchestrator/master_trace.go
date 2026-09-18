package orchestrator

import (
	"encoding/json"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

// Живой след хода Мастера: что он думает и чем смотрит проект прямо сейчас.
//
// До этого ход показывал одно слово на всё ожидание — «Изучаю проект…», — и
// полторы минуты молчания были неотличимы от зависшей модели. Рассуждение и
// обращения к инструментам ядро знало с самого начала, но рассказывало о них
// задним числом, уже готовой репликой.
//
// Text события остаётся короткой человеческой строкой (или пустым), а всё
// содержимое уходит в detail: строку ожидания рисуют и те сборки оболочки,
// которые о подробностях не знают, и сырой JSON в ней был бы мусором.
const (
	// Рассуждение приходит десятками мелких кусков в секунду. Каждый кусок —
	// это запись в журнал хода и кадр SSE; на длинной мысли это тысячи записей
	// ради текста, который человек всё равно читает целиком.
	masterTraceInterval = 400 * time.Millisecond
	// Сколько мысли доезжает до ленты за ход. Дальше молчим: длинная мысль
	// целиком сохранится вместе с готовой репликой, а журнал хода не должен
	// расти на десятки мегабайт ради того, что человек читает один раз.
	masterTraceMindLimit = 32000
)

type masterTrace struct {
	emit    func(kind, text, detail string)
	mind    strings.Builder
	sent    int
	sentAt  time.Time
	pending bool
	round   int
}

func newMasterTrace(s ChatService) *masterTrace {
	return &masterTrace{emit: s.emitDetail}
}

// think копит рассуждение и отдаёт его приростом, а не целиком: мысль на
// восемь тысяч знаков, пересланная четыре раза в секунду, превратила бы журнал
// одного хода в мегабайты одного и того же текста.
func (t *masterTrace) think(delta string) {
	if t == nil || delta == "" {
		return
	}
	t.mind.WriteString(delta)
	t.pending = true
	if time.Since(t.sentAt) < masterTraceInterval {
		return
	}
	t.flush()
}

func (t *masterTrace) flush() {
	if t == nil || !t.pending {
		return
	}
	t.pending = false
	t.sentAt = time.Now()
	full := t.mind.String()
	if len(full) <= t.sent || t.sent >= masterTraceMindLimit {
		return
	}
	delta := full[t.sent:]
	t.sent = len(full)
	t.send("reasoning", "", map[string]any{"round": t.round, "delta": delta})
}

// toolStart называет обращение до того, как оно отработает: чтение большого
// файла идёт секунды, и всё это время человек должен видеть, чего ждёт.
func (t *masterTrace) toolStart(call providers.ToolCall) {
	if t == nil {
		return
	}
	t.flush()
	t.send("tools", call.Name, map[string]any{
		"round": t.round, "tool": call.Name, "argument": masterStepArgument(call.Arguments), "phase": "start",
	})
}

// toolDone закрывает обращение его исходом. Неудача названа неудачей: ответ,
// собранный с ошибкой инструмента, читается иначе.
func (t *masterTrace) toolDone(call providers.ToolCall, result domain.ToolResult) {
	if t == nil {
		return
	}
	t.send("tool_result", "", map[string]any{
		"round": t.round, "tool": call.Name, "argument": masterStepArgument(call.Arguments),
		"result": masterStepResult(result), "failed": !result.OK, "truncated": result.Truncated, "phase": "done",
	})
}

// retry называет повторную попытку хода. Повтор идёт столько же, сколько
// первая попытка, и без строки в ленте выглядит как зависшая модель: тот же
// «Ожидаю модель…», только теперь на пять минут.
func (t *masterTrace) retry(what string, detail map[string]any) {
	if t == nil {
		return
	}
	t.flush()
	if detail == nil {
		detail = map[string]any{}
	}
	detail["round"] = t.round
	detail["what"] = what
	t.send("retry", what, detail)
}

func (t *masterTrace) send(kind, text string, detail map[string]any) {
	if t == nil || t.emit == nil {
		return
	}
	payload, err := json.Marshal(detail)
	if err != nil {
		payload = nil
	}
	t.emit(kind, text, string(payload))
}
