package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/diagnostics"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
)

// recordRunLearningEvidence turns terminal Run artifacts into immutable,
// queryable learning evidence. This path never changes Skills, memory, rules,
// tools or permissions; guarded reviewers consume the records separately.
func (a *App) recordRunLearningEvidence(ctx context.Context, run domain.Run, projectAgentID string) error {
	if !terminalLearningStatus(run.Status) || strings.TrimSpace(run.ID) == "" || strings.TrimSpace(projectAgentID) == "" {
		return nil
	}
	agent, err := a.store.GetProjectAgent(ctx, projectAgentID)
	if err != nil {
		return err
	}
	if agent.WorkspaceID != run.WorkspaceID {
		return fmt.Errorf("learning evidence run and project agent belong to different workspaces")
	}
	report, err := a.runDiagnostics(ctx, run)
	if err != nil {
		return err
	}
	events, err := a.store.ListByRun(ctx, run.ID)
	if err != nil {
		return err
	}
	feedback := learningFeedback(events)
	attributions := append([]domain.SkillAttribution(nil), run.ConfigurationSnapshot.SkillAttributions...)
	if len(attributions) == 0 {
		// Schema v1 snapshots predate persisted attributions. Reconstruct them
		// only for legacy Runs; schema v2 evidence uses the immutable captured IDs.
		attributions = skillAttributions(run.ConfigurationSnapshot.Profile.EquippedSkills)
	}
	now := time.Now().UTC()
	signals := learningSignals(run, report, feedback, attributions, agent, now)
	for _, signal := range signals {
		if err = a.store.SaveLearningSignal(ctx, signal); err != nil {
			return err
		}
	}
	evaluatedSkills := map[string]bool{}
	for _, attribution := range attributions {
		outcome := domain.SkillOutcome{
			ID:          learningRecordID("skilloutcome", run.ID, attribution.SkillID),
			WorkspaceID: run.WorkspaceID, ProjectAgentID: projectAgentID, BlueprintID: agent.BlueprintID,
			RunID: run.ID, SkillID: attribution.SkillID, SkillName: attribution.Name,
			SkillRevision: attribution.Revision, SkillDigest: attribution.Digest,
			PromotionStatus: attribution.PromotionStatus, RunStatus: run.Status, Health: string(report.Health),
			ToolCalls: report.Tools.Calls, ToolFailures: report.Tools.Failed,
			ApprovalDenied: report.Approvals.Denied, FeedbackCount: len(feedback),
			CompletionRevisions:  report.Completion.RevisionRequests,
			VerificationRequired: report.Verification.Required,
			VerificationRecorded: report.Verification.Recorded, CreatedAt: now,
		}
		if err = a.store.SaveSkillOutcome(ctx, outcome); err != nil {
			return err
		}
		evaluatedSkills[attribution.SkillID] = true
	}
	// One wedged canary must not starve the others: every attributed Skill is
	// evaluated and all failures are reported together.
	var canaryErrors []error
	for skillID := range evaluatedSkills {
		if err = a.evaluateAppliedSkillCanary(ctx, skillID); err != nil {
			canaryErrors = append(canaryErrors, fmt.Errorf("evaluate Skill canary %q: %w", skillID, err))
		}
	}
	return errors.Join(canaryErrors...)
}

func terminalLearningStatus(status domain.RunStatus) bool {
	switch status {
	case domain.RunCompleted, domain.RunFailed, domain.RunCancelled, domain.RunInterrupted:
		return true
	default:
		return false
	}
}

func skillAttributions(skills []domain.SkillRuntime) []domain.SkillAttribution {
	result := make([]domain.SkillAttribution, 0, len(skills))
	for _, skill := range skills {
		if strings.TrimSpace(skill.ID) == "" {
			continue
		}
		result = append(result, domain.SkillRuntimeAttribution(skill))
	}
	return result
}

type runLearningFeedback struct {
	Content   string
	Consented bool
}

func learningFeedbackEvents(events []domain.Event) []runLearningFeedback {
	items := make([]runLearningFeedback, 0)
	for _, event := range events {
		if event.Type != domain.EventRunMessageInjected {
			continue
		}
		var payload struct {
			Content        string `json:"content"`
			LearningIntent string `json:"learningIntent"`
		}
		if json.Unmarshal(event.Data, &payload) != nil {
			continue
		}
		content := truncateRunes(strings.TrimSpace(security.Redact(payload.Content)), 2000)
		if content != "" {
			items = append(items, runLearningFeedback{Content: content, Consented: strings.EqualFold(strings.TrimSpace(payload.LearningIntent), "correction")})
		}
	}
	return items
}

func learningFeedback(events []domain.Event) []string {
	records := learningFeedbackEvents(events)
	items := make([]string, 0, len(records))
	for _, record := range records {
		items = append(items, record.Content)
	}
	return items
}

func learningConsentedFeedback(events []domain.Event) []string {
	records := learningFeedbackEvents(events)
	items := make([]string, 0, len(records))
	for _, record := range records {
		if record.Consented {
			items = append(items, record.Content)
		}
	}
	return items
}

func learningSignals(run domain.Run, report diagnostics.RunDiagnostics, feedback []string, attributions []domain.SkillAttribution, agent domain.ProjectAgent, now time.Time) []domain.LearningSignal {
	items := make([]domain.LearningSignal, 0, 7)
	add := func(kind domain.LearningSignalKind, summary string, evidence ...string) {
		clean := make([]string, 0, len(evidence))
		for _, value := range evidence {
			value = truncateRunes(strings.TrimSpace(security.Redact(value)), 2000)
			if value != "" {
				clean = append(clean, value)
			}
		}
		items = append(items, domain.LearningSignal{
			ID:          learningRecordID("learningsignal", run.ID, string(kind)),
			WorkspaceID: run.WorkspaceID, ProjectAgentID: agent.ID, BlueprintID: agent.BlueprintID,
			RunID: run.ID, Kind: kind, Status: "observed", Summary: summary, Evidence: clean,
			SkillAttributions: append([]domain.SkillAttribution(nil), attributions...), CreatedAt: now, UpdatedAt: now,
		})
	}
	verified := run.Status == domain.RunCompleted && report.Health == diagnostics.HealthHealthy && report.Tools.Calls >= agentLearningMinToolCalls && (!report.Verification.Required || report.Verification.Recorded)
	if verified {
		add(domain.LearningSignalVerifiedSuccess, "Сложная траектория завершена и подтверждена",
			fmt.Sprintf("tool-вызовов: %d", report.Tools.Calls), fmt.Sprintf("успешных проверок: %d", report.Verification.SuccessfulCommands))
	}
	if run.Status == domain.RunFailed {
		add(domain.LearningSignalRunFailure, "Run завершился ошибкой", run.Error, "причина остановки: "+report.StopReason)
	}
	if len(feedback) > 0 {
		evidence := []string{fmt.Sprintf("сообщений пользователя во время Run: %d", len(feedback))}
		for index, value := range feedback {
			if index >= 3 {
				break
			}
			evidence = append(evidence, "обратная связь: "+value)
		}
		add(domain.LearningSignalUserFeedback, "Пользователь скорректировал или уточнил активный Run", evidence...)
	}
	if report.Tools.Failed > 0 {
		evidence := []string{fmt.Sprintf("неуспешных tool-вызовов: %d", report.Tools.Failed)}
		for _, metric := range report.Tools.Items {
			if metric.Failed > 0 {
				evidence = append(evidence, fmt.Sprintf("%s: %d", metric.Name, metric.Failed))
			}
		}
		add(domain.LearningSignalToolFailure, "Во время Run были неуспешные tool-вызовы", evidence...)
	}
	if report.Approvals.Denied > 0 {
		add(domain.LearningSignalApprovalDenied, "Пользователь отклонил запрошенное действие", fmt.Sprintf("отклонено approvals: %d", report.Approvals.Denied))
	}
	if report.Verification.Required && !report.Verification.Recorded {
		add(domain.LearningSignalVerificationGap, "Run не содержит требуемого доказательства проверки")
	}
	if report.Completion.RevisionRequests > 0 {
		add(domain.LearningSignalCompletionRevised, "Completion gate потребовал исправить результат", fmt.Sprintf("эпизодов исправления: %d", report.Completion.RevisionRequests))
	}
	return items
}

func learningRecordID(prefix string, parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + "-" + hex.EncodeToString(digest[:12])
}

func (a *App) markRunLearningSignals(ctx context.Context, runID, status string) error {
	if strings.TrimSpace(runID) == "" || (status != "reviewed" && status != "consumed" && status != "dismissed") {
		return nil
	}
	items, err := a.store.ListLearningSignalsForRun(ctx, runID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, item := range items {
		item.Status = status
		item.UpdatedAt = now
		if err = a.store.SaveLearningSignal(ctx, item); err != nil {
			return err
		}
	}
	return nil
}
