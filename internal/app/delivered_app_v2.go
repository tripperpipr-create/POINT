package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/osproc"
	"local-agent-workbench/internal/security"
)

type DeliveredAppRunner interface {
	Run(context.Context, string, ...string) (string, error)
}

type dockerDeliveredAppRunner struct{}

func (dockerDeliveredAppRunner) Run(ctx context.Context, directory string, arguments ...string) (string, error) {
	command := osproc.CommandContext(ctx, "docker", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	return string(output), err
}

type DeliveredApplicationControlRequestV2 struct {
	Version           int    `json:"version"`
	WorkOrderDigest   string `json:"workOrderDigest"`
	DeliveryReceiptID string `json:"deliveryReceiptId"`
	IdempotencyKey    string `json:"idempotencyKey"`
}

func discoverComposeFileV2(root string) (string, error) {
	for _, name := range []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"} {
		path := filepath.Join(root, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return name, nil
		}
	}
	return "", errors.New("delivered workspace has no root Docker Compose file")
}

func (a *App) ControlDeliveredApplicationV2(ctx context.Context, questID, action string, request DeliveredApplicationControlRequestV2) (domain.DeliveredApplicationControl, error) {
	questID, action = strings.TrimSpace(questID), strings.ToLower(strings.TrimSpace(action))
	request.WorkOrderDigest = strings.TrimSpace(request.WorkOrderDigest)
	request.DeliveryReceiptID = strings.TrimSpace(request.DeliveryReceiptID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if questID == "" || (action != "start" && action != "stop") || request.Version <= 0 || request.WorkOrderDigest == "" || request.DeliveryReceiptID == "" || request.IdempotencyKey == "" {
		return domain.DeliveredApplicationControl{}, errors.New("quest, start/stop action, version, digest, deliveryReceiptId and idempotencyKey are required")
	}
	target, err := a.resolveDeliveredAppTargetV2(ctx, questID)
	if err != nil {
		return domain.DeliveredApplicationControl{}, err
	}
	approval, bundle, receipt := target.approval, target.bundle, target.receipt
	if approval.WorkOrder.Version != request.Version || domain.WorkOrderDigest(approval.WorkOrder) != request.WorkOrderDigest {
		return domain.DeliveredApplicationControl{}, errors.New("application action does not match the approved work order version and digest")
	}
	status, gateErr := domain.WorkOrderEvidenceStatus(approval.WorkOrder, bundle)
	if gateErr != nil || status != domain.QuestCompleted {
		return domain.DeliveredApplicationControl{}, errors.New("only a completed verified delivery can be started or stopped")
	}
	if receipt.ID != request.DeliveryReceiptID || receipt.WorkOrderDigest != request.WorkOrderDigest {
		return domain.DeliveredApplicationControl{}, errors.New("delivery receipt does not match the approved target")
	}
	if target.composePath == "" {
		return domain.DeliveredApplicationControl{}, errors.New("delivered workspace has no root Docker Compose file")
	}
	control := domain.DeliveredApplicationControl{
		QuestID: questID, DeliveryReceiptID: receipt.ID, WorkOrderDigest: request.WorkOrderDigest,
		Action: action, URL: deliveredAppURLV2(target),
	}
	// Второе действие поверх идущего запустило бы ту же сборку дважды; повтор
	// того же ключа отвечает из журнала и в реестр не попадает.
	if !a.deliveredApps.begin(questID, action) {
		return domain.DeliveredApplicationControl{}, errors.New("an application action for this quest is already running")
	}
	control, replayed, err := a.store.BeginDeliveredAppControlV2(ctx, request.IdempotencyKey, control)
	if err != nil || replayed {
		a.deliveredApps.finish(questID, control)
		return control, err
	}
	// Команда живёт своим сроком, а не сроком запроса: клиент, который
	// перестал ждать ответа, не должен обрывать `compose up` на середине сборки.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Minute)
	defer cancel()
	runner := a.deliveredAppRunnerOrDefault()
	inspector, inspectable := runner.(deliveredAppInspector)
	say := func(line string) { a.deliveredApps.line(questID, line) }
	arguments := []string{"compose", "-f", target.composePath}
	if action == "start" {
		arguments = append(arguments, "up", "-d")
	} else {
		arguments = append(arguments, "stop")
	}
	say("$ docker compose -f " + target.composeFile + " " + strings.Join(arguments[3:], " "))
	var output string
	var runErr error
	if inspectable {
		output, runErr = inspector.RunStreaming(runCtx, receipt.Target, say, arguments...)
	} else {
		output, runErr = runner.Run(runCtx, receipt.Target, arguments...)
		for _, line := range strings.Split(output, "\n") {
			say(line)
		}
	}
	control.Summary = security.Redact(strings.TrimSpace(output))
	if len(control.Summary) > 2000 {
		control.Summary = control.Summary[len(control.Summary)-2000:]
	}
	if runErr != nil {
		control.Status = "failed"
		if control.Summary == "" {
			control.Summary = security.Redact(runErr.Error())
		}
		say("Команда завершилась ошибкой: " + security.Redact(runErr.Error()))
	} else if action == "start" {
		control.Status = "running"
		if inspectable && loopbackURL(control.URL) {
			control.Ready, control.HTTPStatus, control.ContentType = waitDeliveredAppReady(runCtx, inspector, control.URL, say)
		} else if inspectable {
			if services, probeErr := inspector.RunningServices(runCtx, receipt.Target, target.composePath); probeErr == nil {
				say(fmt.Sprintf("Работает контейнеров: %d", services))
			}
		}
	} else {
		control.Status = "stopped"
	}
	a.deliveredApps.finish(questID, control)
	if err = a.store.FinishDeliveredAppControlV2(runCtx, request.IdempotencyKey, control); err != nil {
		return domain.DeliveredApplicationControl{}, fmt.Errorf("journal delivered application action: %w", err)
	}
	return control, nil
}
