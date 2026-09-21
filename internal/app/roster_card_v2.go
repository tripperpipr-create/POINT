package app

import (
	"context"

	"local-agent-workbench/internal/domain"
)

// Карточка найма — вторая половина наблюдателя: решение живёт в наряде, а
// картина выбора показывается рядом.
//
// Решение (кого берём, кого создаём) хранится в Roster наряда: он
// версионируется, покрыт digest и согласием человека. Картина — кандидаты, их
// готовность, подходящие чертежи, пробелы — считается на чтении и не хранится:
// готовность меняется от внешних причин, и под неизменяемым digest наряда ей
// не место.
type RosterCardView struct {
	WorkOrderID string `json:"workOrderId"`
	Goal        string `json:"goal,omitempty"`
	// State — что показывать человеку: ready | candidate | blocked | blueprint |
	// create | subagent.
	State          string                 `json:"state"`
	MaxAgents      int                    `json:"maxAgents"`
	Reason         string                 `json:"reason,omitempty"`
	AllowSubagents bool                   `json:"allowSubagents"`
	Selected       []RosterCandidate      `json:"selected,omitempty"`
	Considered     []RosterCandidate      `json:"considered,omitempty"`
	Blueprints     []RosterBlueprintMatch `json:"blueprints,omitempty"`
	Draft          *domain.AgentDraft     `json:"draft,omitempty"`
	Subagent       *domain.SubagentPlan   `json:"subagent,omitempty"`
	// ParentName — имя исполнителя, под которым предлагается помощник: человеку
	// нужен не идентификатор родителя, а тот, кто за него отвечает.
	ParentName string `json:"parentName,omitempty"`
}

// hiringCardsForConversation считает карточку найма для открытого наряда
// беседы. Утверждённый наряд карточки не получает: состав там уже зафиксирован
// согласием, и предлагать замену исполнителя после запуска — значит звать
// человека менять то, что уже работает.
func (a *App) hiringCardsForConversation(ctx context.Context, orders []domain.WorkOrder) []RosterCardView {
	for _, order := range orders {
		if !isOpenWorkOrderV2(order) {
			continue
		}
		// New WorkOrders already carry persisted selector IDs. The constructor
		// card for a draft is the only decision surface; the legacy observer must
		// not invent a second, unbound AgentDraft beside it.
		if len(order.Roster.AgentIDs) > 0 {
			return nil
		}
		observation, err := a.ObserveRoster(ctx, rosterNeedFromOrder(order, nil))
		if err != nil {
			// Ошибка подбора не имеет права уронить загрузку разговора: карточка
			// найма — подсказка, а не условие показа ленты.
			return nil
		}
		card := rosterCardFromObservation(order, observation)
		if !rosterCardWorthShowing(card) {
			// Решать нечего: состав собран, замены нет, пробелов нет. Карточка
			// в этом случае повторяла бы блок «Агенты» карточки запуска и стала
			// бы постоянным шумом в ленте.
			return nil
		}
		return []RosterCardView{card}
	}
	return nil
}

func rosterCardFromObservation(order domain.WorkOrder, observation RosterObservation) RosterCardView {
	card := RosterCardView{
		WorkOrderID: order.ID, Goal: order.Goal,
		MaxAgents: observation.MaxAgents, Reason: observation.Reason,
		AllowSubagents: observation.Need.AllowSubagents,
		Selected:       observation.Selected, Considered: observation.Considered,
	}
	for _, gap := range observation.Gaps {
		if len(gap.Blueprints) > 0 {
			card.Blueprints = gap.Blueprints
		}
		if gap.Draft.Name != "" {
			draft := gap.Draft
			card.Draft = &draft
		}
		if gap.Subagent != nil {
			subagent := *gap.Subagent
			card.Subagent = &subagent
		}
		if gap.Kind == "missing_subagent" && card.Subagent == nil && card.Draft == nil {
			// Пробел есть, но права заводить помощника человек не давал: карточка
			// обязана это сказать, а не молчать про нехватку роли.
			card.Reason = "не хватает роли «" + gap.Requirement.Role + "», но создание исполнителей не разрешено в задании"
		}
	}
	// Черновик наряда старше собственного: человек мог его уже поправить, и
	// карточка обязана показывать то, что уйдёт на утверждение.
	for _, draft := range order.Roster.Permanent {
		if draft.Existing {
			continue
		}
		pending := draft
		card.Draft = &pending
		break
	}
	if len(order.Roster.Temporary) > 0 {
		planned := order.Roster.Temporary[0]
		card.Subagent = &planned
	}
	card.ParentName = rosterParentName(order, card.Subagent)
	card.State = rosterCardState(order, card)
	return card
}

// rosterCardWorthShowing отвечает, есть ли в карточке хоть одно решение для
// человека: кого взять вместо выбранного, кого создать, кого добавить в помощь
// или что чинить. Без этого карточка — пересказ уже видимого состава.
func rosterCardWorthShowing(card RosterCardView) bool {
	if card.Draft != nil || card.Subagent != nil || len(card.Blueprints) > 0 || len(card.Considered) > 0 {
		return true
	}
	return card.State == "blocked"
}

func rosterParentName(order domain.WorkOrder, subagent *domain.SubagentPlan) string {
	if subagent == nil {
		return ""
	}
	for _, draft := range order.Roster.Permanent {
		if draft.ID == subagent.ParentAgentID {
			return draft.Name
		}
	}
	return ""
}

// rosterCardState выбирает, о чём карточка говорит в первую очередь. Порядок —
// от самого срочного: заблокированный исполнитель важнее предложения помощника,
// а предложение помощника важнее спокойного «состав собран».
func rosterCardState(order domain.WorkOrder, card RosterCardView) string {
	for _, candidate := range card.Selected {
		if candidate.Readiness == "BLOCKED" {
			return "blocked"
		}
	}
	if card.Draft != nil {
		// «Есть подходящий чертёж» — только когда черновик действительно взят из
		// чертежа. Похожие чертежи показываются и рядом с сочинённым черновиком,
		// но заголовок по ним врал бы: совпадения слов мало, чтобы объявить
		// чертёж подходящим.
		if card.Draft.BlueprintID != "" {
			return "blueprint"
		}
		return "create"
	}
	if card.Subagent != nil {
		return "subagent"
	}
	if len(card.Considered) > 0 && len(order.Roster.Permanent) > 0 {
		return "candidate"
	}
	return "ready"
}
