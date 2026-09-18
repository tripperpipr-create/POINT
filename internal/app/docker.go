package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"local-agent-workbench/internal/tools"
)

// DockerOverview is the IDE panel payload for local Docker interaction.
type DockerOverview struct {
	Status     map[string]any   `json:"status"`
	Containers []map[string]any `json:"containers"`
	Images     []map[string]any `json:"images"`
	Error      string           `json:"error,omitempty"`
}

type DockerContainerActionRequest struct {
	Action    string `json:"action"`
	Container string `json:"container"`
}

// DockerOverview gathers CLI/daemon status plus container and image lists for the IDE panel.
func (a *App) DockerOverview() (DockerOverview, error) {
	if _, err := a.requireWorkspace(); err != nil {
		return DockerOverview{}, err
	}
	inspect := tools.DockerInspect{}
	statusResult := inspect.Execute(context.Background(), json.RawMessage(`{"action":"status"}`))
	overview := DockerOverview{Containers: []map[string]any{}, Images: []map[string]any{}}
	if statusResult.OK {
		_ = json.Unmarshal(statusResult.Output, &overview.Status)
	} else if statusResult.Error != nil {
		overview.Status = map[string]any{"available": false, "daemon": false, "error": statusResult.Error.Message}
	}
	available, _ := overview.Status["available"].(bool)
	daemon, _ := overview.Status["daemon"].(bool)
	if !available || !daemon {
		if overview.Status == nil {
			overview.Status = map[string]any{}
		}
		return overview, nil
	}
	psResult := inspect.Execute(context.Background(), json.RawMessage(`{"action":"ps","all":true}`))
	if psResult.OK {
		var payload struct {
			Containers []map[string]any `json:"containers"`
		}
		_ = json.Unmarshal(psResult.Output, &payload)
		if payload.Containers != nil {
			overview.Containers = payload.Containers
		}
	} else if psResult.Error != nil {
		overview.Error = psResult.Error.Message
	}
	imagesResult := inspect.Execute(context.Background(), json.RawMessage(`{"action":"images"}`))
	if imagesResult.OK {
		var payload struct {
			Images []map[string]any `json:"images"`
		}
		_ = json.Unmarshal(imagesResult.Output, &payload)
		if payload.Images != nil {
			overview.Images = payload.Images
		}
	} else if imagesResult.Error != nil && overview.Error == "" {
		overview.Error = imagesResult.Error.Message
	}
	return overview, nil
}

// DockerContainerLogs returns a capped log tail for the IDE panel.
func (a *App) DockerContainerLogs(container string, tail int) (map[string]any, error) {
	if _, err := a.requireWorkspace(); err != nil {
		return nil, err
	}
	if err := tools.ValidateDockerRef(container); err != nil {
		return nil, err
	}
	if tail <= 0 {
		tail = 100
	}
	if tail > 200 {
		tail = 200
	}
	raw, _ := json.Marshal(map[string]any{"action": "logs", "container": strings.TrimSpace(container), "tail": tail})
	result := (tools.DockerInspect{}).Execute(context.Background(), raw)
	if !result.OK {
		if result.Error != nil {
			return nil, errors.New(result.Error.Message)
		}
		return nil, errors.New("docker logs failed")
	}
	var payload map[string]any
	if err := json.Unmarshal(result.Output, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// DockerContainerAction starts or stops a container for an explicit IDE user action.
func (a *App) DockerContainerAction(req DockerContainerActionRequest) (map[string]any, error) {
	if _, err := a.requireWorkspace(); err != nil {
		return nil, err
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action != "start" && action != "stop" {
		return nil, fmt.Errorf("action must be start or stop")
	}
	if err := tools.ValidateDockerRef(req.Container); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(map[string]any{
		"action": action, "container": strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(req.Container), "/")),
		"reason": "Действие пользователя из панели Docker Point",
	})
	result := (tools.DockerControl{}).Execute(context.Background(), raw)
	if !result.OK {
		if result.Error != nil {
			return nil, errors.New(result.Error.Message)
		}
		return nil, errors.New("docker control failed")
	}
	var payload map[string]any
	if err := json.Unmarshal(result.Output, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}
