package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
)

// Повтор проверки среды без новой версии наряда (TODO Q14).
//
// Проверка на хосте, упёршаяся в среду, — занятый порт, чужой стек — была
// окончательным вердиктом: результат уже в проекте, код верен, а исправить
// можно было только новой версией наряда и новым квестом. Теперь такой отказ
// не выносит вердикт. Квест ждёт человека с причиной; «Проверить снова»
// повторяет только проверки на хосте на той же доставленной ревизии, а
// «Завершить квест» выносит вердикт по уже полученному итогу. Вынесенный
// вердикт по-прежнему окончателен: пакет доказательств неизменяем.

const (
	hostRecheckKey = "hostRecheck"
	// hostRecheckLimit — сколько раз квест может остановиться на среде;
	// дальше выносится вердикт, чтобы квест не висел бесконечно.
	hostRecheckLimit = 3
)

var errNoHostRecheckV2 = errors.New("квест не ждёт повторной проверки среды")

type hostRecheckState struct {
	Actions      []string              `json:"actions"`
	At           string                `json:"at"`
	Count        int                   `json:"count"`
	MachineReady bool                  `json:"machineReady"`
	PreHost      domain.EvidenceBundle `json:"preHost"`
	Bundle       domain.EvidenceBundle `json:"bundle"`
}

func hostRecheckRecord(quest domain.Quest) (hostRecheckState, bool) {
	raw, ok := quest.Controller[hostRecheckKey]
	if !ok || raw == nil {
		return hostRecheckState{}, false
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return hostRecheckState{}, false
	}
	var state hostRecheckState
	if json.Unmarshal(encoded, &state) != nil {
		return hostRecheckState{}, false
	}
	return state, true
}

func hostRecheckPending(quest domain.Quest) bool {
	_, ok := hostRecheckRecord(quest)
	return ok && quest.Status == domain.QuestAwaitingUser
}

// environmentOnlyHostFailuresV2 — действия, которых ждут проваленные проверки
// на хосте, если все они упали из-за среды. Провал кода — не среда: такой
// итог идёт в попытку исправления или в вердикт, как прежде.
func environmentOnlyHostFailuresV2(checks []domain.VerificationCheck) []string {
	var actions []string
	for _, check := range checks {
		if check.Satisfied {
			continue
		}
		action := hostCheckActionV2(check.Summary)
		if action == "" {
			return nil
		}
		actions = append(actions, strings.TrimSpace(strings.TrimPrefix(action, "Нужно действие:")))
	}
	return actions
}

// holdForHostEnvironmentV2 ставит квест ждать человека вместо вердикта.
func (a *App) holdForHostEnvironmentV2(ctx context.Context, approval domain.WorkOrderApproval, quest domain.Quest, bundle, preHost domain.EvidenceBundle, hostChecks []domain.VerificationCheck) bool {
	actions := environmentOnlyHostFailuresV2(hostChecks)
	if len(actions) == 0 {
		return false
	}
	previous, _ := hostRecheckRecord(quest)
	count := previous.Count + 1
	if count > hostRecheckLimit {
		return false
	}
	state := hostRecheckState{
		Actions: actions, At: time.Now().UTC().Format(time.RFC3339Nano), Count: count,
		MachineReady: true, PreHost: preHost, Bundle: bundle,
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return false
	}
	var stored map[string]any
	if json.Unmarshal(encoded, &stored) != nil {
		return false
	}
	if quest.Controller == nil {
		quest.Controller = map[string]any{}
	}
	quest.Controller[hostRecheckKey] = stored
	message := "Проверка на хосте упёрлась в среду: " + strings.Join(actions, "; ") +
		". Результат уже в проекте. Устраните причину и нажмите «Проверить снова» или завершите квест."
	if _, err = a.setWorkOrderQuestStatusV2(ctx, quest, domain.QuestAwaitingUser, truncateRunes(message, 900)); err != nil {
		slog.Warn("host environment hold not recorded; quest goes to verdict", "quest_id", quest.ID, "error", security.Redact(err.Error()))
		return false
	}
	a.publishWorkOrderNoticeV2(ctx, approval, quest, "warning", truncateRunes(message, 900))
	slog.Info("work order quest holds for host environment", "quest_id", quest.ID, "attempt", count)
	return true
}

// recheckWorkOrderHostV2 повторяет проверки на хосте на той же ревизии.
func (a *App) recheckWorkOrderHostV2(ctx context.Context, quest domain.Quest) (domain.QuestStatus, error) {
	return a.resumeHeldHostCheckV2(ctx, quest, true)
}

// finalizeHeldHostCheckV2 выносит вердикт по итогу, на котором квест встал.
func (a *App) finalizeHeldHostCheckV2(ctx context.Context, quest domain.Quest) (domain.QuestStatus, error) {
	return a.resumeHeldHostCheckV2(ctx, quest, false)
}

func (a *App) resumeHeldHostCheckV2(ctx context.Context, quest domain.Quest, recheck bool) (domain.QuestStatus, error) {
	state, ok := hostRecheckRecord(quest)
	if !ok || quest.Status != domain.QuestAwaitingUser {
		return quest.Status, errNoHostRecheckV2
	}
	if _, final, err := a.store.WorkOrderVerdictV2(ctx, quest.ID); err != nil {
		return quest.Status, err
	} else if final {
		return quest.Status, errWorkOrderVerdictFinalV2
	}
	approval, err := a.store.WorkOrderApprovalByQuestV2(ctx, quest.ID)
	if err != nil {
		return quest.Status, err
	}
	message := "Завершаем квест по решению человека"
	if recheck {
		message = "Проверяем на хосте снова"
	}
	quest, err = a.setWorkOrderQuestStatusV2(ctx, quest, domain.QuestApplying, message)
	if err != nil {
		return quest.Status, err
	}
	a.workOrderLaunchWG.Add(1)
	go func() {
		defer a.workOrderLaunchWG.Done()
		background := context.Background()
		if !recheck {
			a.concludeWorkOrderQuestV2(background, approval, quest, state.Bundle, nil, nil, true, state.MachineReady, false)
			return
		}
		bundle := cloneEvidenceBundleV2(state.PreHost)
		hostChecks := a.runWorkOrderHostPhaseV2(background, approval.WorkOrder, &bundle)
		preHost := state.PreHost
		a.concludeWorkOrderQuestV2(background, approval, quest, bundle, &preHost, hostChecks, true, state.MachineReady, true)
	}()
	return domain.QuestApplying, nil
}
