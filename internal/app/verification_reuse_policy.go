package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
)

// Manual review is never reused. Opaque or external checks need a new run;
// only an explicitly approved deterministic local verification may be reused.
func criteriaReuseEligible(criteria []domain.AcceptanceCriterion) bool {
	checked := false
	for _, criterion := range criteria {
		if criterion.Kind == "manual" {
			continue
		}
		if criterion.Kind != "verification" || criterion.Tool != "run_command" || !criterion.Deterministic {
			return false
		}
		checked = true
	}
	return checked
}

func verificationInputsDigest(input criteriaBatchInput) string {
	order := ""
	if input.WorkOrder != nil {
		order = input.WorkOrder.ApprovedDigest
	}
	raw, _ := json.Marshal(struct {
		Environment, Order string
		Criteria           []domain.AcceptanceCriterion
	}{
		sandbox.VerificationContextDigest(input.Sandbox), order, input.Criteria,
	})
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func verificationEvidenceDigest(criteria []agent.CriterionEvidence, summaries []string, commands []executedCriterion) string {
	raw, err := json.Marshal(map[string]any{"criteria": criteria, "summaries": summaries, "commands": commands})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func sealedVerificationEvidence(batch criteriaBatchResult) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"criteria": batch.Criteria, "summaries": batch.Summaries, "commands": batch.Commands, "proofDigest": verificationEvidenceDigest(batch.Criteria, batch.Summaries, batch.Commands)})
	return raw
}
