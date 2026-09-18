package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/osproc"
)

func (a *App) EvidenceBundle(ctx context.Context, questID string) (domain.EvidenceBundle, error) {
	return a.store.GetEvidenceBundle(ctx, questID)
}

func (a *App) IntakeEvidence(ctx context.Context, intakeID string) (domain.EvidenceBundle, error) {
	session, err := a.store.GetIntakeSession(ctx, intakeID)
	if err != nil {
		return domain.EvidenceBundle{}, err
	}
	if session.Evidence != nil {
		return *session.Evidence, nil
	}
	if session.QuestID == "" {
		return domain.EvidenceBundle{}, errors.New("intake has no evidence yet")
	}
	return a.store.GetEvidenceBundle(ctx, session.QuestID)
}

func (a *App) syncIntakeStatus(ctx context.Context, questID string, status domain.IntakeStatus, message string) {
	if questID == "" {
		return
	}
	session, err := a.store.FindIntakeSessionByQuestID(ctx, questID)
	if err != nil {
		return
	}
	if session.Status == domain.IntakeCompleted || session.Status == domain.IntakeNeedsReview {
		return
	}
	session.Status = status
	if message != "" {
		session.Error = message
	}
	session.UpdatedAt = time.Now().UTC()
	_ = a.store.SaveIntakeSession(ctx, session)
}

func (a *App) finalizeIntakeAfterQuest(questID string, success bool) {
	if questID == "" {
		return
	}
	ctx := context.Background()
	session, err := a.store.FindIntakeSessionByQuestID(ctx, questID)
	if err != nil {
		return
	}
	a.syncIntakeStatus(ctx, questID, domain.IntakeVerifying, "")
	session, err = a.store.FindIntakeSessionByQuestID(ctx, questID)
	if err != nil {
		return
	}
	outcome, outcomeErr := a.QuestOutcome(ctx, questID)
	bundle := a.buildEvidenceBundle(session, outcome)
	if saveErr := a.store.SaveEvidenceBundle(ctx, bundle); saveErr == nil {
		session.Evidence = &bundle
	}
	verificationOK := verificationCriteriaSatisfied(session.Brief, bundle)
	switch {
	case hasVerificationCriteria(session.Brief) && !verificationOK:
		session.Status = domain.IntakeBlocked
		if outcomeErr == nil && outcome.Honest != "" {
			session.Error = outcome.Honest
		} else {
			session.Error = "verification criteria were not satisfied on the integrated revision"
		}
	case onlyManualCriteria(session.Brief):
		session.Status = domain.IntakeNeedsReview
		if outcomeErr == nil && outcome.Honest != "" {
			session.Error = outcome.Honest
		}
	case !success:
		session.Status = domain.IntakeBlocked
		if outcomeErr == nil && outcome.Honest != "" {
			session.Error = outcome.Honest
		} else {
			session.Error = "quest execution failed"
		}
	case outcomeErr == nil && outcome.Verified && verificationOK:
		session.Status = domain.IntakeCompleted
		session.Error = ""
	default:
		session.Status = domain.IntakeBlocked
		if outcomeErr == nil && outcome.Honest != "" {
			session.Error = outcome.Honest
		} else {
			session.Error = "acceptance criteria were not verified"
		}
	}
	session.UpdatedAt = time.Now().UTC()
	_ = a.store.SaveIntakeSession(ctx, session)
}

func (a *App) buildEvidenceBundle(session domain.IntakeSession, outcome QuestOutcome) domain.EvidenceBundle {
	now := time.Now().UTC()
	bundle := domain.EvidenceBundle{
		ID:                domain.NewID("evidence"),
		QuestID:           session.QuestID,
		SourceDigest:      session.Source.Digest,
		EnvironmentDigest: session.Environment.Digest,
		CreatedAt:         now,
		Criteria:          []domain.CriterionEvidence{},
		KnownLimitations:  append([]string(nil), session.Blockers...),
	}
	if session.Brief != nil {
		bundle.BriefDigest = domain.TaskBriefDigest(*session.Brief)
	}
	if len(session.Environment.NetworkHosts) > 0 {
		raw, _ := json.Marshal(session.Environment.NetworkHosts)
		sum := sha256.Sum256(raw)
		bundle.NetworkPolicyDigest = hex.EncodeToString(sum[:])
	}
	for _, cmd := range session.Environment.Commands {
		line := strings.TrimSpace(strings.Join(append([]string{cmd.Program}, cmd.Arguments...), " "))
		if line != "" {
			bundle.ReproductionCommands = append(bundle.ReproductionCommands, line)
		}
	}
	if session.Brief != nil {
		states := map[string]string{}
		if outcome.Evidence != nil {
			for _, item := range outcome.Evidence.Criteria {
				states[item.CriterionID] = item.Status
			}
		}
		for i, criterion := range session.Brief.Criteria {
			ev := domain.CriterionEvidence{CriterionID: criterion.ID, Summary: criterion.Text, Tool: criterion.Tool}
			if i < len(outcome.Promises) {
				ev.Satisfied = outcome.Promises[i].Met
				ev.Summary = outcome.Promises[i].Evidence
			}
			if status, ok := states[criterion.ID]; ok {
				ev.Satisfied = status == "satisfied"
				if ev.Summary == "" {
					ev.Summary = status
				}
			}
			if criterion.Kind == "manual" && !ev.Satisfied {
				ev.Summary = "requires user review"
			}
			bundle.Criteria = append(bundle.Criteria, ev)
		}
	}
	if ws, err := a.requireWorkspace(); err == nil {
		if sets, listErr := a.store.ListChangeSets(context.Background(), ws.ID); listErr == nil {
			files := map[string]bool{}
			for _, set := range sets {
				if set.QuestID != session.QuestID {
					continue
				}
				bundle.CommitIDs = append(bundle.CommitIDs, set.ID)
				for _, item := range set.Items {
					files[item.Path] = true
				}
			}
			for path := range files {
				bundle.ChangedFiles = append(bundle.ChangedFiles, path)
			}
			sort.Strings(bundle.ChangedFiles)
		}
	}
	if outcome.Honest != "" && session.Status != domain.IntakeCompleted {
		bundle.KnownLimitations = append(bundle.KnownLimitations, outcome.Honest)
	}
	a.attachPolicyEvidence(&bundle, session)
	if rev := a.workspaceRevisionForSession(session, bundle); rev != "" {
		bundle.WorkspaceRevision = rev
	}
	return bundle
}

func (a *App) attachPolicyEvidence(bundle *domain.EvidenceBundle, session domain.IntakeSession) {
	if bundle == nil || session.WorkspaceID == "" {
		return
	}
	ctx := context.Background()
	asks, err := a.store.ListEgressAsksForWorkspace(ctx, session.WorkspaceID)
	if err != nil {
		return
	}
	for _, ask := range asks {
		if session.QuestID != "" && ask.QuestID != "" && ask.QuestID != session.QuestID {
			continue
		}
		rec := domain.EvidenceDecisionRecord{
			ID: ask.ID, Kind: string(ask.Kind), Target: ask.Target, Reason: ask.Reason,
			Status: string(ask.Status), CreatedAt: ask.CreatedAt,
		}
		switch ask.Status {
		case domain.EgressAskAllowedOnce:
			rec.Action = "allow_once"
		case domain.EgressAskAllowedQuest:
			rec.Action = "allow_quest"
		case domain.EgressAskDenied:
			rec.Action = "deny"
		case domain.EgressAskPending:
			rec.Action = "pending"
		}
		if ask.Kind == domain.EgressAskSupervision {
			bundle.SupervisionInterventions = append(bundle.SupervisionInterventions, rec)
			continue
		}
		bundle.NetworkDecisions = append(bundle.NetworkDecisions, rec)
	}
}

func hasManualCriterion(brief *domain.TaskBrief) bool {
	if brief == nil {
		return false
	}
	for _, criterion := range brief.Criteria {
		if criterion.Kind == "manual" {
			return true
		}
	}
	return false
}

func hasVerificationCriteria(brief *domain.TaskBrief) bool {
	if brief == nil {
		return false
	}
	for _, criterion := range brief.Criteria {
		if criterion.Kind != "manual" {
			return true
		}
	}
	return false
}

func onlyManualCriteria(brief *domain.TaskBrief) bool {
	return hasManualCriterion(brief) && !hasVerificationCriteria(brief)
}

func verificationCriteriaSatisfied(brief *domain.TaskBrief, bundle domain.EvidenceBundle) bool {
	if !hasVerificationCriteria(brief) {
		return true
	}
	byID := map[string]bool{}
	for _, item := range bundle.Criteria {
		byID[item.CriterionID] = item.Satisfied
	}
	for _, criterion := range brief.Criteria {
		if criterion.Kind == "manual" {
			continue
		}
		if !byID[criterion.ID] {
			return false
		}
	}
	return true
}

func (a *App) workspaceRevisionForSession(session domain.IntakeSession, bundle domain.EvidenceBundle) string {
	path := strings.TrimSpace(session.Delivery.WorkspacePath)
	if path != "" {
		if out, err := osproc.Command("git", "-C", path, "rev-parse", "HEAD").Output(); err == nil {
			if hash := strings.TrimSpace(string(out)); hash != "" {
				return hash
			}
		}
	}
	if len(bundle.CommitIDs) > 0 {
		sum := sha256.Sum256([]byte(strings.Join(bundle.CommitIDs, ",")))
		return hex.EncodeToString(sum[:])
	}
	if session.Source.Digest != "" {
		return session.Source.Digest
	}
	return session.Environment.Digest
}

func issueQuestToolLease(session domain.IntakeSession, questID string) domain.QuestToolLease {
	tools := map[string]bool{}
	for _, requirement := range session.Requirements {
		for _, name := range requirement.RequiredTools {
			tools[name] = true
		}
	}
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	ttl := 2 * time.Hour
	if session.Brief != nil && session.Brief.Budget.ActiveSeconds > 0 {
		ttl = time.Duration(session.Brief.Budget.ActiveSeconds) * time.Second
	}
	return domain.QuestToolLease{
		ID:                domain.NewID("lease"),
		QuestID:           questID,
		WorkspaceID:       session.WorkspaceID,
		EnvironmentDigest: session.Environment.Digest,
		ToolNames:         names,
		ExpiresAt:         time.Now().UTC().Add(ttl),
	}
}

func restrictProfileToLease(profile domain.AgentProfile, lease domain.QuestToolLease) domain.AgentProfile {
	if len(lease.ToolNames) == 0 {
		return profile
	}
	allowed := map[string]bool{}
	for _, name := range lease.ToolNames {
		allowed[name] = true
	}
	tools := make([]string, 0, len(profile.AllowedTools))
	for _, name := range profile.AllowedTools {
		if allowed[name] {
			tools = append(tools, name)
		}
	}
	profile.AllowedTools = tools
	return profile
}

func coverageReadyForApproval(coverage domain.CapabilityCoverage, plan domain.EnvironmentPlan) error {
	if plan.Strategy == "blocked" {
		return fmt.Errorf("environment is blocked: %s", strings.Join(plan.Blockers, "; "))
	}
	for _, item := range coverage.Items {
		if item.Ready {
			continue
		}
		for _, missing := range item.Missing {
			if strings.Contains(missing, "catalog tool") || strings.Contains(missing, "verified runtime") {
				return fmt.Errorf("capability %s is not covered: %s", item.RequirementID, strings.Join(item.Missing, ", "))
			}
		}
	}
	if !coverage.Ready {
		for _, item := range coverage.Items {
			if !item.Ready && len(item.Missing) > 0 {
				for _, missing := range item.Missing {
					if strings.Contains(missing, "catalog") || strings.Contains(missing, "runtime") {
						return fmt.Errorf("capability coverage incomplete: %s", strings.Join(item.Missing, ", "))
					}
				}
			}
		}
	}
	return nil
}
