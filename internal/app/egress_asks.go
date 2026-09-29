package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"local-agent-workbench/internal/textutil"
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
	if kind == domain.EgressAskNetworkHost {
		var err error
		target, err = workbenchtools.CanonicalHostGrant(target)
		if err != nil {
			return domain.EgressAsk{}, err
		}
	}
	if workspaceID == "" || target == "" || kind == "" {
		return domain.EgressAsk{}, errors.New("egress ask requires workspace, kind and target")
	}
	if existing, err := a.store.FindPendingEgressAsk(ctx, workspaceID, questID, runID, kind, target); err == nil && existing.ID != "" {
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
		if existing, findErr := a.store.FindPendingEgressAsk(ctx, workspaceID, questID, runID, kind, target); findErr == nil && existing.ID != "" {
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
	if ask.Kind == domain.EgressAskNetworkHost && action != "deny" {
		canonical, validationErr := workbenchtools.CanonicalHostGrant(ask.Target)
		if validationErr != nil || canonical != ask.Target {
			return domain.EgressAsk{}, errors.New("egress ask has an invalid TLS destination")
		}
	}
	now := time.Now().UTC()
	switch action {
	case "allow_once":
		if ask.RunID == "" {
			return domain.EgressAsk{}, errors.New("allow_once requires a live run")
		}
		ask.Status = domain.EgressAskAllowedOnce
	case "allow_quest":
		if ask.QuestID == "" {
			return domain.EgressAsk{}, errors.New("allow_quest requires a quest")
		}
		ask.Status = domain.EgressAskAllowedQuest
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
	if ask.Status == domain.EgressAskAllowedQuest {
		if err = a.persistQuestEgressGrant(ctx, ask); err != nil {
			ask.Status = domain.EgressAskPending
			ask.ResolvedAt = time.Time{}
			_ = a.store.SaveEgressAsk(ctx, ask)
			return domain.EgressAsk{}, err
		}
	}
	if ask.Status == domain.EgressAskAllowedOnce || ask.Status == domain.EgressAskAllowedQuest {
		a.applyEgressGrant(ask, ask.Status == domain.EgressAskAllowedQuest)
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

// restoreQuestEgressGrants restores only durable, explicitly approved quest
// decisions. Unspent allow_once grants deliberately disappear on core restart.
func (a *App) restoreQuestEgressGrants(ctx context.Context, workspaceID string) error {
	asks, err := a.store.ListEgressAsksForWorkspace(ctx, workspaceID)
	if err != nil {
		return err
	}
	quests, err := a.store.ListQuests(ctx, workspaceID)
	if err != nil {
		return err
	}
	approvedHosts := map[string]map[string]bool{}
	for _, quest := range quests {
		if quest.Brief == nil || !domain.IsTaskBriefApproved(*quest.Brief) {
			continue
		}
		hosts := map[string]bool{}
		for _, host := range quest.Brief.Permissions.NetworkHosts {
			if canonical, validationErr := workbenchtools.CanonicalHostGrant(host); validationErr == nil {
				hosts[canonical] = true
			}
		}
		approvedHosts[quest.ID] = hosts
	}
	for _, ask := range asks {
		if ask.Status != domain.EgressAskAllowedQuest || ask.QuestID == "" || ask.Kind != domain.EgressAskNetworkHost {
			continue
		}
		canonical, validationErr := workbenchtools.CanonicalHostGrant(ask.Target)
		if validationErr != nil || canonical != ask.Target || !approvedHosts[ask.QuestID][canonical] {
			continue
		}
		a.applyEgressGrant(ask, true)
	}
	return nil
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
	if quest == nil || quest.Brief == nil || !domain.IsTaskBriefApproved(*quest.Brief) {
		return errors.New("quest has no approved brief for an egress amendment")
	}
	brief := *quest.Brief
	switch ask.Kind {
	case domain.EgressAskNetworkHost:
		host := strings.ToLower(strings.TrimSpace(ask.Target))
		if !textutil.EqualsAnyFold(brief.Permissions.NetworkHosts, host) {
			brief.Permissions.NetworkHosts = append(brief.Permissions.NetworkHosts, host)
		}
	case domain.EgressAskGitRemote:
		if !textutil.EqualsAnyFold(brief.Permissions.ConfirmedGitRemotes, ask.Target) {
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
			if !textutil.EqualsAnyFold(session.Environment.NetworkHosts, host) {
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
		// A network decision must come from ToolError.Target, never prose.
		return domain.EgressAskNetworkHost, ""
	}
	return "", ""
}

func toolFinishedEgressHint(payload map[string]any) (code, message, target string) {
	if payload == nil {
		return "", "", ""
	}
	if errObj, ok := payload["error"].(map[string]any); ok {
		code, _ = errObj["code"].(string)
		message, _ = errObj["message"].(string)
		target, _ = errObj["target"].(string)
		return code, message, target
	}
	// Some publishers flatten tool results.
	if raw, ok := payload["result"]; ok {
		switch typed := raw.(type) {
		case map[string]any:
			if errObj, ok := typed["error"].(map[string]any); ok {
				code, _ = errObj["code"].(string)
				message, _ = errObj["message"].(string)
				target, _ = errObj["target"].(string)
				return code, message, target
			}
		case string:
			var decoded map[string]any
			if json.Unmarshal([]byte(typed), &decoded) == nil {
				if errObj, ok := decoded["error"].(map[string]any); ok {
					code, _ = errObj["code"].(string)
					message, _ = errObj["message"].(string)
					target, _ = errObj["target"].(string)
					return code, message, target
				}
			}
		}
	}
	return "", "", ""
}
