package orchestrator

import (
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

func TestCompileWorkGraphSerialPipeline(t *testing.T) {
	graph := DefaultProjectWorkGraph([]string{"agent-a", "agent-b"})
	flow, err := CompileWorkGraph(graph, "PHP app")
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]bool{}
	writers := 0
	for _, node := range flow.Nodes {
		if node.Kind != domain.FlowNodeAgent {
			continue
		}
		role := domain.FlowNodeStageRole(node)
		roles[role] = true
		if domain.FlowNodeWriteFiles(node) {
			writers++
		}
		if role == domain.StageRoleImplReview && domain.FlowNodeWriteFiles(node) {
			t.Fatal("review node must not write")
		}
	}
	if !roles[domain.StageRoleBootstrap] || !roles[domain.StageRoleIntegrate] || !roles[domain.StageRoleImplReview] || !roles[domain.StageRoleAccept] {
		t.Fatalf("roles=%v", roles)
	}
	if writers < 3 {
		t.Fatalf("expected bootstrap/implement/integrate writers, got %d", writers)
	}
	var hasAcceptAgent, hasAcceptVerifier bool
	for _, node := range flow.Nodes {
		if domain.FlowNodeStageRole(node) != domain.StageRoleAccept {
			continue
		}
		if node.Kind == domain.FlowNodeAgent {
			hasAcceptAgent = true
		}
		if node.Kind == domain.FlowNodeVerifier {
			hasAcceptVerifier = true
		}
	}
	if !hasAcceptAgent || !hasAcceptVerifier {
		t.Fatal("missing accept agent and/or acceptance verifier")
	}
	var bootstrapInst string
	for _, node := range flow.Nodes {
		if domain.FlowNodeStageRole(node) == domain.StageRoleBootstrap {
			bootstrapInst, _ = node.Config["instruction"].(string)
		}
	}
	if bootstrapInst == "" || strings.Contains(strings.ToLower(bootstrapInst), "quest goal") {
		t.Fatalf("bootstrap instruction should be bounded, got %q", bootstrapInst)
	}
	if !strings.Contains(strings.ToLower(bootstrapInst), "composer") {
		t.Fatalf("bootstrap should mention composer: %q", bootstrapInst)
	}
}

func TestCompileWorkGraphParallelWritersGetRoots(t *testing.T) {
	graph := domain.WorkGraph{ID: "g1", Nodes: []domain.WorkNode{
		{ID: "a", Name: "A", Role: domain.StageRoleImplement, AgentID: "x", WriteFiles: true},
		{ID: "b", Name: "B", Role: domain.StageRoleImplement, AgentID: "y", WriteFiles: true},
	}}
	flow, err := CompileWorkGraph(graph, "parallel")
	if err != nil {
		t.Fatal(err)
	}
	groups := ConcurrentWriterGroups(flow)
	if len(groups) != 1 || len(groups[0]) != 2 {
		t.Fatalf("groups=%v", groups)
	}
	roots := map[string]bool{}
	for _, node := range flow.Nodes {
		if !domain.FlowNodeWriteFiles(node) {
			continue
		}
		root := domain.FlowNodeExecutionRoot(node)
		if root == "" || roots[root] {
			t.Fatalf("missing unique executionRoot on %s", node.Name)
		}
		roots[root] = true
	}
}

func TestCompileWorkGraphForbidsSharedPathsOnImplement(t *testing.T) {
	graph := DefaultProjectWorkGraph([]string{"a"})
	var implement domain.WorkNode
	for _, node := range graph.Nodes {
		if node.Role == domain.StageRoleImplement {
			implement = node
		}
	}
	if len(implement.Contract.ForbiddenPaths) == 0 {
		t.Fatal("implement must not own shared lockfiles")
	}
	if len(implement.Contract.OwnedPaths) == 0 {
		t.Fatal("implement must own src/config")
	}
}

func TestEnsureProjectPipelineInsertsIntegrateAndReview(t *testing.T) {
	flow := CompileFlow(CompileRequest{
		Title: "T", AgentIDs: []string{"a", "b"}, Importance: domain.QuestImportant,
		PlanningDepth: 50, Parallelism: 10,
	})
	got := EnsureProjectPipeline(flow, []string{"a", "b"})
	var integrate, review, acceptAgent bool
	for _, node := range got.Nodes {
		switch domain.FlowNodeStageRole(node) {
		case domain.StageRoleIntegrate:
			integrate = true
		case domain.StageRoleImplReview:
			review = true
			if domain.FlowNodeWriteFiles(node) {
				t.Fatal("inserted review writes")
			}
		case domain.StageRoleAccept:
			if node.Kind == domain.FlowNodeAgent {
				acceptAgent = true
				if domain.FlowNodeWriteFiles(node) {
					t.Fatal("inserted accept writes")
				}
			}
		}
	}
	if !integrate || !review || !acceptAgent {
		t.Fatalf("integrate=%v review=%v acceptAgent=%v", integrate, review, acceptAgent)
	}
}
