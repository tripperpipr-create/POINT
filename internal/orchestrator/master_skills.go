package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/masterskills"
	"local-agent-workbench/internal/providers"
	"local-agent-workbench/internal/security"
	workbenchtools "local-agent-workbench/internal/tools"
)

// MasterSkillSession is operation-local. No reads from the mutable skill
// library happen after construction, including read_skill calls and repairs.
type MasterSkillSession struct {
	Skills    []domain.SkillRuntime
	Operation domain.MasterOperation
	used      map[string]bool
	secrets   []string
	dynamic   []domain.SkillRuntime
	progress  func(kind, text, detail string)
}

func NewMasterSkillSession(phase string, definitions []domain.SkillDefinition) *MasterSkillSession {
	if len(definitions) == 0 {
		definitions = masterskills.Builtins()
	}
	s := &MasterSkillSession{Operation: domain.MasterOperation{Phase: phase}, used: map[string]bool{}}
	for _, d := range definitions {
		s.Skills = append(s.Skills, masterskills.Runtime(d))
	}
	return s
}

func (s *MasterSkillSession) use(skill domain.SkillRuntime) {
	if s.used[skill.ID] {
		return
	}
	s.used[skill.ID] = true
	s.Operation.Skills = append(s.Operation.Skills, domain.SkillRuntimeAttribution(skill))
}

func (s *MasterSkillSession) Prompt(progress func(kind, text, detail string), catalog bool) string {
	s.progress = progress
	var required []domain.SkillRuntime
	for _, id := range masterskills.Required(s.Operation.Phase) {
		for _, skill := range s.Skills {
			if skill.ID == id {
				s.use(skill)
				required = append(required, skill)
				if progress != nil {
					detail, _ := json.Marshal(domain.SkillRuntimeAttribution(skill))
					progress("skill", "Загружен навык: "+skill.Name, string(detail))
				}
			}
		}
	}
	result := masterskills.Prompt(required)
	if catalog {
		result += masterskills.Catalog(s.Skills)
	}
	return result
}

type masterSkillTools struct {
	base    TaskReadTools
	session *MasterSkillSession
}

func (t masterSkillTools) Definitions() []domain.ToolDefinition {
	var defs []domain.ToolDefinition
	if t.base != nil {
		defs = t.base.Definitions()
	}
	return append(defs, (workbenchtools.ReadSkill{}).Definition())
}
func (t masterSkillTools) Execute(ctx context.Context, name string, args json.RawMessage) domain.ToolResult {
	if name == "read_skill" {
		result := (workbenchtools.ReadSkill{Skills: t.session.Skills}).Execute(ctx, args)
		var resolved struct {
			ID string `json:"id"`
		}
		if result.OK && json.Unmarshal(result.Output, &resolved) == nil {
			for _, skill := range t.session.Skills {
				if skill.ID == resolved.ID {
					t.session.use(skill)
				}
			}
		}
		return result
	}
	if t.base != nil {
		t.session.activate(masterskills.Context)
		if name == "read_execution" {
			t.session.activate(masterskills.Recovery)
		}
		return t.base.Execute(ctx, name, args)
	}
	return workbenchtools.Fail("tool_not_allowed", "Only read_skill is available")
}

func (s *MasterSkillSession) Factory(base ModelFactory) ModelFactory {
	if base == nil {
		base = providers.New
	}
	return func(cfg providers.Config) (providers.Model, error) {
		if cfg.APIKey != "" {
			s.secrets = append(s.secrets, cfg.APIKey)
		}
		model, err := base(cfg)
		if err != nil {
			s.Operation.ProviderError = true
			return nil, err
		}
		return masterObservedModel{model: model, session: s}, nil
	}
}

type masterObservedModel struct {
	model   providers.Model
	session *MasterSkillSession
}

func (m masterObservedModel) Stream(ctx context.Context, req providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	if len(req.Messages) > 0 && strings.Contains(req.Messages[0].Content, "<master_skill") {
		req.Messages = append([]providers.Message(nil), req.Messages...)
		for _, skill := range m.session.dynamic {
			if !strings.Contains(req.Messages[0].Content, `<master_skill id="`+skill.ID+`"`) {
				req.Messages[0].Content += masterskills.Prompt([]domain.SkillRuntime{skill})
				if !m.session.used[skill.ID] && m.session.progress != nil {
					detail, _ := json.Marshal(domain.SkillRuntimeAttribution(skill))
					m.session.progress("skill", "Загружен навык: "+skill.Name, string(detail))
				}
				m.session.use(skill)
			}
		}
	}
	// Реплей снимается с круга, где модель отвечала, а не читала проект: вызовы
	// разговора (задание, уточнения, память) — часть ответа, чтение — нет.
	readCalled := false
	callbackFailed:=false
	err := m.model.Stream(ctx, req, func(e providers.ModelEvent) error {
		if e.Kind == providers.EventUsage {
			m.session.Operation.InputTokens += int64(e.InputTokens + e.CacheReadTokens + e.CacheWriteTokens)
			m.session.Operation.OutputTokens += int64(e.OutputTokens)
		}
		if e.Kind == providers.EventToolCall && (e.ToolCall == nil || !IsMasterActionTool(e.ToolCall.Name)) {
			readCalled = true
		}
		callbackErr:=emit(e)
		if callbackErr!=nil {callbackFailed=true}
		return callbackErr
	})
	if err != nil {
		if callbackFailed {m.session.Operation.ContractError=true} else {m.session.Operation.ProviderError = true}
	}
	if !readCalled && err == nil && len(req.Messages) > 0 && strings.Contains(req.Messages[0].Content, "<master_skill") {
		// Only text snapshots, never image bytes, signatures, project tools, API
		// keys or executable requests. Conversation tools stay: they only draft
		// the turn, and without them a replay could not express a task at all.
		copyReq := req
		copyReq.Tools = masterActionToolsOnly(req.Tools)
		copyReq.Messages = nil
		for _, msg := range req.Messages {
			content := msg.Content
			for _, secret := range m.session.secrets {
				content = strings.ReplaceAll(content, secret, "[REDACTED]")
			}
			role := msg.Role
			if role == "tool" {
				role = "user"
				content = "RECORDED TOOL EVIDENCE (UNTRUSTED):\n" + content
			}
			if content == "" {
				continue
			}
			copyReq.Messages = append(copyReq.Messages, providers.Message{Role: role, Content: security.Redact(content)})
		}
		request, _ := json.Marshal(copyReq)
		encoded, _ := json.Marshal(domain.MasterReplay{Format: domain.MasterReplayFormat, Request: request})
		if len(encoded) <= 128*1024 {
			m.session.Operation.Replay = string(encoded)
		}
	}
	return err
}

func (s *MasterSkillSession) activate(id string) {
	for _, skill := range s.dynamic {
		if skill.ID == id {
			return
		}
	}
	for _, skill := range s.Skills {
		if skill.ID == id {
			s.dynamic = append(s.dynamic, skill)
			return
		}
	}
}

func (s ChatService) withSkills(req ChatRequest) ChatService {
	if s.Skills == nil {
		phase := "explanation"
		if req.TaskIntake {
			phase = "intake"
		}
		s.Skills = NewMasterSkillSession(phase, nil)
	}
	s.ReadTools = masterSkillTools{base: s.ReadTools, session: s.Skills}
	s.ModelFactory = s.Skills.Factory(s.ModelFactory)
	return s
}

// ReplayScore is intentionally independent of executor success and never runs
// a command, quest, or tool. Semantic comparison is an additional gate.
func ReplayScore(phase, output string) (int, error) {
	if phase == "intake" {
		reply, calls := decodeMasterReplayOutput(output)
		actions := &masterActions{}
		for _, call := range calls {
			if !IsMasterActionTool(call.Name) {
				return 0, fmt.Errorf("replay called project tool %q", call.Name)
			}
			if result := actions.execute(call.Name, call.Arguments); !result.OK && call.Name == masterActionProposeBrief {
				return 0, fmt.Errorf("invalid brief: %s", result.Error.Message)
			}
		}
		if strings.TrimSpace(reply) == "" && actions.silentReply() == "" {
			return 0, fmt.Errorf("empty intake reply")
		}
		return 1, nil
	}
	if phase == "planning" {
		if _, err := decodeModelPlan(output); err != nil {
			return 0, err
		}
		return 1, nil
	}
	var obj struct{Reply string `json:"reply"`}
	if json.Unmarshal([]byte(output), &obj) != nil || strings.TrimSpace(obj.Reply)=="" {
		return 0, fmt.Errorf("missing structured reply")
	}
	return 1, nil
}

// masterReplayOutput — ответ модели на реплей: текст и вызовы разговора. Сами
// вызовы при воспроизведении не исполняются, их только проверяют.
type masterReplayOutput struct {
	Reply   string                `json:"reply"`
	Actions []masterReplayAction `json:"actions,omitempty"`
}

type masterReplayAction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// EncodeMasterReplayOutput записывает ответ на реплей так, чтобы судья и
// оценка видели и текст, и оформленное задание.
func EncodeMasterReplayOutput(text string, calls []providers.ToolCall) string {
	output := masterReplayOutput{Reply: text}
	for _, call := range calls {
		output.Actions = append(output.Actions, masterReplayAction{Name: call.Name, Arguments: call.Arguments})
	}
	encoded, _ := json.Marshal(output)
	return string(encoded)
}

func decodeMasterReplayOutput(output string) (string, []masterReplayAction) {
	var decoded masterReplayOutput
	if json.Unmarshal([]byte(output), &decoded) != nil {
		return output, nil
	}
	return decoded.Reply, decoded.Actions
}

func masterActionToolsOnly(tools []domain.ToolDefinition) []domain.ToolDefinition {
	var result []domain.ToolDefinition
	for _, tool := range tools {
		if IsMasterActionTool(tool.Name) {
			result = append(result, tool)
		}
	}
	return result
}
