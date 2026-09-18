package orchestrator

import (
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// CompileRequest builds a Flow graph from orchestrator traits and the assigned party.
type CompileRequest struct {
	Title              string
	Importance         domain.QuestImportance
	AgentIDs           []string
	PlanningDepth      int
	Parallelism        int
	ApprovalStrictness int
	Preset             string
}

// CompileFlow produces a deterministic execution graph. Extra party members beyond
// primary/reviewer are bound as additional agent nodes when planningDepth/parallelism allow.
func CompileFlow(req CompileRequest) domain.FlowGraph {
	now := time.Now().UTC()
	primary, reviewer, extras := splitParty(req.AgentIDs)
	shape := resolveShape(req, len(req.AgentIDs))
	needApproval := req.ApprovalStrictness >= 80 || req.Preset == "conservative"

	flow := domain.FlowGraph{
		ID:          domain.NewID("flow"),
		Name:        string(shape) + " · " + req.Title,
		Description: fmt.Sprintf("Orchestrator %s · depth %d · parallel %d", req.Preset, req.PlanningDepth, req.Parallelism),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	input := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeInput, Name: "Input"}
	output := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeOutput, Name: "Output"}

	switch shape {
	case domain.QuestCritical:
		parallel := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeParallel, Name: "Independent branches"}
		branchA := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeAgent, Name: "Model A", AgentID: primary}
		branchB := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeAgent, Name: "Model B", AgentID: reviewer}
		join := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeJoin, Name: "Join"}
		verifier := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeVerifier, Name: "Verifier"}
		nodes := []domain.FlowNode{input, parallel, branchA, branchB, join, verifier}
		edges := []domain.FlowEdge{
			{ID: domain.NewID("edge"), From: input.ID, To: parallel.ID},
			{ID: domain.NewID("edge"), From: parallel.ID, To: branchA.ID},
			{ID: domain.NewID("edge"), From: parallel.ID, To: branchB.ID},
			{ID: domain.NewID("edge"), From: branchA.ID, To: join.ID},
			{ID: domain.NewID("edge"), From: branchB.ID, To: join.ID},
			{ID: domain.NewID("edge"), From: join.ID, To: verifier.ID},
		}
		prev := verifier.ID
		if len(extras) > 0 && req.PlanningDepth >= 55 {
			extra := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeAgent, Name: "Specialist", AgentID: extras[0]}
			nodes = append(nodes, extra)
			edges = append(edges, domain.FlowEdge{ID: domain.NewID("edge"), From: prev, To: extra.ID})
			prev = extra.ID
		}
		if needApproval {
			approval := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeApproval, Name: "User resolve"}
			nodes = append(nodes, approval)
			edges = append(edges, domain.FlowEdge{ID: domain.NewID("edge"), From: prev, To: approval.ID})
			prev = approval.ID
		}
		nodes = append(nodes, output)
		edges = append(edges, domain.FlowEdge{ID: domain.NewID("edge"), From: prev, To: output.ID})
		flow.Nodes, flow.Edges = nodes, edges
	case domain.QuestImportant:
		primaryNode := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeAgent, Name: "Primary", AgentID: primary}
		reviewerNode := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeAgent, Name: "Reviewer", AgentID: reviewer}
		verifier := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeVerifier, Name: "Verifier"}
		nodes := []domain.FlowNode{input, primaryNode, reviewerNode}
		edges := []domain.FlowEdge{
			{ID: domain.NewID("edge"), From: input.ID, To: primaryNode.ID},
			{ID: domain.NewID("edge"), From: primaryNode.ID, To: reviewerNode.ID},
		}
		prev := reviewerNode.ID
		if len(extras) > 0 && req.PlanningDepth >= 55 {
			extra := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeAgent, Name: "Specialist", AgentID: extras[0]}
			nodes = append(nodes, extra)
			edges = append(edges, domain.FlowEdge{ID: domain.NewID("edge"), From: prev, To: extra.ID})
			prev = extra.ID
		}
		nodes = append(nodes, verifier)
		edges = append(edges, domain.FlowEdge{ID: domain.NewID("edge"), From: prev, To: verifier.ID})
		prev = verifier.ID
		if needApproval {
			approval := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeApproval, Name: "User resolve"}
			nodes = append(nodes, approval)
			edges = append(edges, domain.FlowEdge{ID: domain.NewID("edge"), From: prev, To: approval.ID})
			prev = approval.ID
		}
		nodes = append(nodes, output)
		edges = append(edges, domain.FlowEdge{ID: domain.NewID("edge"), From: prev, To: output.ID})
		flow.Nodes, flow.Edges = nodes, edges
	default:
		primaryNode := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeAgent, Name: "Primary", AgentID: primary}
		nodes := []domain.FlowNode{input, primaryNode}
		edges := []domain.FlowEdge{{ID: domain.NewID("edge"), From: input.ID, To: primaryNode.ID}}
		prev := primaryNode.ID
		if reviewer != primary && req.PlanningDepth >= 45 {
			reviewerNode := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeAgent, Name: "Reviewer", AgentID: reviewer}
			nodes = append(nodes, reviewerNode)
			edges = append(edges, domain.FlowEdge{ID: domain.NewID("edge"), From: prev, To: reviewerNode.ID})
			prev = reviewerNode.ID
		}
		if needApproval {
			approval := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeApproval, Name: "User resolve"}
			nodes = append(nodes, approval)
			edges = append(edges, domain.FlowEdge{ID: domain.NewID("edge"), From: prev, To: approval.ID})
			prev = approval.ID
		}
		nodes = append(nodes, output)
		edges = append(edges, domain.FlowEdge{ID: domain.NewID("edge"), From: prev, To: output.ID})
		flow.Nodes, flow.Edges = nodes, edges
	}
	applyDefaultAgentRetryPolicy(flow.Nodes)
	return flow
}

func applyDefaultAgentRetryPolicy(nodes []domain.FlowNode) {
	for i := range nodes {
		if nodes[i].Kind != domain.FlowNodeAgent {
			continue
		}
		if strings.TrimSpace(nodes[i].FailurePolicy.Mode) != "" {
			continue
		}
		nodes[i].FailurePolicy = domain.FlowFailurePolicy{Mode: "retry", MaxRetries: 2}
	}
}

func splitParty(agentIDs []string) (primary, reviewer string, extras []string) {
	if len(agentIDs) == 0 {
		return "", "", nil
	}
	primary = agentIDs[0]
	reviewer = primary
	if len(agentIDs) > 1 {
		reviewer = agentIDs[1]
	}
	if len(agentIDs) > 2 {
		extras = append([]string(nil), agentIDs[2:]...)
	}
	return primary, reviewer, extras
}

func resolveShape(req CompileRequest, partyLen int) domain.QuestImportance {
	importance := req.Importance
	if importance == "" {
		importance = domain.QuestNormal
	}
	// Deep planning upgrades a normal quest when a multi-agent party is available.
	if importance == domain.QuestNormal && req.PlanningDepth >= 70 && partyLen > 1 {
		importance = domain.QuestImportant
	}
	// High parallelism with at least two agents prefers independent branches.
	if importance != domain.QuestCritical && req.Parallelism >= 70 && partyLen >= 2 {
		importance = domain.QuestCritical
	}
	// Shallow planning keeps solo/normal graphs lean.
	if req.PlanningDepth < 35 && importance == domain.QuestImportant && partyLen < 2 {
		importance = domain.QuestNormal
	}
	return importance
}

// MaxSubquestSteps limits objective→subquest expansion from planningDepth.
func MaxSubquestSteps(cfg domain.OrchestratorConfig) int {
	switch {
	case cfg.PlanningDepth >= 70:
		return 6
	case cfg.PlanningDepth >= 45:
		return 4
	case cfg.PlanningDepth >= 25:
		return 2
	default:
		return 1
	}
}

// MaxConcurrentAgents limits how many waiting agent nodes may be started at once.
func MaxConcurrentAgents(cfg domain.OrchestratorConfig) int {
	switch {
	case cfg.Parallelism >= 70:
		return 4
	case cfg.Parallelism >= 40:
		return 2
	default:
		return 1
	}
}
