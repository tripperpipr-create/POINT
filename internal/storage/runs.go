package storage

import (
	"context"
	"encoding/json"

	"local-agent-workbench/internal/domain"
)

func saveRunWith(ctx context.Context, db sqlExecer, r domain.Run) error {
	contextItems, _ := json.Marshal(r.ContextItems)
	configurationSnapshot, _ := json.Marshal(r.ConfigurationSnapshot)
	tools, _ := json.Marshal(r.ToolsUsed)
	files, _ := json.Marshal(r.ChangedFiles)
	controller := encodeControllerJSON(r.Controller)
	var finished any
	if r.FinishedAt != nil {
		finished = formatTime(*r.FinishedAt)
	}
	_, err := db.ExecContext(ctx, `INSERT INTO runs(id,agent_id,profile_id,workspace_id,task,context_items,configuration_snapshot,provider,model,status,step,request_count,tools_used,changed_files,error,result,started_at,finished_at,duration_ms,controller_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET status=excluded.status,step=excluded.step,request_count=excluded.request_count,tools_used=excluded.tools_used,changed_files=excluded.changed_files,error=excluded.error,result=excluded.result,finished_at=excluded.finished_at,duration_ms=excluded.duration_ms,controller_json=excluded.controller_json`,
		r.ID, r.AgentID, r.ProfileID, r.WorkspaceID, r.Task, string(contextItems), string(configurationSnapshot), r.Provider, r.Model, r.Status, r.Step, r.RequestCount, string(tools), string(files), r.Error, r.Result, formatTime(r.StartedAt), finished, r.DurationMs, controller)
	return err
}
