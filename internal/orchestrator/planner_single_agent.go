package orchestrator

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// SingleAgentPlanModel — метка плана, построенного без модели.
const SingleAgentPlanModel = "point-single-agent"

// singleAgentPlan строит план без модели для отряда, закреплённого за одним
// исполнителем (путь наряда v2: состав утверждён человеком).
//
// Разбивать утверждённую цель на фазы некому и незачем: все этапы достались
// бы тому же агенту подряд, а модельный планировщик стоил до двух минут на
// «Утвердить» (108 с в живом прогоне 26.09) и мог отказать дважды. Этап
// получает весь бриф; Point по-прежнему добавляет приёмку, а исполнитель
// видит полный бриф в договоре выполнения.
func singleAgentPlan(req PlanRequest) (ModelPlan, bool) {
	if len(req.LockedAgentIDs) != 1 {
		return ModelPlan{}, false
	}
	agentID := req.LockedAgentIDs[0]
	known := false
	for _, agent := range req.Agents {
		if agent.ID == agentID {
			known = true
			break
		}
	}
	if !known {
		return ModelPlan{}, false
	}
	stage := PlanStage{
		Name: "Выполнить задание", AgentID: agentID, Phase: 1,
		Instruction:  singleAgentInstruction(req),
		CriterionIDs: singleAgentCriteria(req),
	}
	return ModelPlan{
		AgentIDs:  []string{agentID},
		Rationale: "Один исполнитель: весь утверждённый бриф одним этапом, приёмку добавляет Point",
		Stages:    []PlanStage{stage},
	}, true
}

func singleAgentCriteria(req PlanRequest) []string {
	brief := req.Proposal.Brief
	if brief == nil {
		return nil
	}
	var ids []string
	for _, criterion := range brief.Criteria {
		if criterion.ID == "" || slices.Contains(hostVerifiedCriteria(req), criterion.ID) {
			continue
		}
		ids = append(ids, criterion.ID)
	}
	return ids
}

const maxSingleAgentInstructionRunes = 2000

func singleAgentInstruction(req PlanRequest) string {
	var b strings.Builder
	b.WriteString("Выполни утверждённое задание целиком одним этапом. Условия, термины и решения брифа соблюдай дословно, похожими не подменяй.")
	brief := req.Proposal.Brief
	if brief == nil {
		b.WriteString("\nЗадание: " + strings.TrimSpace(req.Proposal.Task))
	} else {
		b.WriteString("\nЦель: " + strings.TrimSpace(brief.Goal))
		writeList(&b, "Границы", brief.Scope)
		writeList(&b, "Вне задачи", brief.OutOfScope)
		var decisions []string
		for _, decision := range brief.Decisions {
			decisions = append(decisions, strings.TrimSpace(decision.Topic)+": "+strings.TrimSpace(decision.Decision))
		}
		writeList(&b, "Решения", decisions)
		var criteria []string
		for _, criterion := range brief.Criteria {
			line := criterion.Text
			if slices.Contains(hostVerifiedCriteria(req), criterion.ID) {
				line += " (проверит Point на хосте после доставки; в песочнице проверь сборкой и тестами)"
			}
			criteria = append(criteria, line)
		}
		writeList(&b, "Критерии", criteria)
	}
	if repair := strings.TrimSpace(req.RepairContext); repair != "" {
		b.WriteString("\nПовторная попытка: результат прошлой уже в проекте, проверки на хосте не прошли. Исправь причину, не пересоздавай с нуля:\n" + repair)
	}
	text := b.String()
	if utf8.RuneCountInString(text) > maxSingleAgentInstructionRunes {
		runes := []rune(text)
		text = string(runes[:maxSingleAgentInstructionRunes-1]) + "…"
	}
	return text
}

func writeList(b *strings.Builder, title string, items []string) {
	var kept []string
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			kept = append(kept, item)
		}
	}
	if len(kept) == 0 {
		return
	}
	b.WriteString("\n" + title + ":\n- " + strings.Join(kept, "\n- "))
}

func hostVerifiedCriteria(req PlanRequest) []string {
	if req.Environment == nil {
		return nil
	}
	return req.Environment.HostVerifiedCriteria
}
