package orchestrator

import (
	"encoding/json"
	"errors"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

const intakeContextWindowTokens = 128 * 1024

// Preserve the selected contract verbatim. Other proposals are only a bounded
// index for choosing a discussion; their unrelated histories consume no context.
func intakeContextProposals(proposals []domain.QuestProposal, selected string) []domain.QuestProposal {
	result := make([]domain.QuestProposal, 0, 9)
	for _, proposal := range proposals {
		if proposal.ID == selected {
			result = append(result, proposal)
			break
		}
	}
	count := 0
	for _, proposal := range proposals {
		if proposal.ID == selected || proposal.Status != "pending" {
			continue
		}
		result = append(result, domain.QuestProposal{ID: proposal.ID, Title: proposal.Title, Status: proposal.Status})
		count++
		if count == 8 {
			break
		}
	}
	return result
}

// Do not silently truncate agreements or tool results. Include the output and
// native schema allowance in the same conservative estimate used by execution.
func validateIntakeContext(request providers.ModelRequest) error {
	raw, err := json.Marshal(struct {
		Messages []providers.Message
		Tools    []domain.ToolDefinition
		Schema   json.RawMessage
	}{request.Messages, request.Tools, request.JSONSchema})
	if err != nil {
		return err
	}
	if (len(raw)+3)/4+request.MaxOutputTokens > request.ContextWindowTokens {
		return errors.New("контекст обсуждения превышает окно модели; сохранённое задание не изменено, сократите приложенные материалы или начните отдельное обсуждение")
	}
	return nil
}
