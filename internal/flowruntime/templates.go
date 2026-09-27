package flowruntime

// Graph templates: legacy linear workflows and quest-importance plans become
// FlowGraphs that the runtime executes like any other graph.

import (
	"fmt"
	"time"

	"local-agent-workbench/internal/domain"
)

// CompileLinearWorkflow turns a legacy AgentWorkflow into a linear FlowGraph.
func CompileLinearWorkflow(workspaceID string, workflow domain.AgentWorkflow) domain.FlowGraph {
	now := time.Now().UTC()
	flow := domain.FlowGraph{
		ID: domain.NewID("flow"), WorkspaceID: workspaceID, Name: workflow.Name,
		Description: workflow.Description, CreatedAt: now, UpdatedAt: now,
	}
	inputID := domain.NewID("node")
	flow.Nodes = append(flow.Nodes, domain.FlowNode{ID: inputID, Kind: domain.FlowNodeInput, Name: "Input"})
	prev := inputID
	for index, step := range workflow.Steps {
		nodeID := domain.NewID("node")
		flow.Nodes = append(flow.Nodes, domain.FlowNode{
			ID: nodeID, Kind: domain.FlowNodeAgent, Name: fmt.Sprintf("Step %d", index+1), AgentID: step.ProfileID,
			Config: map[string]any{"instruction": step.Instruction},
		})
		flow.Edges = append(flow.Edges, domain.FlowEdge{ID: domain.NewID("edge"), From: prev, To: nodeID})
		prev = nodeID
	}
	outputID := domain.NewID("node")
	flow.Nodes = append(flow.Nodes, domain.FlowNode{ID: outputID, Kind: domain.FlowNodeOutput, Name: "Output"})
	flow.Edges = append(flow.Edges, domain.FlowEdge{ID: domain.NewID("edge"), From: prev, To: outputID})
	return flow
}

// ImportanceTemplate expands quest importance into an execution graph template.
func ImportanceTemplate(importance domain.QuestImportance, primaryAgentID, reviewerAgentID string) domain.FlowGraph {
	now := time.Now().UTC()
	flow := domain.FlowGraph{
		ID: domain.NewID("flow"), Name: string(importance) + " template", CreatedAt: now, UpdatedAt: now,
	}
	input := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeInput, Name: "Input"}
	output := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeOutput, Name: "Output"}

	switch importance {
	case domain.QuestCritical:
		parallel := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeParallel, Name: "Independent branches"}
		primary := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeAgent, Name: "Model A", AgentID: primaryAgentID}
		branchB := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeAgent, Name: "Model B", AgentID: reviewerAgentID}
		join := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeJoin, Name: "Join"}
		verifier := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeVerifier, Name: "Verifier"}
		approval := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeApproval, Name: "User resolve"}
		flow.Nodes = []domain.FlowNode{input, parallel, primary, branchB, join, verifier, approval, output}
		flow.Edges = []domain.FlowEdge{
			{ID: domain.NewID("edge"), From: input.ID, To: parallel.ID},
			{ID: domain.NewID("edge"), From: parallel.ID, To: primary.ID},
			{ID: domain.NewID("edge"), From: parallel.ID, To: branchB.ID},
			{ID: domain.NewID("edge"), From: primary.ID, To: join.ID},
			{ID: domain.NewID("edge"), From: branchB.ID, To: join.ID},
			{ID: domain.NewID("edge"), From: join.ID, To: verifier.ID},
			{ID: domain.NewID("edge"), From: verifier.ID, To: approval.ID},
			{ID: domain.NewID("edge"), From: approval.ID, To: output.ID},
		}
	case domain.QuestImportant:
		primary := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeAgent, Name: "Primary", AgentID: primaryAgentID}
		reviewer := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeAgent, Name: "Reviewer", AgentID: reviewerAgentID}
		verifier := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeVerifier, Name: "Verifier"}
		flow.Nodes = []domain.FlowNode{input, primary, reviewer, verifier, output}
		flow.Edges = []domain.FlowEdge{
			{ID: domain.NewID("edge"), From: input.ID, To: primary.ID},
			{ID: domain.NewID("edge"), From: primary.ID, To: reviewer.ID},
			{ID: domain.NewID("edge"), From: reviewer.ID, To: verifier.ID},
			{ID: domain.NewID("edge"), From: verifier.ID, To: output.ID},
		}
	default:
		primary := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeAgent, Name: "Primary", AgentID: primaryAgentID}
		flow.Nodes = []domain.FlowNode{input, primary, output}
		flow.Edges = []domain.FlowEdge{
			{ID: domain.NewID("edge"), From: input.ID, To: primary.ID},
			{ID: domain.NewID("edge"), From: primary.ID, To: output.ID},
		}
	}
	return flow
}
