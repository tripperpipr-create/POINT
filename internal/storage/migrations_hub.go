// Схема Гильдии: онтология, чертежи, наборы изменений, потоки, квесты.
package storage

import (
	"context"
	"database/sql"
)

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

func migrationQuestSelectionBreakdownV1(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `ALTER TABLE quest_proposals ADD COLUMN selection_breakdown TEXT NOT NULL DEFAULT '[]'`)
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
