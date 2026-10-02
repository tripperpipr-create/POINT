package flowruntime

type StartRequest struct {
	SandboxWorkspace string
	FileRulesVersion string
	FlowID           string
	WorkspaceID      string
	QuestID          string
	Input            map[string]any
}
