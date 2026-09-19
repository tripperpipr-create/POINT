package orchestrator

import (
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// CompileWorkGraph turns a planning WorkGraph into the executable FlowGraph.
// FlowGraph is the source of truth at runtime.
func CompileWorkGraph(graph domain.WorkGraph, title string) (domain.FlowGraph, error) {
	if err := validateWorkGraph(graph); err != nil {
		return domain.FlowGraph{}, err
	}
	now := time.Now().UTC()
	flow := domain.FlowGraph{
		ID: domain.NewID("flow"), Name: "pipeline · " + title,
		Description: "Compiled from WorkGraph " + graph.ID,
		CreatedAt:   now, UpdatedAt: now,
	}
	input := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeInput, Name: "Input"}
	output := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeOutput, Name: "Output"}
	flow.Nodes = append(flow.Nodes, input)

	compiled := map[string]string{}
	ready := func(node domain.WorkNode) bool {
		for _, dep := range node.DependsOn {
			if compiled[dep] == "" {
				return false
			}
		}
		return true
	}
	remaining := append([]domain.WorkNode(nil), graph.Nodes...)
	prevSerial := input.ID
	safety := 0
	for len(remaining) > 0 {
		safety++
		if safety > 64 {
			return domain.FlowGraph{}, fmt.Errorf("work graph has a cycle or unresolved nodes")
		}
		var wave []domain.WorkNode
		var rest []domain.WorkNode
		for _, node := range remaining {
			if ready(node) {
				wave = append(wave, node)
			} else {
				rest = append(rest, node)
			}
		}
		if len(wave) == 0 {
			return domain.FlowGraph{}, fmt.Errorf("work graph has a cycle or missing dependency")
		}
		remaining = rest
		writers := 0
		for _, node := range wave {
			if node.WriteFiles && (node.Role == domain.StageRoleImplement || node.Role == "") {
				writers++
			}
		}
		if len(wave) == 1 {
			fn := workNodeToFlow(wave[0], writers > 1)
			flow.Nodes = append(flow.Nodes, fn)
			from := prevSerial
			if len(wave[0].DependsOn) == 1 {
				if id := compiled[wave[0].DependsOn[0]]; id != "" {
					from = id
				}
			}
			flow.Edges = append(flow.Edges, domain.FlowEdge{ID: domain.NewID("edge"), From: from, To: fn.ID})
			compiled[wave[0].ID] = fn.ID
			prevSerial = fn.ID
			continue
		}
		fork := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeParallel, Name: "Wave"}
		join := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeJoin, Name: "Join"}
		flow.Nodes = append(flow.Nodes, fork)
		flow.Edges = append(flow.Edges, domain.FlowEdge{ID: domain.NewID("edge"), From: prevSerial, To: fork.ID})
		for _, node := range wave {
			fn := workNodeToFlow(node, writers > 1)
			flow.Nodes = append(flow.Nodes, fn)
			flow.Edges = append(flow.Edges,
				domain.FlowEdge{ID: domain.NewID("edge"), From: fork.ID, To: fn.ID},
				domain.FlowEdge{ID: domain.NewID("edge"), From: fn.ID, To: join.ID},
			)
			compiled[node.ID] = fn.ID
		}
		flow.Nodes = append(flow.Nodes, join)
		prevSerial = join.ID
	}
	verifier := domain.FlowNode{ID: domain.NewID("node"), Kind: domain.FlowNodeVerifier, Name: "Acceptance", Config: map[string]any{
		"requireResult": true, "stageRole": domain.StageRoleAccept, "writeFiles": false,
	}}
	flow.Nodes = append(flow.Nodes, verifier, output)
	flow.Edges = append(flow.Edges,
		domain.FlowEdge{ID: domain.NewID("edge"), From: prevSerial, To: verifier.ID},
		domain.FlowEdge{ID: domain.NewID("edge"), From: verifier.ID, To: output.ID},
	)
	if err := validateCompiledMatchesGraph(graph, flow); err != nil {
		return domain.FlowGraph{}, err
	}
	return flow, nil
}

func workNodeToFlow(node domain.WorkNode, parallelWriters bool) domain.FlowNode {
	cfg := map[string]any{
		"stageRole": node.Role, "writeFiles": node.WriteFiles, "workNodeId": node.ID,
		"workContract": node.Contract,
	}
	if strings.TrimSpace(node.Instruction) != "" {
		cfg["instruction"] = strings.TrimSpace(node.Instruction)
	} else if inst := DefaultStageInstruction(node.Role); inst != "" {
		cfg["instruction"] = inst
	}
	if parallelWriters && node.WriteFiles {
		cfg["executionRoot"] = "root:" + node.ID
	}
	return domain.FlowNode{
		ID: domain.NewID("node"), Kind: domain.FlowNodeAgent, Name: node.Name, AgentID: node.AgentID, Config: cfg,
	}
}

func validateWorkGraph(graph domain.WorkGraph) error {
	if len(graph.Nodes) == 0 {
		return fmt.Errorf("work graph is empty")
	}
	seen := map[string]bool{}
	for _, node := range graph.Nodes {
		if strings.TrimSpace(node.ID) == "" || seen[node.ID] {
			return fmt.Errorf("work node id %q is invalid", node.ID)
		}
		seen[node.ID] = true
	}
	for _, node := range graph.Nodes {
		for _, dep := range node.DependsOn {
			if !seen[dep] {
				return fmt.Errorf("work node %q depends on unknown %q", node.ID, dep)
			}
		}
	}
	return nil
}

func validateCompiledMatchesGraph(graph domain.WorkGraph, flow domain.FlowGraph) error {
	mapped := 0
	for _, node := range flow.Nodes {
		if node.Kind == domain.FlowNodeAgent {
			mapped++
		}
	}
	if mapped != len(graph.Nodes) {
		return fmt.Errorf("compiled flow agent count %d != work graph %d", mapped, len(graph.Nodes))
	}
	return nil
}

// DefaultProjectWorkGraph builds bootstrap → implement → integrate → impl_review.
func DefaultProjectWorkGraph(agentIDs []string) domain.WorkGraph {
	primary := ""
	reviewer := ""
	if len(agentIDs) > 0 {
		primary = agentIDs[0]
	}
	if len(agentIDs) > 1 {
		reviewer = agentIDs[1]
	} else {
		reviewer = primary
	}
	shared := domain.SharedProjectPaths()
	now := time.Now().UTC()
	return domain.WorkGraph{ID: domain.NewID("workgraph"), CreatedAt: now, Nodes: []domain.WorkNode{
		{ID: "bootstrap", Name: "Bootstrap", Role: domain.StageRoleBootstrap, AgentID: primary, WriteFiles: true,
			Instruction: DefaultStageInstruction(domain.StageRoleBootstrap),
			Contract:    domain.WorkContract{OwnedPaths: shared, MergePlan: "serial bootstrap owns shared manifests and vendor"}},
		{ID: "implement", Name: "Implement", Role: domain.StageRoleImplement, AgentID: primary, WriteFiles: true,
			DependsOn:   []string{"bootstrap"},
			Instruction: DefaultStageInstruction(domain.StageRoleImplement),
			Contract: domain.WorkContract{
				OwnedPaths:     []string{"src", "config", "public"},
				ForbiddenPaths: append(append([]string{}, shared...), "bin", "tests", "phpunit.xml", "phpunit.xml.dist", "README.md"),
				MergePlan:      "implement owns application source, config, and public entrypoint",
			}},
		{ID: "integrate", Name: "Integrate", Role: domain.StageRoleIntegrate, AgentID: primary, WriteFiles: true,
			DependsOn:   []string{"implement"},
			Instruction: DefaultStageInstruction(domain.StageRoleIntegrate),
			Contract:    domain.WorkContract{OwnedPaths: domain.IntegrateOwnedPaths(), MergePlan: "merge worktrees onto bootstrap base; stop on conflict"}},
		{ID: "impl_review", Name: "Implementation review", Role: domain.StageRoleImplReview, AgentID: reviewer, WriteFiles: false,
			DependsOn:   []string{"integrate"},
			Instruction: DefaultStageInstruction(domain.StageRoleImplReview),
			Contract:    domain.WorkContract{ForbiddenPaths: []string{"src", "vendor"}, MergePlan: "read-only review of integrated revision"}},
		{ID: "accept", Name: "Accept", Role: domain.StageRoleAccept, AgentID: reviewer, WriteFiles: false,
			DependsOn:   []string{"impl_review"},
			Instruction: DefaultStageInstruction(domain.StageRoleAccept),
			Contract:    domain.WorkContract{ForbiddenPaths: append([]string{"src"}, shared...), MergePlan: "read-only acceptance on integrated revision"}},
	}}
}

// DefaultStageInstruction is the bounded prompt for a pipeline role. It must
// not paste the whole quest goal into bootstrap.
func DefaultStageInstruction(role string) string {
	switch role {
	case domain.StageRoleBootstrap:
		return "Bootstrap only: inspect composer.json, run composer install --no-interaction if vendor is missing, and stop. Do not implement application source, controllers, or tests. Do not spend the step budget exploring README line-by-line after dependencies exist."
	case domain.StageRoleImplement:
		return "Implement the approved quest goal in application source. Prefer propose_patch after list_files or a full-file read; do not use shell redirection (cat/echo) to write source. Inspect third-party libraries under vendor/ or node_modules/ with list_files/read_file when needed — do not clone dependencies into /tmp. Stay inside owned paths; do not rewrite lockfiles, vendored trees, or unrelated project scaffolding unless the quest requires it. Do not start long-lived servers or invent verification beyond the stage brief — accept runs declared checks later. Stop once the feature is in the tree."
	case domain.StageRoleIntegrate:
		return "Integrate writer outputs onto the bootstrap base revision. Own shared lockfiles/vendor and scaffolding (bin/, config/) needed for a coherent tip. Prefer confirming the inherited tip over inventing new files. Stop on merge conflict; do not silent-overwrite. Do not re-implement features."
	case domain.StageRoleImplReview:
		return "Read-only review of the integrated revision: list/read src and key config. Do not write. Report whether the goal looks present; do not re-run full test suites (accept stage verifies)."
	case domain.StageRoleAccept:
		return "Verify acceptance criteria on the integrated revision (phpunit / stated commands). Do not write application source. Do not invent new features. Evidence must point at this revision."
	default:
		return ""
	}
}

// EnsureProjectPipeline inserts integrate + read-only review + accept before the
// verifier when a compiled flow is missing those stage roles, and labels concurrent writers.
func EnsureProjectPipeline(flow domain.FlowGraph, agentIDs []string) domain.FlowGraph {
	hasIntegrate, hasReview, hasAcceptAgent := false, false, false
	for _, node := range flow.Nodes {
		switch domain.FlowNodeStageRole(node) {
		case domain.StageRoleIntegrate:
			hasIntegrate = true
		case domain.StageRoleImplReview:
			hasReview = true
		case domain.StageRoleAccept:
			if node.Kind == domain.FlowNodeAgent {
				hasAcceptAgent = true
			}
		}
	}
	labelConcurrentWriterRoots(&flow)
	if hasIntegrate && hasReview && hasAcceptAgent {
		return flow
	}
	primary, reviewer := "", ""
	if len(agentIDs) > 0 {
		primary = agentIDs[0]
	}
	if len(agentIDs) > 1 {
		reviewer = agentIDs[1]
	} else {
		reviewer = primary
	}
	verifierIdx := -1
	for i, node := range flow.Nodes {
		if node.Kind == domain.FlowNodeVerifier {
			verifierIdx = i
			break
		}
	}
	if verifierIdx < 0 {
		return flow
	}
	verifierID := flow.Nodes[verifierIdx].ID
	var incoming []int
	for i, edge := range flow.Edges {
		if edge.To == verifierID {
			incoming = append(incoming, i)
		}
	}
	anchor := ""
	if len(incoming) > 0 {
		anchor = flow.Edges[incoming[0]].From
	}
	var insert []domain.FlowNode
	if !hasIntegrate {
		insert = append(insert, domain.FlowNode{
			ID: domain.NewID("node"), Kind: domain.FlowNodeAgent, Name: "Integrate", AgentID: primary,
			Config: map[string]any{
				"stageRole": domain.StageRoleIntegrate, "writeFiles": true,
				"instruction": DefaultStageInstruction(domain.StageRoleIntegrate),
				"workContract": domain.WorkContract{
					OwnedPaths: domain.IntegrateOwnedPaths(),
					MergePlan:  "merge isolated writer roots onto bootstrap base; conflicts block",
				},
			},
		})
	}
	if !hasReview {
		insert = append(insert, domain.FlowNode{
			ID: domain.NewID("node"), Kind: domain.FlowNodeAgent, Name: "Implementation review", AgentID: reviewer,
			Config: map[string]any{
				"stageRole": domain.StageRoleImplReview, "writeFiles": false,
				"instruction":  DefaultStageInstruction(domain.StageRoleImplReview),
				"workContract": domain.WorkContract{ForbiddenPaths: []string{"src", "vendor"}, MergePlan: "read-only"},
			},
		})
	}
	if !hasAcceptAgent {
		insert = append(insert, domain.FlowNode{
			ID: domain.NewID("node"), Kind: domain.FlowNodeAgent, Name: "Accept", AgentID: reviewer,
			Config: map[string]any{
				"stageRole": domain.StageRoleAccept, "writeFiles": false,
				"instruction": DefaultStageInstruction(domain.StageRoleAccept),
				"workContract": domain.WorkContract{
					ForbiddenPaths: append([]string{"src"}, domain.SharedProjectPaths()...),
					MergePlan:      "read-only acceptance on integrated revision",
				},
			},
		})
	}
	if len(insert) == 0 || anchor == "" {
		return flow
	}
	flow.Nodes = append(flow.Nodes, insert...)
	for i := range incoming {
		flow.Edges[incoming[i]].To = insert[0].ID
	}
	prev := insert[0].ID
	for i := 1; i < len(insert); i++ {
		flow.Edges = append(flow.Edges, domain.FlowEdge{ID: domain.NewID("edge"), From: prev, To: insert[i].ID})
		prev = insert[i].ID
	}
	flow.Edges = append(flow.Edges, domain.FlowEdge{ID: domain.NewID("edge"), From: prev, To: verifierID})
	if flow.Nodes[verifierIdx].Config == nil {
		flow.Nodes[verifierIdx].Config = map[string]any{}
	}
	flow.Nodes[verifierIdx].Config["stageRole"] = domain.StageRoleAccept
	flow.Nodes[verifierIdx].Config["writeFiles"] = false
	flow.Nodes[verifierIdx].Config["instruction"] = DefaultStageInstruction(domain.StageRoleAccept)
	return flow
}

func labelConcurrentWriterRoots(flow *domain.FlowGraph) {
	if flow == nil {
		return
	}
	children := map[string][]int{}
	for _, edge := range flow.Edges {
		fromKind := domain.FlowNodeKind("")
		for _, node := range flow.Nodes {
			if node.ID == edge.From {
				fromKind = node.Kind
				break
			}
		}
		if fromKind != domain.FlowNodeParallel {
			continue
		}
		for j, candidate := range flow.Nodes {
			if candidate.ID == edge.To {
				children[edge.From] = append(children[edge.From], j)
			}
		}
	}
	for _, idxs := range children {
		writers := 0
		for _, idx := range idxs {
			if domain.FlowNodeWriteFiles(flow.Nodes[idx]) {
				writers++
			}
		}
		if writers < 2 {
			continue
		}
		for _, idx := range idxs {
			if !domain.FlowNodeWriteFiles(flow.Nodes[idx]) {
				continue
			}
			if flow.Nodes[idx].Config == nil {
				flow.Nodes[idx].Config = map[string]any{}
			}
			if domain.FlowNodeExecutionRoot(flow.Nodes[idx]) == "" {
				flow.Nodes[idx].Config["executionRoot"] = "root:" + flow.Nodes[idx].ID
			}
		}
	}
}

// ConcurrentWriterGroups returns agent node IDs that can run in parallel as writers.
func ConcurrentWriterGroups(flow domain.FlowGraph) [][]string {
	var groups [][]string
	byID := map[string]domain.FlowNode{}
	for _, node := range flow.Nodes {
		byID[node.ID] = node
	}
	for _, node := range flow.Nodes {
		if node.Kind != domain.FlowNodeParallel {
			continue
		}
		var writers []string
		for _, edge := range flow.Edges {
			if edge.From != node.ID {
				continue
			}
			child := byID[edge.To]
			if domain.FlowNodeWriteFiles(child) {
				writers = append(writers, child.ID)
			}
		}
		if len(writers) > 1 {
			groups = append(groups, writers)
		}
	}
	return groups
}
