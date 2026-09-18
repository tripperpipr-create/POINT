package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type migration struct {
	version int
	name    string
	up      func(ctx context.Context, tx *sql.Tx) error
}

func (s *SQLite) runVersionedMigrations(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  applied_at TEXT NOT NULL
)`); err != nil {
		return err
	}
	for _, m := range hubMigrations() {
		var existing int
		err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_migrations WHERE version=?`, m.version).Scan(&existing)
		if err != nil {
			return err
		}
		if existing > 0 {
			continue
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if err = m.up(ctx, tx); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, name, applied_at) VALUES(?,?,?)`,
			m.version, m.name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func hubMigrations() []migration {
	return []migration{
		{1, "hub_ontology_v1", migrationHubOntologyV1},
		{2, "profiles_to_blueprints_v1", migrationProfilesToBlueprintsV1},
		{3, "change_set_resolutions_v1", migrationChangeSetResolutionsV1},
		{4, "change_set_exact_snapshots_v1", migrationChangeSetExactSnapshotsV1},
		{5, "companion_model_config_v1", migrationCompanionModelConfigV1},
		{6, "companion_chat_history_v1", migrationCompanionChatHistoryV1},
		{7, "companion_message_provenance_v1", migrationCompanionMessageProvenanceV1},
		{8, "companion_message_questions_v1", migrationCompanionMessageQuestionsV1},
		{9, "companion_action_proposals_v1", migrationCompanionActionProposalsV1},
		{10, "ide_observations_v1", migrationIDEObservationsV1},
		{11, "orchestrator_config_v1", migrationOrchestratorConfigV1},
		{12, "server_profiles_v1", migrationServerProfilesV1},
		{13, "db_connections_v1", migrationDBConnectionsV1},
		{14, "companion_quiet_observe_v1", migrationCompanionQuietObserveV1},
		{15, "flow_sandbox_lineage_v1", migrationFlowSandboxLineageV1},
		{16, "flow_parallel_merge_v1", migrationFlowParallelMergeV1},
		{17, "chat_speaker_v1", migrationChatSpeakerV1},
		{18, "quest_proposal_task_v1", migrationQuestProposalTaskV1},
		{19, "quest_proposal_team_lock_v1", migrationQuestProposalTeamLockV1},
		{20, "companion_configured_v1", migrationCompanionConfiguredV1},
		{21, "agent_improvements_v1", migrationAgentImprovementsV1},
		{22, "agent_learning_promotion_v1", migrationAgentLearningPromotionV1},
		{23, "agent_learning_memory_v1", migrationAgentLearningMemoryV1},
		{24, "agent_learning_instruction_v1", migrationAgentLearningInstructionV1},
		{25, "learning_signals_and_skill_outcomes_v1", migrationLearningSignalsAndSkillOutcomesV1},
		{26, "sandbox_backend_attribution_v1", migrationSandboxBackendAttributionV1},
		{27, "skill_canary_evaluation_v1", migrationSkillCanaryEvaluationV1},
		{28, "agent_benchmarks_v1", migrationAgentBenchmarksV1},
		{29, "compatibility_usage_and_profile_reconcile_v1", migrationCompatibilityUsageAndProfileReconcileV1},
		{30, "manual_tool_execution_approval_v1", migrationManualToolExecutionApprovalV1},
		{31, "llm_connection_binding_v1", migrationLLMConnectionBindingV1},
		{32, "workspace_scoped_events_v1", migrationWorkspaceScopedEventsV1},
		{33, "budget_reservations_v1", migrationBudgetReservationsV1},
		{34, "flow_child_quests_and_failure_policy_v1", migrationFlowChildQuestsV1},
		{35, "quest_selection_breakdown_v1", migrationQuestSelectionBreakdownV1},
		{36, "actor_model_target_v1", migrationActorModelTargetV1},
		{37, "companion_skills_v1", migrationCompanionSkillsV1},
		{38, "task_briefs_v1", migrationTaskBriefsV1},
		{39, "run_checkpoints_v1", migrationRunCheckpointsV1},
		{40, "learning_effect_and_principles_v1", migrationLearningEffectAndPrinciplesV1},
		{41, "quest_replans_v1", migrationQuestReplansV1},
		{42, "chat_message_feedback_v1", migrationChatMessageFeedbackV1},
		{43, "chat_message_reasoning_v1", migrationChatMessageReasoningV1},
		{44, "master_conversations_v1", migrationMasterConversationsV1},
		{45, "workspace_model_routing_v1", migrationWorkspaceModelRoutingV1},
		{46, "model_capability_evidence_v1", migrationModelCapabilityEvidenceV1},
		{47, "quest_controller_v1", migrationQuestControllerV1},
		{48, "agent_prep_chains_v1", migrationAgentPrepChainsV1},
		{49, "team_events_v1", migrationTeamEventsV1},
		{50, "execution_runtime_session_v1", migrationExecutionRuntimeSessionV1},
		{51, "url_intake_v1", migrationURLIntakeV1},
		{52, "egress_asks_v1", migrationEgressAsksV1},
		{53, "project_agent_subagents_v1", migrationProjectAgentSubagentsV1},
		{54, "agent_hub_work_orders_v2", migrationAgentHubWorkOrdersV2},
		{55, "agent_hub_source_snapshots_v2", migrationAgentHubSourceSnapshotsV2},
		{56, "agent_hub_completion_gate_v2", migrationAgentHubCompletionGateV2},
		{57, "agent_hub_quest_controls_v2", migrationAgentHubQuestControlsV2},
		{58, "agent_hub_work_order_diffs_v2", migrationAgentHubWorkOrderDiffsV2},
		{59, "agent_hub_reapproval_v2", migrationAgentHubReapprovalV2},
		{60, "agent_hub_master_work_order_v2", migrationAgentHubMasterWorkOrderV2},
		{61, "agent_hub_writer_lease_v2", migrationAgentHubWriterLeaseV2},
		{62, "agent_hub_model_certification_v2", migrationAgentHubModelCertificationV2},
		{63, "agent_hub_delivered_app_control_v2", migrationAgentHubDeliveredAppControlV2},
		{64, "agent_hub_milestone_runtime_v2", migrationAgentHubMilestoneRuntimeV2},
		{65, "agent_hub_work_order_revision_idempotency_v2", migrationAgentHubWorkOrderRevisionIdempotencyV2},
		{66, "master_conversation_recent_v1", migrationMasterConversationRecentV1},
		{67, "master_turn_event_detail_v1", migrationMasterTurnEventDetailV1},
	}
}

// Каталог чатов Чертога сортирует разговоры всех миров по времени. Первичный
// ключ master_conversations — (workspace_id, id), и глобальному порядку он не
// помогает: без этого индекса запрос уходит во временную таблицу сортировки.
// Условие индекса повторяет условие запроса, поэтому он частичный и остаётся
// маленьким даже при большом архиве.
func migrationMasterConversationRecentV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE INDEX IF NOT EXISTS master_conversations_recent
  ON master_conversations(updated_at DESC)
  WHERE temporary = 0 AND archived = 0;
`)
	return err
}

func migrationAgentHubWorkOrderRevisionIdempotencyV2(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE work_order_revision_idempotency_v2 (
  idempotency_key TEXT PRIMARY KEY,
  work_order_id TEXT NOT NULL,
  expected_version INTEGER NOT NULL,
  expected_digest TEXT NOT NULL,
  result_version INTEGER NOT NULL,
  result_digest TEXT NOT NULL,
  result_payload_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX work_order_revision_idempotency_order_v2 ON work_order_revision_idempotency_v2(work_order_id,result_version);
`)
	return err
}

func migrationAgentHubMilestoneRuntimeV2(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE milestone_runtimes_v2 (
  quest_id TEXT NOT NULL,
  work_order_id TEXT NOT NULL,
  work_order_version INTEGER NOT NULL,
  milestone_id TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(quest_id,work_order_version,milestone_id)
);
CREATE INDEX milestone_runtimes_quest_v2 ON milestone_runtimes_v2(quest_id,work_order_version,updated_at);
`)
	return err
}

func migrationAgentHubDeliveredAppControlV2(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE delivered_app_controls_v2 (
  idempotency_key TEXT PRIMARY KEY,
  quest_id TEXT NOT NULL,
  delivery_receipt_id TEXT NOT NULL,
  work_order_digest TEXT NOT NULL,
  action TEXT NOT NULL CHECK(action IN ('start','stop')),
  response_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX delivered_app_controls_quest_v2 ON delivered_app_controls_v2(quest_id,updated_at DESC);
`)
	return err
}

func migrationAgentHubModelCertificationV2(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE model_certifications_v2 (
  id TEXT PRIMARY KEY,
  connection_id TEXT NOT NULL,
  model TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('certified','experimental')),
  matrix_version TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  evaluated_at TEXT NOT NULL
);
CREATE INDEX model_certifications_lookup_v2 ON model_certifications_v2(connection_id,model,evaluated_at DESC);
CREATE TRIGGER model_certifications_no_update_v2 BEFORE UPDATE ON model_certifications_v2 BEGIN SELECT RAISE(ABORT,'model certifications are immutable'); END;
CREATE TRIGGER model_certifications_no_delete_v2 BEFORE DELETE ON model_certifications_v2 BEGIN SELECT RAISE(ABORT,'model certifications are immutable'); END;
`)
	return err
}

func migrationAgentHubWriterLeaseV2(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE writer_leases_v2 (
  workspace_id TEXT PRIMARY KEY,
  quest_id TEXT NOT NULL UNIQUE,
  token TEXT NOT NULL UNIQUE,
  state TEXT NOT NULL CHECK(state IN ('active','released')),
  acquired_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  released_at TEXT
);
CREATE INDEX writer_leases_quest_v2 ON writer_leases_v2(quest_id,state);
`)
	return err
}

func migrationAgentHubMasterWorkOrderV2(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `ALTER TABLE master_turns ADD COLUMN work_order_id TEXT NOT NULL DEFAULT ''`)
	return err
}

func migrationAgentHubReapprovalV2(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE work_order_approvals_v2_next (
  idempotency_key TEXT PRIMARY KEY,
  work_order_id TEXT NOT NULL,
  version INTEGER NOT NULL,
  digest TEXT NOT NULL,
  quest_id TEXT NOT NULL,
  response_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(work_order_id,version)
);
INSERT INTO work_order_approvals_v2_next SELECT idempotency_key,work_order_id,version,digest,quest_id,response_json,created_at FROM work_order_approvals_v2;
DROP TABLE work_order_approvals_v2;
ALTER TABLE work_order_approvals_v2_next RENAME TO work_order_approvals_v2;
CREATE INDEX work_order_approvals_quest_v2 ON work_order_approvals_v2(quest_id,version DESC);
`)
	return err
}

func migrationAgentHubWorkOrderDiffsV2(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE work_order_revision_diffs_v2 (
  id TEXT PRIMARY KEY,
  work_order_id TEXT NOT NULL,
  from_version INTEGER NOT NULL,
  to_version INTEGER NOT NULL,
  payload_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(work_order_id,from_version,to_version)
);
CREATE INDEX work_order_revision_diffs_order_v2 ON work_order_revision_diffs_v2(work_order_id,to_version DESC);
CREATE TRIGGER work_order_revision_diffs_no_update_v2 BEFORE UPDATE ON work_order_revision_diffs_v2 BEGIN SELECT RAISE(ABORT,'work order diffs are immutable'); END;
CREATE TRIGGER work_order_revision_diffs_no_delete_v2 BEFORE DELETE ON work_order_revision_diffs_v2 BEGIN SELECT RAISE(ABORT,'work order diffs are immutable'); END;
`)
	return err
}

func migrationAgentHubQuestControlsV2(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE work_order_quest_control_events_v2 (
  sequence INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT NOT NULL UNIQUE,
  quest_id TEXT NOT NULL,
  action TEXT NOT NULL,
  from_status TEXT NOT NULL,
  to_status TEXT NOT NULL,
  message TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX work_order_quest_controls_quest_v2 ON work_order_quest_control_events_v2(quest_id,sequence);
CREATE TRIGGER work_order_quest_controls_no_update_v2 BEFORE UPDATE ON work_order_quest_control_events_v2 BEGIN SELECT RAISE(ABORT,'quest control events are immutable'); END;
CREATE TRIGGER work_order_quest_controls_no_delete_v2 BEFORE DELETE ON work_order_quest_control_events_v2 BEGIN SELECT RAISE(ABORT,'quest control events are immutable'); END;
`)
	return err
}

func migrationAgentHubCompletionGateV2(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE work_order_completion_gates_v2 (
  quest_id TEXT PRIMARY KEY,
  work_order_id TEXT NOT NULL,
  version INTEGER NOT NULL,
  digest TEXT NOT NULL,
  evidence_id TEXT NOT NULL,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TRIGGER work_order_quest_no_false_completion_v2
BEFORE UPDATE OF status ON quests
WHEN NEW.status='completed'
 AND OLD.controller_json LIKE '%"source":"work_order_v2"%'
 AND NOT EXISTS (SELECT 1 FROM work_order_completion_gates_v2 gate WHERE gate.quest_id=NEW.id AND gate.status='completed')
BEGIN SELECT RAISE(ABORT,'v2 quest requires a successful evidence and delivery gate'); END;
`)
	return err
}

func migrationAgentHubSourceSnapshotsV2(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE source_snapshots_v2 (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL,
  digest TEXT NOT NULL,
  storage_path TEXT NOT NULL DEFAULT '',
  payload_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX source_snapshots_workspace_v2 ON source_snapshots_v2(workspace_id,created_at DESC);
CREATE TRIGGER source_snapshots_v2_no_update BEFORE UPDATE ON source_snapshots_v2 BEGIN SELECT RAISE(ABORT,'source snapshots are immutable'); END;
CREATE TRIGGER source_snapshots_v2_no_delete BEFORE DELETE ON source_snapshots_v2 BEGIN SELECT RAISE(ABORT,'source snapshots are immutable'); END;
`)
	return err
}

func migrationAgentHubWorkOrdersV2(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE work_order_revisions_v2 (
  id TEXT NOT NULL,
  version INTEGER NOT NULL CHECK(version > 0),
  workspace_id TEXT NOT NULL DEFAULT '',
  digest TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY(id,version)
);
CREATE TABLE work_order_current_v2 (
  id TEXT PRIMARY KEY,
  version INTEGER NOT NULL,
  workspace_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  digest TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE work_order_approvals_v2 (
  idempotency_key TEXT PRIMARY KEY,
  work_order_id TEXT NOT NULL,
  version INTEGER NOT NULL,
  digest TEXT NOT NULL,
  quest_id TEXT NOT NULL UNIQUE,
  response_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX work_order_current_workspace_v2 ON work_order_current_v2(workspace_id,updated_at DESC);
CREATE TRIGGER work_order_revisions_v2_no_update BEFORE UPDATE ON work_order_revisions_v2 BEGIN SELECT RAISE(ABORT,'work order revisions are immutable'); END;
CREATE TRIGGER work_order_revisions_v2_no_delete BEFORE DELETE ON work_order_revisions_v2 BEGIN SELECT RAISE(ABORT,'work order revisions are immutable'); END;
`)
	return err
}

func migrationProjectAgentSubagentsV1(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`ALTER TABLE project_agents ADD COLUMN parent_agent_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE project_agents ADD COLUMN temporary INTEGER NOT NULL DEFAULT 0`,
		`CREATE INDEX project_agents_parent ON project_agents(workspace_id,parent_agent_id,temporary,updated_at DESC)`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrationEgressAsksV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE egress_asks (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  quest_id TEXT NOT NULL DEFAULT '',
  run_id TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL,
  target TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  risk TEXT NOT NULL DEFAULT 'HIGH',
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  resolved_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX egress_asks_workspace_status ON egress_asks(workspace_id, status, created_at DESC);
CREATE UNIQUE INDEX egress_asks_pending_target ON egress_asks(workspace_id, kind, target) WHERE status = 'pending';
`)
	return err
}

func migrationURLIntakeV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE source_bundles (
  id TEXT PRIMARY KEY,
  primary_url TEXT NOT NULL,
  kind TEXT NOT NULL,
  digest TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX source_bundles_digest ON source_bundles(digest);
CREATE TRIGGER source_bundles_no_update BEFORE UPDATE ON source_bundles BEGIN SELECT RAISE(ABORT, 'source bundles are immutable'); END;
CREATE TRIGGER source_bundles_no_delete BEFORE DELETE ON source_bundles BEGIN SELECT RAISE(ABORT, 'source bundles are immutable'); END;

CREATE TABLE intake_sessions (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL DEFAULT '',
  quest_id TEXT NOT NULL DEFAULT '',
  proposal_id TEXT NOT NULL DEFAULT '',
  source_bundle_id TEXT NOT NULL,
  url TEXT NOT NULL,
  status TEXT NOT NULL,
  environment_json TEXT NOT NULL DEFAULT '{}',
  requirements_json TEXT NOT NULL DEFAULT '[]',
  coverage_json TEXT NOT NULL DEFAULT '{}',
  brief_json TEXT,
  delivery_json TEXT NOT NULL DEFAULT '{}',
  blockers_json TEXT NOT NULL DEFAULT '[]',
  error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY(source_bundle_id) REFERENCES source_bundles(id)
);
CREATE INDEX intake_sessions_updated ON intake_sessions(updated_at DESC);
CREATE INDEX intake_sessions_workspace ON intake_sessions(workspace_id, updated_at DESC);

CREATE TABLE quest_tool_leases (
  id TEXT PRIMARY KEY,
  quest_id TEXT NOT NULL,
  workspace_id TEXT NOT NULL,
  environment_digest TEXT NOT NULL,
  tool_names_json TEXT NOT NULL,
  expires_at TEXT NOT NULL
);
CREATE INDEX quest_tool_leases_quest ON quest_tool_leases(quest_id, expires_at DESC);

CREATE TABLE evidence_bundles (
  id TEXT PRIMARY KEY,
  quest_id TEXT NOT NULL UNIQUE,
  payload_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TRIGGER evidence_bundles_no_update BEFORE UPDATE ON evidence_bundles BEGIN SELECT RAISE(ABORT, 'evidence bundles are immutable'); END;
CREATE TRIGGER evidence_bundles_no_delete BEFORE DELETE ON evidence_bundles BEGIN SELECT RAISE(ABORT, 'evidence bundles are immutable'); END;
`)
	return err
}

func migrationExecutionRuntimeSessionV1(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `ALTER TABLE executions ADD COLUMN runtime TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `ALTER TABLE executions ADD COLUMN runtime_session_id TEXT NOT NULL DEFAULT ''`)
	return err
}

func migrationTeamEventsV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE team_events (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  quest_id TEXT NOT NULL,
  flow_run_id TEXT NOT NULL,
  flow_node_id TEXT NOT NULL,
  from_agent_id TEXT NOT NULL,
  to_agent_id TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL,
  message TEXT NOT NULL,
  artifact_id TEXT NOT NULL DEFAULT '',
  delivered_at TEXT,
  created_at TEXT NOT NULL
);
CREATE INDEX team_events_inbox
  ON team_events(workspace_id, flow_run_id, to_agent_id, created_at);
`)
	return err
}

func migrationAgentPrepChainsV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE agent_prep_chains (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  parent_quest_id TEXT NOT NULL,
  prep_quest_id TEXT NOT NULL,
  deferred_task_hash TEXT NOT NULL,
  requirement_json TEXT NOT NULL,
  candidate_agent_id TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL,
  error TEXT NOT NULL DEFAULT '',
  attempts INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX agent_prep_dedup_active
  ON agent_prep_chains(workspace_id, deferred_task_hash, state)
  WHERE state IN ('design','create','verify');
CREATE INDEX agent_prep_parent ON agent_prep_chains(parent_quest_id, updated_at DESC);
`)
	return err
}

func migrationQuestControllerV1(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`ALTER TABLE quests ADD COLUMN kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE quests ADD COLUMN controller_state TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE quests ADD COLUMN controller_json TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE quests ADD COLUMN prerequisite_ids_json TEXT NOT NULL DEFAULT '[]'`,
		`CREATE INDEX quests_controller_state ON quests(workspace_id, controller_state, updated_at DESC)`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrationModelCapabilityEvidenceV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE model_capability_evidence (
  id TEXT PRIMARY KEY,
  connection_id TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL,
  provider TEXT NOT NULL,
  runtime TEXT NOT NULL,
  role TEXT NOT NULL DEFAULT '',
  capabilities_json TEXT NOT NULL DEFAULT '[]',
  tool_calls TEXT NOT NULL,
  json_contract TEXT NOT NULL,
  inspection_before_edit TEXT NOT NULL,
  verification_evidence TEXT NOT NULL,
  within_limits TEXT NOT NULL,
  latency_ms INTEGER NOT NULL DEFAULT 0,
  input_tokens INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  healthy INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE INDEX model_capability_latest
  ON model_capability_evidence(connection_id, model, role, created_at DESC);
`)
	return err
}

func migrationWorkspaceModelRoutingV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE workspace_model_routing (
  workspace_id TEXT PRIMARY KEY,
  coding_connection_id TEXT NOT NULL DEFAULT '',
  coding_model TEXT NOT NULL DEFAULT '',
  cheap_connection_id TEXT NOT NULL DEFAULT '',
  cheap_model TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL
);
CREATE INDEX workspace_model_routing_coding_connection ON workspace_model_routing(coding_connection_id) WHERE coding_connection_id <> '';
CREATE INDEX workspace_model_routing_cheap_connection ON workspace_model_routing(cheap_connection_id) WHERE cheap_connection_id <> '';
`)
	return err
}

// Actor model targets used to be represented more completely in Go than in
// SQLite: legacy profiles lost connection_id, while Companion and Master lost
// Azure api_version on every restart.  Connections are still the live source
// of truth; these columns preserve the immutable compatibility fallback and
// make every actor configuration round-trip without data loss.
func migrationActorModelTargetV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
ALTER TABLE profiles ADD COLUMN connection_id TEXT NOT NULL DEFAULT '';
ALTER TABLE profiles ADD COLUMN api_version TEXT NOT NULL DEFAULT '';
ALTER TABLE companion_config ADD COLUMN api_version TEXT NOT NULL DEFAULT '';
ALTER TABLE orchestrator_config ADD COLUMN api_version TEXT NOT NULL DEFAULT '';
UPDATE profiles SET connection_id = (
  SELECT c.id FROM connections c WHERE c.preset_id = profiles.provider_preset
)
WHERE provider_preset <> ''
  AND (SELECT COUNT(1) FROM connections c WHERE c.preset_id = profiles.provider_preset) = 1;
`)
	return err
}

// Оценка ответа принадлежит самой реплике, а не машине, на которой её поставили.
//
// У компаньона отметки лежат в состоянии рабочей области расширения: переставил
// IDE — и «не помогло» исчезло вместе с причиной, по которой ответ считали
// плохим. У Мастера оценка ценнее вдвойне: по ней видно, какие постановки задач
// человек принимает, а какие переделывает, и это тот самый сигнал, ради которого
// заведены learning_signals.
func migrationChatMessageFeedbackV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `ALTER TABLE companion_messages ADD COLUMN feedback TEXT NOT NULL DEFAULT ''`)
	return err
}

// Как Мастер пришёл к ответу — часть самого ответа, а не свойство сессии.
//
// Рассуждение и раунды инструментов ядро собирало и выбрасывало. Показать их
// только в живом ходе значило бы потерять при первом переоткрытии панели: ровно
// так уже пропадала карточка предложенного квеста, и лечилось это тем же —
// хранением рядом с репликой.
func migrationChatMessageReasoningV1(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`ALTER TABLE companion_messages ADD COLUMN reasoning TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE companion_messages ADD COLUMN steps_json TEXT NOT NULL DEFAULT '[]'`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrationQuestSelectionBreakdownV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `ALTER TABLE quest_proposals ADD COLUMN selection_breakdown TEXT NOT NULL DEFAULT '[]'`)
	return err
}

func migrationFlowChildQuestsV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
ALTER TABLE quests ADD COLUMN flow_run_id TEXT NOT NULL DEFAULT '';
ALTER TABLE quests ADD COLUMN flow_node_id TEXT NOT NULL DEFAULT '';
ALTER TABLE quests ADD COLUMN assigned_agent_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX quests_flow_run_node ON quests(flow_run_id,flow_node_id) WHERE flow_run_id <> '' AND flow_node_id <> '';
ALTER TABLE budget_reservations ADD COLUMN budget_scope_quest_id TEXT NOT NULL DEFAULT '';
UPDATE budget_reservations SET budget_scope_quest_id=quest_id WHERE budget_scope_quest_id='';
CREATE INDEX budget_reservations_scope_status ON budget_reservations(budget_scope_quest_id,status);
`)
	return err
}

func migrationBudgetReservationsV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE budget_reservations (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  quest_id TEXT NOT NULL DEFAULT '',
  execution_id TEXT NOT NULL DEFAULT '',
  run_id TEXT NOT NULL,
  provider TEXT NOT NULL,
  model TEXT NOT NULL,
  estimated_input_tokens INTEGER NOT NULL CHECK(estimated_input_tokens >= 0),
  max_output_tokens INTEGER NOT NULL CHECK(max_output_tokens >= 0),
  reserved_tokens INTEGER NOT NULL CHECK(reserved_tokens >= 0),
  reserved_cents INTEGER NOT NULL CHECK(reserved_cents >= 0),
  actual_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(actual_input_tokens >= 0),
  actual_output_tokens INTEGER NOT NULL DEFAULT 0 CHECK(actual_output_tokens >= 0),
  actual_cents INTEGER NOT NULL DEFAULT 0 CHECK(actual_cents >= 0),
  usage_reported INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL CHECK(status IN ('reserved','reconciled','conservative','released')),
  created_at TEXT NOT NULL,
  reconciled_at TEXT
);
CREATE INDEX budget_reservations_workspace_status_created ON budget_reservations(workspace_id,status,created_at);
CREATE INDEX budget_reservations_quest_status ON budget_reservations(quest_id,status);
CREATE INDEX budget_reservations_run ON budget_reservations(run_id);
CREATE TABLE model_pricing_profiles (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  provider TEXT NOT NULL,
  model TEXT NOT NULL,
  input_cents_per_million INTEGER NOT NULL CHECK(input_cents_per_million >= 0),
  output_cents_per_million INTEGER NOT NULL CHECK(output_cents_per_million >= 0),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(workspace_id,provider,model)
);
`)
	return err
}

func migrationWorkspaceScopedEventsV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
DROP TRIGGER IF EXISTS events_no_update;
DROP TRIGGER IF EXISTS events_no_delete;
ALTER TABLE events ADD COLUMN workspace_id TEXT NOT NULL DEFAULT '';
UPDATE events
SET workspace_id = COALESCE((SELECT runs.workspace_id FROM runs WHERE runs.id = events.run_id), '')
WHERE workspace_id = '';
CREATE INDEX events_workspace_sequence ON events(workspace_id, sequence);
CREATE TRIGGER events_no_update BEFORE UPDATE ON events BEGIN SELECT RAISE(ABORT, 'events are immutable'); END;
CREATE TRIGGER events_no_delete BEFORE DELETE ON events BEGIN SELECT RAISE(ABORT, 'events are immutable'); END;
`)
	return err
}

// Подключение становится тем, на что ссылаются, а не тем, что угадывают.
//
// До этой миграции агент, компаньон и Дирижёр хранили копии provider/preset/URL,
// а ключ к запросу подбирался поиском первого подключения с тем же пресетом.
// Два ключа одного провайдера — личный и рабочий, dev и prod — и запрос уходил
// с чужим ключом молча.
//
// Обратное заполнение намеренно осторожное: связь проставляется только там, где
// подключение с таким пресетом ровно одно. Там, где их несколько, поле остаётся
// пустым — именно эти строки сегодня и ломались, и записать в них угаданное
// значение значило бы закрепить ошибку в данных вместо того, чтобы спросить
// человека.
func migrationLLMConnectionBindingV1(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`ALTER TABLE connections ADD COLUMN default_model TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE connections ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE connections ADD COLUMN api_version TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE connections ADD COLUMN model_catalog_json TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE connections ADD COLUMN catalog_updated_at TEXT`,
		`ALTER TABLE agent_blueprints ADD COLUMN connection_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE project_agents ADD COLUMN connection_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE companion_config ADD COLUMN connection_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE orchestrator_config ADD COLUMN connection_id TEXT NOT NULL DEFAULT ''`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	for _, table := range []string{"agent_blueprints", "project_agents", "companion_config", "orchestrator_config"} {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
UPDATE %s SET connection_id = (
  SELECT c.id FROM connections c WHERE c.preset_id = %s.provider_preset
)
WHERE provider_preset <> ''
  AND (SELECT COUNT(1) FROM connections c WHERE c.preset_id = %s.provider_preset) = 1`, table, table, table)); err != nil {
			return err
		}
	}
	return nil
}

func migrationManualToolExecutionApprovalV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE tool_execution_approvals (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  tool_id TEXT NOT NULL,
  tool_digest TEXT NOT NULL,
  arguments_digest TEXT NOT NULL,
  arguments TEXT NOT NULL,
  reason TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('pending','allowed','denied','consumed')),
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  resolved_at TEXT,
  consumed_at TEXT
);
CREATE INDEX tool_execution_approvals_workspace_created ON tool_execution_approvals(workspace_id, created_at DESC);
CREATE INDEX tool_execution_approvals_expiry_status ON tool_execution_approvals(status, expires_at);
`)
	return err
}

func migrationCompatibilityUsageAndProfileReconcileV1(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `
CREATE TABLE compatibility_usage (
  feature TEXT NOT NULL,
  workspace_id TEXT NOT NULL DEFAULT '',
  application_version TEXT NOT NULL,
  legacy_version TEXT NOT NULL,
  count INTEGER NOT NULL DEFAULT 0 CHECK(count >= 0),
  first_seen TEXT NOT NULL,
  last_seen TEXT NOT NULL,
  PRIMARY KEY(feature, workspace_id, application_version, legacy_version)
);
CREATE INDEX compatibility_usage_workspace_seen ON compatibility_usage(workspace_id, last_seen DESC);
CREATE INDEX compatibility_usage_feature_version ON compatibility_usage(feature, application_version);
`); err != nil {
		return err
	}
	// Migration 2 copied the profile table once. Older binaries could still
	// write another profile afterwards, so reconcile missing rows again before
	// the runtime-only startup bridge is removed. Existing blueprints always win
	// and no profile row is deleted.
	return migrationProfilesToBlueprintsV1(ctx, tx)
}

func migrationAgentBenchmarksV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE agent_benchmark_sets (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  project_agent_id TEXT NOT NULL,
  skill_id TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  revision INTEGER NOT NULL,
  digest TEXT NOT NULL,
  cases_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX agent_benchmark_sets_workspace_updated ON agent_benchmark_sets(workspace_id, updated_at DESC);
CREATE INDEX agent_benchmark_sets_agent_updated ON agent_benchmark_sets(project_agent_id, updated_at DESC);
CREATE TABLE agent_benchmark_evaluations (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  project_agent_id TEXT NOT NULL,
  benchmark_set_id TEXT NOT NULL,
  set_revision INTEGER NOT NULL,
  set_digest TEXT NOT NULL,
  label TEXT NOT NULL,
  cases_json TEXT NOT NULL,
  metrics_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY(benchmark_set_id) REFERENCES agent_benchmark_sets(id) ON DELETE CASCADE
);
CREATE INDEX agent_benchmark_evaluations_set_created ON agent_benchmark_evaluations(benchmark_set_id, created_at DESC);
CREATE INDEX agent_benchmark_evaluations_workspace_created ON agent_benchmark_evaluations(workspace_id, created_at DESC);
`)
	return err
}

func migrationSkillCanaryEvaluationV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `ALTER TABLE agent_improvements ADD COLUMN canary_evaluation_json TEXT NOT NULL DEFAULT ''`)
	return err
}

func migrationSandboxBackendAttributionV1(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`ALTER TABLE sandboxes ADD COLUMN backend TEXT NOT NULL DEFAULT 'filtered-copy'`,
		`ALTER TABLE sandboxes ADD COLUMN backend_version TEXT NOT NULL DEFAULT '1'`,
		`ALTER TABLE sandboxes ADD COLUMN backend_image TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sandboxes ADD COLUMN backend_image_digest TEXT NOT NULL DEFAULT ''`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrationLearningSignalsAndSkillOutcomesV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE learning_signals (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  project_agent_id TEXT NOT NULL,
  blueprint_id TEXT NOT NULL DEFAULT '',
  run_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'observed',
  summary TEXT NOT NULL DEFAULT '',
  evidence_json TEXT NOT NULL DEFAULT '[]',
  skill_attributions_json TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX learning_signals_run_kind ON learning_signals(run_id, kind);
CREATE INDEX learning_signals_workspace_updated ON learning_signals(workspace_id, updated_at DESC);
CREATE INDEX learning_signals_agent_updated ON learning_signals(project_agent_id, updated_at DESC);
CREATE INDEX learning_signals_blueprint_updated ON learning_signals(blueprint_id, updated_at DESC);

CREATE TABLE skill_outcomes (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  project_agent_id TEXT NOT NULL,
  blueprint_id TEXT NOT NULL DEFAULT '',
  run_id TEXT NOT NULL,
  skill_id TEXT NOT NULL,
  skill_name TEXT NOT NULL DEFAULT '',
  skill_revision INTEGER NOT NULL DEFAULT 0,
  skill_digest TEXT NOT NULL,
  promotion_status TEXT NOT NULL DEFAULT '',
  run_status TEXT NOT NULL,
  health TEXT NOT NULL,
  tool_calls INTEGER NOT NULL DEFAULT 0,
  tool_failures INTEGER NOT NULL DEFAULT 0,
  approval_denied INTEGER NOT NULL DEFAULT 0,
  feedback_count INTEGER NOT NULL DEFAULT 0,
  completion_revisions INTEGER NOT NULL DEFAULT 0,
  verification_required INTEGER NOT NULL DEFAULT 0,
  verification_recorded INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX skill_outcomes_run_skill ON skill_outcomes(run_id, skill_id);
CREATE INDEX skill_outcomes_workspace_created ON skill_outcomes(workspace_id, created_at DESC);
CREATE INDEX skill_outcomes_agent_created ON skill_outcomes(project_agent_id, created_at DESC);
CREATE INDEX skill_outcomes_skill_created ON skill_outcomes(skill_id, created_at DESC);
`)
	return err
}

func migrationAgentImprovementsV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE agent_improvements (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  project_agent_id TEXT NOT NULL,
  source_run_id TEXT NOT NULL,
  skill_id TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  trigger_text TEXT NOT NULL DEFAULT '',
  evidence_json TEXT NOT NULL DEFAULT '[]',
  before_skill_json TEXT NOT NULL DEFAULT '',
  after_skill_json TEXT NOT NULL DEFAULT '',
  before_skill_ids_json TEXT NOT NULL DEFAULT '[]',
  after_skill_ids_json TEXT NOT NULL DEFAULT '[]',
  review_mode TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  failure TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX agent_improvements_source_run ON agent_improvements(source_run_id);
CREATE INDEX agent_improvements_workspace_updated ON agent_improvements(workspace_id, updated_at DESC);
CREATE INDEX agent_improvements_agent_updated ON agent_improvements(project_agent_id, updated_at DESC);
CREATE INDEX agent_improvements_skill_updated ON agent_improvements(skill_id, updated_at DESC);
`)
	return err
}

func migrationAgentLearningPromotionV1(ctx context.Context, tx *sql.Tx) error {
	alters := []string{
		`ALTER TABLE agent_improvements ADD COLUMN blueprint_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agent_improvements ADD COLUMN promotion_status TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agent_improvements ADD COLUMN before_blueprint_skill_ids_json TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE agent_improvements ADD COLUMN after_blueprint_skill_ids_json TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE agent_improvements ADD COLUMN before_agent_skill_ids_json TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE agent_improvements ADD COLUMN after_agent_skill_ids_json TEXT NOT NULL DEFAULT '{}'`,
	}
	for _, stmt := range alters {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func migrationAgentLearningMemoryV1(ctx context.Context, tx *sql.Tx) error {
	alters := []string{
		`ALTER TABLE agent_improvements ADD COLUMN memory_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agent_improvements ADD COLUMN memory_status TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agent_improvements ADD COLUMN memory_key TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agent_improvements ADD COLUMN memory_signature TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agent_improvements ADD COLUMN memory_source_workspaces_json TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE agent_improvements ADD COLUMN before_memory_json TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agent_improvements ADD COLUMN after_memory_json TEXT NOT NULL DEFAULT ''`,
	}
	for _, stmt := range alters {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `CREATE INDEX agent_improvements_memory_updated ON agent_improvements(memory_id, updated_at DESC)`)
	return err
}

func migrationAgentLearningInstructionV1(ctx context.Context, tx *sql.Tx) error {
	alters := []string{
		`ALTER TABLE agent_improvements ADD COLUMN instruction_status TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agent_improvements ADD COLUMN instruction_key TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agent_improvements ADD COLUMN instruction_text TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agent_improvements ADD COLUMN instruction_signature TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agent_improvements ADD COLUMN instruction_source_workspaces_json TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE agent_improvements ADD COLUMN before_blueprint_rules_json TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE agent_improvements ADD COLUMN after_blueprint_rules_json TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE agent_improvements ADD COLUMN before_agent_rules_json TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE agent_improvements ADD COLUMN after_agent_rules_json TEXT NOT NULL DEFAULT '{}'`,
	}
	for _, stmt := range alters {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `CREATE INDEX agent_improvements_instruction_updated ON agent_improvements(instruction_signature, updated_at DESC)`)
	return err
}

// Технический balanced-конфиг создаётся до первого сообщения, чтобы локальный
// помощник работал сразу. Он ещё не означает, что человек прошёл настройку:
// отдельный флаг не даёт онбордингу перепрыгнуть выбор характера и мозга.
func migrationCompanionSkillsV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `ALTER TABLE companion_config ADD COLUMN skill_ids_json TEXT NOT NULL DEFAULT ''`)
	return err
}

func migrationCompanionConfiguredV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `ALTER TABLE companion_config ADD COLUMN configured INTEGER NOT NULL DEFAULT 0`)
	return err
}

func migrationHubOntologyV1(ctx context.Context, tx *sql.Tx) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS agent_blueprints (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  role_description TEXT NOT NULL,
  personality TEXT NOT NULL DEFAULT '',
  mission TEXT NOT NULL DEFAULT '',
  system_prompt TEXT NOT NULL,
  goals TEXT NOT NULL DEFAULT '[]',
  rules TEXT NOT NULL DEFAULT '[]',
  constraints_json TEXT NOT NULL DEFAULT '[]',
  skill_ids TEXT NOT NULL DEFAULT '[]',
  allowed_tools TEXT NOT NULL DEFAULT '[]',
  tool_policies TEXT NOT NULL DEFAULT '{}',
  provider TEXT NOT NULL,
  provider_preset TEXT NOT NULL DEFAULT '',
  base_url TEXT NOT NULL DEFAULT '',
  primary_model TEXT NOT NULL DEFAULT '',
  fallback_models TEXT NOT NULL DEFAULT '[]',
  temperature REAL NOT NULL DEFAULT 0.2,
  max_output_tokens INTEGER NOT NULL DEFAULT 4096,
  context_window_tokens INTEGER NOT NULL DEFAULT 32768,
  reasoning_effort TEXT NOT NULL DEFAULT 'medium',
  max_steps INTEGER NOT NULL DEFAULT 12,
  max_duration_seconds INTEGER NOT NULL DEFAULT 900,
  approval_mode TEXT NOT NULL DEFAULT 'safe',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS project_agents (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  blueprint_id TEXT NOT NULL,
  name TEXT NOT NULL,
  role_description TEXT NOT NULL,
  personality TEXT NOT NULL DEFAULT '',
  mission TEXT NOT NULL DEFAULT '',
  system_prompt TEXT NOT NULL,
  goals TEXT NOT NULL DEFAULT '[]',
  rules TEXT NOT NULL DEFAULT '[]',
  constraints_json TEXT NOT NULL DEFAULT '[]',
  project_rules TEXT NOT NULL DEFAULT '[]',
  skill_ids TEXT NOT NULL DEFAULT '[]',
  allowed_tools TEXT NOT NULL DEFAULT '[]',
  tool_policies TEXT NOT NULL DEFAULT '{}',
  provider TEXT NOT NULL,
  provider_preset TEXT NOT NULL DEFAULT '',
  base_url TEXT NOT NULL DEFAULT '',
  primary_model TEXT NOT NULL DEFAULT '',
  fallback_models TEXT NOT NULL DEFAULT '[]',
  temperature REAL NOT NULL DEFAULT 0.2,
  max_output_tokens INTEGER NOT NULL DEFAULT 4096,
  context_window_tokens INTEGER NOT NULL DEFAULT 32768,
  reasoning_effort TEXT NOT NULL DEFAULT 'medium',
  max_steps INTEGER NOT NULL DEFAULT 12,
  max_duration_seconds INTEGER NOT NULL DEFAULT 900,
  approval_mode TEXT NOT NULL DEFAULT 'safe',
  experience INTEGER NOT NULL DEFAULT 0,
  level INTEGER NOT NULL DEFAULT 1,
  tasks_completed INTEGER NOT NULL DEFAULT 0,
  success_count INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS project_agents_workspace ON project_agents(workspace_id);
CREATE TABLE IF NOT EXISTS skill_definitions (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL,
  instructions TEXT NOT NULL,
  references_json TEXT NOT NULL DEFAULT '[]',
  scripts_json TEXT NOT NULL DEFAULT '[]',
  required_tools TEXT NOT NULL DEFAULT '[]',
  permission_delta TEXT NOT NULL DEFAULT '{}',
  configuration TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS project_skills (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  skill_id TEXT NOT NULL,
  configuration TEXT NOT NULL DEFAULT '{}',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS project_skills_workspace ON project_skills(workspace_id);
CREATE TABLE IF NOT EXISTS teams (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  agent_ids TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS teams_workspace ON teams(workspace_id);
CREATE TABLE IF NOT EXISTS quests (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  parent_id TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  objectives TEXT NOT NULL DEFAULT '[]',
  constraints_json TEXT NOT NULL DEFAULT '[]',
  definition_of_done TEXT NOT NULL DEFAULT '[]',
  importance TEXT NOT NULL DEFAULT 'normal',
  status TEXT NOT NULL DEFAULT 'draft',
  team_id TEXT NOT NULL DEFAULT '',
  flow_id TEXT NOT NULL DEFAULT '',
  budget_tokens INTEGER NOT NULL DEFAULT 0,
  budget_cents INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  finished_at TEXT
);
CREATE INDEX IF NOT EXISTS quests_workspace ON quests(workspace_id);
CREATE TABLE IF NOT EXISTS quest_links (
  id TEXT PRIMARY KEY,
  quest_id TEXT NOT NULL,
  link_type TEXT NOT NULL,
  target_id TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS flows (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  nodes TEXT NOT NULL DEFAULT '[]',
  edges TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS flows_workspace ON flows(workspace_id);
CREATE TABLE IF NOT EXISTS flow_runs (
  id TEXT PRIMARY KEY,
  flow_id TEXT NOT NULL,
  workspace_id TEXT NOT NULL,
  quest_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  node_states TEXT NOT NULL DEFAULT '{}',
  snapshot TEXT NOT NULL DEFAULT '{}',
  error TEXT NOT NULL DEFAULT '',
  result TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL,
  finished_at TEXT,
  duration_ms INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS executions (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  project_agent_id TEXT NOT NULL,
  quest_id TEXT NOT NULL DEFAULT '',
  flow_run_id TEXT NOT NULL DEFAULT '',
  flow_node_id TEXT NOT NULL DEFAULT '',
  run_id TEXT NOT NULL DEFAULT '',
  sandbox_id TEXT NOT NULL DEFAULT '',
  task TEXT NOT NULL,
  status TEXT NOT NULL,
  snapshot TEXT NOT NULL DEFAULT '{}',
  error TEXT NOT NULL DEFAULT '',
  result TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL,
  finished_at TEXT,
  duration_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS executions_workspace ON executions(workspace_id);
CREATE TABLE IF NOT EXISTS sandboxes (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  execution_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  path TEXT NOT NULL,
  base_commit TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  closed_at TEXT
);
CREATE TABLE IF NOT EXISTS change_sets (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  execution_id TEXT NOT NULL,
  quest_id TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  applied_at TEXT
);
CREATE TABLE IF NOT EXISTS change_items (
  id TEXT PRIMARY KEY,
  change_set_id TEXT NOT NULL,
  path TEXT NOT NULL,
  kind TEXT NOT NULL,
  original_hash TEXT NOT NULL DEFAULT '',
  proposed_hash TEXT NOT NULL DEFAULT '',
  diff TEXT NOT NULL DEFAULT '',
  patch_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS change_items_set ON change_items(change_set_id);
CREATE TABLE IF NOT EXISTS memories (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  owner_id TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT '',
  confidence REAL NOT NULL DEFAULT 0.5,
  pinned INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS memories_workspace ON memories(workspace_id);
CREATE TABLE IF NOT EXISTS connections (
  id TEXT PRIMARY KEY,
  provider TEXT NOT NULL,
  preset_id TEXT NOT NULL DEFAULT '',
  display_name TEXT NOT NULL,
  base_url TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'unknown',
  secret_ref TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  last_probe_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS usage_records (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  execution_id TEXT NOT NULL DEFAULT '',
  quest_id TEXT NOT NULL DEFAULT '',
  project_agent_id TEXT NOT NULL DEFAULT '',
  provider TEXT NOT NULL,
  model TEXT NOT NULL,
  input_tokens INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  total_tokens INTEGER NOT NULL DEFAULT 0,
  cost_cents INTEGER,
  latency_ms INTEGER NOT NULL DEFAULT 0,
  outcome TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE TRIGGER IF NOT EXISTS usage_records_no_update BEFORE UPDATE ON usage_records BEGIN SELECT RAISE(ABORT, 'usage_records are immutable'); END;
CREATE TRIGGER IF NOT EXISTS usage_records_no_delete BEFORE DELETE ON usage_records BEGIN SELECT RAISE(ABORT, 'usage_records are immutable'); END;
CREATE TABLE IF NOT EXISTS companion_config (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL DEFAULT '',
  preset TEXT NOT NULL DEFAULT 'balanced',
  criticality INTEGER NOT NULL DEFAULT 50,
  creativity INTEGER NOT NULL DEFAULT 50,
  verbosity INTEGER NOT NULL DEFAULT 50,
  initiative INTEGER NOT NULL DEFAULT 50,
  question_strictness INTEGER NOT NULL DEFAULT 70,
  risk_tolerance INTEGER NOT NULL DEFAULT 30,
  auto_act INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS quest_proposals (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  title TEXT NOT NULL,
  rationale TEXT NOT NULL DEFAULT '',
  unknowns TEXT NOT NULL DEFAULT '[]',
  objectives TEXT NOT NULL DEFAULT '[]',
  constraints_json TEXT NOT NULL DEFAULT '[]',
  definition_of_done TEXT NOT NULL DEFAULT '[]',
  team_agent_ids TEXT NOT NULL DEFAULT '[]',
  flow_id TEXT NOT NULL DEFAULT '',
  importance TEXT NOT NULL DEFAULT 'normal',
  estimate_tokens INTEGER NOT NULL DEFAULT 0,
  estimate_cents INTEGER,
  status TEXT NOT NULL DEFAULT 'pending',
  created_at TEXT NOT NULL
);`
	_, err := tx.ExecContext(ctx, ddl)
	return err
}

func migrationProfilesToBlueprintsV1(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `
SELECT id, name, role_description, system_prompt, goals, rules, provider, provider_preset, base_url, model,
       temperature, max_output_tokens, context_window_tokens, reasoning_effort, allowed_tools,
       max_steps, max_duration_seconds, approval_mode, created_at, updated_at
FROM profiles`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type row struct {
		id, name, role, prompt, goals, rules, provider, preset, baseURL, model string
		temperature                                                            float64
		maxOut, ctxWin, maxSteps, maxDur                                       int
		effort, tools, approval, created, updated                              string
	}
	var profiles []row
	for rows.Next() {
		var r row
		if err = rows.Scan(&r.id, &r.name, &r.role, &r.prompt, &r.goals, &r.rules, &r.provider, &r.preset, &r.baseURL, &r.model,
			&r.temperature, &r.maxOut, &r.ctxWin, &r.effort, &r.tools, &r.maxSteps, &r.maxDur, &r.approval, &r.created, &r.updated); err != nil {
			return err
		}
		profiles = append(profiles, r)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for _, p := range profiles {
		var exists int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM agent_blueprints WHERE id=?`, p.id).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		if _, err = tx.ExecContext(ctx, `
INSERT INTO agent_blueprints(
  id, name, role_description, system_prompt, goals, rules, allowed_tools, provider, provider_preset, base_url,
  primary_model, temperature, max_output_tokens, context_window_tokens, reasoning_effort, max_steps,
  max_duration_seconds, approval_mode, created_at, updated_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			p.id, p.name, p.role, p.prompt, p.goals, p.rules, p.tools, p.provider, p.preset, p.baseURL,
			p.model, p.temperature, p.maxOut, p.ctxWin, p.effort, p.maxSteps, p.maxDur, p.approval, p.created, p.updated); err != nil {
			return err
		}
	}
	return nil
}

func migrationChangeSetResolutionsV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `ALTER TABLE change_sets ADD COLUMN resolutions TEXT NOT NULL DEFAULT '[]'`)
	return err
}

func migrationChangeSetExactSnapshotsV1(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`ALTER TABLE change_items ADD COLUMN original_content TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE change_items ADD COLUMN proposed_content TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE change_items ADD COLUMN applied_hash TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE change_items ADD COLUMN applied_operation TEXT NOT NULL DEFAULT ''`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrationFlowSandboxLineageV1(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`ALTER TABLE sandboxes ADD COLUMN parent_sandbox_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sandboxes ADD COLUMN parent_execution_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sandboxes ADD COLUMN baseline_path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE change_sets ADD COLUMN depends_on TEXT NOT NULL DEFAULT '[]'`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrationFlowParallelMergeV1(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`ALTER TABLE sandboxes ADD COLUMN parent_sandbox_ids TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE sandboxes ADD COLUMN parent_execution_ids TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE sandboxes ADD COLUMN baseline_change_set_ids TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE change_sets ADD COLUMN kind TEXT NOT NULL DEFAULT 'execution'`,
		`ALTER TABLE change_sets ADD COLUMN supersedes TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE change_sets ADD COLUMN superseded_by TEXT NOT NULL DEFAULT ''`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrationCompanionModelConfigV1(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`ALTER TABLE companion_config ADD COLUMN provider TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE companion_config ADD COLUMN provider_preset TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE companion_config ADD COLUMN base_url TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE companion_config ADD COLUMN model TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE companion_config ADD COLUMN temperature REAL NOT NULL DEFAULT 0.2`,
		`ALTER TABLE companion_config ADD COLUMN max_output_tokens INTEGER NOT NULL DEFAULT 1200`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrationCompanionChatHistoryV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS companion_messages (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  role TEXT NOT NULL,
  content TEXT NOT NULL,
  level TEXT NOT NULL DEFAULT '',
  mode TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS companion_messages_workspace_created
  ON companion_messages(workspace_id, created_at DESC);
`)
	return err
}

func migrationCompanionMessageProvenanceV1(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		`ALTER TABLE companion_messages ADD COLUMN provider TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE companion_messages ADD COLUMN model TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE companion_messages ADD COLUMN facts_used_json TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE companion_messages ADD COLUMN usage_record_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE companion_messages ADD COLUMN proposal_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE companion_messages ADD COLUMN fallback_reason TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE companion_messages ADD COLUMN input_tokens INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE companion_messages ADD COLUMN output_tokens INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE companion_messages ADD COLUMN total_tokens INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE companion_messages ADD COLUMN latency_ms INTEGER NOT NULL DEFAULT 0`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func migrationCompanionMessageQuestionsV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `ALTER TABLE companion_messages ADD COLUMN questions_json TEXT NOT NULL DEFAULT '[]'`)
	return err
}

func migrationCompanionActionProposalsV1(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `
CREATE TABLE companion_action_proposals (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  title TEXT NOT NULL,
  rationale TEXT NOT NULL,
  payload_json TEXT NOT NULL DEFAULT '{}',
  status TEXT NOT NULL DEFAULT 'pending',
  applied_entity_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX companion_action_proposals_workspace_updated
  ON companion_action_proposals(workspace_id, updated_at DESC);
`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `ALTER TABLE companion_messages ADD COLUMN action_proposal_id TEXT NOT NULL DEFAULT ''`)
	return err
}

func migrationIDEObservationsV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE ide_observations (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT '',
  level TEXT NOT NULL DEFAULT 'info',
  summary TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT '',
  path TEXT NOT NULL DEFAULT '',
  line INTEGER NOT NULL DEFAULT 0,
  command TEXT NOT NULL DEFAULT '',
  exit_code INTEGER,
  observed_at TEXT NOT NULL
);
CREATE INDEX ide_observations_workspace_kind_observed
  ON ide_observations(workspace_id, kind, observed_at DESC);
`)
	return err
}

func migrationOrchestratorConfigV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS orchestrator_config (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL DEFAULT '',
  preset TEXT NOT NULL DEFAULT 'conductor',
  provider TEXT NOT NULL DEFAULT '',
  provider_preset TEXT NOT NULL DEFAULT '',
  base_url TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  temperature REAL NOT NULL DEFAULT 0.1,
  max_output_tokens INTEGER NOT NULL DEFAULT 2000,
  planning_depth INTEGER NOT NULL DEFAULT 70,
  parallelism INTEGER NOT NULL DEFAULT 60,
  approval_strictness INTEGER NOT NULL DEFAULT 40,
  team_preference INTEGER NOT NULL DEFAULT 85,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS orchestrator_config_workspace ON orchestrator_config(workspace_id, updated_at DESC);
`)
	return err
}

func migrationServerProfilesV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS server_profiles (
  id TEXT PRIMARY KEY,
  display_name TEXT NOT NULL,
  host TEXT NOT NULL,
  port INTEGER NOT NULL DEFAULT 22,
  username TEXT NOT NULL,
  auth_method TEXT NOT NULL DEFAULT 'agent',
  private_key_path TEXT NOT NULL DEFAULT '',
  secret_ref TEXT NOT NULL DEFAULT '',
  default_remote_path TEXT NOT NULL DEFAULT '~',
  status TEXT NOT NULL DEFAULT 'unknown',
  last_error TEXT NOT NULL DEFAULT '',
  last_probe_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS server_profiles_updated ON server_profiles(updated_at DESC);
`)
	return err
}

func migrationCompanionQuietObserveV1(ctx context.Context, tx *sql.Tx) error {
	alters := []string{
		`ALTER TABLE ide_observations ADD COLUMN first_seen TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ide_observations ADD COLUMN last_seen TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ide_observations ADD COLUMN count INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE ide_observations ADD COLUMN novelty_hash TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ide_observations ADD COLUMN focus_path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE companion_config ADD COLUMN auto_open_chat_on_critical INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE companion_config ADD COLUMN auto_send_model_prompt INTEGER NOT NULL DEFAULT 0`,
	}
	for _, stmt := range alters {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// Компаньон и Мастер — разные собеседники, но хроника разговоров у них одна.
// Колонка speaker разделяет их, не заводя вторую таблицу: история остаётся
// цельной, а выборка — раздельной. Прежние записи принадлежат компаньону.
func migrationChatSpeakerV1(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `ALTER TABLE companion_messages ADD COLUMN speaker TEXT NOT NULL DEFAULT 'companion'`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS companion_messages_speaker ON companion_messages(workspace_id, speaker, created_at DESC)`)
	return err
}

// migrationQuestProposalTaskV1 даёт задаче место в предложении квеста.
//
// Задача жила только в переписке: в квест уходил Rationale — объяснение выбора
// отряда («пресет conductor · отряд 1 · движком Point»), — и оно попадало в
// описание квеста, а оттуда в контекст исполняющего агента. Агент получал
// строку о том, как его выбирали, там где должно стоять, что сделать.
func migrationQuestProposalTaskV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `ALTER TABLE quest_proposals ADD COLUMN task TEXT NOT NULL DEFAULT ''`)
	return err
}

// Отдельный признак отличает предварительный состав Мастера от выбора
// человека. Старые изменённые карточки пришли из интерфейса, который всегда
// отправлял полный список флажков, поэтому непустой состав в них уже был
// подтверждён вручную и должен остаться таким после обновления.
func migrationQuestProposalTeamLockV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
ALTER TABLE quest_proposals ADD COLUMN team_agent_ids_locked INTEGER NOT NULL DEFAULT 0;
UPDATE quest_proposals
SET team_agent_ids_locked=1
WHERE status='modified' AND team_agent_ids <> '[]'`)
	return err
}
