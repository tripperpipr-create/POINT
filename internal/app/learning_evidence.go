package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/diagnostics"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
)

const (
	learningEvidenceErrorRunes  = 240
	learningEvidenceMaxSteps    = 80
	learningEvidenceMaxPerAgent = 200
	learningEvidencePackDirName = "learning_evidence"
	learningTriggerFailure      = "failed_or_regressed_run"
	learningMinFailureToolCalls = 5
)

// learningToolStep is a bounded, redacted tool outcome for the reviewer.
// It never carries file bodies, full stderr dumps, or final answers.
type learningToolStep struct {
	Tool       string `json:"tool"`
	OK         bool   `json:"ok"`
	ErrorClass string `json:"errorClass,omitempty"`
	ErrorHint  string `json:"errorHint,omitempty"`
}

type learningEvidencePack struct {
	SchemaVersion             int                `json:"schemaVersion"`
	RunID                     string             `json:"runId"`
	WorkspaceID               string             `json:"workspaceId"`
	ProjectAgentID            string             `json:"projectAgentId"`
	QuestID                   string             `json:"questId,omitempty"`
	ExecutionID               string             `json:"executionId,omitempty"`
	RunStatus                 domain.RunStatus   `json:"runStatus"`
	Health                    string             `json:"health"`
	StopReason                string             `json:"stopReason,omitempty"`
	ToolCalls                 int                `json:"toolCalls"`
	ToolFailures              int                `json:"toolFailures"`
	Steps                     []learningToolStep `json:"steps"`
	ToolSequence              []string           `json:"toolSequence"`
	VerificationRequired      bool               `json:"verificationRequired"`
	VerificationRecorded      bool               `json:"verificationRecorded"`
	VerificationGap           string             `json:"verificationGap,omitempty"`
	ApprovalsDenied           int                `json:"approvalsDenied"`
	CompletionRevisions       int                `json:"completionRevisions"`
	CompletionRejected        bool               `json:"completionRejected"`
	ChangedFileCount          int                `json:"changedFileCount"`
	Feedback                  []string           `json:"feedback,omitempty"`
	FeedbackFollowedByHealthy bool               `json:"feedbackFollowedByHealthy,omitempty"`
	CreatedAt                 time.Time          `json:"createdAt"`
}

func classifyLearningToolError(result domain.ToolResult) string {
	if result.OK {
		return ""
	}
	code := ""
	message := ""
	if result.Error != nil {
		code = strings.ToLower(strings.TrimSpace(result.Error.Code))
		message = strings.ToLower(strings.TrimSpace(result.Error.Message))
	}
	combined := code + " " + message
	switch {
	case strings.Contains(combined, "timeout") || strings.Contains(combined, "timed_out") || strings.Contains(combined, "deadline"):
		return "timeout"
	case strings.Contains(combined, "denied") || strings.Contains(combined, "approval") || strings.Contains(combined, "forbidden") || strings.Contains(combined, "permission"):
		return "denied"
	case strings.Contains(combined, "verif") || strings.Contains(combined, "assert") || strings.Contains(combined, "test_fail"):
		return "verify_fail"
	case strings.Contains(combined, "not_found") || strings.Contains(combined, "missing"):
		return "not_found"
	default:
		if code != "" {
			return truncateRunes(code, 40)
		}
		return "tool_error"
	}
}

func (a *App) learningTrajectory(ctx context.Context, run domain.Run, report diagnostics.RunDiagnostics) (learningTrajectory, error) {
	events, err := a.store.ListByRun(ctx, run.ID)
	if err != nil {
		return learningTrajectory{}, err
	}
	pack := buildLearningEvidencePack(run, report, events, "")
	if questID := a.learningQuestIDFromEvents(events); questID != "" {
		pack.QuestID = questID
	}
	_ = a.persistLearningEvidencePack(pack, run.ProfileID)
	return trajectoryFromEvidencePack(pack), nil
}

func (a *App) learningQuestIDFromEvents(events []domain.Event) string {
	for _, event := range events {
		if questID := strings.TrimSpace(event.QuestID); questID != "" {
			return questID
		}
	}
	return ""
}

func buildLearningEvidencePack(run domain.Run, report diagnostics.RunDiagnostics, events []domain.Event, projectAgentID string) learningEvidencePack {
	if projectAgentID == "" {
		projectAgentID = run.ProfileID
	}
	seen := map[string]bool{}
	ordered := make([]string, 0)
	steps := make([]learningToolStep, 0, 16)
	toolCalls := 0
	toolFailures := 0
	executionID := ""
	for _, event := range events {
		if event.Type != domain.EventToolFinished {
			continue
		}
		var payload struct {
			Tool   string            `json:"tool"`
			Result domain.ToolResult `json:"result"`
		}
		if json.Unmarshal(event.Data, &payload) != nil || strings.TrimSpace(payload.Tool) == "" {
			continue
		}
		toolCalls++
		if executionID == "" {
			executionID = event.ExecutionID
		}
		if !payload.Result.OK {
			toolFailures++
		}
		if !seen[payload.Tool] {
			seen[payload.Tool] = true
			ordered = append(ordered, payload.Tool)
		}
		if len(steps) < learningEvidenceMaxSteps {
			hint := ""
			if payload.Result.Error != nil {
				hint = truncateRunes(security.Redact(payload.Result.Error.Message), learningEvidenceErrorRunes)
				if payload.Result.Error.Hint != "" && hint == "" {
					hint = truncateRunes(security.Redact(payload.Result.Error.Hint), learningEvidenceErrorRunes)
				}
				hint = stripPathLikeFragments(hint)
			}
			steps = append(steps, learningToolStep{
				Tool: payload.Tool, OK: payload.Result.OK,
				ErrorClass: classifyLearningToolError(payload.Result), ErrorHint: hint,
			})
		}
	}
	feedback := learningConsentedFeedback(events)
	gap := ""
	if report.Verification.Required && !report.Verification.Recorded {
		gap = "required_verification_missing"
	}
	return learningEvidencePack{
		SchemaVersion: 1, RunID: run.ID, WorkspaceID: run.WorkspaceID, ProjectAgentID: projectAgentID,
		ExecutionID: executionID, RunStatus: run.Status, Health: string(report.Health), StopReason: report.StopReason,
		ToolCalls: toolCalls, ToolFailures: toolFailures, Steps: steps, ToolSequence: ordered,
		VerificationRequired: report.Verification.Required, VerificationRecorded: report.Verification.Recorded,
		VerificationGap: gap, ApprovalsDenied: report.Approvals.Denied,
		CompletionRevisions: report.Completion.RevisionRequests, CompletionRejected: report.Completion.Rejected,
		ChangedFileCount: len(run.ChangedFiles), Feedback: feedback,
		FeedbackFollowedByHealthy: len(feedback) > 0 && report.Health == diagnostics.HealthHealthy,
		CreatedAt:                 time.Now().UTC(),
	}
}

func trajectoryFromEvidencePack(pack learningEvidencePack) learningTrajectory {
	evidence := []string{fmt.Sprintf("%d завершённых tool-вызовов", pack.ToolCalls)}
	if pack.ToolFailures > 0 {
		evidence = append(evidence, fmt.Sprintf("неуспешных tool-вызовов: %d", pack.ToolFailures))
	}
	if pack.VerificationRequired {
		if pack.VerificationRecorded {
			evidence = append(evidence, "верификация записана")
		} else {
			evidence = append(evidence, "пробел верификации: "+pack.VerificationGap)
		}
	}
	if pack.ApprovalsDenied > 0 {
		evidence = append(evidence, fmt.Sprintf("отклонено approvals: %d", pack.ApprovalsDenied))
	}
	if pack.CompletionRevisions > 0 {
		evidence = append(evidence, fmt.Sprintf("эпизодов исправления completion: %d", pack.CompletionRevisions))
	}
	if pack.ChangedFileCount > 0 {
		evidence = append(evidence, fmt.Sprintf("изменено файлов: %d", pack.ChangedFileCount))
	}
	if len(pack.Feedback) > 0 {
		evidence = append(evidence, fmt.Sprintf("явно разрешённых исправлений пользователя: %d", len(pack.Feedback)))
	}
	return learningTrajectory{
		Tools: pack.ToolSequence, ToolCalls: pack.ToolCalls, ExecutionID: pack.ExecutionID,
		Evidence: evidence, Feedback: pack.Feedback, Steps: pack.Steps,
		VerificationRequired: pack.VerificationRequired, VerificationRecorded: pack.VerificationRecorded,
		VerificationGap: pack.VerificationGap, ApprovalsDenied: pack.ApprovalsDenied,
		CompletionRevisions: pack.CompletionRevisions, CompletionRejected: pack.CompletionRejected,
		ChangedFileCount: pack.ChangedFileCount, Health: pack.Health, RunStatus: pack.RunStatus,
		StopReason: pack.StopReason, QuestID: pack.QuestID,
	}
}

func (a *App) persistLearningEvidencePack(pack learningEvidencePack, projectAgentID string) error {
	if a == nil || strings.TrimSpace(a.dataDir) == "" || strings.TrimSpace(pack.RunID) == "" {
		return nil
	}
	agentID := strings.TrimSpace(projectAgentID)
	if agentID == "" {
		agentID = strings.TrimSpace(pack.ProjectAgentID)
	}
	if agentID == "" {
		agentID = "unknown-agent"
	}
	dir := filepath.Join(a.dataDir, learningEvidencePackDirName, sanitizeEvidencePath(agentID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(pack, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, sanitizeEvidencePath(pack.RunID)+".json")
	if err = os.WriteFile(path, payload, 0o600); err != nil {
		return err
	}
	return rotateLearningEvidencePacks(dir, learningEvidenceMaxPerAgent)
}

func sanitizeEvidencePath(value string) string {
	value = strings.TrimSpace(value)
	replacer := strings.NewReplacer(`/`, "_", `\`, "_", `:`, "_", `..`, "_")
	return replacer.Replace(value)
}

func rotateLearningEvidencePacks(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	type dated struct {
		name    string
		modTime time.Time
	}
	files := make([]dated, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		files = append(files, dated{name: entry.Name(), modTime: info.ModTime()})
	}
	if len(files) <= keep {
		return nil
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.After(files[j].modTime) })
	for _, item := range files[keep:] {
		_ = os.Remove(filepath.Join(dir, item.name))
	}
	return nil
}

func stripPathLikeFragments(content string) string {
	lower := strings.ToLower(content)
	for _, fragment := range []string{`c:\`, `d:\`, `/users/`, `/home/`, `/workspace/`, `:\users\`} {
		if idx := strings.Index(lower, fragment); idx >= 0 {
			return truncateRunes(strings.TrimSpace(content[:idx])+" [path-redacted]", learningEvidenceErrorRunes)
		}
	}
	return content
}

func learningEvidenceForModel(pack learningEvidencePack, trigger string) map[string]any {
	steps := make([]map[string]any, 0, len(pack.Steps))
	for _, step := range pack.Steps {
		item := map[string]any{"tool": step.Tool, "ok": step.OK}
		if step.ErrorClass != "" {
			item["errorClass"] = step.ErrorClass
		}
		if step.ErrorHint != "" {
			item["errorHint"] = step.ErrorHint
		}
		steps = append(steps, item)
	}
	contextValue := map[string]any{
		"toolSequence":         pack.ToolSequence,
		"toolCalls":            pack.ToolCalls,
		"toolFailures":         pack.ToolFailures,
		"steps":                steps,
		"changedFileCount":     pack.ChangedFileCount,
		"verificationRequired": pack.VerificationRequired,
		"verificationRecorded": pack.VerificationRecorded,
		"approvalsDenied":      pack.ApprovalsDenied,
		"completionRevisions":  pack.CompletionRevisions,
		"health":               pack.Health,
		"runStatus":            pack.RunStatus,
		"trigger":              trigger,
	}
	if pack.VerificationGap != "" {
		contextValue["verificationGap"] = pack.VerificationGap
	}
	if pack.StopReason != "" {
		contextValue["stopReason"] = pack.StopReason
	}
	if trigger == learningTriggerFeedback || trigger == learningTriggerFailure {
		if len(pack.Feedback) > 0 {
			contextValue["userCorrections"] = pack.Feedback
			contextValue["feedbackFollowedByHealthy"] = pack.FeedbackFollowedByHealthy
		}
	}
	return contextValue
}
