package agent

import (
	"encoding/json"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

type persistedConversationHistory struct {
	Stable []providers.Message  `json:"stable"`
	Rounds []conversationRound  `json:"rounds"`
	Memory []contextMemoryEntry `json:"memory"`
}

func (h *conversationHistory) marshal() ([]byte, error) {
	if h == nil {
		return json.Marshal(persistedConversationHistory{})
	}
	return json.Marshal(persistedConversationHistory{
		Stable: append([]providers.Message(nil), h.stable...),
		Rounds: append([]conversationRound(nil), h.rounds...),
		Memory: append([]contextMemoryEntry(nil), h.memory...),
	})
}

func unmarshalConversationHistory(raw []byte) (*conversationHistory, error) {
	var persisted persistedConversationHistory
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &persisted); err != nil {
			return nil, err
		}
	}
	return &conversationHistory{
		stable: append([]providers.Message(nil), persisted.Stable...),
		rounds: append([]conversationRound(nil), persisted.Rounds...),
		memory: append([]contextMemoryEntry(nil), persisted.Memory...),
	}, nil
}

type persistedCompletionTracker struct {
	ExplicitVerification           bool                           `json:"explicitVerification"`
	VerificationToolAvailable      bool                           `json:"verificationToolAvailable"`
	CommandAttempts                int                            `json:"commandAttempts"`
	SuccessfulVerificationRevision int                            `json:"successfulVerificationRevision"`
	LatestExitCode                 *int                           `json:"latestExitCode,omitempty"`
	LatestTimedOut                 bool                           `json:"latestTimedOut"`
	LatestCommandRecognized        bool                           `json:"latestCommandRecognized"`
	VerificationTools              []string                       `json:"verificationTools,omitempty"`
	SuggestedTools                 []string                       `json:"suggestedTools,omitempty"`
	Checks                         map[string]verificationAttempt `json:"checks,omitempty"`
	Brief                          *domain.TaskBrief              `json:"brief,omitempty"`
}

func (t *completionTracker) marshal() ([]byte, error) {
	if t == nil {
		return json.Marshal(persistedCompletionTracker{})
	}
	tools := make([]string, 0, len(t.verificationTools))
	for name := range t.verificationTools {
		tools = append(tools, name)
	}
	checks := make(map[string]verificationAttempt, len(t.checks))
	for key, value := range t.checks {
		checks[key] = value
	}
	return json.Marshal(persistedCompletionTracker{
		ExplicitVerification:           t.explicitVerification,
		VerificationToolAvailable:      t.verificationToolAvailable,
		CommandAttempts:                t.commandAttempts,
		SuccessfulVerificationRevision: t.successfulVerificationRevision,
		LatestExitCode:                 t.latestExitCode,
		LatestTimedOut:                 t.latestTimedOut,
		LatestCommandRecognized:        t.latestCommandRecognized,
		VerificationTools:              tools,
		SuggestedTools:                 append([]string(nil), t.suggestedTools...),
		Checks:                         checks,
		Brief:                          t.brief,
	})
}

func unmarshalCompletionTracker(raw []byte) (*completionTracker, error) {
	var persisted persistedCompletionTracker
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &persisted); err != nil {
			return nil, err
		}
	}
	tools := make(map[string]struct{}, len(persisted.VerificationTools))
	for _, name := range persisted.VerificationTools {
		tools[name] = struct{}{}
	}
	checks := persisted.Checks
	if checks == nil {
		checks = map[string]verificationAttempt{}
	}
	return &completionTracker{
		explicitVerification:           persisted.ExplicitVerification,
		verificationToolAvailable:      persisted.VerificationToolAvailable,
		commandAttempts:                persisted.CommandAttempts,
		successfulVerificationRevision: persisted.SuccessfulVerificationRevision,
		latestExitCode:                 persisted.LatestExitCode,
		latestTimedOut:                 persisted.LatestTimedOut,
		latestCommandRecognized:        persisted.LatestCommandRecognized,
		verificationTools:              tools,
		suggestedTools:                 append([]string(nil), persisted.SuggestedTools...),
		checks:                         checks,
		brief:                          persisted.Brief,
	}, nil
}

type persistedObservationTracker struct {
	Files     map[string][]fileObservation    `json:"files,omitempty"`
	Workspace map[string]workspaceObservation `json:"workspace,omitempty"`
}

func (t *observationTracker) marshal() ([]byte, error) {
	if t == nil {
		return json.Marshal(persistedObservationTracker{})
	}
	files := make(map[string][]fileObservation, len(t.files))
	for key, value := range t.files {
		files[key] = append([]fileObservation(nil), value...)
	}
	workspace := make(map[string]workspaceObservation, len(t.workspace))
	for key, value := range t.workspace {
		workspace[key] = value
	}
	return json.Marshal(persistedObservationTracker{Files: files, Workspace: workspace})
}

func unmarshalObservationTracker(raw []byte) (*observationTracker, error) {
	var persisted persistedObservationTracker
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &persisted); err != nil {
			return nil, err
		}
	}
	files := persisted.Files
	if files == nil {
		files = map[string][]fileObservation{}
	}
	workspace := persisted.Workspace
	if workspace == nil {
		workspace = map[string]workspaceObservation{}
	}
	return &observationTracker{files: files, workspace: workspace}, nil
}
