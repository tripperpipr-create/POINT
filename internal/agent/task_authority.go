package agent

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
)

func copyExecutionBrief(b *domain.TaskBrief) *domain.TaskBrief {
	if b == nil {
		return nil
	}
	raw, _ := json.Marshal(b)
	var copy domain.TaskBrief
	_ = json.Unmarshal(raw, &copy)
	// An approved brief keeps the tool name its draft carried, and its digest
	// covers that text. Resolve the synonym on the execution copy only, so the
	// gate and the evidence tracker agree on one name without touching the
	// signature. Renaming a synonym grants nothing: the resolved name is still
	// checked against the tools this profile enables.
	for i := range copy.Criteria {
		copy.Criteria[i].Tool = domain.CanonicalToolName(copy.Criteria[i].Tool)
	}
	return &copy
}

func (e *Engine) strongTaskSandbox() bool {
	backend, ok := e.processExecutor.(interface{ Capabilities() sandbox.Capabilities })
	return ok && backend.Capabilities().StrongOSBoundary
}

// Task authority narrows the user's profile. It can never grant a tool absent
// from that profile, turn a DENY into ALLOW, or grant host/external side effects.
func RestrictTaskProfile(profile domain.AgentProfile, brief *domain.TaskBrief, customToolSets ...[]domain.CustomTool) (domain.AgentProfile, error) {
	if brief == nil {
		return profile, nil
	}
	if !domain.IsTaskBriefApproved(*brief) {
		return profile, errors.New("execution task brief is not approved")
	}
	customNames := map[string]bool{}
	for _, tool := range firstCustomToolSet(customToolSets) {
		customNames[tool.ID] = true
	}
	tools := make([]string, 0, len(profile.AllowedTools))
	for _, name := range profile.AllowedTools {
		if name == "propose_patch" && !brief.Permissions.WriteFiles {
			continue
		}
		if (name == "run_command" || customNames[name] || strings.HasPrefix(name, "customtool_")) && !brief.Permissions.ExecuteCommands {
			continue
		}
		// External writes have independent user-controlled UI actions.
		if name == "db_exec" || name == "ssh_exec_remote" || name == "docker_control" {
			continue
		}
		tools = append(tools, name)
	}
	profile.AllowedTools = tools
	policies := map[string]string{}
	for k, v := range profile.ToolPolicies {
		policies[k] = v
	}
	profile.ToolPolicies = policies
	allowed := map[string]bool{}
	for _, host := range brief.Permissions.NetworkHosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" {
			continue
		}
		allowed[host] = true
		// Freeze quest egress at approval time: brief hosts become the only
		// ALLOW entries for this run. Profile hosts outside the brief are DENY.
		key := "network:" + host
		if existing, ok := policies[key]; ok && strings.EqualFold(strings.TrimSpace(existing), "DENY") {
			continue
		}
		policies[key] = "ALLOW"
	}
	for key := range policies {
		if strings.HasPrefix(strings.ToLower(key), "network:") {
			host := strings.ToLower(strings.TrimSpace(key[len("network:"):]))
			if !allowed[host] {
				policies[key] = "DENY"
			}
		}
	}
	// Explicit host ALLOW entries remain the only egress grants; empty brief
	// list freezes the quest to deny-all for the assignment lifetime.
	policies["network"] = "DENY"
	return profile, nil
}

func (e *Engine) taskAutoApproved(active *activeRun, profile domain.AgentProfile, tool string) bool {
	b := active.taskBrief
	if b == nil || !domain.IsTaskBriefApproved(*b) {
		return false
	}
	if profile.ApprovalMode == domain.ApprovalAlways {
		return false
	}
	if explicit, ok := profile.ToolPolicies[tool]; ok && !strings.EqualFold(strings.TrimSpace(explicit), "ALLOW") {
		return false
	}
	// Fast Agent (Cursor daily): auto-approve file writes on precise tasks.
	// Commands stay manual unless project+Docker path below also applies.
	if b.FastAgent && (b.Mode == domain.TaskModePrecise || b.Mode == domain.TaskModeProject && b.WorkOrder != nil) {
		if tool == "propose_patch" {
			return b.Permissions.WriteFiles
		}
		return false
	}
	if b.Mode != domain.TaskModeProject || !e.strongTaskSandbox() {
		return false
	}
	if tool == "propose_patch" {
		return b.Permissions.WriteFiles
	}
	return b.Permissions.ExecuteCommands && (tool == "run_command" || strings.HasPrefix(tool, "customtool_"))
}

// ValidateTaskVerification checks the agreed machine criteria, without inferring
// new mandatory checks from words in the task or unrelated profile abilities.
func ValidateTaskVerification(profile domain.AgentProfile, brief *domain.TaskBrief, customTools []domain.CustomTool) error {
	if brief == nil {
		return nil
	}
	names := verificationToolNames(profile, customTools)
	for _, criterion := range brief.Criteria {
		if criterion.Kind == "manual" {
			continue
		}
		tool := domain.CanonicalToolName(criterion.Tool)
		if _, ok := names[tool]; !ok {
			message := "criterion " + criterion.ID + " requires an enabled verification-capable tool: " + criterion.Tool
			if len(names) == 0 {
				message += "; this agent has none — enable run_command or a custom tool with providesVerification, and allow commands in the task"
			} else {
				message += "; enabled here: " + strings.Join(sortedToolNames(names), ", ")
			}
			return errors.New(message)
		}
		if strings.EqualFold(strings.TrimSpace(profile.ToolPolicies[tool]), "DENY") {
			return errors.New("criterion " + criterion.ID + " uses a denied tool: " + tool)
		}
	}
	return nil
}

func sortedToolNames(names map[string]struct{}) []string {
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
