// Схема второго контура: наряды, вехи, снимки источника, шлюз завершения.
//
// Порядок версий задан реестром в migrations.go; здесь только тела.
package storage

import (
	"context"
	"database/sql"
)

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
