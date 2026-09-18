package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

type ExpandIntakeRequest struct {
	NetworkHosts        []string `json:"networkHosts,omitempty"`
	ConfirmedGitRemotes []string `json:"confirmedGitRemotes,omitempty"`
	ActiveSeconds       int      `json:"activeSeconds,omitempty"`
	Tokens              int64    `json:"tokens,omitempty"`
	MaxAttempts         int      `json:"maxAttempts,omitempty"`
	AllowRemotePush     bool     `json:"allowRemotePush,omitempty"`
	ExpectedVersion     int      `json:"expectedVersion"`
}

// ExpandIntake widens an approved intake contract. External publish and new
// network hosts require a fresh approval of the revised brief.
func (a *App) ExpandIntake(ctx context.Context, id string, request ExpandIntakeRequest) (domain.IntakeSession, error) {
	item, err := a.store.GetIntakeSession(ctx, id)
	if err != nil {
		return domain.IntakeSession{}, err
	}
	if item.Brief == nil {
		return domain.IntakeSession{}, errors.New("intake has no brief to expand")
	}
	if request.ExpectedVersion != 0 && request.ExpectedVersion != item.Brief.Version {
		return domain.IntakeSession{}, errors.New("intake brief changed; review the current version before expansion")
	}
	brief := *item.Brief
	hosts := map[string]bool{}
	for _, host := range brief.Permissions.NetworkHosts {
		hosts[strings.ToLower(strings.TrimSpace(host))] = true
	}
	for _, host := range request.NetworkHosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" || hosts[host] {
			continue
		}
		brief.Permissions.NetworkHosts = append(brief.Permissions.NetworkHosts, host)
		item.Environment.NetworkHosts = append(item.Environment.NetworkHosts, host)
		hosts[host] = true
	}
	remotes := map[string]bool{}
	for _, remote := range brief.Permissions.ConfirmedGitRemotes {
		remotes[strings.ToLower(strings.TrimSpace(remote))] = true
	}
	for _, remote := range request.ConfirmedGitRemotes {
		remote = strings.TrimSpace(remote)
		key := strings.ToLower(remote)
		if remote == "" || remotes[key] {
			continue
		}
		brief.Permissions.ConfirmedGitRemotes = append(brief.Permissions.ConfirmedGitRemotes, remote)
		remotes[key] = true
	}
	if request.ActiveSeconds > brief.Budget.ActiveSeconds {
		brief.Budget.ActiveSeconds = request.ActiveSeconds
	}
	if request.Tokens > brief.Budget.Tokens {
		brief.Budget.Tokens = request.Tokens
	}
	if request.MaxAttempts > brief.Budget.MaxAttempts {
		brief.Budget.MaxAttempts = request.MaxAttempts
	}
	if request.AllowRemotePush {
		item.Delivery.RemotePublish = true
	}
	brief.Version++
	brief.ApprovedVersion = 0
	brief.ApprovedDigest = ""
	brief.State = "ready"
	brief = domain.NormalizeTaskBrief(brief)
	item.Brief = &brief
	item.Environment.Digest = ""
	item.Status = domain.IntakeAwaitingApproval
	item.Blockers = append(item.Blockers, "contract expansion requires a new approval")
	item.UpdatedAt = time.Now().UTC()
	if item.ProposalID != "" {
		proposals, listErr := a.store.ListQuestProposals(ctx, item.WorkspaceID)
		if listErr == nil {
			for _, proposal := range proposals {
				if proposal.ID != item.ProposalID {
					continue
				}
				proposal.Brief = cloneTaskBrief(&brief)
				proposal.Status = "pending"
				_ = a.store.SaveQuestProposal(ctx, proposal)
				break
			}
		}
	}
	if err = a.store.SaveIntakeSession(ctx, item); err != nil {
		return domain.IntakeSession{}, err
	}
	return item, nil
}
