package domain

import "strings"

const SystemFastAgentID = "system-fast"

func ConversationScope(workspaceID string) string {
	if strings.HasPrefix(workspaceID, "point-chat-") {
		return "point_chat"
	}
	return "project"
}

// HostLiveFastAgent is an explicit, approved local execution route.
func HostLiveFastAgent(profile AgentProfile, brief *TaskBrief) bool {
	return profile.ID == SystemFastAgentID && profile.ExecutionMode == "host_live" && brief != nil && brief.FastAgent && IsTaskBriefApproved(*brief)
}
