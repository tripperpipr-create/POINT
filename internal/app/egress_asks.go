package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	workbenchtools "local-agent-workbench/internal/tools"
)

type ResolveEgressAskRequest struct {
	Action string `json:"action"` // allow_once | allow_quest | deny
}

// EnsureEgressAsk records a pending Master→user network/git decision when missing.
func (a *App) EnsureEgressAsk(ctx context.Context, workspaceID, questID, runID string, kind domain.EgressAskKind, target, reason string) (domain.EgressAsk, error) {
	target = strings.TrimSpace(target)
	if workspaceID == "" || target == "" || kind == "" {
		return domain.EgressAsk{}, errors.New("egress ask requires workspace, kind and target")
	}
	if existing, err := a.store.FindPendingEgressAsk(ctx, workspaceID, kind, target); err == nil && existing.ID != "" {
		return existing, nil
	}
	if questID != "" {
		if quests, qErr := a.store.ListQuests(ctx, workspaceID); qErr == nil {
			for _, quest := range quests {
				if quest.ID != questID || !deniedEgressReplay(quest, target) {
					continue
				}
				ask := domain.EgressAsk{
					ID: domain.NewID("egress"), WorkspaceID: workspaceID, QuestID: questID, RunID: runID,
					Kind: kind, Target: target, Reason: "repeat deny; continue offline", Risk: "HIGH",
					Status: domain.EgressAskDenied, CreatedAt: time.Now().UTC(),
				}
				if runID != "" {
					_ = a.InjectRunMessage(runID, egressDenyMessage(ask), "")
				}
				a.emitOrchestratorEvent(workspaceID, runID, questID, map[string]any{
					"code": "egress_repeat_deny", "kind": string(kind), "target": target, "action": "continue_offline",
				})
				return ask, nil
			}
		}
	}
	ask := domain.EgressAsk{
		ID:          domain.NewID("egress"),
		WorkspaceID: workspaceID,
		QuestID:     questID,
		RunID:       runID,
		Kind:        kind,
		Target:      target,
		Reason:      strings.TrimSpace(reason),
		Risk:        "HIGH",
		Status:      domain.EgressAskPending,
		CreatedAt:   time.Now().UTC(),
	}
	if err := a.store.SaveEgressAsk(ctx, ask); err != nil {
		if existing, findErr := a.store.FindPendingEgressAsk(ctx, workspaceID, kind, target); findErr == nil && existing.ID != "" {
			return existing, nil
		}
		return domain.EgressAsk{}, err
	}
	a.emitOrchestratorEvent(workspaceID, runID, questID, map[string]any{
		"code": "egress_ask", "askId": ask.ID, "kind": string(kind), "target": target, "reason": ask.Reason,
	})
	return ask, nil
}

func (a *App) ResolveEgressAsk(ctx context.Context, id string, request ResolveEgressAskRequest) (domain.EgressAsk, error) {
	ask, err := a.store.GetEgressAsk(ctx, id)
	if err != nil {
		return domain.EgressAsk{}, err
	}
	if ask.Kind == domain.EgressAskSupervision {
		return domain.EgressAsk{}, errors.New("use supervision resolve for hang interventions")
	}
	if ask.Status != domain.EgressAskPending {
		return domain.EgressAsk{}, errors.New("egress ask is already resolved")
	}
	if err = a.guardWorld(ask.WorkspaceID); err != nil {
		return domain.EgressAsk{}, err
	}
	action := strings.ToLower(strings.TrimSpace(request.Action))
	now := time.Now().UTC()
	switch action {
	case "allow_once":
		ask.Status = domain.EgressAskAllowedOnce
		a.applyEgressGrant(ask, false)
	case "allow_quest":
		ask.Status = domain.EgressAskAllowedQuest
		a.applyEgressGrant(ask, true)
		if err = a.persistQuestEgressGrant(ctx, ask); err != nil {
			return domain.EgressAsk{}, err
		}
	case "deny":
		ask.Status = domain.EgressAskDenied
		if ask.QuestID != "" && ask.WorkspaceID != "" {
			if quests, qErr := a.store.ListQuests(ctx, ask.WorkspaceID); qErr == nil {
				for _, quest := range quests {
					if quest.ID != ask.QuestID {
						continue
					}
					recordDeniedEgress(&quest, ask.Target)
					_ = a.store.SaveQuest(ctx, quest)
					break
				}
			}
		}
	default:
		return domain.EgressAsk{}, fmt.Errorf("unknown egress action %q", request.Action)
	}
	ask.ResolvedAt = now
	if err = a.store.SaveEgressAsk(ctx, ask); err != nil {
		return domain.EgressAsk{}, err
	}
	if ask.RunID != "" && ask.Status != domain.EgressAskDenied {
		_ = a.InjectRunMessage(ask.RunID, egressAllowMessage(ask), "")
	} else if ask.RunID != "" && ask.Status == domain.EgressAskDenied {
		_ = a.InjectRunMessage(ask.RunID, egressDenyMessage(ask), "")
	}
	a.emitOrchestratorEvent(ask.WorkspaceID, ask.RunID, ask.QuestID, map[string]any{
		"code": "egress_resolved", "askId": ask.ID, "action": action, "target": ask.Target, "kind": string(ask.Kind),
	})
	return ask, nil
}

func (a *App) emitOrchestratorEvent(workspaceID, runID, questID string, payload map[string]any) {
	raw, _ := json.Marshal(payload)
	a.emitEvent(domain.Event{
		ID: domain.NewID("evt"), Type: domain.EventOrchestratorWatch, CreatedAt: time.Now().UTC(),
		WorkspaceID: workspaceID, RunID: runID, QuestID: questID, Actor: "orchestrator", Data: raw,
	})
}

func (a *App) GetEgressAsk(ctx context.Context, id string) (domain.EgressAsk, error) {
	ask, err := a.store.GetEgressAsk(ctx, id)
	if err != nil {
		return domain.EgressAsk{}, err
	}
	if err = a.guardWorld(ask.WorkspaceID); err != nil {
		return domain.EgressAsk{}, err
	}
	return ask, nil
}

func (a *App) applyEgressGrant(ask domain.EgressAsk, questScoped bool) {
	if a.networkGrants == nil {
		a.networkGrants = workbenchtools.NewNetworkGrantBook()
	}
	switch ask.Kind {
	case domain.EgressAskNetworkHost:
		if questScoped {
			a.networkGrants.GrantHostQuest(ask.QuestID, ask.RunID, ask.Target)
		} else {
			a.networkGrants.GrantHostOnce(ask.RunID, ask.Target)
		}
	case domain.EgressAskGitRemote:
		if questScoped {
			a.networkGrants.GrantRemoteQuest(ask.QuestID, ask.RunID, ask.Target)
		} else {
			a.networkGrants.GrantRemoteOnce(ask.RunID, ask.Target)
		}
	}
}

func (a *App) persistQuestEgressGrant(ctx context.Context, ask domain.EgressAsk) error {
	if ask.QuestID == "" {
		return nil
	}
	quests, err := a.store.ListQuests(ctx, ask.WorkspaceID)
	if err != nil {
		return err
	}
	var quest *domain.Quest
	for i := range quests {
		if quests[i].ID == ask.QuestID {
			quest = &quests[i]
			break
		}
	}
	if quest == nil || quest.Brief == nil {
		return nil
	}
	brief := *quest.Brief
	switch ask.Kind {
	case domain.EgressAskNetworkHost:
		host := strings.ToLower(strings.TrimSpace(ask.Target))
		if !containsFold(brief.Permissions.NetworkHosts, host) {
			brief.Permissions.NetworkHosts = append(brief.Permissions.NetworkHosts, host)
		}
	case domain.EgressAskGitRemote:
		if !containsFold(brief.Permissions.ConfirmedGitRemotes, ask.Target) {
			brief.Permissions.ConfirmedGitRemotes = append(brief.Permissions.ConfirmedGitRemotes, strings.TrimSpace(ask.Target))
		}
	}
	brief.Version++
	approved, err := domain.ApproveTaskBrief(brief)
	if err != nil {
		return err
	}
	quest.Brief = &approved
	quest.UpdatedAt = time.Now().UTC()
	if err = a.store.SaveQuest(ctx, *quest); err != nil {
		return err
	}
	if session, listErr := a.store.FindIntakeSessionByQuestID(ctx, ask.QuestID); listErr == nil {
		session.Brief = cloneTaskBrief(&approved)
		if ask.Kind == domain.EgressAskNetworkHost {
			host := strings.ToLower(strings.TrimSpace(ask.Target))
			if !containsFold(session.Environment.NetworkHosts, host) {
				session.Environment.NetworkHosts = append(session.Environment.NetworkHosts, host)
			}
		}
		session.UpdatedAt = time.Now().UTC()
		_ = a.store.SaveIntakeSession(ctx, session)
	}
	return nil
}

func egressAllowMessage(ask domain.EgressAsk) string {
	scope := "this run once"
	if ask.Status == domain.EgressAskAllowedQuest {
		scope = "this quest"
	}
	return fmt.Sprintf("Master/user allowed %s %q for %s. Retry only that exact destination; do not widen to mirrors or other remotes.", ask.Kind, ask.Target, scope)
}

func egressDenyMessage(ask domain.EgressAsk) string {
	return fmt.Sprintf("Master/user denied %s %q. Continue offline without that destination, or stop cleanly. Do not retry alternate URLs.", ask.Kind, ask.Target)
}

func egressTargetFromToolError(code, message string) (domain.EgressAskKind, string) {
	message = strings.TrimSpace(message)
	switch code {
	case "git_remote_unconfirmed":
		if start := strings.Index(message, `"`); start >= 0 {
			if end := strings.Index(message[start+1:], `"`); end >= 0 {
				return domain.EgressAskGitRemote, message[start+1 : start+1+end]
			}
		}
		return domain.EgressAskGitRemote, "unspecified-remote"
	case "network_denied":
		if start := strings.Index(message, `"`); start >= 0 {
			if end := strings.Index(message[start+1:], `"`); end >= 0 {
				return domain.EgressAskNetworkHost, strings.ToLower(message[start+1 : start+1+end])
			}
		}
	}
	return "", ""
}

func toolFinishedEgressHint(payload map[string]any) (code, message string) {
	if payload == nil {
		return "", ""
	}
	if errObj, ok := payload["error"].(map[string]any); ok {
		code, _ = errObj["code"].(string)
		message, _ = errObj["message"].(string)
		return code, message
	}
	// Some publishers flatten tool results.
	if raw, ok := payload["result"]; ok {
		switch typed := raw.(type) {
		case map[string]any:
			if errObj, ok := typed["error"].(map[string]any); ok {
				code, _ = errObj["code"].(string)
				message, _ = errObj["message"].(string)
				return code, message
			}
		case string:
			var decoded map[string]any
			if json.Unmarshal([]byte(typed), &decoded) == nil {
				if errObj, ok := decoded["error"].(map[string]any); ok {
					code, _ = errObj["code"].(string)
					message, _ = errObj["message"].(string)
					return code, message
				}
			}
		}
	}
	return "", ""
}
