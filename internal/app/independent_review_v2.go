package app

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/flowruntime"
)

// Независимый проверяющий (TODO Q12).
//
// При двух и более писателях этап Implementation review смотрит итоговую
// ревизию. Прежде он получал рассказы писателей и его ответ ни на что не
// влиял: в E6 ревью закрылось за 10 с без модели, хотя lock-файл расходился
// с манифестом. Теперь проверяющий видит только diff, изменённые файлы и
// исходы проверок Point, а не то, что писатели о себе рассказали. Итог он
// заканчивает вердиктом по каждому критерию. «failed» проваливает этап, и
// квест встаёт на решение человека с причиной, а не идёт к приёмке.

type reviewCriterionVerdict struct {
	ID       string `json:"id"`
	Status   string `json:"status"` // passed | failed | concern
	Evidence string `json:"evidence"`
}

var reviewVerdictBlock = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")

// parseIndependentReview достаёт вердикт из последнего JSON-блока итога.
func parseIndependentReview(result string) ([]reviewCriterionVerdict, bool) {
	blocks := reviewVerdictBlock.FindAllStringSubmatch(result, -1)
	candidates := make([]string, 0, len(blocks)+1)
	for index := len(blocks) - 1; index >= 0; index-- {
		candidates = append(candidates, blocks[index][1])
	}
	if start, end := strings.LastIndex(result, `{"criteria"`), strings.LastIndex(result, "}"); start >= 0 && end > start {
		candidates = append(candidates, result[start:end+1])
	}
	for _, candidate := range candidates {
		var payload struct {
			Criteria []reviewCriterionVerdict `json:"criteria"`
		}
		if json.Unmarshal([]byte(candidate), &payload) == nil && len(payload.Criteria) > 0 {
			return payload.Criteria, true
		}
	}
	return nil, false
}

// flowNodeRole — роль этапа в снимке Flow прогона.
func (a *App) flowNodeRole(ctx context.Context, flowRunID, nodeID string) string {
	flowRun, err := a.store.GetFlowRun(ctx, flowRunID)
	if err != nil {
		return ""
	}
	flow, ok, err := flowruntime.FlowFromSnapshot(flowRun)
	if err != nil {
		return ""
	}
	if !ok {
		if flow, err = a.store.GetFlow(ctx, flowRun.FlowID); err != nil {
			return ""
		}
	}
	for _, node := range flow.Nodes {
		if node.ID == nodeID {
			return domain.FlowNodeStageRole(node)
		}
	}
	return ""
}

// independentReviewFailure — причина провала этапа по вердикту проверяющего;
// пусто, если этап не ревью, вердикта нет или проваленных критериев нет.
func (a *App) independentReviewFailure(ctx context.Context, flowRunID, nodeID, result string) (string, []reviewCriterionVerdict) {
	if a.flowNodeRole(ctx, flowRunID, nodeID) != domain.StageRoleImplReview {
		return "", nil
	}
	verdicts, ok := parseIndependentReview(result)
	if !ok {
		return "", nil
	}
	var failed []string
	for _, verdict := range verdicts {
		if strings.EqualFold(strings.TrimSpace(verdict.Status), "failed") {
			failed = append(failed, strings.TrimSpace(verdict.ID+": "+verdict.Evidence))
		}
	}
	if len(failed) == 0 {
		return "", verdicts
	}
	return "Независимый проверяющий: не выполнено — " + strings.Join(failed, "; "), verdicts
}

// reviewChecksContext — исходы проверок Point для проверяющего: перед
// приёмкой на итоговом дереве и на нетронутом дереве до правок.
func (a *App) reviewChecksContext(ctx context.Context, flowRunID string) (domain.RunContextInput, bool) {
	var lines []string
	for _, source := range []string{"pre_accept", "baseline"} {
		stored, err := a.store.LatestVerificationV2(ctx, flowRunID, source)
		if err != nil || stored.ID == "" {
			continue
		}
		var payload struct {
			Criteria []agent.CriterionEvidence `json:"criteria"`
		}
		if json.Unmarshal(stored.Evidence, &payload) != nil {
			continue
		}
		label := map[string]string{"pre_accept": "на итоговом дереве", "baseline": "на нетронутом дереве, до правок"}[source]
		for _, item := range payload.Criteria {
			code := ""
			if item.Check != nil && item.Check.ExitCode != nil {
				code = fmt.Sprintf(", код %d", *item.Check.ExitCode)
			}
			lines = append(lines, fmt.Sprintf("%s — %s (%s%s)", item.CriterionID, item.Status, label, code))
		}
	}
	if len(lines) == 0 {
		return domain.RunContextInput{}, false
	}
	return domain.RunContextInput{Kind: domain.ContextText, Label: "Проверки Point", Content: strings.Join(lines, "\n")}, true
}

// stageContextItems — контекст этапа; проверяющий получает и исходы проверок
// Point.
func (a *App) stageContextItems(quest domain.Quest, flow domain.FlowGraph, flowRun domain.FlowRun, node domain.FlowNode, changeSets []domain.ChangeSet) []domain.RunContextInput {
	items := flowNodeContext(quest, flow, flowRun, node.ID, changeSets)
	if domain.FlowNodeStageRole(node) == domain.StageRoleImplReview {
		if checks, ok := a.reviewChecksContext(context.Background(), flowRun.ID); ok {
			items = append(items, checks)
		}
	}
	return items
}
