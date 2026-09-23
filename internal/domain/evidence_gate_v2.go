package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

const CurrentWorkOrderEvidenceVersion = 3

const (
	WorkOrderAssuranceVerified = "verified"
	WorkOrderAssurancePartial  = "partial"
	WorkOrderAssuranceFailed   = "failed"
)

func WorkOrderSourceDigest(order WorkOrder) string {
	if len(order.Sources) == 0 {
		return "sha256:none"
	}
	refs := make([]struct {
		ID     string `json:"id"`
		Digest string `json:"digest"`
	}, 0, len(order.Sources))
	for _, source := range order.Sources {
		refs = append(refs, struct {
			ID     string `json:"id"`
			Digest string `json:"digest"`
		}{ID: source.ID, Digest: source.Digest})
	}
	raw, _ := json.Marshal(refs)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// GateVerdict — исход шлюза с названной причиной. Прежде шлюз отвечал
// `(QuestBlocked, nil)` в пяти местах: отказ без объяснения. Живая приёмка от
// этого показывала «0/2» и молчала о том, какое из условий не сошлось, а
// разбор приходилось начинать с воспроизведения вслепую.
//
// Err — структурное расхождение: доказательство недостоверно, и транзакция
// шлюза откатывается. Reason и Missing — честный провал: bundle сохраняется,
// потому что он и есть объяснение, почему работа не принята.
type GateVerdict struct {
	Status    QuestStatus
	Assurance string
	Err       error
	Reason    string
	Missing   []string
}

func gateRejected(err error) GateVerdict {
	return GateVerdict{Status: QuestBlocked, Assurance: WorkOrderAssuranceFailed, Err: err, Reason: err.Error()}
}

func gateFailed(reason string, missing ...string) GateVerdict {
	return GateVerdict{Status: QuestBlocked, Assurance: WorkOrderAssuranceFailed, Reason: reason, Missing: missing}
}

func gatePartial(reason string, missing ...string) GateVerdict {
	return GateVerdict{Status: QuestCompleted, Assurance: WorkOrderAssurancePartial, Reason: reason, Missing: missing}
}

// WorkOrderEvidenceStatus is the only semantic path from verification to a
// terminal v2 quest state. Structural mismatches are rejected as untrusted
// evidence; genuine failed checks are retained and produce blocked.
//
// Причину отказа даёт WorkOrderEvidenceVerdict — эта пара сохранена для
// вызывающих, которым довольно статуса.
func WorkOrderEvidenceStatus(order WorkOrder, bundle EvidenceBundle) (QuestStatus, error) {
	verdict := WorkOrderEvidenceVerdict(order, bundle)
	return verdict.Status, verdict.Err
}

// WorkOrderEvidenceVerdict — тот же шлюз, что и прежде, слово в слово по
// правилам; отличается он только тем, что называет условие, на котором
// остановился.
func WorkOrderEvidenceVerdict(order WorkOrder, bundle EvidenceBundle) GateVerdict {
	if strings.TrimSpace(bundle.ID) == "" || strings.TrimSpace(bundle.QuestID) == "" {
		return gateRejected(errors.New("evidence id and quest id are required"))
	}
	if bundle.Version != CurrentWorkOrderEvidenceVersion || strings.TrimSpace(bundle.PointVersion) == "" {
		return gateRejected(fmt.Errorf("completed requires evidence bundle version %d with Point version", CurrentWorkOrderEvidenceVersion))
	}
	if bundle.BriefDigest != WorkOrderDigest(order) {
		return gateRejected(errors.New("evidence is bound to a different work order digest"))
	}
	if bundle.SourceDigest != WorkOrderSourceDigest(order) {
		return gateRejected(errors.New("evidence is bound to different source snapshots"))
	}
	if strings.TrimSpace(bundle.EnvironmentDigest) == "" {
		return gateRejected(errors.New("environment evidence is required"))
	}
	if strings.TrimSpace(bundle.StackPreset.ID) == "" || strings.TrimSpace(bundle.StackPreset.Version) == "" {
		return gateRejected(errors.New("versioned stack preset evidence is required"))
	}
	if len(bundle.ModelCalls) == 0 {
		return gateFailed("model call ledger is empty", "model_calls")
	}
	if err := validateModelCallLedger(bundle.ModelCalls); err != nil {
		return gateRejected(err)
	}
	if err := validateContextDisclosureLedger(bundle.ContextDisclosures); err != nil {
		return gateRejected(err)
	}
	if len(bundle.SourceVersions) != len(order.Sources) {
		return gateRejected(errors.New("source version ledger does not match the approved work order"))
	}
	for index := range order.Sources {
		if bundle.SourceVersions[index].ID != order.Sources[index].ID || bundle.SourceVersions[index].Digest != order.Sources[index].Digest {
			return gateRejected(errors.New("source version ledger contains a different snapshot"))
		}
	}
	byID := make(map[string]CriterionEvidence, len(bundle.Criteria))
	for _, item := range bundle.Criteria {
		if item.CriterionID == "" {
			return gateRejected(errors.New("criterion evidence id is required"))
		}
		if _, duplicate := byID[item.CriterionID]; duplicate {
			return gateRejected(fmt.Errorf("duplicate evidence for criterion %q", item.CriterionID))
		}
		byID[item.CriterionID] = item
	}
	checksByID := make(map[string]VerificationCheck, len(bundle.VerificationChecks))
	for _, check := range bundle.VerificationChecks {
		if strings.TrimSpace(check.ID) == "" || strings.TrimSpace(check.Kind) == "" {
			return gateRejected(errors.New("verification checks require id and kind"))
		}
		if _, duplicate := checksByID[check.ID]; duplicate {
			return gateRejected(fmt.Errorf("duplicate verification check %q", check.ID))
		}
		checksByID[check.ID] = check
	}
	// Manual delivery is itself a human gate even when every criterion is
	// machine-verifiable. A professional-mode quest must never become completed
	// before the user accepts the isolated result.
	hasManual := order.Delivery.ApplyMode == "manual"
	hasPartial := false
	actualFailure := false
	allMachineSatisfied := true
	// Незакрытые условия собираются поимённо, а не сводятся к одному флагу:
	// «работа не принята» и «не принята вот по этим двум критериям» стоят
	// человеку разного времени.
	var missing []string
	for _, criterion := range order.Criteria {
		item, ok := byID[criterion.ID]
		if !ok {
			allMachineSatisfied = false
			missing = append(missing, "criterion:"+criterion.ID+" (нет доказательства)")
			continue
		}
		if criterion.Kind == "manual" {
			hasPartial = true
			missing = append(missing, "criterion:"+criterion.ID+" (manual)")
			continue
		}
		check, checkOK := checksByID[criterion.ID]
		if !item.Satisfied || !checkOK || !verificationCheckSatisfiesCriterion(criterion, item, check) {
			allMachineSatisfied = false
			missing = append(missing, "criterion:"+criterion.ID)
			if item.ExitCode != nil || checkOK && check.ExitCode != nil {
				actualFailure = true
			} else {
				hasPartial = true
			}
		}
	}
	if len(byID) != len(order.Criteria) {
		return gateRejected(errors.New("evidence contains unknown or missing criteria"))
	}
	if bundle.DeliveryConflict {
		return gateFailed("workspace changed outside the approved delivery; transfer rolled back whole", "delivery_conflict")
	}
	// A profile entry nobody executed is a promise, not proof. This only
	// judges a bundle that claims a delivered result: a quest that never got
	// that far is blocked below anyway, and erroring here would throw away the
	// evidence of why it failed. Manual delivery is a human gate that cannot
	// reach completed, so there the profile belongs to the reviewer.
	if !hasManual && bundle.DeliveryVerified {
		for _, required := range order.Completion.Checks {
			if required.Kind == CompletionCheckAcceptance {
				continue
			}
			check, ok := checksByID[CompletionCheckEvidenceID(required.Kind)]
			if !ok {
				hasPartial = true
				allMachineSatisfied = false
				missing = append(missing, "completion:"+required.Kind+" (unavailable)")
				continue
			}
			if strings.TrimSpace(check.Command) != strings.TrimSpace(required.Command) {
				return gateRejected(fmt.Errorf("completion profile check %q was executed with a different command", required.Kind))
			}
			if !check.Satisfied || check.ExitCode == nil || *check.ExitCode != required.ExpectedExitCode {
				allMachineSatisfied = false
				missing = append(missing, "completion:"+required.Kind)
				if check.ExitCode == nil {
					hasPartial = true
				} else {
					actualFailure = true
				}
			}
		}
	}
	if actualFailure || !bundle.DeliveryVerified || strings.TrimSpace(bundle.WorkspaceRevision) == "" {
		if !bundle.DeliveryVerified {
			missing = append(missing, "delivery_verified")
		}
		if strings.TrimSpace(bundle.WorkspaceRevision) == "" {
			missing = append(missing, "workspace_revision")
		}
		return gateFailed("work is not proven: "+strings.Join(missing, ", "), missing...)
	}
	receipt := bundle.DeliveryReceipt
	if receipt == nil || strings.TrimSpace(receipt.ID) == "" || receipt.QuestID != bundle.QuestID ||
		receipt.WorkOrderDigest != bundle.BriefDigest || receipt.WorkspaceRevision != bundle.WorkspaceRevision ||
		strings.TrimSpace(receipt.Target) == "" || receipt.DeliveredAt.IsZero() {
		return gateRejected(errors.New("delivery receipt is missing or does not match verified evidence"))
	}
	if order.Delivery.CommitMode == "squash" && strings.TrimSpace(receipt.CommitID) == "" {
		return gateRejected(errors.New("squash delivery requires a commit receipt"))
	}
	// A promised application that is not actually running is a failed delivery,
	// not untrusted evidence. Erroring here would roll back the bundle and take
	// the failed service check — the only explanation the user has — with it.
	if order.Delivery.KeepServicesRunning && (receipt.URL != order.Delivery.ApplicationURL || strings.TrimSpace(receipt.ComposeFile) == "" || !receipt.ServicesRunning) {
		serviceCheck, serviceCheckFound := checksByID[CompletionCheckEvidenceID("service_start")]
		if !serviceCheckFound || serviceCheck.ExitCode == nil {
			hasPartial = true
			missing = append(missing, "services_running (unavailable)")
		} else {
			switch {
			case !receipt.ServicesRunning:
				return gateFailed("promised application is not running", "services_running")
			case strings.TrimSpace(receipt.ComposeFile) == "":
				return gateFailed("promised application has no compose file", "compose_file")
			default:
				return gateFailed("delivered application URL does not match the approved one", "application_url")
			}
		}
	}
	if hasManual {
		return GateVerdict{Status: QuestNeedsReview, Reason: "manual delivery or a manual criterion: a human accepts the result"}
	}
	if hasPartial || !allMachineSatisfied {
		return gatePartial("work completed with checks that require manual review or were unavailable", missing...)
	}
	return GateVerdict{Status: QuestCompleted, Assurance: WorkOrderAssuranceVerified}
}

func validateModelCallLedger(items []ModelCallLedgerEntry) error {
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Provider) == "" || strings.TrimSpace(item.Model) == "" || item.CreatedAt.IsZero() {
			return errors.New("model call ledger entries require id, provider, model and timestamp")
		}
		if seen[item.ID] {
			return fmt.Errorf("duplicate model call ledger entry %q", item.ID)
		}
		seen[item.ID] = true
		if item.InputTokens < 0 || item.OutputTokens < 0 || item.CostCents < 0 || item.ActiveMillis < 0 {
			return fmt.Errorf("model call ledger entry %q contains negative usage", item.ID)
		}
	}
	return nil
}

func validateContextDisclosureLedger(items []ContextDisclosureEntry) error {
	for _, item := range items {
		if strings.TrimSpace(item.Model) == "" || strings.TrimSpace(item.Category) == "" || strings.TrimSpace(item.Digest) == "" || item.SentAt.IsZero() || item.Bytes < 0 {
			return errors.New("context disclosure entries require model, category, digest, byte count and timestamp")
		}
		base := strings.ToLower(filepath.Base(item.Path))
		if base == ".env" || strings.HasPrefix(base, ".env.") || base == ".npmrc" || base == ".pypirc" || base == "credentials" ||
			strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") || strings.HasSuffix(base, ".p12") || strings.HasSuffix(base, ".pfx") || strings.HasSuffix(base, ".kdbx") ||
			strings.Contains(base, "id_rsa") || strings.Contains(base, "id_ed25519") {
			return errors.New("sensitive files are forbidden in context disclosure ledger")
		}
	}
	return nil
}

func verificationCheckSatisfiesCriterion(criterion AcceptanceCriterion, item CriterionEvidence, check VerificationCheck) bool {
	if !check.Satisfied || check.ExitCode == nil || item.ExitCode == nil ||
		strings.TrimSpace(check.Command) == "" || strings.TrimSpace(item.Command) == "" ||
		strings.TrimSpace(check.Command) != strings.TrimSpace(item.Command) {
		return false
	}
	expected := 0
	if criterion.ExpectedExitCode != nil {
		expected = *criterion.ExpectedExitCode
	}
	return *check.ExitCode == expected && *item.ExitCode == expected
}
