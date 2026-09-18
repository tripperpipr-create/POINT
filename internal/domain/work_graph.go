package domain

import "time"

const StageArtifactSchemaV1 = 1

const (
	StageRoleBootstrap  = "bootstrap"
	StageRoleImplement  = "implement"
	StageRoleIntegrate  = "integrate"
	StageRoleImplReview = "impl_review"
	StageRoleAccept     = "accept"
)

const (
	ArtifactValid       = "valid"
	ArtifactInvalidated = "invalidated"
)

// WorkNode is a planning unit. Execution truth is the compiled FlowGraph.
type WorkNode struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Role         string       `json:"role"`
	AgentID      string       `json:"agentId,omitempty"`
	DependsOn    []string     `json:"dependsOn,omitempty"`
	Contract     WorkContract `json:"contract"`
	WriteFiles   bool         `json:"writeFiles"`
	Instruction  string       `json:"instruction,omitempty"`
	MaxSubagents int          `json:"maxSubagents,omitempty"`
}

// WorkGraph is the planner model (modules, ownership, dependencies).
type WorkGraph struct {
	ID        string     `json:"id"`
	QuestID   string     `json:"questId,omitempty"`
	Nodes     []WorkNode `json:"nodes"`
	CreatedAt time.Time  `json:"createdAt"`
}

// StageArtifact is a durable handoff between stages. Digest alone is not enough.
type StageArtifact struct {
	ID             string    `json:"id"`
	SchemaVersion  int       `json:"schemaVersion"`
	Kind           string    `json:"kind"`
	StageID        string    `json:"stageId"`
	ContentRef     string    `json:"contentRef"`
	Content        string    `json:"content,omitempty"`
	InputRevision  string    `json:"inputRevision,omitempty"`
	OutputRevision string    `json:"outputRevision,omitempty"`
	ProducerID     string    `json:"producerId,omitempty"`
	AttemptID      string    `json:"attemptId,omitempty"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"createdAt"`
}

func FlowNodeStageRole(node FlowNode) string {
	if node.Config == nil {
		return ""
	}
	role, _ := node.Config["stageRole"].(string)
	return role
}

func FlowNodeExecutionRoot(node FlowNode) string {
	if node.Config == nil {
		return ""
	}
	root, _ := node.Config["executionRoot"].(string)
	return root
}

func FlowNodeWriteFiles(node FlowNode) bool {
	if node.Kind != FlowNodeAgent {
		return false
	}
	role := FlowNodeStageRole(node)
	switch role {
	case StageRoleImplReview, StageRoleAccept:
		return false
	}
	if node.Config != nil {
		if v, ok := node.Config["writeFiles"].(bool); ok {
			return v
		}
	}
	return true
}

// SharedProjectPaths are owned by bootstrap/integrate, never by parallel implementers.
func SharedProjectPaths() []string {
	return []string{
		"composer.json", "composer.lock", "vendor", "package.json", "package-lock.json",
		"pnpm-lock.yaml", "yarn.lock", "go.mod", "go.sum", "pyproject.toml", "poetry.lock",
		"requirements.txt", "Cargo.lock", "docker-compose.yml", "docker-compose.yaml",
		".env", "symfony.lock",
	}
}

// IntegrateOwnedPaths covers merge targets: app source plus scaffolding that
// serial writers may legitimately touch during integration (bin/, config/, …).
func IntegrateOwnedPaths() []string {
	return append([]string{
		"src", "bin", "config", "public", "templates", "tests", "migrations",
	}, SharedProjectPaths()...)
}
