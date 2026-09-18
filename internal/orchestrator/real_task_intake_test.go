package orchestrator

import (
	"context"
	"encoding/json"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in intake smoke; separate from the two-pass, ten-task execution benchmark.
func TestRealModelTaskIntake(t *testing.T) {
	model := os.Getenv("POINT_INTAKE_TEST_MODEL")
	if model == "" {
		t.Skip("set POINT_INTAKE_TEST_MODEL to an installed Ollama model")
	}
	cases := []struct {
		name, message string
		mode          domain.TaskMode
		state         string
	}{
		{"precise", "Напиши в ответе одну функцию Go Clamp(x, min, max int) int: вернуть min при x < min, max при x > max, иначе x; при min > max вызвать panic(\"invalid range\"). Только код функции без импортов. Не изменяй файлы и не запускай команды. Критерии: точная сигнатура, указанное поведение на границах и panic при неверном диапазоне. Дополнительных возможностей не требуется.", domain.TaskModePrecise, "ready"},
		{"project", "Напиши приложение для личного пользования.", domain.TaskModeProject, "discussion"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newChatStoreStub()
			service := ChatService{Store: store, ModelFactory: func(config providers.Config) (providers.Model, error) {
				model, err := providers.New(config)
				if err != nil {
					return nil, err
				}
				return realIntakeTrace{model: model, t: t}, nil
			}}
			started := time.Now()
			response, err := service.Chat(context.Background(), ChatRequest{WorkspaceID: "real-intake", TaskIntake: true, Message: tc.message, Config: domain.OrchestratorConfig{Provider: domain.ProviderOllama, Model: model, BaseURL: "http://127.0.0.1:11434", MaxOutputTokens: 4096}})
			raw, _ := json.Marshal(response)
			t.Logf("model=%s duration=%s response=%s", model, time.Since(started), raw)
			if err != nil {
				t.Fatal(err)
			}
			if response.Mode != "model" || response.Proposal == nil || response.Proposal.Brief == nil {
				t.Fatal("no structured model draft")
			}
			b := response.Proposal.Brief
			if b.Mode != tc.mode || b.State != tc.state {
				t.Fatalf("mode=%s state=%s", b.Mode, b.State)
			}
			if domain.IsTaskBriefApproved(*b) || len(store.quests) != 0 {
				t.Fatal("intake executed task")
			}
			if tc.name == "precise" && (len(response.Questions) != 0 || len(b.OpenQuestions) != 0 || b.Permissions.WriteFiles || b.Permissions.ExecuteCommands) {
				t.Fatal("complete precise task expanded or interviewed")
			}
			if tc.name == "project" && (len(response.Questions) == 0 || len(response.Questions) > 3 || len(b.OpenQuestions) == 0) {
				t.Fatal("broad task lacks bounded interview")
			}
		})
	}
}

// Only enabled for fixed public test prompts, never application conversations.
type realIntakeTrace struct {
	model providers.Model
	t     *testing.T
}

func (m realIntakeTrace) Stream(ctx context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	var raw strings.Builder
	input, output := 0, 0
	started := time.Now()
	err := m.model.Stream(ctx, request, func(e providers.ModelEvent) error {
		if e.Kind == providers.EventTextDelta {
			raw.WriteString(e.Delta)
		}
		if e.Kind == providers.EventUsage {
			input += e.InputTokens
			output += e.OutputTokens
		}
		return emit(e)
	})
	m.t.Logf("round duration=%s input=%d output=%d response=%s error=%v", time.Since(started), input, output, raw.String(), err)
	return err
}
