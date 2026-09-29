package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
)

// Повтор этапа знает, чем кончилась прерванная попытка (Q11, E6).
//
// В E6 интегратор нашёл блокер — lock-файл без `ssh2` — и начал синхронизацию,
// когда обрыв соединения с llmux уронил прогон. Автоповтор начал этап с нуля:
// он не знал ни находки, ни начатого действия и записал блокер «риском вне
// scope». Попытка — не только ошибка: её последние слова и действия — факты,
// которые следующей попытке не нужно добывать заново.

const (
	previousAttemptTextRunes = 1500
	previousAttemptSteps     = 6
	previousAttemptMaxRunes  = 3000
)

type previousAttemptStep struct {
	tool      string
	arguments string
	callID    string
	finished  bool
	ok        bool
	errorCode string
}

// previousAttemptSummary собирает из журнала прогона короткий отчёт о
// прерванной попытке: ошибку, последний ответ модели, последние действия с
// исходом, действие в полёте и изменённые файлы.
func (a *App) previousAttemptSummary(ctx context.Context, execution domain.ExecutionInstance, attempt int) string {
	var events []domain.Event
	var changed []string
	if runID := strings.TrimSpace(execution.RunID); runID != "" {
		events, _ = a.store.ListByRun(ctx, runID)
		if run, err := a.store.GetRun(ctx, runID); err == nil {
			changed = run.ChangedFiles
		}
	}
	return summarizePreviousAttempt(attempt, execution.Error, events, changed)
}

func summarizePreviousAttempt(attempt int, failure string, events []domain.Event, changed []string) string {
	lastText := ""
	var steps []*previousAttemptStep
	byCall := map[string]*previousAttemptStep{}
	for _, event := range events {
		switch event.Type {
		case domain.EventModelResponded:
			var payload struct {
				Content string `json:"content"`
			}
			if json.Unmarshal(event.Data, &payload) == nil && strings.TrimSpace(payload.Content) != "" {
				lastText = strings.TrimSpace(payload.Content)
			}
		case domain.EventToolRequested:
			var payload struct {
				Tool      string          `json:"tool"`
				Arguments json.RawMessage `json:"arguments"`
				CallID    string          `json:"callId"`
			}
			if json.Unmarshal(event.Data, &payload) != nil || payload.Tool == "" || payload.CallID == "" {
				continue
			}
			step := &previousAttemptStep{tool: payload.Tool, arguments: compactArguments(payload.Arguments), callID: payload.CallID}
			steps = append(steps, step)
			byCall[payload.CallID] = step
		case domain.EventToolFinished:
			var payload struct {
				CallID string            `json:"callId"`
				Result domain.ToolResult `json:"result"`
			}
			if json.Unmarshal(event.Data, &payload) != nil {
				continue
			}
			if step := byCall[payload.CallID]; step != nil {
				step.finished, step.ok = true, payload.Result.OK
				if payload.Result.Error != nil {
					step.errorCode = payload.Result.Error.Code
				}
			}
		}
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("Попытка %d этого этапа прервана: %s", attempt, strings.TrimSpace(failure)))
	if lastText != "" {
		lines = append(lines, "Последний ответ модели в ней:\n"+truncateRunes(lastText, previousAttemptTextRunes))
	}
	if len(steps) > 0 {
		recent := steps[max(0, len(steps)-previousAttemptSteps):]
		lines = append(lines, "Последние действия:")
		for _, step := range recent {
			outcome := "ok"
			switch {
			case !step.finished:
				outcome = "в полёте: результат не получен, действие могло не выполниться или выполниться частично"
			case !step.ok:
				outcome = "ошибка " + step.errorCode
			}
			lines = append(lines, fmt.Sprintf("- %s %s → %s", step.tool, step.arguments, outcome))
		}
	}
	if len(changed) > 0 {
		shown := changed[:min(len(changed), 12)]
		lines = append(lines, "Изменённые в ней файлы (в эту попытку не перенесены): "+strings.Join(shown, ", "))
	}
	lines = append(lines, "Продолжай с найденного, а не с начала исследования; действие в полёте проверь, прежде чем повторять.")
	return truncateRunes(security.Redact(strings.Join(lines, "\n")), previousAttemptMaxRunes)
}

func compactArguments(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) == nil {
		for _, key := range []string{"command", "path", "query", "pattern"} {
			if text, ok := value[key].(string); ok && text != "" {
				return truncateRunes(text, 160)
			}
		}
	}
	return truncateRunes(string(raw), 160)
}

// previousAttemptContext — элемент контекста повторной попытки этапа.
func previousAttemptContext(flowRun domain.FlowRun, nodeID string) (domain.RunContextInput, bool) {
	summary, _ := flowRun.NodeStates[nodeID].Output["previousAttempt"].(string)
	if strings.TrimSpace(summary) == "" {
		return domain.RunContextInput{}, false
	}
	return domain.RunContextInput{Kind: domain.ContextText, Label: "Прерванная попытка этапа", Content: summary}, true
}
