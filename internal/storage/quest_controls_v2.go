package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
)

// WorkOrderScopeRevisionState — квест приостановлен правкой утверждённого
// наряда и ждёт утверждения новой версии.
const WorkOrderScopeRevisionState = "scope_revision"

var ErrWorkOrderRevisionPending = errors.New("наряд изменён после утверждения: утвердите новую версию — прежняя не продолжается")

// ErrQuestStatusChanged — статус квеста изменился после того, как переход
// был рассчитан: кто-то — обычно человек кнопкой в карточке — успел раньше.
var ErrQuestStatusChanged = errors.New("статус квеста изменился: переход отменён, решение, принятое раньше, сохраняется")

// SetWorkOrderQuestStatusV2 пишет статус квеста наряда, только если в базе
// всё ещё expected. Прежде переходы писались снимком квеста целиком, и
// финализатор, державший копию в `applying`, перезаписывал отмену человеком на
// `blocked` — отменённый квест снова можно было «продолжить». Меняются только
// статус, контроллер и время: остальное снимок не владеет правом переписать.
func (s *SQLite) SetWorkOrderQuestStatusV2(ctx context.Context, quest domain.Quest, expected domain.QuestStatus) error {
	var finished any
	if quest.FinishedAt != nil {
		finished = formatTime(*quest.FinishedAt)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE quests SET status=?,controller_state=?,controller_json=?,updated_at=?,finished_at=? WHERE id=? AND workspace_id=? AND status=?`,
		quest.Status, quest.ControllerState, marshalJSON(quest.Controller), formatTime(quest.UpdatedAt), finished, quest.ID, quest.WorkspaceID, expected)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrQuestStatusChanged
	}
	return nil
}

// RecordWorkOrderQuestPauseV2 записывает паузу, поставленную не человеком, а
// ядром, — например, восстановлением после рестарта. Действие своё, не
// `pause`: починка при старте пропускает квесты, которые человек сам
// поставил на паузу, и пауза рестарта не должна выглядеть его решением. Продолжение берёт
// прежний статус из последней паузы: без этой записи оно возвращало `running`
// квесту, который до рестарта ещё проходил проверку окружения, и карточка
// показывала работу, которую никто не делал.
func (s *SQLite) RecordWorkOrderQuestPauseV2(ctx context.Context, questID string, from domain.QuestStatus, message string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO work_order_quest_control_events_v2(id,quest_id,action,from_status,to_status,message,created_at) VALUES(?,?,?,?,?,?,?)`,
		domain.NewID("control"), questID, "recover_pause", from, domain.QuestPaused, security.Redact(message), formatTime(time.Now().UTC()))
	return err
}

func (s *SQLite) ControlWorkOrderQuestV2(ctx context.Context, questID, action, message string) (domain.QuestStatus, error) {
	questID, action = strings.TrimSpace(questID), strings.ToLower(strings.TrimSpace(action))
	message = strings.TrimSpace(message)
	if questID == "" {
		return "", errors.New("quest id is required")
	}
	if action != "pause" && action != "resume" && action != "cancel" && action != "message" {
		return "", errors.New("unsupported quest control action")
	}
	if action == "message" && (message == "" || len(message) > 32768) {
		return "", errors.New("quest message must contain 1 to 32768 bytes")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var current domain.QuestStatus
	var workspaceID, controllerState string
	if err = tx.QueryRowContext(ctx, `SELECT quest.status,quest.workspace_id,quest.controller_state FROM quests quest JOIN work_order_approvals_v2 approval ON approval.quest_id=quest.id WHERE quest.id=?`, questID).Scan(&current, &workspaceID, &controllerState); err != nil {
		return "", err
	}
	// Пауза правки снимается только утверждением новой версии: продолжение
	// исполняло бы прежнюю, с её сетью и правами, мимо решения владельца.
	if action == "resume" && controllerState == WorkOrderScopeRevisionState {
		return "", ErrWorkOrderRevisionPending
	}
	target := current
	switch action {
	case "pause":
		target = domain.QuestPaused
	case "cancel":
		target = domain.QuestCancelled
	case "resume":
		// Ожидание человека — тот же случай, что блокировка: исполнителя нет,
		// продолжать нечего. Человек отдал ключ или авторизовал CLI, и нужен
		// не «продолжить», а новая проверка окружения — уже с credential.
		if current == domain.QuestBlocked || current == domain.QuestAwaitingUser {
			// Заблокированный квест возобновляется не «продолжением», а новой
			// проверкой окружения: исполнителя у него нет — он и не начинался
			// либо остановлен на полпути. Вернуть ему `running` значило бы
			// показать работу, которой никто не делает; правильный шаг — снова
			// пройти preflight, и если причина блокировки цела, он честно
			// вернётся в `blocked` с той же причиной.
			target = domain.QuestPreflight
			break
		}
		var prior domain.QuestStatus
		err = tx.QueryRowContext(ctx, `SELECT from_status FROM work_order_quest_control_events_v2 WHERE quest_id=? AND action IN ('pause','recover_pause') ORDER BY sequence DESC LIMIT 1`, questID).Scan(&prior)
		if errors.Is(err, sql.ErrNoRows) {
			prior = domain.QuestRunning
		} else if err != nil {
			return "", err
		}
		target = prior
	case "message":
		// A message is queued for the checkpoint coordinator; it does not
		// silently grant permissions or resume execution.
	}
	if action != "message" && !domain.CanTransitionWorkOrderQuest(current, target) {
		return "", errors.New("quest state does not allow this action")
	}
	now := formatTime(time.Now().UTC())
	if action == "resume" && (current == domain.QuestBlocked || current == domain.QuestAwaitingUser) {
		result, leaseErr := tx.ExecContext(ctx, `INSERT INTO writer_leases_v2(workspace_id,quest_id,token,state,acquired_at,updated_at,released_at)
VALUES(?,?,?,'active',?,?,NULL)
ON CONFLICT(workspace_id) DO UPDATE SET quest_id=excluded.quest_id,token=excluded.token,state='active',acquired_at=excluded.acquired_at,updated_at=excluded.updated_at,released_at=NULL
WHERE writer_leases_v2.state='released' OR writer_leases_v2.quest_id=excluded.quest_id`, workspaceID, questID, domain.NewID("writerlease"), now, now)
		if leaseErr != nil {
			return "", leaseErr
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return "", errors.New("another writer quest holds the workspace lease")
		}
	}
	if action != "message" {
		var finished any
		if target == domain.QuestCancelled {
			finished = now
		}
		result, updateErr := tx.ExecContext(ctx, `UPDATE quests SET status=?,controller_state=?,updated_at=?,finished_at=? WHERE id=? AND status=?`, target, target, now, finished, questID, current)
		if updateErr != nil {
			return "", updateErr
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return "", errors.New("quest changed concurrently")
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO work_order_quest_control_events_v2(id,quest_id,action,from_status,to_status,message,created_at) VALUES(?,?,?,?,?,?,?)`,
		domain.NewID("control"), questID, action, current, target, security.Redact(message), now); err != nil {
		return "", err
	}
	if target == domain.QuestCancelled {
		if _, err = tx.ExecContext(ctx, `UPDATE writer_leases_v2 SET state='released',updated_at=?,released_at=? WHERE quest_id=? AND state='active'`, now, now, questID); err != nil {
			return "", err
		}
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return target, nil
}
