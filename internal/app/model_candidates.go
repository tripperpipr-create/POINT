package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/executors"
)

// modelCandidates returns only explicit connection/model pairs. Unknown model
// limits remain unknown; routing must not invent capacity or price.
func (a *App) modelCandidates(ctx context.Context) ([]domain.ModelCandidate, error) {
	connections, err := a.store.ListConnections(ctx)
	if err != nil {
		return nil, err
	}
	evidence, err := a.store.ListModelCapabilityEvidence(ctx, "", "", 500)
	if err != nil {
		return nil, err
	}
	certifications, err := a.store.ListModelCertificationsV2(ctx, "", "")
	if err != nil {
		return nil, err
	}
	latestCertification := map[string]domain.ModelCertification{}
	for _, item := range certifications {
		key := item.ConnectionID + "\x00" + item.Model
		current, exists := latestCertification[key]
		if !exists || current.Status != "certified" && item.Status == "certified" {
			latestCertification[key] = item
		}
	}
	latest := map[string]domain.ModelCapabilityEvidence{}
	for _, item := range evidence {
		key := item.ConnectionID + "\x00" + item.Model
		if _, exists := latest[key]; !exists {
			latest[key] = item
		}
	}
	var result []domain.ModelCandidate
	for _, connection := range connections {
		if connection.Status != domain.ConnectionConnected {
			continue
		}
		models := connection.Models
		if len(models) == 0 && strings.TrimSpace(connection.DefaultModel) != "" {
			models = []domain.ConnectionModel{{ID: strings.TrimSpace(connection.DefaultModel), DisplayName: strings.TrimSpace(connection.DefaultModel), State: "unknown"}}
		}
		for _, model := range models {
			if strings.TrimSpace(model.ID) == "" {
				continue
			}
			capabilities := append([]string(nil), model.Capabilities...)
			runtime := string(executors.KindForProvider(connection.Provider))
			if runtime != string(executors.KindPoint) {
				capabilities = appendUniqueStrings(capabilities, "agent", "tools", "mcp")
			}
			probe, probed := latest[connection.ID+"\x00"+model.ID]
			if probed {
				capabilities = appendUniqueStrings(capabilities, probe.Capabilities...)
			}
			if !probed || !probe.Healthy {
				continue
			}
			if runtime != string(executors.KindPoint) {
				capabilities = appendUniqueStrings(capabilities, executors.CapabilityNames(executors.NewCLI(executors.Kind(runtime)).Capabilities())...)
			}
			workspaceID := ""
			a.mu.RLock()
			if a.currentWorkspace != nil {
				workspaceID = a.currentWorkspace.ID
			}
			a.mu.RUnlock()
			_, pricingKnown, _ := a.estimateModelCost(ctx, workspaceID, string(connection.Provider), model.ID, 1000, 1000)
			certification, certified := latestCertification[connection.ID+"\x00"+model.ID]
			status := "experimental"
			adapter := domain.AdapterCapabilityManifest{
				Tools: stringsContainV2(capabilities, "tools"), StructuredOutput: stringsContainV2(capabilities, "structured-output"),
				ContextTokens: model.ContextWindow, CostVisibility: "unknown",
			}
			if pricingKnown {
				adapter.CostVisibility = "known"
			}
			if runtime != string(executors.KindPoint) {
				caps := executors.NewCLI(executors.Kind(runtime)).Capabilities()
				adapter.Tools, adapter.StructuredOutput, adapter.PauseResume = caps.MCP, caps.StructuredEvents, caps.Resume
			}
			if certified {
				status, adapter = certification.Status, certification.Capabilities
			}
			result = append(result, domain.ModelCandidate{
				ConnectionID: connection.ID, Provider: connection.Provider, Model: model.ID,
				Runtime: runtime, Capabilities: capabilities, ContextWindow: model.ContextWindow,
				MaxOutput: model.MaxOutput, Healthy: true, HealthSource: "capability_probe",
				LatencyMs: probe.LatencyMs, PricingKnown: pricingKnown,
				Certification: status, Experimental: status != "certified", Adapter: adapter,
			})
		}
	}
	return result, nil
}

func stringsContainV2(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (a *App) ModelCertificationsV2(connectionID, model string) ([]domain.ModelCertification, error) {
	return a.store.ListModelCertificationsV2(context.Background(), strings.TrimSpace(connectionID), strings.TrimSpace(model))
}

func (a *App) ModelCandidates() ([]domain.ModelCandidate, error) {
	return a.modelCandidates(context.Background())
}

func (a *App) ModelCapabilityEvidence(connectionID, model string) ([]domain.ModelCapabilityEvidence, error) {
	return a.store.ListModelCapabilityEvidence(context.Background(), strings.TrimSpace(connectionID), strings.TrimSpace(model), 200)
}

func (a *App) applyModelBinding(profile *domain.AgentProfile, binding *domain.ModelBinding) error {
	if binding == nil {
		return nil
	}
	if binding.ProjectAgentID != "" && profile.ID != binding.ProjectAgentID {
		return errors.New("model binding belongs to another project agent")
	}
	if strings.TrimSpace(binding.Model) == "" {
		return errors.New("model binding requires a model")
	}
	if strings.TrimSpace(binding.ConnectionID) == "" {
		return errors.New("model binding requires a connection")
	}
	if expected := string(executors.KindForProvider(binding.Provider)); binding.Runtime != "" && binding.Runtime != expected {
		return fmt.Errorf("model binding runtime %q does not match provider %q", binding.Runtime, binding.Provider)
	}
	if binding.ConnectionID != "" {
		connection, err := a.ResolveConnection(ConnectionRef{ConnectionID: binding.ConnectionID, Label: "stage model binding"})
		if err != nil {
			return err
		}
		if connection.Status != domain.ConnectionConnected {
			return fmt.Errorf("stage model connection %q is not connected", connection.ID)
		}
		if binding.Provider != "" && connection.Provider != binding.Provider {
			return errors.New("stage model binding provider does not match its connection")
		}
		if !connectionAllowsModel(connection, binding.Model) {
			return fmt.Errorf("model %q is not in connection %q (%s) catalog; re-check the connection to refresh its model list", binding.Model, connection.ID, connection.DisplayName)
		}
		profile.ConnectionID = connection.ID
		profile.Provider = connection.Provider
		profile.ProviderPreset = connection.PresetID
		profile.BaseURL = connection.BaseURL
		profile.APIVersion = connection.APIVersion
	} else if binding.Provider != "" {
		profile.Provider = binding.Provider
	}
	profile.Model = binding.Model
	if binding.FallbackMode == "wait" {
		profile.FallbackModels = nil
	} else if binding.FallbackModels != nil {
		profile.FallbackModels = append([]string(nil), binding.FallbackModels...)
	}
	return nil
}

func modelBindingFromNode(node domain.FlowNode) (*domain.ModelBinding, error) {
	if node.Config == nil || node.Config["modelBinding"] == nil {
		return nil, nil
	}
	raw, err := json.Marshal(node.Config["modelBinding"])
	if err != nil {
		return nil, err
	}
	var binding domain.ModelBinding
	if err = json.Unmarshal(raw, &binding); err != nil {
		return nil, err
	}
	return &binding, nil
}

func appendUniqueStrings(values []string, extra ...string) []string {
	seen := make(map[string]bool, len(values)+len(extra))
	out := make([]string, 0, len(values)+len(extra))
	for _, value := range append(values, extra...) {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}
