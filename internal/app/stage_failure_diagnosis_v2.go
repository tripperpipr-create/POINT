package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"local-agent-workbench/internal/diagnostics"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
)

// Диагноз проваленного этапа — то, что видят человек и Мастер.
//
// Прежде сообщение о провале было склейкой сводок всех проверок, и в ней
// «x Build failed in 17.43s» стояло рядом с «lock-consistency: ok». Диагноз
// называет проваленные проверки по одной — команда, причина, подсказка,
// класс — и общий класс, по которому политика повтора решает, может ли Point
// повторить этап сам.

// StageFailureCheck — одна проваленная проверка.
type StageFailureCheck struct {
	CriterionID string `json:"criterionId,omitempty"`
	Command     string `json:"command,omitempty"`
	ExitCode    int    `json:"exitCode"`
	Cause       string `json:"cause"`
	Hint        string `json:"hint,omitempty"`
	Class       string `json:"class"`
	// Baseline — исход той же проверки на нетронутом дереве (Q11): «failed»
	// значит, что она падала и до правок этапа.
	Baseline string `json:"baseline,omitempty"`
}

// StageFailureDiagnosis — итог по этапу.
type StageFailureDiagnosis struct {
	Class  string              `json:"class"`
	Image  string              `json:"image,omitempty"`
	Passed int                 `json:"passed"`
	Total  int                 `json:"total"`
	Checks []StageFailureCheck `json:"checks"`
}

// failureClassRank — чем выше, тем больше нужно от человека. Общий класс этапа
// — самый требовательный из классов его проверок.
var failureClassRank = map[string]int{
	diagnostics.FailureTransient: 1,
	diagnostics.FailureRuntime:   2,
	diagnostics.FailureCriterion: 3,
	diagnostics.FailureCode:      4,
	diagnostics.FailureHuman:     5,
}

func (d *StageFailureDiagnosis) add(check StageFailureCheck) {
	if check.Class == "" {
		check.Class = diagnostics.FailureCode
	}
	d.Checks = append(d.Checks, check)
	if failureClassRank[check.Class] > failureClassRank[d.Class] {
		d.Class = check.Class
	}
}

// Summary — одна фраза для статуса квеста.
func (d StageFailureDiagnosis) Summary(nodeName string) string {
	if len(d.Checks) == 0 {
		return fmt.Sprintf("Этап «%s» не выполнен", nodeName)
	}
	parts := make([]string, 0, len(d.Checks))
	for _, check := range d.Checks {
		part := check.Cause
		if check.CriterionID != "" {
			part = check.CriterionID + " — " + check.Cause
		}
		parts = append(parts, part)
	}
	if d.Total == 0 {
		return fmt.Sprintf("Этап «%s» не выполнен: %s", nodeName, strings.Join(parts, "; "))
	}
	return fmt.Sprintf("Этап «%s»: не прошли %d из %d проверок. %s", nodeName, len(d.Checks), d.Total, strings.Join(parts, "; "))
}

// stageFailureDiagnosisV2 собирает диагноз проваленного узла: из доказательства
// приёмки, если этап проверял критерии, иначе из ошибки исполнения.
func (a *App) stageFailureDiagnosisV2(ctx context.Context, run domain.FlowRun, nodeID, failure string) StageFailureDiagnosis {
	diagnosis := StageFailureDiagnosis{}
	state := run.NodeStates[nodeID]
	executionID, _ := state.Output["executionId"].(string)
	var execution domain.ExecutionInstance
	if executionID != "" {
		if loaded, err := a.store.GetExecution(ctx, executionID); err == nil {
			execution = loaded
			if record, sandboxErr := a.store.GetSandbox(ctx, execution.SandboxID); sandboxErr == nil {
				diagnosis.Image = strings.TrimSpace(record.BackendImage)
			}
		}
	}
	if execution.RunID != "" {
		if events, err := a.store.ListByRun(ctx, execution.RunID); err == nil && diagnosis.fromEvidence(events) {
			diagnosis.markBaseline(a.baselineCriterionStatuses(ctx, run.ID))
			return diagnosis
		}
	}
	text := strings.TrimSpace(execution.Error)
	if text == "" {
		text = strings.TrimSpace(state.Error)
	}
	if text == "" {
		text = failure
	}
	message := diagnostics.DiagnoseMessage(security.Redact(text))
	diagnosis.add(StageFailureCheck{Cause: message.Cause, Hint: message.Hint, Class: message.Class})
	return diagnosis
}

// fromEvidence читает последнюю проверку этапа. Проваленные критерии дают
// по записи с причиной, которую run_command уже разобрал.
func (d *StageFailureDiagnosis) fromEvidence(events []domain.Event) bool {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != domain.EventCompletionChecked {
			continue
		}
		var payload struct {
			Evidence struct {
				Criteria []struct {
					CriterionID string `json:"criterionId"`
					Kind        string `json:"kind"`
					Status      string `json:"status"`
					Check       *struct {
						Arguments json.RawMessage `json:"arguments"`
						ExitCode  *int            `json:"exitCode"`
						Detail    string          `json:"detail"`
					} `json:"check"`
				} `json:"criteria"`
			} `json:"evidence"`
		}
		if json.Unmarshal(events[i].Data, &payload) != nil || len(payload.Evidence.Criteria) == 0 {
			return false
		}
		for _, criterion := range payload.Evidence.Criteria {
			if criterion.Kind == "manual" {
				continue
			}
			d.Total++
			if criterion.Status != "failed" {
				d.Passed++
				continue
			}
			check := StageFailureCheck{CriterionID: criterion.CriterionID}
			if criterion.Check != nil {
				var arguments struct {
					Command string `json:"command"`
				}
				_ = json.Unmarshal(criterion.Check.Arguments, &arguments)
				check.Command = security.Redact(arguments.Command)
				if criterion.Check.ExitCode != nil {
					check.ExitCode = *criterion.Check.ExitCode
				}
				result := domain.ToolResult{OK: true, Output: json.RawMessage(criterion.Check.Detail)}
				if !json.Valid(result.Output) {
					result = domain.ToolResult{Error: &domain.ToolError{Message: criterion.Check.Detail}}
				}
				if failure, ok := acceptCheckFailure(result); ok {
					check.Cause, check.Hint, check.Class = security.Redact(failure.Cause), failure.Hint, failure.Class
				}
			}
			if check.Cause == "" {
				check.Cause = fmt.Sprintf("код %d", check.ExitCode)
			}
			d.add(check)
		}
		return len(d.Checks) > 0
	}
	return false
}

// markBaseline отделяет «падало и до правки» от регрессии кода (Q11). Такая
// проверка — дефект исходного проекта или самой проверки: повтор этапа той же
// стратегией её не починит, а политика повтора без человека её не повторит.
func (d *StageFailureDiagnosis) markBaseline(statuses map[string]string) {
	if len(statuses) == 0 {
		return
	}
	d.Class = ""
	for index := range d.Checks {
		check := &d.Checks[index]
		switch statuses[check.CriterionID] {
		case "failed":
			check.Baseline = "failed"
			check.Class = diagnostics.FailureCriterion
			check.Hint = strings.TrimSpace("проверка падала и до правок этапа — дефект исходного проекта или самой проверки, а не кода этапа. " + check.Hint)
		case "satisfied":
			check.Baseline = "passed"
		}
		if failureClassRank[check.Class] > failureClassRank[d.Class] {
			d.Class = check.Class
		}
	}
}
