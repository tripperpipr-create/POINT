package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

type capabilityProbeModel struct {
	mode  string
	calls int
}

func (m *capabilityProbeModel) Stream(_ context.Context, _ providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.calls++
	tool := func(id, name, arguments string) error {
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(arguments)}})
	}
	switch m.mode {
	case "writer":
		switch m.calls {
		case 1:
			return tool("inspect", "inspect_source", `{"path":"probe.txt"}`)
		case 2:
			return tool("edit", "propose_probe_edit", `{"path":"probe.txt","oldText":"const answer = 40","newText":"const answer = 42"}`)
		case 3:
			return tool("verify", "verify_probe", `{"check":"answer"}`)
		default:
			if err := emit(providers.ModelEvent{Kind: providers.EventUsage, InputTokens: 320, OutputTokens: 42}); err != nil {
				return err
			}
			return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: `{"role":"configured","result":"complete","evidence":["inspection","edit","verification"]}`})
		}
	case "reader":
		switch m.calls {
		case 1:
			return tool("inspect", "inspect_source", `{"path":"probe.txt"}`)
		case 2:
			return tool("verify", "verify_probe", `{"check":"answer"}`)
		default:
			return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: `{"role":"configured","result":"complete","evidence":["inspection","verification"]}`})
		}
	default:
		if m.calls == 1 {
			return tool("blind-edit", "propose_probe_edit", `{"path":"probe.txt","oldText":"40","newText":"42"}`)
		}
		return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "done"})
	}
}

func capabilityProbeProfile(write bool) domain.AgentProfile {
	profile := domain.AgentProfile{
		Name: "Probe", RoleDescription: "Go service specialist", Provider: domain.ProviderOllama,
		BaseURL: "http://127.0.0.1:11434", Model: "local", ContextWindowTokens: 8192,
	}
	if write {
		profile.AllowedTools = []string{"read_file", "propose_patch", "run_command"}
	} else {
		profile.AllowedTools = []string{"read_file", "search_text"}
	}
	return profile
}

func TestModelCapabilityProbeReportsIndependentWriterEvidence(t *testing.T) {
	result := runModelCapabilityProbe(context.Background(), ModelCapabilityProbeRequest{Profile: capabilityProbeProfile(true)}, &capabilityProbeModel{mode: "writer"})
	for name, check := range map[string]ModelCapabilityCheck{
		"tools": result.ToolCalls, "json": result.JSONContract, "inspection": result.InspectionBeforeEdit,
		"verification": result.VerificationEvidence, "limits": result.WithinLimits,
	} {
		if check.Status != "PASS" {
			t.Fatalf("%s check = %+v", name, check)
		}
	}
	if result.ToolFailures != 0 || result.InputTokens != 320 || result.OutputTokens != 42 {
		t.Fatalf("unexpected raw metrics: %+v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(encoded)), "score") {
		t.Fatalf("probe must not expose a synthetic score: %s", encoded)
	}
}

func TestModelCapabilityProbeKeepsReadOnlyRoleReadOnly(t *testing.T) {
	result := runModelCapabilityProbe(context.Background(), ModelCapabilityProbeRequest{Profile: capabilityProbeProfile(false)}, &capabilityProbeModel{mode: "reader"})
	if result.ToolCalls.Status != "PASS" || result.VerificationEvidence.Status != "PASS" {
		t.Fatalf("read-only probe did not exercise tools: %+v", result)
	}
	if result.InspectionBeforeEdit.Status != "NOT_APPLICABLE" || result.ToolFailures != 0 {
		t.Fatalf("read-only boundary was not explicit: %+v", result)
	}
}

func TestModelCapabilityProbeNamesConcreteFailures(t *testing.T) {
	result := runModelCapabilityProbe(context.Background(), ModelCapabilityProbeRequest{Profile: capabilityProbeProfile(true)}, &capabilityProbeModel{mode: "blind"})
	if result.ToolCalls.Status != "FAIL" || result.JSONContract.Status != "FAIL" || result.InspectionBeforeEdit.Status != "FAIL" || result.VerificationEvidence.Status != "FAIL" {
		t.Fatalf("expected independent failed checks: %+v", result)
	}
	if result.ToolFailures == 0 || len(result.Limitations) < 3 || len(result.Suggestions) < 3 {
		t.Fatalf("failure advice lacks source metrics: %+v", result)
	}
}
