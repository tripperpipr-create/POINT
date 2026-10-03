package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/environment"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/security"
)

// Образ песочницы закрепляется до утверждения (TODO Q17).
//
// Прежде образ выбирался заново на каждом этапе: пересобранный или
// заменённый локальный тег исполнялся под утверждением, данным другому
// образу. Теперь готовый к утверждению наряд получает следующей версией
// дайджест образа, который получит его песочница, и отпечаток требований, по
// которым он выбран. Утвердить наряд без закреплённого образа нельзя, а
// каждое создание песочницы сверяет дайджест (sandbox.ErrRuntimeImageChanged).
// Бэкенд без образов (локальная копия) ничего не закрепляет.

// workOrderImagePinTimeout — разрешение может собрать runtime-образ.
const workOrderImagePinTimeout = 20 * time.Minute

var errWorkOrderImagePending = errors.New("образ песочницы этого наряда ещё проверяется; утвердите наряд, когда образ будет закреплён")

func (a *App) workOrderImageResolverV2() (sandbox.ImageResolver, bool) {
	resolver, ok := a.sandboxBackend.(sandbox.ImageResolver)
	return resolver, ok
}

// workOrderImageBasisV2 — отпечаток требований наряда к образу: дайджест,
// закреплённый по другим требованиям, недействителен.
func workOrderImageBasisV2(order domain.WorkOrder) string {
	requirements := environment.RuntimeRequirementsForWorkOrder(&order)
	raw, _ := json.Marshal(struct {
		ID, Version                                            string
		Commands, Unsupported, Conflicts, Packages, Candidates []string
		Versions                                               map[string]string
	}{requirements.ID, requirements.Version, requirements.RequiredCommands, requirements.UnsupportedTools,
		requirements.VersionConflicts, requirements.Packages, requirements.CandidateImages, requirements.ToolVersions})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

// withCurrentImagePinV2 снимает закреплённый образ, если требования наряда
// изменились: следующий закрепит образ заново.
func withCurrentImagePinV2(order domain.WorkOrder) domain.WorkOrder {
	if order.Sandbox.ImageDigest != "" && order.Sandbox.ImageBasis != workOrderImageBasisV2(order) {
		order.Sandbox.Image, order.Sandbox.ImageDigest, order.Sandbox.ImageBasis = "", "", ""
	}
	return order
}

// workOrderImagePinnedV2 — образ наряда закреплён по его нынешним требованиям
// или закреплять нечего.
func (a *App) workOrderImagePinnedV2(order domain.WorkOrder) bool {
	if _, ok := a.workOrderImageResolverV2(); !ok {
		return true
	}
	return order.Sandbox.ImageDigest != "" && order.Sandbox.ImageBasis == workOrderImageBasisV2(order)
}

// startWorkOrderImagePinV2 закрепляет образ готового наряда в фоне.
func (a *App) startWorkOrderImagePinV2(order domain.WorkOrder) {
	if order.State != "ready" || a.workOrderImagePinnedV2(order) {
		return
	}
	id := order.ID
	a.staffing.start("image:"+id, workOrderImagePinTimeout, false, func(ctx context.Context) {
		if err := a.pinWorkOrderImageV2(ctx, id); err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("work order image pin failed", "work_order_id", id, "error", security.Redact(err.Error()))
		}
	})
}

func (a *App) pinWorkOrderImageV2(ctx context.Context, id string) error {
	resolver, ok := a.workOrderImageResolverV2()
	if !ok {
		return nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		current, err := a.store.GetWorkOrderV2(ctx, id)
		if err != nil {
			return err
		}
		if current.State != "ready" || a.workOrderImagePinnedV2(current) {
			return nil
		}
		image, digest, resolveErr := resolver.ResolveRuntimeImage(ctx, environment.RuntimeRequirementsForWorkOrder(&current))
		if ctx.Err() != nil {
			return ctx.Err()
		}
		changed, err := a.applyWorkOrderImagePinV2(ctx, current, image, digest, resolveErr)
		if changed {
			continue
		}
		if err != nil {
			return err
		}
		return resolveErr
	}
	return errors.New("work order kept changing while its sandbox image was being pinned")
}

// applyWorkOrderImagePinV2 пишет следующую версию с образом или причиной
// отказа под замком сохранения. changed — наряд успел смениться.
func (a *App) applyWorkOrderImagePinV2(ctx context.Context, basis domain.WorkOrder, image, digest string, resolveErr error) (bool, error) {
	a.staffing.save.Lock()
	defer a.staffing.save.Unlock()
	latest, err := a.store.GetWorkOrderV2(ctx, basis.ID)
	if err != nil {
		return false, err
	}
	if latest.Version != basis.Version || latest.Digest != basis.Digest {
		return true, nil
	}
	next := latest
	if resolveErr != nil {
		message := strings.TrimSpace(security.Redact(resolveErr.Error()))
		if message == latest.Sandbox.ImageError {
			return false, nil
		}
		next.Sandbox.ImageError = message
	} else {
		next.Sandbox.Image, next.Sandbox.ImageDigest = image, digest
		next.Sandbox.ImageBasis, next.Sandbox.ImageError = workOrderImageBasisV2(latest), ""
	}
	next.Version++
	next.UpdatedAt = time.Now().UTC()
	saved, err := a.store.SaveWorkOrderV2(ctx, next)
	if err == nil && resolveErr == nil {
		slog.Info("work order image pinned", "work_order_id", saved.ID, "version", saved.Version, "image", image, "digest", digest)
	}
	return false, err
}

// requireWorkOrderImagePinnedV2 — отказ утверждения без закреплённого образа;
// заодно закрепление запускается заново (например, после включения Docker).
func (a *App) requireWorkOrderImagePinnedV2(order domain.WorkOrder) error {
	if a.workOrderImagePinnedV2(order) {
		return nil
	}
	a.startWorkOrderImagePinV2(order)
	if message := strings.TrimSpace(order.Sandbox.ImageError); message != "" {
		return errors.New("образ песочницы не закреплён: " + message)
	}
	return errWorkOrderImagePending
}

// holdWorkOrderForImageChangeV2 ставит квест наряда на паузу, если песочница
// этапа получила бы не утверждённый образ. Слепой повтор этапа дал бы тот же
// отказ, а исполнение под чужим образом утверждение не покрывает.
func (a *App) holdWorkOrderForImageChangeV2(ctx context.Context, questID string, cause error) bool {
	if !errors.Is(cause, sandbox.ErrRuntimeImageChanged) {
		return false
	}
	approval, err := a.store.WorkOrderApprovalByQuestV2(ctx, questID)
	if err != nil {
		return false
	}
	quest, err := a.workOrderQuestV2(ctx, approval.WorkOrder.WorkspaceID, approval.QuestID)
	if err != nil || launchMovedByHumanV2(quest.Status) {
		return false
	}
	message := workOrderImageChangedMessageV2(cause)
	if err = a.pauseWorkOrderByCoreV2(ctx, quest, message); err != nil {
		slog.Error("work order image change pause not persisted", "quest_id", quest.ID, "error", err)
		return false
	}
	a.publishWorkOrderNoticeV2(ctx, approval, quest, "warning", message)
	return true
}

func workOrderImageChangedMessageV2(cause error) string {
	detail := strings.TrimSpace(strings.TrimPrefix(security.Redact(cause.Error()), sandbox.ErrRuntimeImageChanged.Error()+":"))
	return "Образ песочницы изменился после утверждения (" + detail + "). Под этим утверждением он не исполняется: создайте новую версию наряда — Point закрепит текущий образ."
}
