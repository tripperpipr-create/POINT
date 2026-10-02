package app

import (
	"bytes"
	"context"
	"encoding/json"
	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
	"testing"
	"time"
)

func TestReuseRequiresExplicitDeterminismAndPreservesManualReview(t *testing.T) {
	criteria := []domain.AcceptanceCriterion{{ID: "test", Kind: "verification", Tool: "run_command"}, {ID: "http", Kind: "manual"}}
	if criteriaReuseEligible(criteria) {
		t.Fatal("opaque legacy criterion may be reused")
	}
	criteria[0].Deterministic = true
	if !criteriaReuseEligible(criteria) {
		t.Fatal("explicit local check is not eligible")
	}
	criteria[0].Kind = "reproduction"
	if criteriaReuseEligible(criteria) {
		t.Fatal("external/reproduction state may be reused")
	}
	zero := 0
	proof := sealedVerificationEvidence(criteriaBatchResult{Criteria: []agent.CriterionEvidence{
		{CriterionID: "test", Kind: "verification", Status: "satisfied", Check: &agent.CheckEvidence{ExitCode: &zero, Status: "passed"}},
		{CriterionID: "http", Kind: "manual", Status: "needs_review"},
	}})
	batch, ok := reusedCriteriaBatch(domain.VerificationResult{ID: "v", Source: "pre_accept", AllPassed: true, TreeDigest: "tree", Evidence: proof, CreatedAt: time.Now()}, "tree")
	if !ok || !batch.AllOK || !batch.NeedsReview || len(agent.PendingCriterionIDs(batch.Criteria)) != 1 {
		t.Fatalf("manual review lost: %+v", batch)
	}
}

func TestReuseRejectsCorruptOrNonIndependentProof(t *testing.T) {
	zero := 0
	criteria := []agent.CriterionEvidence{{CriterionID: "test", Kind: "verification", Status: "satisfied", Check: &agent.CheckEvidence{Tool: "run_command", ExitCode: &zero, Status: "passed"}}}
	encode := func() json.RawMessage { return sealedVerificationEvidence(criteriaBatchResult{Criteria: criteria}) }
	stored := domain.VerificationResult{ID: "v", Source: "pre_accept", AllPassed: true, TreeDigest: "tree", Evidence: encode()}
	expected := []domain.AcceptanceCriterion{{ID: "test", Kind: "verification", Tool: "run_command", Deterministic: true}}
	if _, ok := reusedCriteriaBatch(stored, "tree", expected); !ok {
		t.Fatal("valid independent proof rejected")
	}
	sealed := sealedVerificationEvidence(criteriaBatchResult{Criteria: criteria, Summaries: []string{"original summary"}})
	stored.Evidence = bytes.Replace(sealed, []byte("original summary"), []byte("changed summary"), 1)
	if _, ok := reusedCriteriaBatch(stored, "tree", expected); ok {
		t.Fatal("valid JSON with a corrupt proof checksum accepted")
	}
	stored.Evidence = encode()
	stored.Source = "writer"
	if _, ok := reusedCriteriaBatch(stored, "tree", expected); ok {
		t.Fatal("writer proof accepted as independent")
	}
	stored.Source = "pre_accept"
	criteria[0].CriterionID = "different"
	stored.Evidence = encode()
	if _, ok := reusedCriteriaBatch(stored, "tree", expected); ok {
		t.Fatal("corrupt criterion identity accepted")
	}
	criteria[0].CriterionID = "test"
	one := 1
	criteria[0].Check.ExitCode = &one
	stored.Evidence = encode()
	if _, ok := reusedCriteriaBatch(stored, "tree", expected); ok {
		t.Fatal("nonzero exit accepted from corrupt proof")
	}
}

func TestReusableChecksCannotObserveApprovedExternalHosts(t *testing.T) {
	f := newVerificationFixture(t)
	_, err := f.app.runCriteriaBatch(criteriaBatchInput{Context: context.Background(), Phase: "pre_accept", RunID: "run-local-proof", QuestID: f.quest.ID, Sandbox: f.record, Criteria: f.quest.Brief.Criteria, NetworkPolicy: "ALLOWLIST", NetworkHosts: []string{"registry.npmjs.org"}})
	if err != nil {
		t.Fatal(err)
	}
	f.backend.mu.Lock()
	defer f.backend.mu.Unlock()
	if len(f.backend.processRequests) == 0 {
		t.Fatal("independent check did not run")
	}
	for _, request := range f.backend.processRequests {
		if request.NetworkPolicy != "DENY" || len(request.AllowedNetworkHosts) != 0 {
			t.Fatalf("reusable check observed external state: %+v", request)
		}
	}
}

func TestReuseInputsBindApprovedCriteriaAndOrder(t *testing.T) {
	input := criteriaBatchInput{Criteria: []domain.AcceptanceCriterion{{ID: "test", Kind: "verification", Tool: "run_command", Deterministic: true, Arguments: json.RawMessage(`{"command":"go test ./..."}`)}}, WorkOrder: &domain.WorkOrder{ApprovedDigest: "approved-1"}}
	base := verificationInputsDigest(input)
	input.WorkOrder = &domain.WorkOrder{ApprovedDigest: "approved-2"}
	if base == verificationInputsDigest(input) {
		t.Fatal("approved order ignored")
	}
	input.WorkOrder = &domain.WorkOrder{ApprovedDigest: "approved-1"}
	input.Criteria[0].Deterministic = false
	if base == verificationInputsDigest(input) {
		t.Fatal("determinism decision ignored")
	}
	input.Criteria[0].Deterministic = true
	input.Criteria[0].Arguments = json.RawMessage(`{"command":"go test ./...","timeoutSeconds":1}`)
	if base == verificationInputsDigest(input) {
		t.Fatal("criterion execution arguments ignored")
	}
}

func TestReuseRejectsChangedStoredCommandEvenWithSameKey(t *testing.T) {
	zero := 0
	args := json.RawMessage(`{"command":"go test ./..."}`)
	planned := []executedCriterion{{CriterionID: "test", Tool: "run_command", Arguments: args}}
	batch := criteriaBatchResult{Commands: append([]executedCriterion(nil), planned...), Criteria: []agent.CriterionEvidence{{CriterionID: "test", Status: "satisfied", Check: &agent.CheckEvidence{Tool: "run_command", Arguments: args, ExitCode: &zero}}}}
	if !reusedProofMatchesCommands(batch, planned) {
		t.Fatal("matching proof rejected")
	}
	batch.Criteria[0].Check.Arguments = json.RawMessage(`{"command":"true"}`)
	if reusedProofMatchesCommands(batch, planned) {
		t.Fatal("corrupt check arguments accepted")
	}
	batch.Criteria[0].Check.Arguments = args
	batch.Commands[0].Arguments = json.RawMessage(`{"command":"true"}`)
	if reusedProofMatchesCommands(batch, planned) {
		t.Fatal("corrupt command batch accepted")
	}
}
