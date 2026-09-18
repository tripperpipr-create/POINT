package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/storage"
	"local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
)

type CustomToolPreviewRequest struct {
	Tool      domain.CustomTool `json:"tool"`
	Arguments json.RawMessage   `json:"arguments"`
}

type ToolExecutionRequest struct {
	ToolName   string             `json:"toolName"`
	Tool       *domain.CustomTool `json:"tool,omitempty"`
	Arguments  json.RawMessage    `json:"arguments"`
	Mode       string             `json:"mode"` // validate or execute_readonly
	ApprovalID string             `json:"approvalId,omitempty"`
	Approved   bool               `json:"approved,omitempty"` // rejected compatibility field; it is not authority
}

type ToolExecutionApprovalRequest struct {
	ToolName  string          `json:"toolName"`
	Arguments json.RawMessage `json:"arguments"`
}

type ToolExecutionApprovalPreview struct {
	Approval domain.ToolExecutionApproval `json:"approval"`
	Preview  any                          `json:"preview"`
}

func (a *App) SaveCustomTool(tool domain.CustomTool) (domain.CustomTool, error) {
	a.toolExecutionMu.Lock()
	defer a.toolExecutionMu.Unlock()
	now := time.Now().UTC()
	isNew := tool.ID == ""
	var err error
	tool, err = normalizeCustomTool(tool)
	if err != nil {
		return domain.CustomTool{}, err
	}
	if isNew {
		tool.CreatedAt = now
	}
	if tool.CreatedAt.IsZero() {
		tool.CreatedAt = now
	}
	tool.UpdatedAt = now
	// Имя занято одним инструментом.
	//
	// Без этого повторное создание похожего кладёт рядом второй `run_tests_2`,
	// и через десяток правок никто не скажет, какой из них живой. Повторное
	// сохранение существующего — новая редакция, номер растёт.
	existing, err := a.store.ListCustomTools(context.Background())
	if err != nil {
		return domain.CustomTool{}, err
	}
	name := strings.ToLower(strings.TrimSpace(tool.DisplayName))
	tool.Revision = 1
	// Доверие не приходит извне: его накапливают подтверждённые прогоны, а не
	// поле в запросе.
	tool.TrustedRuns = 0
	for index := range existing {
		item := existing[index]
		if item.ID == tool.ID {
			tool.Revision = item.Revision + 1
			// Правка названия или описания доверие сохраняет, правка того, что
			// запустится, — обнуляет: иначе под доверенным именем однажды
			// окажется другая команда.
			if item.ExecutableSignature() == tool.ExecutableSignature() {
				tool.TrustedRuns = item.TrustedRuns
			}
			continue
		}
		if strings.ToLower(strings.TrimSpace(item.DisplayName)) == name {
			return domain.CustomTool{}, fmt.Errorf("custom tool %q already exists as %s; save a new revision of it instead of a duplicate", item.DisplayName, item.ID)
		}
	}
	if tool.Kind == domain.CustomToolCommand {
		if err := a.recordCompatibilityUsage(context.Background(), a.currentWorldID(), domain.CompatibilityCustomCommandSave, legacyCustomCommandVersion); err != nil {
			return domain.CustomTool{}, err
		}
	}
	if err := a.store.SaveCustomTool(context.Background(), tool); err != nil {
		return domain.CustomTool{}, err
	}
	return tool, nil
}

func canonicalToolArguments(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, errors.New("tool arguments must be one JSON object")
	}
	if value == nil {
		return nil, errors.New("tool arguments must be one JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("tool arguments must contain one JSON object")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func customToolDigest(tool domain.CustomTool) (string, error) {
	encoded, err := json.Marshal(tool)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(encoded)), nil
}

func argumentsDigest(arguments json.RawMessage) string {
	return fmt.Sprintf("%x", sha256.Sum256(arguments))
}

func redactedToolArguments(arguments json.RawMessage) json.RawMessage {
	redacted := json.RawMessage(security.Redact(string(arguments)))
	if !json.Valid(redacted) {
		return json.RawMessage(`{}`)
	}
	return redacted
}

// RequestToolExecutionApproval persists the exact manual invocation that the
// UI will show. It does not execute a process and expires quickly.
func (a *App) RequestToolExecutionApproval(request ToolExecutionApprovalRequest) (ToolExecutionApprovalPreview, error) {
	a.toolExecutionMu.Lock()
	defer a.toolExecutionMu.Unlock()
	request.ToolName = strings.TrimSpace(request.ToolName)
	if request.ToolName == "" {
		return ToolExecutionApprovalPreview{}, errors.New("toolName is required")
	}
	arguments, err := canonicalToolArguments(request.Arguments)
	if err != nil {
		return ToolExecutionApprovalPreview{}, err
	}
	customTools, err := a.store.ListCustomTools(context.Background())
	if err != nil {
		return ToolExecutionApprovalPreview{}, err
	}
	var custom *domain.CustomTool
	for index := range customTools {
		if customTools[index].ID == request.ToolName {
			custom = &customTools[index]
			break
		}
	}
	if custom == nil {
		return ToolExecutionApprovalPreview{}, errors.New("saved custom tool not found")
	}
	preview, err := a.PreviewTool(ToolExecutionRequest{ToolName: custom.ID, Arguments: arguments, Mode: "validate"})
	if err != nil {
		return ToolExecutionApprovalPreview{}, err
	}
	workspace, err := a.requireWorkspace()
	if err != nil {
		return ToolExecutionApprovalPreview{}, err
	}
	digest, err := customToolDigest(*custom)
	if err != nil {
		return ToolExecutionApprovalPreview{}, err
	}
	now := time.Now().UTC()
	reason := "ручной запуск сохранённого инструмента"
	var input struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(arguments, &input) == nil && strings.TrimSpace(input.Reason) != "" {
		reason = strings.TrimSpace(input.Reason)
	}
	approval := domain.ToolExecutionApproval{
		ID: domain.NewID("tool-approval"), WorkspaceID: workspace.ID, ToolID: custom.ID, ToolDigest: digest,
		ArgumentsDigest: argumentsDigest(arguments), Arguments: redactedToolArguments(arguments), Reason: security.Redact(reason),
		Status: domain.ToolExecutionApprovalPending, CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute),
	}
	if err = a.store.SaveToolExecutionApproval(context.Background(), approval); err != nil {
		return ToolExecutionApprovalPreview{}, err
	}
	return ToolExecutionApprovalPreview{Approval: approval, Preview: preview}, nil
}

func (a *App) ResolveToolExecutionApproval(id string, allow bool) (domain.ToolExecutionApproval, error) {
	a.toolExecutionMu.Lock()
	defer a.toolExecutionMu.Unlock()
	workspace, err := a.requireWorkspace()
	if err != nil {
		return domain.ToolExecutionApproval{}, err
	}
	return a.store.ResolveToolExecutionApproval(context.Background(), strings.TrimSpace(id), workspace.ID, allow, time.Now().UTC())
}

func normalizeCustomTool(tool domain.CustomTool) (domain.CustomTool, error) {
	if tool.ID == "" {
		tool.ID = domain.NewID("customtool")
	}
	tool.DisplayName = strings.TrimSpace(tool.DisplayName)
	tool.Description = strings.TrimSpace(tool.Description)
	tool.Command = strings.TrimSpace(tool.Command)
	tool.Program = strings.TrimSpace(tool.Program)
	if tool.Kind == "" {
		// Prefer process for new drafts; keep legacy command when only a shell string is provided.
		switch {
		case tool.Program != "" || len(tool.Arguments) > 0 || len(tool.Parameters) > 0:
			tool.Kind = domain.CustomToolProcess
		case tool.Command != "":
			tool.Kind = domain.CustomToolCommand
		default:
			tool.Kind = domain.CustomToolProcess
		}
	}
	if tool.Description == "" && tool.DisplayName != "" {
		tool.Description = fmt.Sprintf("Запускает «%s» после подтверждения.", tool.DisplayName)
	}
	if tool.TimeoutSeconds == 0 {
		tool.TimeoutSeconds = 120
	}
	for index := range tool.Parameters {
		parameter := &tool.Parameters[index]
		parameter.Name = strings.TrimSpace(parameter.Name)
		parameter.DisplayName = strings.TrimSpace(parameter.DisplayName)
		parameter.Description = strings.TrimSpace(parameter.Description)
		for valueIndex := range parameter.EnumValues {
			parameter.EnumValues[valueIndex] = strings.TrimSpace(parameter.EnumValues[valueIndex])
		}
		if parameter.MaxLength == 0 && (parameter.Type == domain.CustomToolParameterString || parameter.Type == domain.CustomToolParameterWorkspacePath) {
			parameter.MaxLength = 1024
		}
	}
	if tool.Kind == domain.CustomToolCommand {
		tool.Program, tool.Arguments, tool.Parameters = "", nil, nil
	} else {
		tool.Command = ""
	}
	tool.CWD = filepath.ToSlash(strings.TrimSpace(tool.CWD))
	if tool.CWD == "" {
		tool.CWD = "."
	}
	cleanCWD := filepath.Clean(filepath.FromSlash(tool.CWD))
	if filepath.IsAbs(cleanCWD) || cleanCWD == ".." || strings.HasPrefix(cleanCWD, ".."+string(filepath.Separator)) {
		return domain.CustomTool{}, errors.New("custom tool working directory must stay inside the workspace")
	}
	tool.CWD = filepath.ToSlash(cleanCWD)
	if err := storage.ValidateCustomTool(tool); err != nil {
		return domain.CustomTool{}, err
	}
	return tool, nil
}

// PreviewCustomTool validates an unsaved process-tool definition and expands
// sample model arguments without creating a process or writing to storage.
func (a *App) PreviewCustomTool(request CustomToolPreviewRequest) (tools.CustomProcessPreview, error) {
	tool, err := normalizeCustomTool(request.Tool)
	if err != nil {
		return tools.CustomProcessPreview{}, err
	}
	if tool.Kind != domain.CustomToolProcess {
		return tools.CustomProcessPreview{}, errors.New("preview sandbox supports process tools only")
	}
	fs, err := a.fs()
	if err != nil {
		return tools.CustomProcessPreview{}, err
	}
	return (tools.CustomProcess{FS: fs, Config: tool}).Preview(request.Arguments)
}

// PreviewTool validates a registered tool without side effects. Read-only
// built-ins may additionally execute in execute_readonly mode. Draft custom
// tools may be previewed through the same endpoint as the saved-tool path.
func (a *App) PreviewTool(request ToolExecutionRequest) (any, error) {
	if request.Tool != nil && (strings.TrimSpace(string(request.Tool.Kind)) != "" || strings.TrimSpace(request.Tool.DisplayName) != "" || strings.TrimSpace(request.Tool.Program) != "" || strings.TrimSpace(request.Tool.Command) != "") {
		return a.PreviewCustomTool(CustomToolPreviewRequest{Tool: *request.Tool, Arguments: request.Arguments})
	}
	if request.Mode == "" {
		request.Mode = "validate"
	}
	if request.Mode != "validate" && request.Mode != "execute_readonly" {
		return nil, errors.New("mode must be validate or execute_readonly")
	}
	if strings.TrimSpace(request.ToolName) == "" {
		return nil, errors.New("toolName or tool draft is required")
	}
	fs, err := a.fs()
	if err != nil {
		return nil, err
	}
	customTools, err := a.store.ListCustomTools(context.Background())
	if err != nil {
		return nil, err
	}
	for _, custom := range customTools {
		if custom.ID != request.ToolName {
			continue
		}
		if custom.Kind == domain.CustomToolProcess {
			preview, previewErr := (tools.CustomProcess{FS: fs, Config: custom}).Preview(request.Arguments)
			if previewErr != nil {
				return nil, previewErr
			}
			return preview, nil
		}
		var input struct {
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(request.Arguments, &input); err != nil || strings.TrimSpace(input.Reason) == "" {
			return nil, errors.New("reason is required")
		}
		return map[string]any{"toolId": custom.ID, "displayName": custom.DisplayName, "kind": custom.Kind, "command": custom.Command, "cwd": custom.CWD, "reason": input.Reason, "timeoutSeconds": custom.TimeoutSeconds}, nil
	}
	registry, _ := agent.BuildToolRegistry(fs, nil)
	tool, ok := registry.Get(request.ToolName)
	if !ok {
		return nil, errors.New("tool not found")
	}
	safe := request.ToolName == "list_files" || request.ToolName == "read_file" || request.ToolName == "project_map"
	if !safe {
		return nil, errors.New("tool is not available from the preview API")
	}
	if validator, ok := tool.(tools.ArgumentValidator); ok {
		if invalid := validator.ValidateArguments(request.Arguments); invalid != nil {
			return *invalid, nil
		}
	}
	if request.Mode == "execute_readonly" {
		return tool.Execute(context.Background(), request.Arguments), nil
	}
	return map[string]any{"definition": tool.Definition(), "valid": true}, nil
}

// ExecuteTool runs a saved custom tool only after the caller records explicit
// approval. Normal agent runs retain their stricter approval workflow.
func (a *App) ExecuteTool(request ToolExecutionRequest) (domain.ToolResult, error) {
	a.toolExecutionMu.Lock()
	defer a.toolExecutionMu.Unlock()
	if request.Tool != nil && strings.TrimSpace(request.ToolName) == "" {
		request.ToolName = request.Tool.ID
	}
	if request.Mode == "" {
		request.Mode = "execute_readonly"
	}
	if request.Mode != "execute_readonly" {
		return domain.ToolResult{}, errors.New("execute requires execute_readonly mode")
	}
	customTools, err := a.store.ListCustomTools(context.Background())
	if err != nil {
		return domain.ToolResult{}, err
	}
	for _, custom := range customTools {
		if custom.ID != request.ToolName {
			continue
		}
		if strings.TrimSpace(request.ApprovalID) == "" {
			if request.Approved {
				return domain.ToolResult{}, errors.New("approved=true is not an approval; request and resolve a one-time tool execution approval")
			}
			return domain.ToolResult{}, errors.New("custom tool execution requires a one-time approvalId")
		}
		arguments, argumentsErr := canonicalToolArguments(request.Arguments)
		if argumentsErr != nil {
			return domain.ToolResult{}, argumentsErr
		}
		workspace, workspaceErr := a.requireWorkspace()
		if workspaceErr != nil {
			return domain.ToolResult{}, workspaceErr
		}
		toolDigest, digestErr := customToolDigest(custom)
		if digestErr != nil {
			return domain.ToolResult{}, digestErr
		}
		if _, consumeErr := a.store.ConsumeToolExecutionApproval(context.Background(), strings.TrimSpace(request.ApprovalID), workspace.ID, custom.ID, toolDigest, argumentsDigest(arguments), time.Now().UTC()); consumeErr != nil {
			return domain.ToolResult{}, fmt.Errorf("tool execution approval rejected: %w", consumeErr)
		}
		if custom.Kind == domain.CustomToolCommand {
			if usageErr := a.recordCompatibilityUsage(context.Background(), workspace.ID, domain.CompatibilityCustomCommandExecute, legacyCustomCommandVersion); usageErr != nil {
				return domain.ToolResult{}, usageErr
			}
		}
		return a.executeCustomToolInDisposableSandbox(context.Background(), custom, arguments)
	}
	value, err := a.PreviewTool(ToolExecutionRequest{ToolName: request.ToolName, Arguments: request.Arguments, Mode: "execute_readonly"})
	if err != nil {
		return domain.ToolResult{}, err
	}
	result, ok := value.(domain.ToolResult)
	if !ok {
		return domain.ToolResult{}, errors.New("only read-only built-in tools can execute here")
	}
	return result, nil
}

// executeCustomToolInDisposableSandbox makes execute_readonly true at the
// filesystem boundary. A custom command may mutate its writable staging copy,
// but neither a host process nor a strong process backend receives the live
// workspace root and the snapshot is always discarded after execution.
func (a *App) executeCustomToolInDisposableSandbox(ctx context.Context, custom domain.CustomTool, arguments json.RawMessage) (domain.ToolResult, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.ToolResult{}, err
	}
	executionID := domain.NewID("tool-execution")
	record, err := a.sandboxBackend.Create(ctx, sandbox.CreateRequest{
		WorkspaceID: ws.ID, WorkspacePath: ws.Path, ExecutionID: executionID,
	})
	if err != nil {
		return domain.ToolResult{}, fmt.Errorf("create disposable tool sandbox: %w", err)
	}
	discard := func() error {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return a.sandboxBackend.Close(cleanupCtx, record, ws.Path)
	}
	sandboxFS, err := workspace.Open(record.Path)
	if err != nil {
		_ = discard()
		return domain.ToolResult{}, fmt.Errorf("open disposable tool sandbox: %w", err)
	}
	// Hub execute has no agent profile; inherit the same default as agent runs (DENY).
	networkPolicy := "DENY"
	var result domain.ToolResult
	if custom.Kind == domain.CustomToolProcess {
		result = (tools.CustomProcess{FS: sandboxFS, Config: custom, NetworkPolicy: networkPolicy, Executor: a.sandboxProcessExecutor(), RunID: executionID}).Execute(ctx, arguments)
	} else {
		result = (tools.CustomCommand{FS: sandboxFS, Config: custom, NetworkPolicy: networkPolicy, Executor: a.sandboxProcessExecutor(), RunID: executionID}).Execute(ctx, arguments)
	}
	if err = discard(); err != nil {
		return result, fmt.Errorf("discard disposable tool sandbox: %w", err)
	}
	return result, nil
}

func (a *App) DeleteCustomTool(id string) error {
	a.toolExecutionMu.Lock()
	defer a.toolExecutionMu.Unlock()
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("custom tool id is required")
	}
	agents, err := a.store.ListAllProjectAgents(context.Background())
	if err != nil {
		return err
	}
	for _, agent := range agents {
		if containsString(agent.AllowedTools, id) {
			return fmt.Errorf("custom tool is enabled for agent %q; disable it there first", agent.Name)
		}
	}
	blueprints, err := a.store.ListBlueprints(context.Background())
	if err != nil {
		return err
	}
	for _, blueprint := range blueprints {
		if containsString(blueprint.AllowedTools, id) {
			return fmt.Errorf("custom tool is enabled for blueprint %q; disable it there first", blueprint.Name)
		}
	}
	profiles, err := a.storedProfiles(context.Background())
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		if containsString(profile.AllowedTools, id) {
			return fmt.Errorf("custom tool is enabled for profile %q; disable it there first", profile.Name)
		}
	}
	return a.store.DeleteCustomTool(context.Background(), id)
}

// trustedCustomTool — доверен ли самодельный инструмент прямо сейчас.
//
// Читает хранилище на каждый вопрос намеренно: счётчик растёт по ходу работы, а
// снимок, снятый один раз при запуске, устарел бы к третьему прогону.
func (a *App) trustedCustomTool(name string) bool {
	if !strings.HasPrefix(name, "customtool_") {
		return false
	}
	tools, err := a.store.ListCustomTools(context.Background())
	if err != nil {
		return false
	}
	for _, tool := range tools {
		if tool.ID == name {
			return tool.Trusted()
		}
	}
	return false
}

// noteCustomToolApproval засчитывает успешный прогон, за которым стояло
// подтверждение человека.
//
// Отдельного признака «подтверждено» в событии нет, и он не нужен: пока
// инструмент не доверен, без окна он не исполняется вовсе. Значит успешный
// прогон недоверенного инструмента и есть подтверждённый прогон. После порога
// считать нечего — окон больше не будет.
func (a *App) noteCustomToolApproval(name string) {
	if !strings.HasPrefix(name, "customtool_") {
		return
	}
	ctx := context.Background()
	tools, err := a.store.ListCustomTools(ctx)
	if err != nil {
		return
	}
	for _, tool := range tools {
		if tool.ID != name || !tool.TrustEligible() || tool.Trusted() {
			continue
		}
		tool.TrustedRuns++
		tool.UpdatedAt = time.Now().UTC()
		if saveErr := a.store.SaveCustomTool(ctx, tool); saveErr != nil {
			slog.Warn("custom tool trust not recorded", "tool", tool.ID, "error", saveErr)
			return
		}
		if tool.Trusted() {
			slog.Info("custom tool trusted", "tool", tool.ID, "name", tool.DisplayName, "runs", tool.TrustedRuns)
		}
		return
	}
}

// noteCustomToolApprovalFromEvent разбирает событие завершения инструмента.
func (a *App) noteCustomToolApprovalFromEvent(event domain.Event) {
	var payload struct {
		Tool   string `json:"tool"`
		Result struct {
			OK bool `json:"ok"`
		} `json:"result"`
	}
	if json.Unmarshal(event.Data, &payload) != nil || !payload.Result.OK {
		return
	}
	a.noteCustomToolApproval(payload.Tool)
}
