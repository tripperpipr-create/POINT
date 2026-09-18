package app

import (
	"context"
	"sort"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
)

// CompiledPromptPreview is the exact system message the runtime would send.
// The constructor must display this instead of a separate JS compiler.
type CompiledPromptPreview struct {
	SystemMessage  string `json:"systemMessage"`
	IdentityPrompt string `json:"identityPrompt"`
	Provider       string `json:"provider"`
}

// PreviewCompiledPrompt compiles an unsaved Project Agent draft with the same
// identity compiler, skill enrichment and execution contract as StartRun.
func (a *App) PreviewCompiledPrompt(draft domain.ProjectAgent) (CompiledPromptPreview, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return CompiledPromptPreview{}, err
	}
	if draft.WorkspaceID == "" {
		draft.WorkspaceID = ws.ID
	}
	if err = a.guardWorld(draft.WorkspaceID); err != nil {
		return CompiledPromptPreview{}, err
	}
	profile := domain.ProfileFromProjectAgent(draft)
	if err = a.enrichProjectAgentForRun(ws.ID, draft, &profile, nil); err != nil {
		return CompiledPromptPreview{}, err
	}
	customTools, err := a.store.ListCustomTools(context.Background())
	if err != nil {
		return CompiledPromptPreview{}, err
	}
	sort.Slice(customTools, func(i, j int) bool { return customTools[i].ID < customTools[j].ID })
	systemMessage := profile.SystemPrompt
	if profile.Provider != domain.ProviderCursor {
		systemMessage = agent.SystemMessage(profile, customTools)
	}
	return CompiledPromptPreview{
		SystemMessage:  systemMessage,
		IdentityPrompt: domain.CompileProjectAgentPrompt(draft),
		Provider:       string(profile.Provider),
	}, nil
}
