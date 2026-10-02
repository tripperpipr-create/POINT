package orchestrator

import (
	"context"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
	"testing"
)

func TestAutoMasterRoutesAndManualPriority(t *testing.T) {
	for _, tc := range []struct {
		name, mode, route string
		calls             []providers.ToolCall
	}{
		{"answer", "auto", "answer", nil},
		{"fast", "auto", "fast", []providers.ToolCall{toolCall("fast", "dispatch_fast_task", map[string]any{"task": "Create hello.txt with the requested greeting"})}},
		{"clarify", "auto", "clarify", []providers.ToolCall{toolCall("ask", "ask_clarifications", map[string]any{"items": []map[string]any{{"text": "Which folder should contain the report?", "kind": "text"}}})}},
		{"plan", "auto", "plan", []providers.ToolCall{proposeBriefCall("plan", "Complex task", "", validBrief())}},
		{"manual-discuss", "discuss", "answer", nil},
		{"manual-plan", "plan", "plan", []providers.ToolCall{proposeBriefCall("plan", "Plan only", "", validBrief())}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := &turnModel{rounds: []roundScript{{text: "Response for the selected route", calls: tc.calls}}}
			service := ChatService{Store: newChatStoreStub(), ModelFactory: model.factory()}
			req := intakeRequest("User task")
			req.WorkMode = tc.mode
			response, err := service.Chat(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if response.Route != tc.route {
				t.Fatalf("route=%q response=%+v", response.Route, response)
			}
			offered := false
			for _, tool := range model.requests[0].Tools {
				if tool.Name == "dispatch_fast_task" {
					offered = true
				}
			}
			if offered != (tc.mode == "auto") {
				t.Fatalf("manual mode did not control executor tools: %v", offered)
			}
			if tc.route == "plan" && (response.Proposal == nil || domain.IsTaskBriefApproved(*response.Proposal.Brief) || response.RunID != "" || response.FastTask != "") {
				t.Fatal("plan crossed its approval boundary")
			}
			if tc.route == "fast" && response.FastTask == "" {
				t.Fatal("bounded task was lost")
			}
		})
	}
}

func TestFastTaskRejectsUnresolvedPlanOrQuestions(t *testing.T) {
	for _, actions := range []*masterActions{{brief: &domain.TaskBrief{}}, {clarifications: []domain.MasterQuestion{{Text: "Unknown target"}}}} {
		result := actions.execute(masterActionFastTask, toolCall("fast", masterActionFastTask, map[string]any{"task": "Write file"}).Arguments)
		if result.OK || actions.fastTask != "" {
			t.Fatal("fast route bypassed unresolved work")
		}
	}
}
