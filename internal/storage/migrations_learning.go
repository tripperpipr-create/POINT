// Схема обучения: улучшения агентов, память, инструкции, бенчмарки.
package storage

import (
	"context"
	"database/sql"
)

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
