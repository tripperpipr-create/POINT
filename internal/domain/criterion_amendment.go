package domain

import (
	"fmt"
	"time"
)

// CriterionAmendment — правка команды машинного критерия, разрешённая
// человеком при повторе проваленного этапа.
//
// Утверждённый наряд неизменяем, и прежде любая правка проверки означала новую
// версию и квест с нуля. 30.09.2026 критерий `npm pack --pack-destination
// /tmp/…` падал без `mkdir -p` — исполнители видели это и обходили, а приёмка
// не могла. Поправка лежит рядом с нарядом, как решение по ручному критерию:
// не переписывается, и итог квеста называет её явно — шлюз не ослабляется
// молча.
type CriterionAmendment struct {
	QuestID         string    `json:"questId"`
	CriterionID     string    `json:"criterionId"`
	PreviousCommand string    `json:"previousCommand"`
	Command         string    `json:"command"`
	Reason          string    `json:"reason,omitempty"`
	ProposedBy      string    `json:"proposedBy,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
}

// Limitation — строка итога квеста о том, что проверка изменена.
func (a CriterionAmendment) Limitation() string {
	line := fmt.Sprintf("Проверка %s изменена по решению человека: было «%s», стало «%s»", a.CriterionID, a.PreviousCommand, a.Command)
	if a.Reason != "" {
		line += " — " + a.Reason
	}
	return line
}

// LatestCriterionAmendments — действующая поправка по каждому критерию:
// последняя по времени.
func LatestCriterionAmendments(items []CriterionAmendment) map[string]CriterionAmendment {
	latest := map[string]CriterionAmendment{}
	for _, item := range items {
		if current, ok := latest[item.CriterionID]; !ok || !item.CreatedAt.Before(current.CreatedAt) {
			latest[item.CriterionID] = item
		}
	}
	return latest
}
