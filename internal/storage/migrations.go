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
		{68, "project_agent_lifecycle_v1", migrationProjectAgentLifecycleV1},
	}
}
