package app

import (
	"context"
	"encoding/base64"

	"local-agent-workbench/internal/orchestrator"
)

// ReportArtifactView is transport-safe: XLSX is binary and therefore travels
// as base64, while the extension writes the exact same bytes for every format.
type ReportArtifactView struct {
	AgentID       string `json:"agentId"`
	Model         string `json:"model"`
	Format        string `json:"format"`
	SuggestedName string `json:"suggestedName"`
	MediaType     string `json:"mediaType"`
	ContentBase64 string `json:"contentBase64"`
}

func (a *App) GenerateReport(ctx context.Context, request orchestrator.ReportRequest) (ReportArtifactView, error) {
	workspaceID := a.currentWorldID()
	cfg, err := a.masterConfig(ctx, workspaceID)
	if err != nil {
		return ReportArtifactView{}, err
	}
	// Only user-supplied text is sent to the model. Project facts and files are
	// never attached implicitly; adding sources must remain an explicit action.
	artifact, err := orchestrator.GenerateReport(ctx, cfg, request, a.budgetedModelFactory(modelBudgetScope{
		WorkspaceID: workspaceID, ProjectAgentID: "reporter", Outcome: "report_model",
	}))
	if err != nil {
		return ReportArtifactView{}, err
	}
	return ReportArtifactView{
		AgentID: artifact.AgentID, Model: artifact.Model, Format: artifact.Format,
		SuggestedName: artifact.SuggestedName, MediaType: artifact.MediaType,
		ContentBase64: base64.StdEncoding.EncodeToString(artifact.Content),
	}, nil
}
