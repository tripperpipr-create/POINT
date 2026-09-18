package app

import (
	"context"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestAutoRoutingUsesOnlyCertifiedModelsAndSeparatesVerifier(t *testing.T) {
	application := newTestApp(t)
	var err error
	ctx := context.Background()
	if _, err = application.OpenWorkspace(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"certified-a", "certified-b"} {
		model := "model-" + id
		now := time.Now().UTC().Add(time.Duration(index) * time.Millisecond)
		connection := domain.Connection{
			ID: id, Provider: domain.ProviderOpenAI, PresetID: "openai", DisplayName: id,
			BaseURL: "https://example.invalid/v1", Status: domain.ConnectionConnected,
			Models: []domain.ConnectionModel{{ID: model, ContextWindow: 32768, State: "confirmed"}}, CreatedAt: now, UpdatedAt: now,
		}
		if err = application.store.SaveConnection(ctx, connection); err != nil {
			t.Fatal(err)
		}
		evidence := domain.ModelCapabilityEvidence{
			ID: "evidence-" + id, ConnectionID: id, Model: model, Provider: domain.ProviderOpenAI, Runtime: "point",
			Capabilities: []string{"tools", "structured-output", "coding", "review"}, Healthy: true, LatencyMs: int64(10 + index), CreatedAt: now,
		}
		if err = application.store.SaveModelCapabilityEvidence(ctx, evidence); err != nil {
			t.Fatal(err)
		}
		if err = application.store.SaveModelCertificationV2(ctx, domain.ModelCertification{
			ID: "cert-" + id, ConnectionID: id, Model: model, Provider: domain.ProviderOpenAI, Runtime: "point",
			Status: "certified", MatrixVersion: "agent-hub-v2-3x3", RequiredRuns: 9, PassedRuns: 9, CostKnown: true,
			Capabilities: domain.AdapterCapabilityManifest{Tools: true, StructuredOutput: true, ContextTokens: 32768, CostVisibility: "known", AllowedRoles: []string{"writer", "verifier", "router"}},
			EvidenceIDs:  []string{evidence.ID}, LastEvaluatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err = application.SaveModelPricingProfile(domain.ModelPricingProfile{Provider: string(domain.ProviderOpenAI), Model: model, InputCentsPerMillion: 1, OutputCentsPerMillion: 1}); err != nil {
			t.Fatal(err)
		}
	}
	flow := domain.FlowGraph{Nodes: []domain.FlowNode{
		{ID: "writer", Kind: domain.FlowNodeAgent, Name: "Implement", AgentID: "agent-writer"},
		{ID: "verifier", Kind: domain.FlowNodeAgent, Name: "Verify", AgentID: "agent-verifier"},
	}}
	contract := &domain.WorkOrderExecutionContract{Routing: domain.ModelRoutingPolicy{
		Mode: "auto", RouterConnectionID: "certified-a", RouterModel: "model-certified-a", CostKnown: true, Certification: "certified",
	}}
	if err = application.applyApprovedWorkOrderFlowPolicyV2(&flow, contract); err != nil {
		t.Fatal(err)
	}
	writer, err := modelBindingFromNode(flow.Nodes[0])
	if err != nil || writer == nil || writer.Source != "work_order_v2_auto" {
		t.Fatalf("writer binding=%#v err=%v", writer, err)
	}
	verifier, err := modelBindingFromNode(flow.Nodes[1])
	if err != nil || verifier == nil {
		t.Fatalf("verifier binding=%#v err=%v", verifier, err)
	}
	if writer.ConnectionID == verifier.ConnectionID && writer.Model == verifier.Model {
		t.Fatalf("independent certified verifier was available but not selected: writer=%#v verifier=%#v", writer, verifier)
	}
}

func TestAutoRoutingRejectsProbeOnlyExperimentalModel(t *testing.T) {
	application := newTestApp(t)
	var err error
	flow := domain.FlowGraph{Nodes: []domain.FlowNode{{ID: "writer", Kind: domain.FlowNodeAgent, Name: "Implement", AgentID: "agent"}}}
	contract := &domain.WorkOrderExecutionContract{Routing: domain.ModelRoutingPolicy{
		Mode: "auto", RouterConnectionID: "missing", RouterModel: "probe-only", CostKnown: true, Certification: "certified",
	}}
	if err = application.applyApprovedWorkOrderFlowPolicyV2(&flow, contract); err == nil {
		t.Fatal("Auto accepted a model without certified matrix evidence")
	}
}
