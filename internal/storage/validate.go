package storage

// Проверка машинных определений до записи: профиль агента (карта allowed
// строится из каталога инструментов), свой инструмент и workflow. Хранилище
// не пишет того, что потом нельзя исполнить.

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	_ "modernc.org/sqlite"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/egress"
)

func ValidateProfile(p domain.AgentProfile, additionalTools ...string) error {
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Model) == "" {
		return errors.New("profile name and model are required")
	}
	if domain.IsAgentCLIProvider(p.Provider) {
		return errors.New("CLI providers are removed; use an HTTP API provider")
	}
	if !domain.IsHTTPAPIProvider(p.Provider) {
		return errors.New("unsupported provider")
	}
	parsed, err := url.Parse(p.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("base URL must be an absolute HTTP or HTTPS URL")
	}
	if parsed.User != nil {
		return errors.New("credentials in the provider URL are not allowed")
	}
	if p.ApprovalMode != domain.ApprovalSafe && p.ApprovalMode != domain.ApprovalAlways {
		return errors.New("unsupported approval mode")
	}
	// Что можно записать в профиль, решает каталог. Ручной список здесь уже
	// успел ослепнуть: git-инструменты и read_skill были в реестре, но профиль
	// с ними не сохранялся, и длинные скиллы не грузились вовсе.
	allowed := map[string]bool{}
	for _, item := range domain.BuiltInToolCatalog() {
		allowed[item.Name] = true
	}
	for _, tool := range additionalTools {
		allowed[tool] = true
	}
	seen := make(map[string]bool)
	for _, tool := range p.AllowedTools {
		if !allowed[tool] {
			return fmt.Errorf("unsupported tool %q", tool)
		}
		if seen[tool] {
			return fmt.Errorf("duplicate tool %q", tool)
		}
		seen[tool] = true
	}
	if len(p.SystemPrompt) > 64*1024 || len(p.RoleDescription) > 4*1024 || len(p.Name) > 200 {
		return errors.New("profile fields exceed their size limit")
	}
	if len(p.Goals) > 32 || len(p.Rules) > 64 {
		return errors.New("profile has too many goals or rules")
	}
	for _, value := range append(append([]string{}, p.Goals...), p.Rules...) {
		if strings.TrimSpace(value) == "" || len(value) > 2048 {
			return errors.New("goals and rules must be non-empty and under 2048 bytes each")
		}
	}
	if p.MaxSteps < 1 || p.MaxSteps > 100 {
		return errors.New("max steps must be between 1 and 100")
	}
	if p.MaxDurationSeconds < 1 || p.MaxDurationSeconds > 3600 {
		return errors.New("max duration must be between 1 and 3600 seconds")
	}
	if p.Temperature < 0 || p.Temperature > 2 {
		return errors.New("temperature must be between 0 and 2")
	}
	if p.MaxOutputTokens != 0 && (p.MaxOutputTokens < 1 || p.MaxOutputTokens > 131072) {
		return errors.New("max output tokens must be between 1 and 131072")
	}
	if p.ContextWindowTokens != 0 && (p.ContextWindowTokens < 4096 || p.ContextWindowTokens > 1048576) {
		return errors.New("context window tokens must be between 4096 and 1048576")
	}
	if p.ContextWindowTokens > 0 && p.MaxOutputTokens > 0 && p.ContextWindowTokens-p.MaxOutputTokens < 1024 {
		return errors.New("context window must reserve at least 1024 tokens for model input")
	}
	if p.ReasoningEffort != "" && p.ReasoningEffort != "none" && p.ReasoningEffort != "minimal" && p.ReasoningEffort != "low" && p.ReasoningEffort != "medium" && p.ReasoningEffort != "high" {
		return errors.New("unsupported reasoning effort")
	}
	if _, err := egress.CompileToolPolicies(p.ToolPolicies); err != nil {
		return fmt.Errorf("invalid controlled egress policy: %w", err)
	}
	return nil
}

var customToolID = regexp.MustCompile(`^customtool_[0-9a-f]{24}$`)

var customToolParameterName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

var customToolPlaceholder = regexp.MustCompile(`\{\{([a-z][a-z0-9_]{0,63})\}\}`)

var windowsDrivePath = regexp.MustCompile(`^[A-Za-z]:`)

var workflowID = regexp.MustCompile(`^workflow_[0-9a-f]{24}$`)

var workflowStepID = regexp.MustCompile(`^step_[0-9a-f]{24}$`)

func ValidateWorkflow(workflow domain.AgentWorkflow, profileIDs ...string) error {
	if !workflowID.MatchString(workflow.ID) {
		return errors.New("invalid workflow ID")
	}
	if strings.TrimSpace(workflow.Name) == "" || len(workflow.Name) > 200 || len(workflow.Description) > 4*1024 {
		return errors.New("workflow name is required and fields must stay within size limits")
	}
	if len(workflow.Steps) < 1 || len(workflow.Steps) > 12 {
		return errors.New("workflow must contain between 1 and 12 steps")
	}
	profiles := make(map[string]bool, len(profileIDs))
	for _, id := range profileIDs {
		profiles[id] = true
	}
	seen := make(map[string]bool, len(workflow.Steps))
	for index, step := range workflow.Steps {
		if !workflowStepID.MatchString(step.ID) {
			return fmt.Errorf("workflow step %d has an invalid ID", index+1)
		}
		if seen[step.ID] {
			return fmt.Errorf("duplicate workflow step ID %q", step.ID)
		}
		seen[step.ID] = true
		if strings.TrimSpace(step.Name) == "" || len(step.Name) > 200 || len(step.Instruction) > 16*1024 {
			return fmt.Errorf("workflow step %d fields exceed their limits", index+1)
		}
		kind := step.Kind
		if kind != "" && kind != "agent" && kind != "cursor" && kind != "manual" {
			return fmt.Errorf("workflow step %d has unsupported kind", index+1)
		}
		if step.OnFailure != "" && step.OnFailure != "stop" && step.OnFailure != "skip" {
			return fmt.Errorf("workflow step %d has unsupported onFailure", index+1)
		}
		if step.Condition != nil && step.Condition.Type != "always" && step.Condition.Type != "previous_status" {
			return fmt.Errorf("workflow step %d has unsupported condition", index+1)
		}
		if kind != "manual" && !profiles[step.ProfileID] {
			return fmt.Errorf("workflow step %d references an unknown profile", index+1)
		}
	}
	return nil
}

func ValidateCustomTool(tool domain.CustomTool) error {
	if !customToolID.MatchString(tool.ID) {
		return errors.New("invalid custom tool ID")
	}
	if tool.Kind != domain.CustomToolCommand && tool.Kind != domain.CustomToolProcess {
		return errors.New("unsupported custom tool kind")
	}
	if strings.TrimSpace(tool.DisplayName) == "" || strings.TrimSpace(tool.Description) == "" {
		return errors.New("tool name and description are required")
	}
	if len(tool.DisplayName) > 120 || len(tool.Description) > 4*1024 || len(tool.Command) > 32*1024 || len(tool.Program) > 4*1024 || len(tool.CWD) > 4*1024 {
		return errors.New("custom tool fields exceed their size limit")
	}
	if tool.TimeoutSeconds < 1 || tool.TimeoutSeconds > 600 {
		return errors.New("custom tool timeout must be between 1 and 600 seconds")
	}
	if tool.Kind == domain.CustomToolCommand {
		if strings.TrimSpace(tool.Command) == "" {
			return errors.New("fixed command is required")
		}
		return nil
	}
	if strings.TrimSpace(tool.Program) == "" || strings.ContainsAny(tool.Program, "\x00\r\n") {
		return errors.New("process program is required and must be one line")
	}
	if filepath.IsAbs(tool.Program) || strings.HasPrefix(tool.Program, "/") || windowsDrivePath.MatchString(tool.Program) || strings.HasPrefix(tool.Program, `\\`) {
		return errors.New("process program must be a PATH name or workspace-relative path")
	}
	if len(tool.Arguments) > 64 || len(tool.Parameters) > 16 {
		return errors.New("process tool exceeds 64 arguments or 16 parameters")
	}
	parameters := make(map[string]bool, len(tool.Parameters))
	for index, parameter := range tool.Parameters {
		if !customToolParameterName.MatchString(parameter.Name) || parameters[parameter.Name] {
			return fmt.Errorf("process parameter %d has an invalid or duplicate name", index+1)
		}
		parameters[parameter.Name] = true
		if strings.TrimSpace(parameter.DisplayName) == "" || strings.TrimSpace(parameter.Description) == "" || len(parameter.DisplayName) > 120 || len(parameter.Description) > 1024 {
			return fmt.Errorf("process parameter %q requires bounded display name and description", parameter.Name)
		}
		switch parameter.Type {
		case domain.CustomToolParameterString, domain.CustomToolParameterWorkspacePath:
			if parameter.MaxLength < 0 || parameter.MaxLength > 16*1024 {
				return fmt.Errorf("process parameter %q maxLength is invalid", parameter.Name)
			}
		case domain.CustomToolParameterInteger:
			if len(parameter.EnumValues) > 0 {
				return fmt.Errorf("integer parameter %q cannot have enum values", parameter.Name)
			}
		case domain.CustomToolParameterEnum:
			if len(parameter.EnumValues) < 1 || len(parameter.EnumValues) > 100 {
				return fmt.Errorf("enum parameter %q requires 1–100 values", parameter.Name)
			}
			seen := make(map[string]bool, len(parameter.EnumValues))
			for _, value := range parameter.EnumValues {
				if strings.TrimSpace(value) == "" || len(value) > 1024 || seen[value] {
					return fmt.Errorf("enum parameter %q has an invalid or duplicate value", parameter.Name)
				}
				seen[value] = true
			}
		default:
			return fmt.Errorf("process parameter %q has unsupported type", parameter.Name)
		}
	}
	referenced := make(map[string]bool, len(parameters))
	totalArgumentBytes := 0
	for index, argument := range tool.Arguments {
		totalArgumentBytes += len(argument)
		if len(argument) > 4*1024 || strings.IndexByte(argument, 0) >= 0 {
			return fmt.Errorf("process argument %d exceeds its limit", index+1)
		}
		matches := customToolPlaceholder.FindAllStringSubmatch(argument, -1)
		remaining := customToolPlaceholder.ReplaceAllString(argument, "")
		if strings.Contains(remaining, "{{") || strings.Contains(remaining, "}}") {
			return fmt.Errorf("process argument %d contains an invalid placeholder", index+1)
		}
		for _, match := range matches {
			if !parameters[match[1]] {
				return fmt.Errorf("process argument %d references unknown parameter %q", index+1, match[1])
			}
			referenced[match[1]] = true
		}
	}
	if totalArgumentBytes > 32*1024 {
		return errors.New("process arguments exceed 32 KiB")
	}
	for name := range parameters {
		if !referenced[name] {
			return fmt.Errorf("process parameter %q is not used by any argument", name)
		}
	}
	return nil
}
