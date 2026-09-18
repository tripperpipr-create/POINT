package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/executors"
)

// applyApprovedWorkOrderFlowPolicyV2 binds the immutable routing choice to
// every writer stage. Auto bindings are produced by the certified planner and
// are only checked here; Fixed bindings are deterministic and override any
// reusable agent default without mutating the Blueprint or ProjectAgent.
func (a *App) applyApprovedWorkOrderFlowPolicyV2(flow *domain.FlowGraph, contract *domain.WorkOrderExecutionContract) error {
	if flow == nil || contract == nil {
		return errors.New("approved WorkOrder execution contract is required")
	}
	policy := contract.Routing
	switch policy.Mode {
	case "fixed":
		connection, err := a.ResolveConnection(ConnectionRef{ConnectionID: policy.FixedConnectionID, Label: "approved WorkOrder routing"})
		if err != nil {
			return err
		}
		// Approval compiles the user's exact choice without touching the network.
		// Connectivity is a preflight/runtime concern: otherwise approval would
		// be non-deterministic and a temporary outage could invalidate a signed
		// WorkOrder. A previously probed catalog is still authoritative.
		if !connectionAllowsModel(connection, policy.FixedModel) {
			return fmt.Errorf("approved model %q is not in connection %q (%s) catalog; re-check the connection to refresh its model list", policy.FixedModel, connection.ID, connection.DisplayName)
		}
		runtime := string(executors.KindForProvider(connection.Provider))
		for index := range flow.Nodes {
			node := &flow.Nodes[index]
			if node.Kind != domain.FlowNodeAgent || strings.TrimSpace(node.AgentID) == "" {
				continue
			}
			if node.Config == nil {
				node.Config = map[string]any{}
			}
			node.Config["modelBinding"] = domain.ModelBinding{
				ProjectAgentID: node.AgentID, ConnectionID: connection.ID,
				Provider: connection.Provider, Model: policy.FixedModel, Runtime: runtime,
				Source: "work_order_v2", Reason: "fixed model approved in WorkOrder",
				FallbackMode:   policy.FallbackMode,
				FallbackModels: append([]string(nil), policy.FallbackModels...),
			}
		}
		return nil
	case "auto":
		if policy.Certification != "certified" || !policy.CostKnown {
			return errors.New("Auto routing requires a certified router with known cost")
		}
		if _, err := a.certifiedModelV2(context.Background(), policy.RouterConnectionID, policy.RouterModel); err != nil {
			return fmt.Errorf("Auto router: %w", err)
		}
		candidates, err := a.certifiedModelCandidatesV2(context.Background())
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			return errors.New("Auto routing has no healthy certified executor with known cost")
		}
		var writer *domain.ModelBinding
		for index := range flow.Nodes {
			node := &flow.Nodes[index]
			if node.Kind != domain.FlowNodeAgent {
				continue
			}
			binding, err := modelBindingFromNode(*node)
			if err != nil {
				return err
			}
			if binding == nil || !candidateMatchesBindingV2(candidates, *binding) {
				selected, ok := selectCertifiedCandidateV2(candidates, nil)
				if !ok {
					return fmt.Errorf("Auto routing cannot bind stage %q to a certified model", node.Name)
				}
				binding = &domain.ModelBinding{
					ProjectAgentID: node.AgentID, ConnectionID: selected.ConnectionID, Provider: selected.Provider,
					Model: selected.Model, Runtime: selected.Runtime, Source: "work_order_v2_auto",
					Reason: "deterministic certified fallback selected by Point",
				}
			}
			isVerifier := isVerifierNodeV2(*node)
			if isVerifier && writer != nil && binding.ConnectionID == writer.ConnectionID && binding.Model == writer.Model {
				if independent, ok := selectCertifiedCandidateV2(candidates, writer); ok {
					binding = &domain.ModelBinding{
						ProjectAgentID: node.AgentID, ConnectionID: independent.ConnectionID, Provider: independent.Provider,
						Model: independent.Model, Runtime: independent.Runtime, Source: "work_order_v2_auto",
						Reason: "independent certified verifier selected by Point",
					}
				} else {
					if node.Config == nil {
						node.Config = map[string]any{}
					}
					node.Config["cleanModelContext"] = true
				}
			}
			if node.Config == nil {
				node.Config = map[string]any{}
			}
			node.Config["modelBinding"] = *binding
			if !isVerifier && writer == nil {
				copy := *binding
				writer = &copy
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported approved routing mode %q", policy.Mode)
	}
}

func (a *App) certifiedModelV2(ctx context.Context, connectionID, model string) (domain.ModelCertification, error) {
	items, err := a.store.ListModelCertificationsV2(ctx, strings.TrimSpace(connectionID), strings.TrimSpace(model))
	if err != nil {
		return domain.ModelCertification{}, err
	}
	for _, item := range items {
		if item.Status == "certified" && item.CostKnown {
			return item, nil
		}
	}
	return domain.ModelCertification{}, fmt.Errorf("model %q on connection %q is not certified with known cost", model, connectionID)
}

func (a *App) autoRouterConfigV2(ctx context.Context, base domain.OrchestratorConfig, contract *domain.WorkOrderExecutionContract) (domain.OrchestratorConfig, error) {
	if contract == nil || contract.Routing.Mode != "auto" {
		return base, nil
	}
	policy := contract.Routing
	certification, err := a.certifiedModelV2(ctx, policy.RouterConnectionID, policy.RouterModel)
	if err != nil {
		return domain.OrchestratorConfig{}, err
	}
	if !certification.Capabilities.StructuredOutput || certification.Capabilities.ContextTokens <= 0 {
		return domain.OrchestratorConfig{}, errors.New("Auto router lacks certified structured output or context capacity")
	}
	connection, err := a.ResolveConnection(ConnectionRef{ConnectionID: policy.RouterConnectionID, Label: "Auto router"})
	if err != nil {
		return domain.OrchestratorConfig{}, err
	}
	if connection.Status != domain.ConnectionConnected {
		return domain.OrchestratorConfig{}, fmt.Errorf("Auto router connection %q is not connected", connection.ID)
	}
	if executors.KindForProvider(connection.Provider) != executors.KindPoint {
		return domain.OrchestratorConfig{}, errors.New("Auto router must support Point structured planning")
	}
	base.ConnectionID, base.Provider, base.ProviderPreset = connection.ID, connection.Provider, connection.PresetID
	base.BaseURL, base.APIVersion, base.Model = connection.BaseURL, connection.APIVersion, policy.RouterModel
	if base.Preset == "" {
		base.Preset = "conductor"
	}
	if base.MaxOutputTokens <= 0 {
		base.MaxOutputTokens = 4000
	}
	return base, nil
}

func (a *App) certifiedModelCandidatesV2(ctx context.Context) ([]domain.ModelCandidate, error) {
	candidates, err := a.modelCandidates(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.ModelCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Certification == "certified" && candidate.PricingKnown && candidate.Healthy {
			result = append(result, candidate)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].LatencyMs != result[j].LatencyMs {
			return result[i].LatencyMs < result[j].LatencyMs
		}
		if result[i].ConnectionID != result[j].ConnectionID {
			return result[i].ConnectionID < result[j].ConnectionID
		}
		return result[i].Model < result[j].Model
	})
	return result, nil
}

func candidateMatchesBindingV2(candidates []domain.ModelCandidate, binding domain.ModelBinding) bool {
	for _, candidate := range candidates {
		if candidate.ConnectionID == binding.ConnectionID && candidate.Model == binding.Model && candidate.Runtime == binding.Runtime {
			return true
		}
	}
	return false
}

func selectCertifiedCandidateV2(candidates []domain.ModelCandidate, differentFrom *domain.ModelBinding) (domain.ModelCandidate, bool) {
	for _, candidate := range candidates {
		if differentFrom != nil && candidate.ConnectionID == differentFrom.ConnectionID && candidate.Model == differentFrom.Model {
			continue
		}
		return candidate, true
	}
	return domain.ModelCandidate{}, false
}

func isVerifierNodeV2(node domain.FlowNode) bool {
	label := strings.ToLower(node.Name + " " + node.ToolName)
	return strings.Contains(label, "review") || strings.Contains(label, "verif") || strings.Contains(label, "провер") || strings.Contains(label, "test")
}

// connectionAllowsModel is the single gate shared by approval
// (applyApprovedWorkOrderFlowPolicyV2) and runtime (applyModelBinding). An empty
// catalog does not mean "no such model": llmux and custom endpoints are not
// obliged to serve /v1/models, and the model there is named by a human. A
// refusal is only warranted when the catalog is known and the model is absent.
func connectionAllowsModel(connection domain.Connection, model string) bool {
	if len(connection.Models) == 0 && strings.TrimSpace(connection.DefaultModel) == "" {
		return strings.TrimSpace(model) != ""
	}
	return connectionHasModelV2(connection, model)
}

func connectionHasModelV2(connection domain.Connection, model string) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	if len(connection.Models) == 0 {
		return strings.TrimSpace(connection.DefaultModel) == model
	}
	for _, candidate := range connection.Models {
		if strings.TrimSpace(candidate.ID) == model {
			return true
		}
	}
	return false
}
