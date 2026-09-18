package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"local-agent-workbench/internal/osproc"
	"local-agent-workbench/internal/domain"
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
	approval, err := a.store.WorkOrderApprovalByQuestV2(ctx, questID)
	if err != nil {
		return domain.DeliveredApplicationControl{}, err
	}
	if approval.WorkOrder.Version != request.Version || domain.WorkOrderDigest(approval.WorkOrder) != request.WorkOrderDigest {
		return domain.DeliveredApplicationControl{}, errors.New("application action does not match the approved work order version and digest")
	}
	bundle, err := a.store.GetEvidenceBundle(ctx, questID)
	if err != nil {
		return domain.DeliveredApplicationControl{}, err
	}
	status, gateErr := domain.WorkOrderEvidenceStatus(approval.WorkOrder, bundle)
	if gateErr != nil || status != domain.QuestCompleted || bundle.DeliveryReceipt == nil {
		return domain.DeliveredApplicationControl{}, errors.New("only a completed verified delivery can be started or stopped")
	}
	receipt := bundle.DeliveryReceipt
	if receipt.ID != request.DeliveryReceiptID || receipt.WorkOrderDigest != request.WorkOrderDigest || filepath.Clean(receipt.Target) != filepath.Clean(approval.WorkOrder.Workspace.Path) {
		return domain.DeliveredApplicationControl{}, errors.New("delivery receipt does not match the approved target")
	}
	composeFile := strings.TrimSpace(receipt.ComposeFile)
	if composeFile == "" {
		composeFile, err = discoverComposeFileV2(receipt.Target)
		if err != nil {
			return domain.DeliveredApplicationControl{}, err
		}
	}
	if filepath.Base(composeFile) != composeFile {
		return domain.DeliveredApplicationControl{}, errors.New("delivery receipt compose path is not a root file")
	}
	composePath := filepath.Join(receipt.Target, composeFile)
	if info, statErr := os.Stat(composePath); statErr != nil || info.IsDir() {
		return domain.DeliveredApplicationControl{}, errors.New("delivery receipt compose file is unavailable")
	}
	control := domain.DeliveredApplicationControl{
		QuestID: questID, DeliveryReceiptID: receipt.ID, WorkOrderDigest: request.WorkOrderDigest,
		Action: action, URL: receipt.URL,
	}
	control, replayed, err := a.store.BeginDeliveredAppControlV2(ctx, request.IdempotencyKey, control)
	if err != nil || replayed {
		return control, err
	}
	runner := a.deliveredAppRunner
	if runner == nil {
		runner = dockerDeliveredAppRunner{}
	}
	arguments := []string{"compose", "-f", composePath}
	if action == "start" {
		arguments = append(arguments, "up", "-d")
	} else {
		arguments = append(arguments, "stop")
	}
	output, runErr := runner.Run(ctx, receipt.Target, arguments...)
	control.Summary = security.Redact(strings.TrimSpace(output))
	if len(control.Summary) > 2000 {
		control.Summary = control.Summary[:2000]
	}
	if runErr != nil {
		control.Status = "failed"
		if control.Summary == "" {
			control.Summary = security.Redact(runErr.Error())
		}
	} else if action == "start" {
		control.Status = "running"
	} else {
		control.Status = "stopped"
	}
	if err = a.store.FinishDeliveredAppControlV2(ctx, request.IdempotencyKey, control); err != nil {
		return domain.DeliveredApplicationControl{}, fmt.Errorf("journal delivered application action: %w", err)
	}
	return control, nil
}
