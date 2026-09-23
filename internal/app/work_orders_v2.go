package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/osproc"
	"local-agent-workbench/internal/storage"
)

type ApproveWorkOrderV2Request struct {
	Version        int    `json:"version"`
	Digest         string `json:"digest"`
	IdempotencyKey string `json:"idempotencyKey"`
	// APIKey is transient launch material supplied by the desktop
	// SecretStorage. It is never persisted in the WorkOrder, quest or evidence.
	APIKey string `json:"apiKey,omitempty"`
	// RosterConsent — идентификаторы черновиков, создание которых человек
	// подтвердил. Утверждение наряда создаёт агентов внутри своей транзакции,
	// и до этой правки согласие держалось только на доверии к интерфейсу.
	RosterConsent []string `json:"rosterConsent,omitempty"`
}

type ReviseWorkOrderV2Request struct {
	ExpectedVersion int              `json:"expectedVersion"`
	ExpectedDigest  string           `json:"expectedDigest"`
	IdempotencyKey  string           `json:"idempotencyKey"`
	WorkOrder       domain.WorkOrder `json:"workOrder"`
}

func (a *App) SaveWorkOrderV2(ctx context.Context, order domain.WorkOrder) (domain.WorkOrder, error) {
	var err error
	order, err = a.prepareWorkOrderWorkspaceV2(ctx, order)
	if err != nil {
		return domain.WorkOrder{}, err
	}
	order = a.refreshWorkOrderRosterStateV2(ctx, order)
	return a.store.SaveWorkOrderV2(ctx, order)
}

// refreshWorkOrderRosterStateV2 makes ready a server invariant. A settled
// brief with a proposed or unavailable agent stays in staffing until the feed
// card has created a real runnable ProjectAgent.
func (a *App) refreshWorkOrderRosterStateV2(ctx context.Context, order domain.WorkOrder) domain.WorkOrder {
	if order.State != "staffing" && order.State != "ready" {
		return order
	}
	ids := make([]string, 0, len(order.Roster.Permanent))
	parents := make(map[string]bool, len(order.Roster.Permanent))
	for _, draft := range order.Roster.Permanent {
		if !draft.Existing || strings.TrimSpace(draft.ID) == "" {
			order.State = "staffing"
			order.Roster.AgentIDs = ids
			return order
		}
		id := strings.TrimSpace(draft.ID)
		ids = append(ids, id)
		parents[id] = true
	}
	if len(ids) == 0 {
		order.State = "staffing"
		order.Roster.AgentIDs = nil
		return order
	}
	for _, temporary := range order.Roster.Temporary {
		if !parents[strings.TrimSpace(temporary.ParentAgentID)] || strings.TrimSpace(temporary.Role) == "" || strings.TrimSpace(temporary.Mission) == "" {
			order.State = "staffing"
			order.Roster.AgentIDs = ids
			return order
		}
	}
	order.Roster.AgentIDs = ids
	if err := a.requireProjectAgentsReady(ctx, order.WorkspaceID, ids); err != nil {
		order.State = "staffing"
		return order
	}
	order.State = "ready"
	return order
}

func (a *App) WorkOrderV2(ctx context.Context, id string) (domain.WorkOrder, error) {
	return a.store.GetWorkOrderV2(ctx, id)
}

func (a *App) WorkOrderDiffsV2(ctx context.Context, id string) ([]domain.WorkOrderRevisionDiff, error) {
	return a.store.ListWorkOrderDiffsV2(ctx, id)
}

func (a *App) ReviseWorkOrderV2(ctx context.Context, id string, request ReviseWorkOrderV2Request) (domain.WorkOrder, error) {
	id = strings.TrimSpace(id)
	request.ExpectedDigest = strings.TrimSpace(request.ExpectedDigest)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if id == "" || request.ExpectedVersion <= 0 || request.ExpectedDigest == "" || request.IdempotencyKey == "" {
		return domain.WorkOrder{}, errors.New("revision requires work order id, expected version/digest and idempotency key")
	}
	replay, replayed, replayErr := a.store.WorkOrderRevisionReplayV2(ctx, request.IdempotencyKey)
	if replayErr != nil {
		return domain.WorkOrder{}, replayErr
	}
	if replayed {
		if replay.WorkOrderID != id || replay.ExpectedVersion != request.ExpectedVersion || replay.ExpectedDigest != request.ExpectedDigest {
			return domain.WorkOrder{}, errors.New("idempotency key belongs to a different work order revision")
		}
	}
	current, err := a.store.GetWorkOrderV2(ctx, id)
	if err != nil {
		return domain.WorkOrder{}, err
	}
	next := request.WorkOrder
	next.ID = current.ID
	next.WorkspaceID = current.WorkspaceID
	next.ConversationID = current.ConversationID
	next.Version = request.ExpectedVersion + 1
	next.CreatedAt = current.CreatedAt
	next.State = strings.TrimSpace(next.State)
	next, err = a.prepareWorkOrderWorkspaceV2(ctx, next)
	if err != nil {
		return domain.WorkOrder{}, err
	}
	next = domain.NormalizeWorkOrder(next)
	next = a.refreshWorkOrderRosterStateV2(ctx, next)
	if err = domain.ValidateWorkOrder(next); err != nil {
		return domain.WorkOrder{}, err
	}
	if replayed {
		if domain.WorkOrderDigest(next) != domain.WorkOrderDigest(replay.Result) {
			return domain.WorkOrder{}, errors.New("idempotency key belongs to a different work order revision payload")
		}
		return replay.Result, nil
	}
	currentDigest := domain.WorkOrderDigest(current)
	if request.ExpectedVersion != current.Version || request.ExpectedDigest != currentDigest {
		// Recover the narrow crash window where the immutable revision committed
		// but its idempotency receipt did not. A byte-equivalent next version is
		// safe to replay; any different content remains a conflict.
		if current.Version == request.ExpectedVersion+1 && domain.WorkOrderDigest(next) == currentDigest {
			if err = a.store.SaveWorkOrderRevisionReplayV2(ctx, request.IdempotencyKey, request.ExpectedVersion, request.ExpectedDigest, current); err != nil {
				return domain.WorkOrder{}, err
			}
			return current, nil
		}
		return domain.WorkOrder{}, errors.New("work order changed; review the current version before revising")
	}
	// A scope revision must stop Flow scheduling before the immutable diff is
	// published. Otherwise a newly eligible stage can start against the old
	// brief in the gap between the revision transaction and the next poll.
	if current.Runtime != nil && current.Runtime.FlowRunID != "" {
		switch current.Runtime.Status {
		case domain.QuestPreflight, domain.QuestRunning, domain.QuestVerifying, domain.QuestApplying, domain.QuestAwaitingUser:
			quest, questErr := a.WorkOrderQuestV2(ctx, current.Runtime.QuestID)
			if questErr != nil {
				return domain.WorkOrder{}, questErr
			}
			if _, questErr = a.controlWorkOrderFlowRuntimeV2(ctx, quest, "pause", WorkOrderQuestControlRequest{}); questErr != nil {
				return domain.WorkOrder{}, questErr
			}
		}
	}
	saved, err := a.store.SaveWorkOrderV2(ctx, next)
	if err != nil {
		return domain.WorkOrder{}, err
	}
	if err = a.store.SaveWorkOrderRevisionReplayV2(ctx, request.IdempotencyKey, request.ExpectedVersion, request.ExpectedDigest, saved); err != nil {
		return domain.WorkOrder{}, err
	}
	a.recordMasterEvidence(ctx, saved, "", "revision", "user_revised")
	return saved, nil
}

// DeleteWorkOrderV2 убирает карточку запуска из ленты разговора.
//
// Наряд — договор до работы, а не сама работа: пока он один, удалять нечего,
// кроме него самого. Отказ один и тот же, что у схемы и отряда: живой квест,
// который по этому наряду идёт. Закрытый квест договор не держит — его карточка
// уже история.
func (a *App) DeleteWorkOrderV2(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("не указан наряд")
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return err
	}
	order, err := a.store.GetWorkOrderV2(ctx, id)
	if err != nil {
		return err
	}
	if order.Runtime != nil && order.Runtime.QuestID != "" {
		quests, questErr := a.store.ListQuests(ctx, ws.ID)
		if questErr != nil {
			return questErr
		}
		for _, quest := range quests {
			if quest.ID != order.Runtime.QuestID {
				continue
			}
			switch quest.Status {
			case domain.QuestCompleted, domain.QuestNeedsReview, domain.QuestBlocked, domain.QuestFailed, domain.QuestCancelled:
			default:
				return fmt.Errorf("наряд ведёт квест %q — закройте или отмените квест", quest.Title)
			}
		}
	}
	return a.store.DeleteWorkOrderV2(ctx, ws.ID, id)
}

func (a *App) ApproveWorkOrderV2(ctx context.Context, id string, request ApproveWorkOrderV2Request) (domain.WorkOrderApproval, error) {
	if len(request.APIKey) > 64*1024 {
		return domain.WorkOrderApproval{}, errors.New("API credential exceeds 64 KiB")
	}
	if replay, ok, err := a.store.WorkOrderApprovalReplayV2(ctx, request.IdempotencyKey); err != nil {
		return domain.WorkOrderApproval{}, err
	} else if ok {
		if replay.WorkOrder.ID != strings.TrimSpace(id) || replay.WorkOrder.Version != request.Version || replay.WorkOrder.ApprovedDigest != strings.TrimSpace(request.Digest) {
			return domain.WorkOrderApproval{}, errors.New("idempotency key belongs to a different approval")
		}
		return a.resumeApprovedWorkOrderV2(ctx, replay, request.APIKey)
	}
	order, err := a.store.GetWorkOrderV2(ctx, id)
	if err != nil {
		return domain.WorkOrderApproval{}, err
	}
	if order.State != "ready" {
		return domain.WorkOrderApproval{}, fmt.Errorf("work order is not ready for approval: %s", order.State)
	}
	if err = requireRosterConsentV2(order, request.RosterConsent); err != nil {
		return domain.WorkOrderApproval{}, err
	}
	// Ростер проверяется до каталогов и до транзакции. Утверждение заводит
	// агентов в обход SaveProjectAgent, то есть мимо лимитов и проверки
	// определения: без этих двух гейтов наряд создавал исполнителя, который
	// рождался заблокированным, и человек узнавал об этом из провала квеста.
	if err = a.requireRosterRunnableV2(ctx, order); err != nil {
		return domain.WorkOrderApproval{}, err
	}
	created := false
	if order.Workspace.Mode == "managed" {
		owner, ownerErr := a.store.WorkOrderWorkspaceOwnerV2(ctx, order.Workspace.Path)
		if ownerErr != nil {
			return domain.WorkOrderApproval{}, ownerErr
		}
		if owner == order.ID {
			hasApproval, approvalErr := a.store.WorkOrderHasApprovalV2(ctx, order.ID)
			if approvalErr != nil {
				return domain.WorkOrderApproval{}, approvalErr
			}
			if info, statErr := os.Stat(order.Workspace.Path); hasApproval && statErr == nil && info.IsDir() {
				approval, approveErr := a.store.ApproveWorkOrderV2(ctx, id, request.Version, request.Digest, request.IdempotencyKey)
				if approveErr != nil {
					return domain.WorkOrderApproval{}, approveErr
				}
				return a.resumeApprovedWorkOrderV2(ctx, approval, request.APIKey)
			}
		}
		if err = os.MkdirAll(filepath.Dir(order.Workspace.Path), 0700); err != nil {
			return domain.WorkOrderApproval{}, err
		}
		if err = os.Mkdir(order.Workspace.Path, 0700); err != nil {
			if os.IsExist(err) {
				return domain.WorkOrderApproval{}, errors.New("managed project path now exists; revise the work order to choose a new path")
			}
			return domain.WorkOrderApproval{}, err
		}
		created = true
		if order.Workspace.InitializeGit {
			command := osproc.CommandContext(ctx, "git", "-C", order.Workspace.Path, "init")
			if output, initErr := command.CombinedOutput(); initErr != nil {
				_ = os.RemoveAll(order.Workspace.Path)
				return domain.WorkOrderApproval{}, fmt.Errorf("initialize managed Git repository: %w: %s", initErr, strings.TrimSpace(string(output)))
			}
		}
	}
	approval, err := a.store.ApproveWorkOrderV2(ctx, id, request.Version, request.Digest, request.IdempotencyKey)
	if err != nil && created {
		_ = os.RemoveAll(order.Workspace.Path)
	}
	if err != nil {
		return domain.WorkOrderApproval{}, err
	}
	a.recordMasterEvidence(ctx, approval.WorkOrder, approval.QuestID, "approval", "approved")
	return a.resumeApprovedWorkOrderV2(ctx, approval, request.APIKey)
}

// requireRosterRunnableV2 проверяет обе половины ростера: существующие агенты
// обязаны быть готовы теми же правилами, что показывает их карточка, а новые
// черновики — опираться на инструменты, которые в этой сборке есть.
func (a *App) requireRosterRunnableV2(ctx context.Context, order domain.WorkOrder) error {
	for _, draft := range order.Roster.Permanent {
		if !draft.Existing {
			return fmt.Errorf("agent %q must be created from its staffing card before launch", draft.Name)
		}
	}
	if len(order.Roster.Permanent) == 0 {
		return errors.New("work order roster has no runnable project agents")
	}
	existing := make([]string, 0, len(order.Roster.Permanent))
	for _, draft := range order.Roster.Permanent {
		if draft.Existing {
			existing = append(existing, draft.ID)
			continue
		}
		known := a.filterKnownTools(ctx, draft.RequiredTools)
		if len(known) != len(draft.RequiredTools) {
			missing := missingRosterTools(known, draft.RequiredTools)
			return fmt.Errorf("агент %q просит инструменты, которых нет в этой сборке: %s", draft.Name, strings.Join(missing, ", "))
		}
	}
	if len(existing) == 0 {
		return nil
	}
	return a.requireProjectAgentsReady(ctx, order.WorkspaceID, existing)
}

// requireRosterConsentV2 не даёт утвердить наряд, который молча создаст нового
// агента. Ранее созданные исполнители помечены Existing и через гейт проходят,
// поэтому старые наряды не ломаются.
func requireRosterConsentV2(order domain.WorkOrder, consent []string) error {
	given := make(map[string]bool, len(consent))
	for _, id := range consent {
		if id = strings.TrimSpace(id); id != "" {
			given[id] = true
		}
	}
	missing := []string{}
	for _, draft := range order.Roster.Permanent {
		if !draft.RequiresConsent || draft.Existing || given[strings.TrimSpace(draft.ID)] {
			continue
		}
		name := strings.TrimSpace(draft.Name)
		if name == "" {
			name = strings.TrimSpace(draft.Role)
		}
		if name == "" {
			name = draft.ID
		}
		missing = append(missing, fmt.Sprintf("%q (%s)", name, draft.ID))
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("approval creates new project agents and needs explicit consent for %s", strings.Join(missing, ", "))
}

func (a *App) prepareWorkOrderWorkspaceV2(ctx context.Context, order domain.WorkOrder) (domain.WorkOrder, error) {
	if order.ID == "" {
		order.ID = domain.NewID("workorder")
	}
	plan := order.Workspace
	plan.Mode = strings.ToLower(strings.TrimSpace(plan.Mode))
	if plan.Mode == "managed" {
		home, err := os.UserHomeDir()
		if err != nil {
			return order, err
		}
		root := filepath.Join(home, "Point", "Projects")
		generated := strings.TrimSpace(plan.Path) == ""
		if generated {
			base := workOrderSlug(order.Goal)
			for suffix := 1; suffix <= 999; suffix++ {
				name := base
				if suffix > 1 {
					name = fmt.Sprintf("%s-%d", base, suffix)
				}
				candidate := filepath.Join(root, name)
				owner, lookupErr := a.store.WorkOrderWorkspaceOwnerV2(ctx, candidate)
				if lookupErr != nil {
					return order, lookupErr
				}
				if _, statErr := os.Stat(candidate); os.IsNotExist(statErr) && owner == "" {
					plan.Path = candidate
					break
				}
			}
			if plan.Path == "" {
				return order, errors.New("could not allocate a unique managed project path")
			}
		}
		absolute, err := filepath.Abs(plan.Path)
		if err != nil {
			return order, err
		}
		rootAbs, _ := filepath.Abs(root)
		relative, err := filepath.Rel(rootAbs, absolute)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return order, fmt.Errorf("managed projects must be direct descendants of %s", rootAbs)
		}
		owner, lookupErr := a.store.WorkOrderWorkspaceOwnerV2(ctx, absolute)
		if lookupErr != nil {
			return order, lookupErr
		}
		if _, err = os.Stat(absolute); !os.IsNotExist(err) {
			if err == nil && owner != order.ID {
				return order, errors.New("managed project path already exists")
			}
			if err != nil {
				return order, err
			}
		}
		if owner != "" && owner != order.ID {
			return order, errors.New("managed project path is reserved by another work order")
		}
		plan.Path, plan.Isolation = absolute, "snapshot"
		if order.WorkspaceID == "" {
			order.WorkspaceID = domain.NewID("ws")
		}
	} else if plan.Mode == "existing" {
		absolute, err := filepath.Abs(strings.TrimSpace(plan.Path))
		if err != nil {
			return order, err
		}
		info, err := os.Stat(absolute)
		if err != nil || !info.IsDir() {
			return order, errors.New("existing workspace must be an accessible directory")
		}
		plan.Path = absolute
		if plan.Isolation == "" {
			if isCleanGitWorkspace(absolute) {
				plan.Isolation = "git_worktree"
			} else {
				plan.Isolation = "snapshot"
			}
		}
		if plan.Isolation == "git_worktree" && !isCleanGitWorkspace(absolute) {
			plan.Isolation = "snapshot"
		}
		stored, lookupErr := a.store.WorkspaceByPath(ctx, absolute)
		if lookupErr == nil {
			order.WorkspaceID = stored.ID
		} else if storage.IsNotFound(lookupErr) && order.WorkspaceID == "" {
			order.WorkspaceID = domain.NewID("ws")
		} else if lookupErr != nil && !storage.IsNotFound(lookupErr) {
			return order, lookupErr
		}
	}
	order.Workspace = plan
	return order, nil
}

func workOrderSlug(value string) string {
	var output strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			output.WriteRune(r)
			lastDash = false
		} else if !lastDash && output.Len() > 0 {
			output.WriteByte('-')
			lastDash = true
		}
		if output.Len() >= 48 {
			break
		}
	}
	result := strings.Trim(output.String(), "-")
	if result == "" {
		return "project"
	}
	return result
}

func isCleanGitWorkspace(path string) bool {
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return false
	}
	cmd := osproc.Command("git", "-C", path, "status", "--porcelain", "--untracked-files=normal")
	output, err := cmd.Output()
	return err == nil && len(strings.TrimSpace(string(output))) == 0
}
