// Схема исполнения: сессии, бюджеты, подключения, разрешения, серверы.
package storage

import (
	"context"
	"database/sql"
	"fmt"
)

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
