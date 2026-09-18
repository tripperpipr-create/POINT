// Ответы о положении дел: состояние, ростер, доступные действия.
package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/textutil"
)

// situationOf — снимок мира и признак того, что он получен.
//
// Второе значение важнее, чем кажется. Отсутствие поставщика или отказ хранилища
// возвращались теми же нулями, что и спокойный проект, и «Ничего не ждёт вашего
// решения» Мастер говорил одинаково уверенно — и когда проверил, и когда не смог.
// Хуже того, очередь в шапке приходит другим запросом: он мог и уцелеть, и тогда
// человек читал «3 ЖДУТ РЕШЕНИЯ» рядом с обещанием тишины.
func (s ChatService) situationOf(ctx context.Context, workspaceID string) (Situation, bool) {
	if s.Situation == nil {
		return Situation{}, false
	}
	snapshot, err := s.Situation(ctx, workspaceID)
	if err != nil {
		return Situation{}, false
	}
	return snapshot, true
}

// statusReply — что происходит прямо сейчас, числами, а не настроением.
func (s ChatService) statusReply(snapshot Situation, known bool, agents, active int) string {
	lines := make([]string, 0, 5)
	switch {
	// Незнание называется незнанием. Остальные числа ниже посчитаны отдельно и
	// от этого снимка не зависят — их Мастер приводит по-прежнему.
	case !known:
		lines = append(lines, "Свести очередь решений и наборы изменений сейчас не удалось — этих чисел у меня нет.")
	case snapshot.WaitingDecisions > 0:
		lines = append(lines, fmt.Sprintf("Ждут вашего решения: %d. Это первое, что стоит разобрать.", snapshot.WaitingDecisions))
	default:
		lines = append(lines, "Ничего не ждёт вашего решения.")
	}
	if snapshot.RunningExecutions > 0 {
		lines = append(lines, fmt.Sprintf("Сейчас выполняется: %d.", snapshot.RunningExecutions))
	}
	if snapshot.PendingChangeSets > 0 {
		lines = append(lines, fmt.Sprintf("Наборов изменений на ревью: %d.", snapshot.PendingChangeSets))
	}
	lines = append(lines, fmt.Sprintf("Активных квестов: %d. В ростере %s.", active, textutil.Count(agents, "агент", "агента", "агентов")))
	return strings.Join(lines, "\n")
}

// rosterReply перечисляет отряд и честно говорит, кто к работе не готов.
func (s ChatService) rosterReply(agents []domain.ProjectAgent) string {
	if len(agents) == 0 {
		return "Ростер пуст — нанимать пока некого."
	}
	ready := 0
	for _, agent := range agents {
		if len(s.blockersFor(agent)) == 0 {
			ready++
		}
	}
	names := make([]string, 0, len(agents))
	for _, agent := range agents {
		names = append(names, agent.Name)
	}
	return fmt.Sprintf("В ростере %s: %s. Готовы к квесту: %d из %d — причины неготовности в карточках.",
		textutil.Count(len(agents), "агент", "агента", "агентов"), strings.Join(names, ", "), ready, len(agents))
}

// rosterMembers показывает тот же состав в структурном виде, чтобы интерфейс
// нарисовал причины неготовности рядом с именами.
func (s ChatService) rosterMembers(agents []domain.ProjectAgent) []PartyMember {
	members := make([]PartyMember, 0, len(agents))
	for _, agent := range agents {
		members = append(members, PartyMember{
			AllowedTools: append([]string(nil), agent.AllowedTools...),
			AgentID:      agent.ID,
			Name:         agent.Name,
			Role:         strings.TrimSpace(agent.RoleDescription),
			Blocking:     s.blockersFor(agent),
		})
	}
	return members
}

// actionsFor — куда уйти прямо из разговора. Показываем только то, что сейчас
// имеет смысл: кнопка в пустоту хуже её отсутствия.
func (s ChatService) actionsFor(snapshot Situation) []ChatAction {
	// Неполученный снимок — нули, а нули не порождают кнопок: звать разбирать
	// очередь, о размере которой мы не знаем, — та же выдумка, только кликабельная.
	actions := make([]ChatAction, 0, 3)
	if snapshot.WaitingDecisions > 0 {
		actions = append(actions, ChatAction{
			Label: fmt.Sprintf("Разобрать очередь (%d)", snapshot.WaitingDecisions),
			Tab:   "decisions",
			Hint:  "решения, без которых работа стоит",
		})
	}
	if snapshot.PendingChangeSets > 0 {
		actions = append(actions, ChatAction{
			Label: fmt.Sprintf("Ревью изменений (%d)", snapshot.PendingChangeSets),
			Tab:   "changesets",
		})
	}
	if snapshot.ActiveQuests > 0 {
		actions = append(actions, ChatAction{Label: "Открыть квесты", Tab: "quests"})
	}
	return actions
}
