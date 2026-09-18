// Сборка реестра инструментов под конкретный прогон.
//
// Конструкторов несколько, потому что источников доступа несколько: профиль
// агента, серверы, источники данных, узел потока. Тело у сборки одно.
package agent

import (
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
	workbenchtools "local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
)

// BuildToolRegistry is shared by execution and read-only run preflight so the
// tool definitions shown before launch are exactly those sent to the model.
func BuildToolRegistry(fs *workspace.FS, customTools []domain.CustomTool, profiles ...domain.AgentProfile) (*workbenchtools.Registry, *workbenchtools.PatchManager) {
	return BuildToolRegistryWithSources(fs, customTools, nil, nil, profiles...)
}

// BuildToolRegistryWithSources registers optional SSH and database tools.
func BuildToolRegistryWithSources(fs *workspace.FS, customTools []domain.CustomTool, serverProfiles workbenchtools.ServerProfileSource, dbSource workbenchtools.DBConnectionSource, profiles ...domain.AgentProfile) (*workbenchtools.Registry, *workbenchtools.PatchManager) {
	return buildToolRegistryWithExecution(fs, customTools, serverProfiles, dbSource, nil, "", nil, nil, nil, runCorrelation{}, profiles...)
}

func BuildToolRegistryForFlow(fs *workspace.FS, customTools []domain.CustomTool, serverProfiles workbenchtools.ServerProfileSource, dbSource workbenchtools.DBConnectionSource, teamBus workbenchtools.TeamBus, workspaceID, questID, flowRunID, flowNodeID string, profiles ...domain.AgentProfile) (*workbenchtools.Registry, *workbenchtools.PatchManager) {
	return buildToolRegistryWithExecution(fs, customTools, serverProfiles, dbSource, nil, "", nil, nil, teamBus, runCorrelation{
		WorkspaceID: workspaceID, QuestID: questID, FlowRunID: flowRunID, FlowNodeID: flowNodeID,
	}, profiles...)
}

func buildToolRegistryWithExecution(fs *workspace.FS, customTools []domain.CustomTool, serverProfiles workbenchtools.ServerProfileSource, dbSource workbenchtools.DBConnectionSource, executor sandbox.ProcessExecutor, runID string, confirmedRemotes []string, grants *workbenchtools.NetworkGrantBook, teamBus workbenchtools.TeamBus, correlation runCorrelation, profiles ...domain.AgentProfile) (*workbenchtools.Registry, *workbenchtools.PatchManager) {
	patches := workbenchtools.NewPatchManager(fs)
	networkPolicy := ""
	var allowedNetworkHosts []string
	if len(profiles) > 0 {
		networkPolicy = strings.ToUpper(strings.TrimSpace(profiles[0].ToolPolicies["network"]))
		if networkPolicy == "" {
			networkPolicy = "DENY"
		}
		for key, value := range profiles[0].ToolPolicies {
			if !strings.EqualFold(strings.TrimSpace(value), "ALLOW") || !strings.HasPrefix(strings.ToLower(key), "network:") {
				continue
			}
			if host := strings.TrimSpace(key[len("network:"):]); host != "" {
				allowedNetworkHosts = append(allowedNetworkHosts, host)
			}
		}
	}
	sshConfig := workbenchtools.SSHToolConfig{
		Profiles: serverProfiles, NetworkPolicy: networkPolicy, AllowedNetworkHosts: allowedNetworkHosts,
	}
	dbConfig := workbenchtools.DBToolConfig{
		Source: dbSource, NetworkPolicy: networkPolicy, AllowedNetworkHosts: allowedNetworkHosts,
	}
	runCommand := workbenchtools.RunCommand{
		FS: fs, NetworkPolicy: networkPolicy, AllowedNetworkHosts: allowedNetworkHosts,
		ConfirmedGitRemotes: append([]string(nil), confirmedRemotes...), Grants: grants,
		Executor: executor, RunID: runID, QuestID: correlation.QuestID,
	}
	toolItems := []workbenchtools.Tool{
		workbenchtools.ProjectMap{FS: fs}, workbenchtools.SearchCode{FS: fs}, workbenchtools.ListFiles{FS: fs},
		workbenchtools.ReadFile{FS: fs}, workbenchtools.SearchText{FS: fs}, patches,
		runCommand,
		workbenchtools.GitDiff{FS: fs}, workbenchtools.GitBranches{FS: fs},
		workbenchtools.GitLog{FS: fs}, workbenchtools.GitTags{FS: fs},
		workbenchtools.DockerInspect{}, workbenchtools.DockerControl{},
		workbenchtools.SSHTestConnection{Config: sshConfig},
		workbenchtools.SSHListRemote{Config: sshConfig},
		workbenchtools.SSHReadRemote{Config: sshConfig},
		workbenchtools.SSHExecRemote{Config: sshConfig},
		workbenchtools.DBListConnections{Config: dbConfig},
		workbenchtools.DBSchema{Config: dbConfig},
		workbenchtools.DBQuery{Config: dbConfig},
		workbenchtools.DBExec{Config: dbConfig},
		workbenchtools.TeamPublish{Bus: teamBus, WorkspaceID: correlation.WorkspaceID, QuestID: correlation.QuestID, FlowRunID: correlation.FlowRunID, FlowNodeID: correlation.FlowNodeID, AgentID: firstProfileID(profiles)},
		workbenchtools.TeamInbox{Bus: teamBus, FlowRunID: correlation.FlowRunID, AgentID: firstProfileID(profiles)},
		workbenchtools.PermissionPrompt{},
	}
	if len(profiles) > 0 && len(profiles[0].EquippedSkills) > 0 {
		toolItems = append(toolItems, workbenchtools.ReadSkill{Skills: profiles[0].EquippedSkills})
	}
	for _, customTool := range customTools {
		if customTool.Kind == domain.CustomToolProcess {
			toolItems = append(toolItems, workbenchtools.CustomProcess{FS: fs, Config: customTool, NetworkPolicy: networkPolicy, AllowedNetworkHosts: allowedNetworkHosts, Executor: executor, RunID: runID})
		} else {
			toolItems = append(toolItems, workbenchtools.CustomCommand{FS: fs, Config: customTool, NetworkPolicy: networkPolicy, AllowedNetworkHosts: allowedNetworkHosts, Executor: executor, RunID: runID})
		}
	}
	return workbenchtools.NewRegistry(toolItems...), patches
}

func firstProfileID(profiles []domain.AgentProfile) string {
	if len(profiles) == 0 {
		return ""
	}
	return profiles[0].ID
}

func confirmedRemotesFromBrief(brief *domain.TaskBrief) []string {
	if brief == nil {
		return nil
	}
	return append([]string(nil), brief.Permissions.ConfirmedGitRemotes...)
}
