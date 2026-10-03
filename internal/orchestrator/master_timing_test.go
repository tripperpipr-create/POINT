package orchestrator

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"local-agent-workbench/internal/providers"
)

func TestMasterTimingRecordsEveryRetry(t *testing.T) {
	model := &reasoningThenAnswerModel{answerAt: 2}
	var events []map[string]any
	ctx := WithMasterTiming(context.Background(), func(kind string, fields map[string]any) {
		if kind != "model_call" {
			t.Fatal(kind)
		}
		events = append(events, fields)
	})
	err := streamMasterModel(ctx, model, providers.ModelRequest{Model: "qwen3.5:9b", MaxOutputTokens: 8192}, true, nil, func(providers.ModelEvent) error { return nil })
	if err != nil || len(events) != 2 {
		t.Fatalf("%v %+v", err, events)
	}
	if events[0]["failed"] != true || events[1]["failed"] != false {
		t.Fatal(events)
	}
	for _, event := range events {
		start := event["startedAt"].(time.Time)
		end := event["endedAt"].(time.Time)
		if start.IsZero() || end.Before(start) || event["durationMs"] != end.Sub(start).Milliseconds() {
			t.Fatal(event)
		}
	}
}

func TestMasterReadTimingCoversActualParallelExecution(t *testing.T) {
	tools := &slowReadingTools{}
	calls := []masterReadJob{{0, toolCall("a", "read_file", map[string]string{"path": "a.go"})}, {1, toolCall("b", "read_file", map[string]string{"path": "b.go"})}}
	results := executeMasterReadCalls(context.Background(), tools, calls, 0)
	if len(results) != 2 || !results[0].result.OK || !results[1].result.OK || results[0].ended.Before(results[1].started) || results[1].ended.Before(results[0].started) {
		t.Fatalf("%+v", results)
	}
	var details []string
	trace := newMasterTrace(ChatService{OnProgress: func(kind, _, detail string) {
		if kind == "read_tool" {
			details = append(details, detail)
		}
	}})
	for i, result := range results {
		trace.readTiming(calls[i].call, result)
	}
	for i, detail := range details {
		var fields struct {
			CallID   string    `json:"callId"`
			Duration int64     `json:"durationMs"`
			Started  time.Time `json:"startedAt"`
			Ended    time.Time `json:"endedAt"`
		}
		if err := json.Unmarshal([]byte(detail), &fields); err != nil {
			t.Fatal(err)
		}
		if fields.CallID != calls[i].call.ID || fields.Duration != fields.Ended.Sub(fields.Started).Milliseconds() || fields.Started.IsZero() {
			t.Fatal(detail)
		}
	}
	if len(details) != 2 {
		t.Fatal(details)
	}
}
