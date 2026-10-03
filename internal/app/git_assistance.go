package app

import (
	"context"
	"encoding/json"
	"errors"
	"local-agent-workbench/internal/forge"
	"local-agent-workbench/internal/gitflow"
	"local-agent-workbench/internal/orchestrator"
)

type GitSuggestionInput struct {
	GitTarget
	Purpose  string         `json:"purpose"`
	Revision string         `json:"revision"`
	Text     string         `json:"text,omitempty"`
	Forge    *forge.Request `json:"forge,omitempty"`
	APIKey   string         `json:"apiKey,omitempty"`
}
type GitSuggestion struct {
	Revision string `json:"revision"`
	Purpose  string `json:"purpose"`
	Text     string `json:"text"`
	Patch    string `json:"patch,omitempty"`
}

func (a *App) GitAssistanceConfig(ctx context.Context, t GitTarget) (any, error) {
	id, _, e := a.gitWorkbenchRoot(ctx, t)
	if e != nil {
		return nil, e
	}
	cfg, e := a.gitAgentConfig(ctx, id)
	return map[string]string{"connectionId": cfg.ConnectionID}, e
}
func (a *App) GitSuggest(ctx context.Context, q GitSuggestionInput) (GitSuggestion, error) {
	id, root, e := a.gitWorkbenchRoot(ctx, q.GitTarget)
	if e != nil {
		return GitSuggestion{}, e
	}
	source := q.Text
	if q.Forge != nil {
		request := *q.Forge
		request.Action = "review"
		v, e := a.RunForgeRequest(ctx, request)
		if e != nil {
			return GitSuggestion{}, e
		}
		raw, _ := json.Marshal(v.Data)
		var view struct {
			SHA string `json:"sha"`
		}
		_ = json.Unmarshal(raw, &view)
		if q.Revision == "" || view.SHA != q.Revision {
			return GitSuggestion{}, errors.New("MR изменился: обновите карточку")
		}
	} else {
		s, e := gitflow.ReadSnapshot(ctx, a.gitInspectRunner(), root)
		if e != nil {
			return GitSuggestion{}, e
		}
		if q.Revision == "" || s.Revision != q.Revision {
			return GitSuggestion{}, gitflow.ErrStaleRevision
		}
		area := "working"
		if q.Purpose == "commit" {
			area = "staged"
		}
		source, e = gitflow.Diff(ctx, a.gitInspectRunner(), root, "", area)
		if e != nil {
			return GitSuggestion{}, e
		}
	}
	if len(source) > 64000 {
		source = source[:64000]
	}
	instructions := map[string]string{
		"commit":      "Предложи сообщение коммита по подготовленным изменениям.",
		"description": "Предложи описание MR: что изменено, почему, проверка.",
		"diff":        "Объясни изменения и существенные риски.",
		"ci":          "Объясни CI по фактам; предложи следующий шаг.",
		"conflict":    "Предложи разрешение конфликтов, при уверенности верни unified patch для рабочего файла, иначе только объяснение.",
	}
	prompt, ok := instructions[q.Purpose]
	if !ok {
		return GitSuggestion{}, errors.New("неизвестный вид помощи")
	}
	cfg, e := a.gitAgentConfig(ctx, id)
	if e != nil {
		return GitSuggestion{}, e
	}
	text, patch, e := orchestrator.SuggestGit(ctx, cfg, q.APIKey, prompt, source, a.gitAgentFactory(id))
	return GitSuggestion{Revision: q.Revision, Purpose: q.Purpose, Text: text, Patch: patch}, e
}
