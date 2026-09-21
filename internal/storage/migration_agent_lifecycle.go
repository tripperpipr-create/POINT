package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

type legacyOpenWorkOrder struct {
	id, workspaceID, raw string
	version              int
}

// migrateOpenLegacyWorkOrderAgents preserves the IDs shown on open legacy
// cards while moving their embedded permanent drafts into the lifecycle table.
// Approved historical contracts are deliberately excluded.
func migrateOpenLegacyWorkOrderAgents(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,version,workspace_id,payload_json FROM work_order_current_v2 WHERE status!='approved' ORDER BY id`)
	if err != nil {
		return err
	}
	var entries []legacyOpenWorkOrder
	for rows.Next() {
		var entry legacyOpenWorkOrder
		if err = rows.Scan(&entry.id, &entry.version, &entry.workspaceID, &entry.raw); err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, entry)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}

	for _, entry := range entries {
		var order domain.WorkOrder
		if err = json.Unmarshal([]byte(entry.raw), &order); err != nil {
			return fmt.Errorf("decode legacy work order %q: %w", entry.id, err)
		}
		if len(order.Roster.AgentIDs) > 0 || len(order.Roster.Permanent) == 0 {
			continue
		}
		now := time.Now().UTC()
		ids := make([]string, 0, len(order.Roster.Permanent))
		for index := range order.Roster.Permanent {
			draft := &order.Roster.Permanent[index]
			draft.ID = strings.TrimSpace(draft.ID)
			if draft.ID == "" {
				draft.ID = domain.NewID("agentdraft")
			}
			ids = append(ids, draft.ID)
			if !draft.Existing {
				if err = migrateLegacyAgentDraft(ctx, tx, order, *draft, now); err != nil {
					return err
				}
				draft.Existing = true
				draft.RequiresConsent = false
			}
		}
		order.Roster.AgentIDs = ids
		nextVersion, versionErr := nextFreeWorkOrderVersion(ctx, tx, entry)
		if versionErr != nil {
			return versionErr
		}
		order.Version = nextVersion
		order.Digest = ""
		order.UpdatedAt = now
		raw, marshalErr := json.Marshal(order)
		if marshalErr != nil {
			return marshalErr
		}
		digest := domain.WorkOrderDigest(order)
		if _, err = tx.ExecContext(ctx, `INSERT INTO work_order_revisions_v2(id,version,workspace_id,digest,payload_json,created_at) VALUES(?,?,?,?,?,?)`,
			order.ID, order.Version, order.WorkspaceID, digest, string(raw), formatTime(now)); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE work_order_current_v2 SET version=?,digest=?,payload_json=?,updated_at=? WHERE id=? AND version=?`,
			order.Version, digest, string(raw), formatTime(now), entry.id, entry.version); err != nil {
			return err
		}
		for _, agentID := range ids {
			if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO agent_selection_bindings(conversation_id,work_order_id,workspace_id,agent_id,selection_digest,revision,created_at) VALUES(?,?,?,?,?,?,?)`,
				order.ConversationID, order.ID, order.WorkspaceID, agentID, "legacy:"+digest, order.Version, formatTime(now)); err != nil {
				return err
			}
		}
	}
	return nil
}

// nextFreeWorkOrderVersion keeps the rewrite above the immutable revision
// history: a card recreated under the same deterministic ID leaves older,
// higher revisions behind, so current.version + 1 can already be taken.
func nextFreeWorkOrderVersion(ctx context.Context, tx *sql.Tx, entry legacyOpenWorkOrder) (int, error) {
	var highest int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM work_order_revisions_v2 WHERE id=?`, entry.id).Scan(&highest); err != nil {
		return 0, err
	}
	if highest > entry.version {
		return highest + 1, nil
	}
	return entry.version + 1, nil
}

func migrateLegacyAgentDraft(ctx context.Context, tx *sql.Tx, order domain.WorkOrder, draft domain.AgentDraft, now time.Time) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM project_agents WHERE id=?`, draft.ID).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}
	connectionID, model := order.Routing.FixedConnectionID, order.Routing.FixedModel
	if order.Routing.Mode == "auto" {
		connectionID, model = order.Routing.RouterConnectionID, order.Routing.RouterModel
	}
	var provider, preset, baseURL string
	_ = tx.QueryRowContext(ctx, `SELECT provider,preset_id,base_url FROM connections WHERE id=?`, connectionID).Scan(&provider, &preset, &baseURL)
	agent := domain.ProjectAgent{
		ID: draft.ID, WorkspaceID: order.WorkspaceID, Status: domain.ProjectAgentDraft,
		RoleFamily: legacyRoleFamily(draft.Name + " " + draft.Role), Name: draft.Name,
		RoleDescription: draft.Role, Mission: draft.Mission, SystemPrompt: draft.Mission,
		Goals: []string{draft.Mission}, Rules: []string{"Respect the approved WorkOrder scope and evidence gates."},
		AllowedTools: append([]string(nil), draft.RequiredTools...), ConnectionID: connectionID,
		Provider: domain.ProviderKind(provider), ProviderPreset: preset, BaseURL: baseURL, PrimaryModel: model,
		Temperature: 0.2, MaxOutputTokens: 4096, ContextWindowTokens: 32768, ReasoningEffort: "medium",
		MaxSteps: 24, MaxDurationSeconds: order.Budget.ActiveSeconds, ApprovalMode: domain.ApprovalSafe,
		Level: 1, CreatedAt: now, UpdatedAt: now,
	}
	if strings.TrimSpace(agent.Name) == "" {
		agent.Name = "Draft agent"
	}
	if strings.TrimSpace(agent.RoleDescription) == "" {
		agent.RoleDescription = agent.RoleFamily
	}
	if draft.BlueprintID != "" && !draft.ProjectOnly {
		blueprint, blueprintErr := blueprintV2Tx(ctx, tx, draft.BlueprintID)
		if blueprintErr == nil {
			inherited := projectAgentFromBlueprintV2(draft.ID, order.WorkspaceID, blueprint, now)
			inherited.Status, inherited.RoleFamily = domain.ProjectAgentDraft, agent.RoleFamily
			inherited.Name, inherited.RoleDescription, inherited.Mission = agent.Name, agent.RoleDescription, agent.Mission
			if len(agent.AllowedTools) > 0 {
				inherited.AllowedTools = agent.AllowedTools
			}
			agent = inherited
		} else if !errors.Is(blueprintErr, sql.ErrNoRows) {
			return blueprintErr
		}
	}
	if err := insertProjectAgentV2Tx(ctx, tx, agent); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO agent_lifecycle_events(id,workspace_id,agent_id,work_order_id,kind,detail_json,created_at) VALUES(?,?,?,?,?,?,?)`,
		domain.NewID("agentlife"), order.WorkspaceID, agent.ID, order.ID, "legacy_draft_migrated", marshalJSON(map[string]any{"roleFamily": agent.RoleFamily}), formatTime(now))
	return err
}

func legacyRoleFamily(value string) string {
	value = strings.ToLower(value)
	for _, match := range []struct {
		family string
		words  []string
	}{
		{"security_auditor", []string{"security", "безопас", "аудит"}},
		{"tester", []string{"test", "qa", "тест"}},
		{"designer", []string{"design", "ui", "ux", "дизайн"}},
		{"devops", []string{"devops", "deploy", "infra", "инфра", "развер"}},
		{"analyst", []string{"analyst", "research", "data", "аналит", "исслед"}},
	} {
		for _, word := range match.words {
			if strings.Contains(value, word) {
				return match.family
			}
		}
	}
	return "developer"
}
