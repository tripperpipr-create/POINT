package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/filepolicy"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/security"
)

// Исходная проверка (baseline) — критерии приёмки на нетронутом дереве
// (TODO Q11).
//
// E1/E2: одна и та же ошибка vue-demi повторялась после ручного обхода и в
// следующем квесте. Никто не знал, что сборка падала и до правки, и новая
// попытка шла прежней стратегией вслепую. Теперь, когда пишущий этап
// запускается, Point параллельно гоняет те же критерии, что приёмка, на
// неизменном снимке его песочницы — работу этапа это не задерживает. Итог
// хранится в verification_results_v2 (source=baseline). Исполнитель узнаёт,
// какие проверки не проходили до его правок. Разбор провала этапа отделяет
// «падало и до правки» — дефект проекта или самой проверки — от регрессии
// кода, и такой провал не повторяется той же стратегией без человека.

const baselineVerificationTimeout = 20 * time.Minute

// startBaselineVerificationV2 запускает исходную проверку один раз на прогон
// Flow — для пишущего этапа, за которым идёт приёмка.
func (a *App) startBaselineVerificationV2(flowRunID, nodeID, executionID string) {
	if verifyServiceMode() == verifyServiceOff || flowRunID == "" {
		return
	}
	a.staffing.start("baseline:"+flowRunID, baselineVerificationTimeout, false, func(ctx context.Context) {
		if err := a.runBaselineVerificationV2(ctx, flowRunID, nodeID, executionID); err != nil {
			slog.Warn("baseline verification skipped", "flow_run_id", flowRunID, "node_id", nodeID, "error", security.Redact(err.Error()))
		}
	})
}

func (a *App) runBaselineVerificationV2(ctx context.Context, flowRunID, nodeID, executionID string) error {
	if done, err := a.store.BaselineVerificationV2(ctx, flowRunID); err == nil && done.ID != "" {
		return nil
	}
	plan, ok := a.preAcceptPlanFor(ctx, flowRunID, nodeID)
	if !ok {
		return nil
	}
	exec, err := a.findExecution(executionID)
	if err != nil {
		return err
	}
	record, err := a.store.GetSandbox(ctx, exec.SandboxID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(record.BaselinePath) == "" {
		return fmt.Errorf("sandbox %s has no immutable baseline", record.ID)
	}
	clean, err := os.MkdirTemp(filepath.Dir(record.Path), "baseline-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(clean)
	if record.FileRulesVersion == filepolicy.Current {
		err = sandbox.CopyPortable(ctx, record.BaselinePath, clean, record.FileRulesVersion)
	} else {
		err = sandbox.CopyCarried(record.BaselinePath, clean)
	}
	if err != nil {
		return err
	}
	tree, err := sandbox.TreeDigestWithRules(clean, record.FileRulesVersion)
	if err != nil {
		return err
	}
	input := criteriaBatchInput{
		Phase: "baseline", Context: ctx, FlowRunID: flowRunID, FlowNodeID: nodeID,
		QuestID: plan.flowRun.QuestID, RunID: exec.RunID, Criteria: plan.criteria, Sandbox: record, Root: clean,
		NetworkPolicy: plan.policy, NetworkHosts: plan.hosts, WorkOrder: plan.workOrder,
	}
	batch, err := a.runCriteriaBatch(input)
	if err != nil {
		return err
	}
	result := domain.VerificationResult{
		ID: domain.NewID("verification"), WorkspaceID: plan.flowRun.WorkspaceID, QuestID: plan.flowRun.QuestID,
		FlowRunID: flowRunID, ExecutionID: exec.ID, RunID: exec.RunID, BatchKey: "baseline:" + tree, TreeDigest: tree,
		ImageDigest: sandbox.ExecutionImageForRecord(record), Source: "baseline", AllPassed: batch.AllOK,
		Evidence: sealedVerificationEvidence(batch), CreatedAt: time.Now().UTC(),
	}
	if err = a.store.SaveVerificationResultV2(ctx, result); err != nil {
		return err
	}
	if failed := baselineFailedCriteria(batch.Criteria); len(failed) > 0 && exec.RunID != "" && !batch.PreparationFailed {
		note := "Исходная проверка Point на нетронутом проекте (до ваших правок): уже не проходят " + strings.Join(failed, "; ") +
			". Это не ваша регрессия. Если задание не про это — не чините попутно и назовите это в итоге; если про это — вот исходное состояние."
		if injectErr := a.engine.InjectRunMessage(exec.RunID, note, "baseline"); injectErr != nil {
			slog.Info("baseline note not delivered to the writer", "run_id", exec.RunID, "error", security.Redact(injectErr.Error()))
		}
	}
	slog.Info("baseline verification finished", "flow_run_id", flowRunID, "all_passed", batch.AllOK, "criteria", len(batch.Criteria))
	return nil
}

func baselineFailedCriteria(criteria []agent.CriterionEvidence) []string {
	var failed []string
	for _, item := range criteria {
		if item.Status != "failed" {
			continue
		}
		label := item.CriterionID
		if item.Check != nil && item.Check.ExitCode != nil {
			label += fmt.Sprintf(" (код %d)", *item.Check.ExitCode)
		}
		failed = append(failed, label)
	}
	return failed
}

// baselineCriterionStatuses — исход исходной проверки по критериям прогона
// Flow; пусто, если её не было.
func (a *App) baselineCriterionStatuses(ctx context.Context, flowRunID string) map[string]string {
	stored, err := a.store.BaselineVerificationV2(ctx, flowRunID)
	if err != nil || stored.ID == "" {
		return nil
	}
	var payload struct {
		Criteria []agent.CriterionEvidence `json:"criteria"`
	}
	if json.Unmarshal(stored.Evidence, &payload) != nil {
		return nil
	}
	statuses := make(map[string]string, len(payload.Criteria))
	for _, item := range payload.Criteria {
		statuses[item.CriterionID] = item.Status
	}
	return statuses
}
