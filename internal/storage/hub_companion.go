package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

func (s *SQLite) SaveCompanionConfig(ctx context.Context, cfg domain.CompanionConfig) error {
	configured := 0
	if cfg.Configured {
		configured = 1
	}
	autoAct := 0
	if cfg.AutoAct {
		autoAct = 1
	}
	autoOpen := 0
	if cfg.AutoOpenChatOnCritical {
		autoOpen = 1
	}
	autoSend := 0
	if cfg.AutoSendModelPrompt {
		autoSend = 1
	}
	result, err := s.db.ExecContext(ctx, `
INSERT INTO companion_config(id,workspace_id,configured,preset,connection_id,provider,provider_preset,base_url,api_version,model,temperature,max_output_tokens,criticality,creativity,verbosity,initiative,question_strictness,risk_tolerance,auto_act,auto_open_chat_on_critical,auto_send_model_prompt,skill_ids_json,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET configured=excluded.configured, preset=excluded.preset, connection_id=excluded.connection_id, provider=excluded.provider, provider_preset=excluded.provider_preset,
  base_url=excluded.base_url, api_version=excluded.api_version, model=excluded.model, temperature=excluded.temperature, max_output_tokens=excluded.max_output_tokens,
  criticality=excluded.criticality, creativity=excluded.creativity,
  verbosity=excluded.verbosity, initiative=excluded.initiative, question_strictness=excluded.question_strictness,
  risk_tolerance=excluded.risk_tolerance, auto_act=excluded.auto_act,
  auto_open_chat_on_critical=excluded.auto_open_chat_on_critical, auto_send_model_prompt=excluded.auto_send_model_prompt,
  skill_ids_json=excluded.skill_ids_json, updated_at=excluded.updated_at
WHERE companion_config.workspace_id=excluded.workspace_id`,
		cfg.ID, cfg.WorkspaceID, configured, cfg.Preset, cfg.ConnectionID, cfg.Provider, cfg.ProviderPreset, cfg.BaseURL, cfg.APIVersion, cfg.Model, cfg.Temperature, cfg.MaxOutputTokens,
		cfg.Criticality, cfg.Creativity, cfg.Verbosity, cfg.Initiative,
		cfg.QuestionStrictness, cfg.RiskTolerance, autoAct, autoOpen, autoSend, marshalJSON(cfg.SkillIDs), formatTime(cfg.CreatedAt), formatTime(cfg.UpdatedAt))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("companion config %q belongs to another workspace", cfg.ID)
	}
	return nil
}

func (s *SQLite) GetCompanionConfig(ctx context.Context, workspaceID string) (domain.CompanionConfig, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id,workspace_id,configured,preset,connection_id,provider,provider_preset,base_url,api_version,model,temperature,max_output_tokens,criticality,creativity,verbosity,initiative,question_strictness,risk_tolerance,auto_act,auto_open_chat_on_critical,auto_send_model_prompt,skill_ids_json,created_at,updated_at
FROM companion_config WHERE workspace_id=? ORDER BY updated_at DESC LIMIT 1`, workspaceID)
	var cfg domain.CompanionConfig
	var created, updated, skillIDs string
	var configured, autoAct, autoOpen, autoSend int
	if err := row.Scan(&cfg.ID, &cfg.WorkspaceID, &configured, &cfg.Preset, &cfg.ConnectionID, &cfg.Provider, &cfg.ProviderPreset, &cfg.BaseURL, &cfg.APIVersion, &cfg.Model,
		&cfg.Temperature, &cfg.MaxOutputTokens, &cfg.Criticality, &cfg.Creativity, &cfg.Verbosity,
		&cfg.Initiative, &cfg.QuestionStrictness, &cfg.RiskTolerance, &autoAct, &autoOpen, &autoSend, &skillIDs, &created, &updated); err != nil {
		return domain.CompanionConfig{}, err
	}
	unmarshalJSON(skillIDs, &cfg.SkillIDs)
	cfg.Configured = configured == 1
	cfg.AutoAct = autoAct == 1
	cfg.AutoOpenChatOnCritical = autoOpen == 1
	cfg.AutoSendModelPrompt = autoSend == 1
	cfg.CreatedAt, cfg.UpdatedAt = parseTime(created), parseTime(updated)
	return cfg, nil
}

func (s *SQLite) SaveCompanionMessage(ctx context.Context, message domain.CompanionMessage) error {
	if message.Speaker == "master" && message.ConversationID == "" {
		message.ConversationID = "legacy"
	}
	_, err := s.db.ExecContext(ctx, `
	INSERT INTO companion_messages(id,workspace_id,speaker,role,content,level,mode,provider,model,facts_used_json,questions_json,usage_record_id,proposal_id,action_proposal_id,fallback_reason,input_tokens,output_tokens,total_tokens,latency_ms,reasoning,steps_json,created_at,conversation_id,turn_id,attachments_json,memory_ids_json,search_text,clarifications_json)
	VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, message.ID, message.WorkspaceID, speakerOrDefault(message.Speaker), message.Role, message.Content, message.Level, message.Mode,
		message.Provider, message.Model, marshalJSON(message.FactsUsed), marshalJSON(message.Questions), message.UsageRecordID, message.ProposalID,
		message.ActionProposalID, message.FallbackReason, message.InputTokens, message.OutputTokens, message.TotalTokens, message.LatencyMs,
		message.Reasoning, marshalJSON(message.Steps),
		message.CreatedAt.UTC().Format("2006-01-02T15:04:05.000000000Z07:00"), message.ConversationID, message.TurnID, marshalJSON(message.Attachments), marshalJSON(message.MemoryIDs), strings.ToLower(message.Content), marshalJSON(message.Clarifications))
	return err
}

// SaveCompanionMessageOnce is used for deterministic lifecycle messages whose
// ID is derived from the quest. INSERT OR IGNORE makes Flow callback replay a
// no-op instead of publishing the same completion twice.
func (s *SQLite) SaveCompanionMessageOnce(ctx context.Context, message domain.CompanionMessage) (bool, error) {
	if message.Speaker == "master" && message.ConversationID == "" {
		message.ConversationID = "legacy"
	}
	result, err := s.db.ExecContext(ctx, `
	INSERT OR IGNORE INTO companion_messages(id,workspace_id,speaker,role,content,level,mode,provider,model,facts_used_json,questions_json,usage_record_id,proposal_id,action_proposal_id,fallback_reason,input_tokens,output_tokens,total_tokens,latency_ms,reasoning,steps_json,created_at,conversation_id,turn_id,attachments_json,memory_ids_json,search_text,clarifications_json)
	VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, message.ID, message.WorkspaceID, speakerOrDefault(message.Speaker), message.Role, message.Content, message.Level, message.Mode,
		message.Provider, message.Model, marshalJSON(message.FactsUsed), marshalJSON(message.Questions), message.UsageRecordID, message.ProposalID,
		message.ActionProposalID, message.FallbackReason, message.InputTokens, message.OutputTokens, message.TotalTokens, message.LatencyMs,
		message.Reasoning, marshalJSON(message.Steps),
		message.CreatedAt.UTC().Format("2006-01-02T15:04:05.000000000Z07:00"), message.ConversationID, message.TurnID, marshalJSON(message.Attachments), marshalJSON(message.MemoryIDs), strings.ToLower(message.Content), marshalJSON(message.Clarifications))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

// speakerOrDefault держит инвариант «пусто значит компаньон» в одном месте:
// иначе прежние записи и новый код разошлись бы в трактовке пустой строки.
func speakerOrDefault(value string) string {
	if strings.TrimSpace(value) == "" {
		return "companion"
	}
	return value
}

func (s *SQLite) ListCompanionMessages(ctx context.Context, workspaceID string, limit int) ([]domain.CompanionMessage, error) {
	return s.ListChatMessages(ctx, workspaceID, "companion", limit)
}

// ListChatMessages возвращает разговор одного собеседника. Компаньон и Мастер
// делят таблицу, но не диалог: смешать их значило бы показать пользователю
// чужие реплики в своём окне.
func (s *SQLite) ListChatMessages(ctx context.Context, workspaceID, speaker string, limit int) ([]domain.CompanionMessage, error) {
	if limit <= 0 || limit > 200 {
		limit = 80
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id,workspace_id,speaker,role,content,level,mode,provider,model,facts_used_json,questions_json,usage_record_id,proposal_id,action_proposal_id,fallback_reason,input_tokens,output_tokens,total_tokens,latency_ms,feedback,reasoning,steps_json,created_at FROM (
  SELECT id,workspace_id,speaker,role,content,level,mode,provider,model,facts_used_json,questions_json,usage_record_id,proposal_id,action_proposal_id,fallback_reason,input_tokens,output_tokens,total_tokens,latency_ms,feedback,reasoning,steps_json,created_at
  FROM companion_messages WHERE workspace_id=? AND speaker=?
  ORDER BY created_at DESC, id DESC LIMIT ?
) ORDER BY created_at ASC, id ASC`, workspaceID, speakerOrDefault(speaker), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.CompanionMessage, 0, limit)
	for rows.Next() {
		var message domain.CompanionMessage
		var created, facts, questions, steps string
		if err = rows.Scan(&message.ID, &message.WorkspaceID, &message.Speaker, &message.Role, &message.Content, &message.Level, &message.Mode,
			&message.Provider, &message.Model, &facts, &questions, &message.UsageRecordID, &message.ProposalID, &message.ActionProposalID, &message.FallbackReason,
			&message.InputTokens, &message.OutputTokens, &message.TotalTokens, &message.LatencyMs, &message.Feedback, &message.Reasoning, &steps, &created); err != nil {
			return nil, err
		}
		unmarshalJSON(facts, &message.FactsUsed)
		unmarshalJSON(questions, &message.Questions)
		unmarshalJSON(steps, &message.Steps)
		message.CreatedAt = parseTime(created)
		result = append(result, message)
	}
	return result, rows.Err()
}

// SetChatMessageFeedback ставит или снимает оценку одной реплики. Пустая строка
// — снятие: нажать «полезно» второй раз значит передумать, а не подтвердить.
func (s *SQLite) SetChatMessageFeedback(ctx context.Context, workspaceID, messageID, value string) error {
	if value != "up" && value != "down" {
		value = ""
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE companion_messages SET feedback=? WHERE id=? AND workspace_id=?`,
		value, messageID, workspaceID)
	return err
}

func (s *SQLite) DeleteCompanionMessages(ctx context.Context, workspaceID string) error {
	if strings.TrimSpace(workspaceID) == "" {
		return fmt.Errorf("companion history workspace is required")
	}
	// Clear only the everyday assistant. Master and purpose-specific log chats
	// are separate conversations stored in the same table.
	_, err := s.db.ExecContext(ctx, `DELETE FROM companion_messages WHERE workspace_id=? AND speaker='companion'`, workspaceID)
	return err
}

func (s *SQLite) SaveCompanionActionProposal(ctx context.Context, proposal domain.CompanionActionProposal) error {
	payload := struct {
		Flow               *domain.FlowGraph       `json:"flow,omitempty"`
		Agent              *domain.ProjectAgent    `json:"agent,omitempty"`
		Team               *domain.Team            `json:"team,omitempty"`
		Skill              *domain.SkillDefinition `json:"skill,omitempty"`
		Tool               *domain.CustomTool      `json:"tool,omitempty"`
		ContinuationPrompt string                  `json:"continuationPrompt,omitempty"`
		ContinuationLabel  string                  `json:"continuationLabel,omitempty"`
	}{
		Flow: proposal.Flow, Agent: proposal.Agent, Team: proposal.Team, Skill: proposal.Skill, Tool: proposal.Tool,
		ContinuationPrompt: proposal.ContinuationPrompt, ContinuationLabel: proposal.ContinuationLabel,
	}
	result, err := s.db.ExecContext(ctx, `
INSERT INTO companion_action_proposals(id,workspace_id,kind,title,rationale,payload_json,status,applied_entity_id,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET kind=excluded.kind, title=excluded.title, rationale=excluded.rationale,
  payload_json=excluded.payload_json, status=excluded.status, applied_entity_id=excluded.applied_entity_id, updated_at=excluded.updated_at
WHERE companion_action_proposals.workspace_id=excluded.workspace_id`,
		proposal.ID, proposal.WorkspaceID, proposal.Kind, proposal.Title, proposal.Rationale, marshalJSON(payload),
		proposal.Status, proposal.AppliedEntityID, formatTime(proposal.CreatedAt), formatTime(proposal.UpdatedAt))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("companion action proposal %q belongs to another workspace", proposal.ID)
	}
	return nil
}

func (s *SQLite) ListCompanionActionProposals(ctx context.Context, workspaceID string) ([]domain.CompanionActionProposal, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id,workspace_id,kind,title,rationale,payload_json,status,applied_entity_id,created_at,updated_at
FROM companion_action_proposals WHERE workspace_id=? ORDER BY updated_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.CompanionActionProposal, 0)
	for rows.Next() {
		var proposal domain.CompanionActionProposal
		var payloadJSON, created, updated string
		if err = rows.Scan(&proposal.ID, &proposal.WorkspaceID, &proposal.Kind, &proposal.Title, &proposal.Rationale,
			&payloadJSON, &proposal.Status, &proposal.AppliedEntityID, &created, &updated); err != nil {
			return nil, err
		}
		var payload struct {
			Flow               *domain.FlowGraph       `json:"flow,omitempty"`
			Agent              *domain.ProjectAgent    `json:"agent,omitempty"`
			Team               *domain.Team            `json:"team,omitempty"`
			Skill              *domain.SkillDefinition `json:"skill,omitempty"`
			Tool               *domain.CustomTool      `json:"tool,omitempty"`
			ContinuationPrompt string                  `json:"continuationPrompt,omitempty"`
			ContinuationLabel  string                  `json:"continuationLabel,omitempty"`
		}
		unmarshalJSON(payloadJSON, &payload)
		proposal.Flow, proposal.Agent, proposal.Team, proposal.Skill, proposal.Tool = payload.Flow, payload.Agent, payload.Team, payload.Skill, payload.Tool
		proposal.ContinuationPrompt, proposal.ContinuationLabel = payload.ContinuationPrompt, payload.ContinuationLabel
		proposal.CreatedAt, proposal.UpdatedAt = parseTime(created), parseTime(updated)
		result = append(result, proposal)
	}
	return result, rows.Err()
}

func (s *SQLite) SaveIDEObservationBatch(ctx context.Context, workspaceID, kind string, replace bool, items []domain.IDEObservation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if replace {
		if _, err = tx.ExecContext(ctx, `DELETE FROM ide_observations WHERE workspace_id=? AND kind=?`, workspaceID, kind); err != nil {
			return err
		}
	}
	for _, item := range items {
		var exitCode any
		if item.ExitCode != nil {
			exitCode = *item.ExitCode
		}
		if _, err = tx.ExecContext(ctx, `
INSERT INTO ide_observations(id,workspace_id,kind,source,level,summary,detail,path,line,command,exit_code,observed_at,first_seen,last_seen,count,novelty_hash,focus_path)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, item.ID, workspaceID, kind, item.Source, item.Level, item.Summary, item.Detail,
			item.Path, item.Line, item.Command, exitCode, formatTime(item.ObservedAt),
			formatTime(firstSeen(item)), formatTime(lastSeen(item)), countOrOne(item.Count), item.NoveltyHash, item.FocusPath); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `
DELETE FROM ide_observations WHERE id IN (
  SELECT id FROM ide_observations WHERE workspace_id=? AND kind=?
  ORDER BY observed_at DESC, id DESC LIMIT -1 OFFSET 100
)`, workspaceID, kind); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLite) ListIDEObservations(ctx context.Context, workspaceID string, limit int) ([]domain.IDEObservation, error) {
	if limit <= 0 || limit > 300 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id,workspace_id,kind,source,level,summary,detail,path,line,command,exit_code,observed_at,first_seen,last_seen,count,novelty_hash,focus_path
FROM ide_observations WHERE workspace_id=? ORDER BY observed_at DESC, id DESC LIMIT ?`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.IDEObservation, 0, limit)
	for rows.Next() {
		var item domain.IDEObservation
		var exitCode sql.NullInt64
		var observed, firstSeenRaw, lastSeenRaw string
		if err = rows.Scan(&item.ID, &item.WorkspaceID, &item.Kind, &item.Source, &item.Level, &item.Summary,
			&item.Detail, &item.Path, &item.Line, &item.Command, &exitCode, &observed,
			&firstSeenRaw, &lastSeenRaw, &item.Count, &item.NoveltyHash, &item.FocusPath); err != nil {
			return nil, err
		}
		if exitCode.Valid {
			value := int(exitCode.Int64)
			item.ExitCode = &value
		}
		item.ObservedAt = parseTime(observed)
		item.FirstSeen = parseTime(firstSeenRaw)
		item.LastSeen = parseTime(lastSeenRaw)
		if item.Count <= 0 {
			item.Count = 1
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func firstSeen(item domain.IDEObservation) time.Time {
	if !item.FirstSeen.IsZero() {
		return item.FirstSeen
	}
	return item.ObservedAt
}

func lastSeen(item domain.IDEObservation) time.Time {
	if !item.LastSeen.IsZero() {
		return item.LastSeen
	}
	return item.ObservedAt
}

func countOrOne(count int) int {
	if count <= 0 {
		return 1
	}
	return count
}

func (s *SQLite) SaveQuestProposal(ctx context.Context, proposal domain.QuestProposal) error {
	briefJSON, err := taskBriefJSON(proposal.Brief)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Validate immutable content before upsert. Insert history after the parent
	// exists; ownership/version checks below still observe the previous brief.
	var previousOwner string
	var previousBrief sql.NullString
	previousErr := tx.QueryRowContext(ctx, "SELECT workspace_id,brief_json FROM quest_proposals WHERE id=?", proposal.ID).Scan(&previousOwner, &previousBrief)
	if previousErr != nil && previousErr != sql.ErrNoRows {
		return previousErr
	}
	if previousErr == nil && previousOwner != proposal.WorkspaceID {
		return fmt.Errorf("quest proposal %q belongs to another workspace", proposal.ID)
	}
	if proposal.Brief != nil {
		current, decodeErr := decodeTaskBrief(previousBrief)
		if decodeErr != nil {
			return decodeErr
		}
		if current == nil && proposal.Brief.Version != 1 {
			return fmt.Errorf("initial task brief version must be 1")
		}
		if current != nil && (proposal.Brief.Version < current.Version || proposal.Brief.Version > current.Version+1 || (proposal.Brief.Version == current.Version && domain.TaskBriefDigest(*proposal.Brief) != domain.TaskBriefDigest(*current))) {
			return fmt.Errorf("task brief changes require the next version")
		}
	}
	var estimate any
	if proposal.EstimateCents != nil {
		estimate = *proposal.EstimateCents
	}
	teamLocked := 0
	if proposal.TeamAgentIDsLocked {
		teamLocked = 1
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO quest_proposals(id,workspace_id,title,task,rationale,unknowns,objectives,constraints_json,definition_of_done,team_agent_ids,team_agent_ids_locked,selection_breakdown,flow_id,importance,estimate_tokens,estimate_cents,status,created_at,brief_json)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET title=excluded.title, task=excluded.task, rationale=excluded.rationale, unknowns=excluded.unknowns,
  objectives=excluded.objectives, constraints_json=excluded.constraints_json, definition_of_done=excluded.definition_of_done,
  team_agent_ids=excluded.team_agent_ids, team_agent_ids_locked=excluded.team_agent_ids_locked, selection_breakdown=excluded.selection_breakdown, flow_id=excluded.flow_id, importance=excluded.importance,
  estimate_tokens=excluded.estimate_tokens, estimate_cents=excluded.estimate_cents, status=excluded.status, brief_json=COALESCE(excluded.brief_json,quest_proposals.brief_json)
WHERE quest_proposals.workspace_id=excluded.workspace_id`,
		proposal.ID, proposal.WorkspaceID, proposal.Title, proposal.Task, proposal.Rationale, marshalJSON(proposal.Unknowns),
		marshalJSON(proposal.Objectives), marshalJSON(proposal.Constraints), marshalJSON(proposal.DefinitionOfDone),
		marshalJSON(proposal.TeamAgentIDs), teamLocked, marshalJSON(proposal.SelectionBreakdown), proposal.FlowID, proposal.Importance, proposal.EstimateTokens, estimate,
		proposal.Status, formatTime(proposal.CreatedAt), briefJSON)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("quest proposal %q belongs to another workspace", proposal.ID)
	}
	if err = saveTaskBriefRevision(ctx, tx, proposal); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLite) ListQuestProposals(ctx context.Context, workspaceID string) ([]domain.QuestProposal, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id,workspace_id,title,task,rationale,unknowns,objectives,constraints_json,definition_of_done,team_agent_ids,team_agent_ids_locked,selection_breakdown,flow_id,importance,estimate_tokens,estimate_cents,status,created_at,brief_json
FROM quest_proposals WHERE workspace_id=? ORDER BY created_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.QuestProposal
	for rows.Next() {
		var proposal domain.QuestProposal
		var unknowns, objectives, constraints, dod, agents, selection, created string
		var estimate sql.NullInt64
		var briefJSON sql.NullString
		var teamLocked int
		if err = rows.Scan(&proposal.ID, &proposal.WorkspaceID, &proposal.Title, &proposal.Task, &proposal.Rationale, &unknowns,
			&objectives, &constraints, &dod, &agents, &teamLocked, &selection, &proposal.FlowID, &proposal.Importance, &proposal.EstimateTokens,
			&estimate, &proposal.Status, &created, &briefJSON); err != nil {
			return nil, err
		}
		proposal.Brief, err = decodeTaskBrief(briefJSON)
		if err != nil {
			return nil, err
		}
		unmarshalJSON(unknowns, &proposal.Unknowns)
		unmarshalJSON(objectives, &proposal.Objectives)
		unmarshalJSON(constraints, &proposal.Constraints)
		unmarshalJSON(dod, &proposal.DefinitionOfDone)
		unmarshalJSON(agents, &proposal.TeamAgentIDs)
		unmarshalJSON(selection, &proposal.SelectionBreakdown)
		proposal.TeamAgentIDsLocked = teamLocked != 0
		if estimate.Valid {
			value := estimate.Int64
			proposal.EstimateCents = &value
		}
		proposal.CreatedAt = parseTime(created)
		result = append(result, proposal)
	}
	return result, rows.Err()
}
