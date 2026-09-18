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
	if err = tx.QueryRowContext(ctx, `SELECT quest.status FROM quests quest JOIN work_order_approvals_v2 approval ON approval.quest_id=quest.id WHERE quest.id=?`, questID).Scan(&current); err != nil {
		return "", err
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
		err = tx.QueryRowContext(ctx, `SELECT from_status FROM work_order_quest_control_events_v2 WHERE quest_id=? AND action='pause' ORDER BY sequence DESC LIMIT 1`, questID).Scan(&prior)
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
