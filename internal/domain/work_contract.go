package domain

type WorkContract struct {
	OwnedPaths         []string `json:"ownedPaths,omitempty"`
	ForbiddenPaths     []string `json:"forbiddenPaths,omitempty"`
	InterfacePaths     []string `json:"interfacePaths,omitempty"`
	InterfaceContracts []string `json:"interfaceContracts,omitempty"`
	CriterionIDs       []string `json:"criterionIds,omitempty"`
	MergePlan          string   `json:"mergePlan,omitempty"`
	MaxSubagents       int      `json:"maxSubagents,omitempty"`
	ParentAgentID      string   `json:"parentAgentId,omitempty"`
	BaseRevision       string   `json:"baseRevision,omitempty"`
}
