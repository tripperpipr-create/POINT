// Схема компаньона: настройка, переписка, наблюдения редактора, вмешательства.
package storage

import (
	"context"
	"database/sql"
)

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
