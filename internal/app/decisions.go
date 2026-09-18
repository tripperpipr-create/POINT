package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/textutil"
)

// Очередь решений — единственное место, где агент физически стоит и ждёт
// человека. Она собирается из пяти независимых источников, которые раньше
// приходилось выуживать из bootstrap на 42 поля и склеивать в клиенте.
//
// Ответ намеренно самодостаточен: каждый элемент несёт не только «что просят»,
// но и то, каким запросом его решать. Клиенту не нужен switch по типам, а
// значит новый источник решений не требует правок в UI.

type DecisionKind string

const (
	DecisionApproval     DecisionKind = "approval"      // патч, команда, пользовательский инструмент
	DecisionChangeSet    DecisionKind = "change-set"    // набор изменений на применение
	DecisionConflict     DecisionKind = "conflict"      // трёхстороннее слияние после Join
	DecisionFlowGate     DecisionKind = "flow-gate"     // узел-approval внутри флоу
	DecisionQuest        DecisionKind = "quest"         // предложение квеста
	DecisionAction       DecisionKind = "action"        // предложенное действие
	DecisionEgress       DecisionKind = "egress"        // сеть/git вне allowlist → Мастер→пользователь
	DecisionSupervision  DecisionKind = "supervision"   // надзор Мастера (антизависание)
)

// DecisionResolve описывает, как принять или отклонить элемент.
//
// Path — путь POST-запроса, Field — имя поля решения в теле, Accept/Reject —
// коды действий. Коды здесь не только значения: интерфейс переводит их в
// глаголы кнопок, поэтому «approve» обязан оставаться «approve», даже если
// маршрут ждёт в теле булево.
//
// Отсюда и остальные два поля. Тело запроса собиралось по одному правилу на
// всех — «поле решения плюс id», — а маршруты его не принимают: ядро отвергает
// чужие поля, и очередь получала 400 на каждое нажатие. Ни один элемент очереди
// нельзя было ни принять, ни отклонить. Теперь маршрут сам говорит, как назвать
// идентификатор и что положить в поле решения.
type DecisionResolve struct {
	Path   string `json:"path"`
	Field  string `json:"field,omitempty"`
	Accept string `json:"accept,omitempty"`
	Reject string `json:"reject,omitempty"`
	// IDField — как маршрут называет идентификатор в теле. Пусто означает, что
	// идентификатор уже в пути и в теле его быть не должно.
	IDField string `json:"idField,omitempty"`
	// AcceptValue/RejectValue — что кладётся в Field. Пусто — сам код действия.
	// Часть маршрутов ждёт булево (allow, approved), и код им чужой.
	AcceptValue any `json:"acceptValue,omitempty"`
	RejectValue any `json:"rejectValue,omitempty"`
}

type Decision struct {
	Brief     *domain.TaskBrief `json:"brief,omitempty"`
	ID        string            `json:"id"`
	Kind      DecisionKind      `json:"kind"`
	Label     string            `json:"label"`
	Title     string            `json:"title"`
	Detail    string            `json:"detail,omitempty"`
	Risk      string            `json:"risk"`
	Who       string            `json:"who,omitempty"`
	RunID     string            `json:"runId,omitempty"`
	FlowRunID string            `json:"flowRunId,omitempty"`
	NodeID    string            `json:"nodeId,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
	WaitingMs int64             `json:"waitingMs"`
	Blocking  bool              `json:"blocking"`
	Resolve   DecisionResolve   `json:"resolve"`
}

type DecisionQueue struct {
	Items       []Decision     `json:"items"`
	Total       int            `json:"total"`
	Blocking    int            `json:"blocking"`
	OldestMs    int64          `json:"oldestMs"`
	ByKind      map[string]int `json:"byKind"`
	GeneratedAt time.Time      `json:"generatedAt"`
}

// riskByTool берёт оценку из того же каталога, который видит пользователь при
// настройке агента. Отдельная шкала здесь была бы вторым источником истины.
func riskByTool(name string, customRisk map[string]string) string {
	if risk, ok := customRisk[name]; ok && risk != "" {
		return risk
	}
	for _, item := range domain.BuiltInToolCatalog() {
		if item.Name == name {
			return item.Risk
		}
	}
	// Неизвестный инструмент — это пользовательский процесс. Политика всегда
	// требует для него подтверждения, поэтому и риск считаем максимальным.
	return "CRITICAL"
}

func toolLabel(name string) string {
	switch name {
	case "propose_patch":
		return "ПАТЧ"
	case "run_command":
		return "КОМАНДА"
	default:
		return "ИНСТРУМЕНТ"
	}
}

// Сколько решение ждёт человека. Ноль означает «неизвестно», а не «только что»:
// очередь сортируется по этому числу, и запись без отметки времени объявляла
// ожидание длиной в две тысячи лет — навсегда занимала верх, отодвигая
// настоящую срочную работу, а на экране показывала «17532000ч».
//
// Отрицательное время тоже гасим: часы машины могут прыгнуть назад (поправка
// NTP), и тогда только что созданная запись уехала бы в конец очереди.
func waitingMs(since, now time.Time) int64 {
	if since.IsZero() {
		return 0
	}
	ms := now.Sub(since).Milliseconds()
	if ms < 0 {
		return 0
	}
	return ms
}

// Decisions собирает очередь по текущему миру. Порядок — по времени ожидания:
// первым стоит тот, кто ждёт дольше всех, потому что именно он дороже всего.
func (a *App) Decisions(ctx context.Context) (DecisionQueue, error) {
	now := time.Now().UTC()
	workspaceID := a.currentWorldID()
	queue := DecisionQueue{Items: []Decision{}, ByKind: map[string]int{}, GeneratedAt: now}

	customRisk := map[string]string{}
	if tools, err := a.store.ListCustomTools(ctx); err == nil {
		for _, tool := range tools {
			customRisk[tool.ID] = "CRITICAL"
		}
	}

	// 1. Подтверждения инструментов. Незавершённый прогон — единственное место,
	//    где агент действительно простаивает, поэтому только такие блокирующие.
	//
	//    Статус waiting_approval здесь обязателен: движок выставляет именно его,
	//    когда просит разрешения и блокируется (agent/engine.go, awaitApproval).
	//    Без него очередь пропускала ровно те прогоны, которые ждут человека —
	//    агент стоял, а экран «всё, что ждёт вашего решения» показывал пустоту.
	runs, err := a.store.ListRunsForWorkspace(ctx, workspaceID, 100)
	if err != nil {
		return queue, err
	}
	for _, run := range runs {
		switch run.Status {
		case domain.RunRunning, domain.RunPaused, domain.RunWaiting:
		default:
			continue
		}
		approvals, err := a.store.ApprovalsByRun(ctx, run.ID)
		if err != nil {
			return queue, err
		}
		for _, approval := range approvals {
			if approval.Status != domain.ApprovalPending {
				continue
			}
			queue.Items = append(queue.Items, Decision{
				ID:        approval.ID,
				Kind:      DecisionApproval,
				Label:     toolLabel(approval.ToolName),
				Title:     strings.TrimSpace(approval.Reason),
				Detail:    approval.ToolName,
				Risk:      riskByTool(approval.ToolName, customRisk),
				Who:       approval.AgentID,
				RunID:     run.ID,
				CreatedAt: approval.CreatedAt,
				WaitingMs: waitingMs(approval.CreatedAt, now),
				Blocking:  true,
				Resolve: DecisionResolve{
					// Маршрут ждёт {"allow": true|false}, идентификатор — в пути.
					Path:  fmt.Sprintf("/api/approvals/%s/resolve", approval.ID),
					Field: "allow", Accept: "approve", Reject: "deny",
					AcceptValue: true, RejectValue: false,
				},
			})
		}
	}

	// 2. Наборы изменений: pending — на применение, conflict — на разбор слияния.
	sets, err := a.store.ListChangeSets(ctx, workspaceID)
	if err != nil {
		return queue, err
	}
	for _, set := range sets {
		// accept — код действия, а не адрес: интерфейс подписывает им кнопку
		// («ПРИМЕНИТЬ», «РАЗОБРАТЬ»). Без него подпись откатывалась к
		// обезличенному «ПРИНЯТЬ», одинаковому для несопоставимых поступков.
		kind, label, path := DecisionChangeSet, "НАБОР", fmt.Sprintf("/api/change-sets/%s/apply", set.ID)
		accept := "apply"
		if set.Status == domain.ChangeSetConflict {
			kind, label, path = DecisionConflict, "КОНФЛИКТ", fmt.Sprintf("/api/change-sets/%s/resolve", set.ID)
			accept = "resolve"
		} else if set.Status != domain.ChangeSetPending {
			continue
		}
		risk := "MEDIUM"
		if kind == DecisionConflict || len(set.DependsOn) > 0 {
			// Зависимый набор нельзя применить частично, а конфликт — молчаливо.
			risk = "HIGH"
		}
		queue.Items = append(queue.Items, Decision{
			ID:        set.ID,
			Kind:      kind,
			Label:     label,
			Title:     set.Title,
			Detail:    textutil.Count(len(set.Items), "файл", "файла", "файлов"),
			Risk:      risk,
			CreatedAt: set.CreatedAt,
			WaitingMs: waitingMs(set.CreatedAt, now),
			Blocking:  kind == DecisionConflict,
			Resolve:   DecisionResolve{Path: path, Accept: accept, Reject: fmt.Sprintf("/api/change-sets/%s/reject", set.ID)},
		})
	}

	// 3. Узлы флоу, остановленные на подтверждении. Здесь ждёт вся кампания.
	flowRuns, err := a.store.ListFlowRuns(ctx, workspaceID, 100)
	if err != nil {
		return queue, err
	}
	for _, flowRun := range flowRuns {
		// Тот же набор статусов, что и у прогонов агента: остановленный на
		// подтверждении флоу помечается waiting_approval, и без него ждущий
		// человека узел не доходил до очереди.
		switch flowRun.Status {
		case domain.RunRunning, domain.RunPaused, domain.RunWaiting:
		default:
			continue
		}
		for nodeID, state := range flowRun.NodeStates {
			if state.Status != "waiting_approval" {
				continue
			}
			started := flowRun.StartedAt
			if state.StartedAt != nil {
				started = *state.StartedAt
			}
			queue.Items = append(queue.Items, Decision{
				ID:        flowRun.ID + "/" + nodeID,
				Kind:      DecisionFlowGate,
				Label:     "УЗЕЛ",
				Title:     fmt.Sprintf("Флоу ждёт подтверждения на узле %s", nodeID),
				Risk:      "HIGH",
				FlowRunID: flowRun.ID,
				NodeID:    nodeID,
				CreatedAt: started,
				WaitingMs: waitingMs(started, now),
				Blocking:  true,
				Resolve: DecisionResolve{
					// Маршрут ждёт {"approved": true|false}, оба идентификатора — в пути.
					Path:  fmt.Sprintf("/api/flow-runs/%s/nodes/%s/resolve", flowRun.ID, nodeID),
					Field: "approved", Accept: "approve", Reject: "reject",
					AcceptValue: true, RejectValue: false,
				},
			})
		}
	}

	// 4. Предложения квестов. Работа ещё не начата — никто не простаивает.
	proposals, err := a.store.ListQuestProposals(ctx, workspaceID)
	if err != nil {
		return queue, err
	}
	for _, proposal := range proposals {
		if proposal.Status != "pending" && proposal.Status != "modified" {
			continue
		}
		queue.Items = append(queue.Items, Decision{
			Brief:     cloneTaskBrief(proposal.Brief),
			ID:        proposal.ID,
			Kind:      DecisionQuest,
			Label:     "ПРЕДЛОЖЕНИЕ",
			Title:     proposal.Title,
			Detail:    proposal.Rationale,
			Risk:      "LOW",
			CreatedAt: proposal.CreatedAt,
			WaitingMs: waitingMs(proposal.CreatedAt, now),
			Resolve: DecisionResolve{
				// Маршрут ждёт {"proposalId": ..., "action": "start"|"ignore"}.
				Path:  "/api/quest-proposals/decide",
				Field: "action", Accept: "start", Reject: "ignore",
				IDField: "proposalId",
			},
		})
	}

	// 5. Предложенные действия (агент, отряд, навык, флоу).
	actions, err := a.store.ListCompanionActionProposals(ctx, workspaceID)
	if err != nil {
		return queue, err
	}
	for _, action := range actions {
		if action.Status != "pending" && action.Status != "modified" {
			continue
		}
		queue.Items = append(queue.Items, Decision{
			ID:        action.ID,
			Kind:      DecisionAction,
			Label:     "ДЕЙСТВИЕ",
			Title:     action.Title,
			Detail:    action.Rationale,
			Risk:      "LOW",
			CreatedAt: action.CreatedAt,
			WaitingMs: waitingMs(action.CreatedAt, now),
			Resolve: DecisionResolve{
				// Маршрут ждёт {"proposalId": ..., "action": "apply"|"ignore"}.
				Path:  "/api/companion/actions/decide",
				Field: "action", Accept: "apply", Reject: "ignore",
				IDField: "proposalId",
			},
		})
	}

	// 6. Эскалации сети/git и надзор Мастера.
	asks, err := a.store.ListPendingEgressAsks(ctx, workspaceID)
	if err != nil {
		return queue, err
	}
	for _, ask := range asks {
		kind, label, accept, reject := DecisionEgress, "СЕТЬ", "allow_quest", "deny"
		title := fmt.Sprintf("Разрешить сеть: %s", ask.Target)
		if ask.Kind == domain.EgressAskGitRemote {
			label, title = "GIT", fmt.Sprintf("Подтвердить репозиторий: %s", ask.Target)
		}
		if ask.Kind == domain.EgressAskSupervision {
			kind, label, accept, reject = DecisionSupervision, "НАДЗОР", "continue", "stop"
			title = "Агент зациклился — продолжить с другой стратегией или остановить"
		}
		detail := ask.Reason
		queue.Items = append(queue.Items, Decision{
			ID:        ask.ID,
			Kind:      kind,
			Label:     label,
			Title:     title,
			Detail:    detail,
			Risk:      ask.Risk,
			RunID:     ask.RunID,
			CreatedAt: ask.CreatedAt,
			WaitingMs: waitingMs(ask.CreatedAt, now),
			Blocking:  true,
			Resolve: DecisionResolve{
				Path: fmt.Sprintf("/api/egress-asks/%s/resolve", ask.ID),
				Field: "action", Accept: accept, Reject: reject,
			},
		})
	}

	// Дольше всех ждущий — первым. При равном ожидании порядок фиксируем по ID,
	// иначе выдача плясала бы между запросами и ломала выделение в списке.
	sort.SliceStable(queue.Items, func(i, j int) bool {
		if queue.Items[i].WaitingMs != queue.Items[j].WaitingMs {
			return queue.Items[i].WaitingMs > queue.Items[j].WaitingMs
		}
		return queue.Items[i].ID < queue.Items[j].ID
	})

	queue.Total = len(queue.Items)
	for _, item := range queue.Items {
		queue.ByKind[string(item.Kind)]++
		if item.Blocking {
			queue.Blocking++
		}
	}
	if queue.Total > 0 {
		queue.OldestMs = queue.Items[0].WaitingMs
	}
	return queue, nil
}
