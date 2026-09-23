package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func validWorkOrder() WorkOrder {
	return NormalizeWorkOrder(WorkOrder{
		State: "ready", Goal: "Создать приложение", Scope: []string{"API"},
		Criteria:  []AcceptanceCriterion{{ID: "health", Kind: "verification", Text: "Health отвечает", Tool: "run_command", Arguments: json.RawMessage(`{"command":"go test ./..."}`)}},
		Workspace: WorkspacePlan{Mode: "managed", Path: `C:\Users\Test\Point\Projects\app`, Isolation: "snapshot"},
		Stack:     StackPresetRef{ID: "web", Version: "1", Category: "web", Source: "benchmark"},
		Routing:   ModelRoutingPolicy{Mode: "fixed", FixedConnectionID: "conn", FixedModel: "model", FallbackMode: "auto"},
		Network:   []NetworkGrant{{Host: "registry.npmjs.org:443", Purpose: "dependencies"}},
		Budget:    BudgetEnvelope{Preset: "medium", Tokens: 1000, ActiveSeconds: 60, MaxParallel: 1, MaxAttempts: 1},
		Delivery:  DeliveryPolicy{ApplyMode: "automatic", CommitMode: "none", KeepPartialDays: 30},
	})
}

func TestWorkOrderApprovalIsBoundToExactVersion(t *testing.T) {
	approved, err := ApproveWorkOrder(validWorkOrder())
	if err != nil {
		t.Fatal(err)
	}
	if !IsApprovedWorkOrder(approved) {
		t.Fatal("approved work order is not recognized")
	}
	approved.Goal = "Другая цель"
	if IsApprovedWorkOrder(approved) {
		t.Fatal("changed work order retained approval")
	}
}

func TestStaffingWorkOrderIsCompleteButCannotBeApproved(t *testing.T) {
	order := validWorkOrder()
	order.State = "staffing"
	order.Roster = AgentRosterPlan{Permanent: []AgentDraft{{
		ID: "agentdraft-frontend", Name: "Frontend", Role: "Frontend developer",
		Mission: "Own the UI", RequiredTools: []string{"read_file"}, ProjectOnly: true, RequiresConsent: true,
	}}}
	if err := ValidateWorkOrder(order); err != nil {
		t.Fatalf("complete staffing work order rejected: %v", err)
	}
	if _, err := ApproveWorkOrder(order); err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("staffing work order was approved: %v", err)
	}
}

func TestWorkOrderRejectsUnsafeAutoAndNetwork(t *testing.T) {
	order := validWorkOrder()
	order.Routing = ModelRoutingPolicy{Mode: "auto", RouterConnectionID: "router", RouterModel: "router", CostKnown: false}
	order.Network = []NetworkGrant{{Host: "*:443", Purpose: "everything"}}
	err := ValidateWorkOrder(order)
	if err == nil || !strings.Contains(err.Error(), "known model cost") || !strings.Contains(err.Error(), "exact public FQDN") {
		t.Fatalf("unexpected validation result: %v", err)
	}
}

func TestWorkOrderAutoRequiresCertifiedStructuredRouter(t *testing.T) {
	order := validWorkOrder()
	order.Routing = ModelRoutingPolicy{Mode: "auto", RouterConnectionID: "router", RouterModel: "router-model", CostKnown: true, Certification: "experimental", Adapter: AdapterCapabilityManifest{StructuredOutput: true, ContextTokens: 128000, CostVisibility: "known"}}
	if err := ValidateWorkOrder(order); err == nil || !strings.Contains(err.Error(), "certified router") {
		t.Fatalf("experimental Auto router was accepted: %v", err)
	}
	order.Routing.Certification = "certified"
	if err := ValidateWorkOrder(order); err != nil {
		t.Fatalf("certified Auto router rejected: %v", err)
	}
}

func TestWorkOrderNeverCarriesSecretValue(t *testing.T) {
	order := validWorkOrder()
	order.Secrets = []SecretRequirement{{Name: "EXCHANGE_API_KEY", Purpose: "read market data", SecretRef: "point-v2:secret", Required: true, Satisfied: true}}
	approved, err := ApproveWorkOrder(order)
	if err != nil || !IsApprovedWorkOrder(approved) {
		t.Fatalf("secret reference approval failed: %v", err)
	}
}

func TestWorkOrderRejectsLiveWriteWithoutExpertOptIn(t *testing.T) {
	order := validWorkOrder()
	order.Workspace.Isolation = "live_write"
	if err := ValidateWorkOrder(order); err == nil {
		t.Fatal("live_write was accepted without explicit expert opt-in")
	}
	order.Workspace.ExpertOptIn = true
	if err := ValidateWorkOrder(order); err != nil {
		t.Fatalf("explicit expert live_write should be valid: %v", err)
	}
}

func TestWorkOrderCanDiscussMissingSecretButCannotApproveIt(t *testing.T) {
	order := validWorkOrder()
	order.State = "discussion"
	order.Secrets = []SecretRequirement{{Name: "EXCHANGE_API_KEY", Purpose: "read-only market data", Required: true}}
	if err := ValidateWorkOrder(order); err != nil {
		t.Fatalf("discussion card hid an unsatisfied secret instead of showing it: %v", err)
	}
	order.State = "ready"
	if err := ValidateWorkOrder(order); err == nil || !strings.Contains(err.Error(), "EXCHANGE_API_KEY") {
		t.Fatalf("ready work order accepted an unsatisfied required secret: %v", err)
	}
}
