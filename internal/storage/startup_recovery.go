package storage

import (
	"context"
	"errors"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// Восстановление после остановки ядра.
//
// Прогон, ход Мастера или задание обучения в живом состоянии после старта
// процесса описывают работу, которую никто больше не делает, — если процесс
// был один. Ядра проектов делят одну базу, и живое состояние чужого мира
// принадлежит соседнему ядру, которое его прямо сейчас и делает. Поэтому ядро
// восстанавливает только свой мир, а однопроцессные инструменты (Open) — всё.
//
// Пустой мир в запросах ниже означает «все миры»: `(?='' OR workspace_id=?)`.

// RecoverAbandonedWork переводит брошенную работу одного мира в честные
// состояния: прогоны — в паузу по контрольной точке или в прерванные, ходы
// Мастера — в прерванные с сохранённым частичным ответом, задания обучения —
// в отложенные. Временные беседы мира истекают вместе с прежней сессией ядра.
func (s *SQLite) RecoverAbandonedWork(ctx context.Context, workspaceID string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return errors.New("abandoned work recovery needs a workspace")
	}
	if err := s.markInterrupted(ctx, workspaceID); err != nil {
		return err
	}
	if err := s.recoverMasterLearning(ctx, workspaceID); err != nil {
		return err
	}
	if err := s.interruptMasterTurns(ctx, workspaceID); err != nil {
		return err
	}
	return s.purgeTemporaryMasterConversations(ctx, workspaceID)
}

// ExpireTemporaryMasterConversations убирает временные беседы мира, когда его
// ядро останавливается. Беседы других миров живут, пока живо их ядро.
func (s *SQLite) ExpireTemporaryMasterConversations(ctx context.Context, workspaceID string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil
	}
	return s.purgeTemporaryMasterConversations(ctx, workspaceID)
}

const deliveredAppInterruptedSummary = "Ядро остановилось посреди действия с приложением; его исход неизвестен, и Point его не повторяет. Проверьте состояние приложения и запустите действие заново."

func (s *SQLite) MarkInterrupted(ctx context.Context) error {
	return s.markInterrupted(ctx, "")
}

func (s *SQLite) markInterrupted(ctx context.Context, workspaceID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.beginTx(ctx)
	if err != nil {
		return err
	}
	// Leave paused runs with a resumable checkpoint alone so Resume can
	// continue after restart. Running/waiting with a safe checkpoint become
	// paused (not irreversible interrupted). In-flight mutations stay
	// unknown_outcome via journal + non-resumable checkpoint.
	if _, err = tx.ExecContext(ctx, `UPDATE runs SET status=?, error='', finished_at=NULL
WHERE status IN (?, ?) AND id IN (
  SELECT c.run_id FROM run_checkpoints c
  INNER JOIN (
    SELECT run_id, MAX(seq) AS seq FROM run_checkpoints GROUP BY run_id
  ) latest ON latest.run_id=c.run_id AND latest.seq=c.seq
  WHERE c.in_flight_call_id=''
) AND (?='' OR workspace_id=?)`, domain.RunPaused, domain.RunRunning, domain.RunWaiting, workspaceID, workspaceID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE runs SET status=?, error=CASE WHEN error='' THEN 'Application stopped before the run finished' ELSE error END, finished_at=?, duration_ms=CAST((julianday(?) - julianday(started_at))*86400000 AS INTEGER) WHERE status IN (?, ?) AND (?='' OR workspace_id=?)`, domain.RunInterrupted, now, now, domain.RunRunning, domain.RunWaiting, workspaceID, workspaceID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE runs SET status=?, error=CASE WHEN error='' THEN 'Application stopped before the run finished' ELSE error END, finished_at=?, duration_ms=CAST((julianday(?) - julianday(started_at))*86400000 AS INTEGER)
WHERE status=? AND id NOT IN (
  SELECT c.run_id FROM run_checkpoints c
  INNER JOIN (
    SELECT run_id, MAX(seq) AS seq FROM run_checkpoints GROUP BY run_id
  ) latest ON latest.run_id=c.run_id AND latest.seq=c.seq
  WHERE c.in_flight_call_id=''
) AND (?='' OR workspace_id=?)`, domain.RunInterrupted, now, now, domain.RunPaused, workspaceID, workspaceID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workflow_runs SET status=?, error=CASE WHEN error='' THEN 'Application stopped before the workflow finished' ELSE error END, finished_at=?, duration_ms=CAST((julianday(?) - julianday(started_at))*86400000 AS INTEGER) WHERE status IN (?, ?, ?) AND (?='' OR workspace_id=?)`, domain.RunInterrupted, now, now, domain.RunRunning, domain.RunWaiting, domain.RunPaused, workspaceID, workspaceID); err != nil {
		_ = tx.Rollback()
		return err
	}
	// A Flow is resumable when at least one of its executions points at the
	// latest safe checkpoint. Preserve the parent state together with that
	// execution; otherwise the child can be resumed but the scheduler has
	// already irreversibly abandoned its graph.
	if _, err = tx.ExecContext(ctx, `UPDATE flow_runs SET status=?, error='', finished_at=NULL
WHERE status IN (?, ?, ?) AND id IN (
  SELECT DISTINCT execution.flow_run_id FROM executions execution
  INNER JOIN run_checkpoints checkpoint ON checkpoint.run_id=execution.run_id
  INNER JOIN (
    SELECT run_id, MAX(seq) AS seq FROM run_checkpoints GROUP BY run_id
  ) latest ON latest.run_id=checkpoint.run_id AND latest.seq=checkpoint.seq
  WHERE execution.flow_run_id<>'' AND checkpoint.in_flight_call_id=''
) AND (?='' OR workspace_id=?)`, domain.RunPaused, domain.RunRunning, domain.RunWaiting, domain.RunPaused, workspaceID, workspaceID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE flow_runs SET status=?, error=CASE WHEN error='' THEN 'Application stopped before the flow finished' ELSE error END, finished_at=?, duration_ms=CAST((julianday(?) - julianday(started_at))*86400000 AS INTEGER)
WHERE status IN (?, ?, ?) AND id NOT IN (
  SELECT DISTINCT execution.flow_run_id FROM executions execution
  INNER JOIN run_checkpoints checkpoint ON checkpoint.run_id=execution.run_id
  INNER JOIN (
    SELECT run_id, MAX(seq) AS seq FROM run_checkpoints GROUP BY run_id
  ) latest ON latest.run_id=checkpoint.run_id AND latest.seq=checkpoint.seq
  WHERE execution.flow_run_id<>'' AND checkpoint.in_flight_call_id=''
) AND (?='' OR workspace_id=?)`, domain.RunInterrupted, now, now, domain.RunRunning, domain.RunWaiting, domain.RunPaused, workspaceID, workspaceID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE executions SET status=?, error='', finished_at=NULL
WHERE status IN (?, ?, ?) AND run_id IN (
  SELECT c.run_id FROM run_checkpoints c
  INNER JOIN (
    SELECT run_id, MAX(seq) AS seq FROM run_checkpoints GROUP BY run_id
  ) latest ON latest.run_id=c.run_id AND latest.seq=c.seq
  WHERE c.in_flight_call_id=''
) AND (?='' OR workspace_id=?)`, domain.RunPaused, domain.RunRunning, domain.RunWaiting, domain.RunPaused, workspaceID, workspaceID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE executions SET status=?, error=CASE WHEN error='' THEN 'Application stopped before the execution finished' ELSE error END, finished_at=?, duration_ms=CAST((julianday(?) - julianday(started_at))*86400000 AS INTEGER)
WHERE status IN (?, ?, ?) AND (run_id='' OR run_id NOT IN (
  SELECT c.run_id FROM run_checkpoints c
  INNER JOIN (
    SELECT run_id, MAX(seq) AS seq FROM run_checkpoints GROUP BY run_id
  ) latest ON latest.run_id=c.run_id AND latest.seq=c.seq
  WHERE c.in_flight_call_id=''
)) AND (?='' OR workspace_id=?)`, domain.RunInterrupted, now, now, domain.RunRunning, domain.RunWaiting, domain.RunPaused, workspaceID, workspaceID); err != nil {
		_ = tx.Rollback()
		return err
	}
	// Подтверждения и правки своего мира не лежат в своих таблицах с миром:
	// они принадлежат прогону, а прогон — миру.
	if _, err = tx.ExecContext(ctx, `UPDATE approvals SET status=?, resolved_at=? WHERE status=? AND (?='' OR run_id IN (SELECT id FROM runs WHERE workspace_id=?))`, domain.ApprovalDenied, now, domain.ApprovalPending, workspaceID, workspaceID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE patches SET status='rejected' WHERE status='pending' AND (?='' OR run_id IN (SELECT id FROM runs WHERE workspace_id=?))`, workspaceID, workspaceID); err != nil {
		_ = tx.Rollback()
		return err
	}
	// Запуск или остановка приложения доставки, начатые прошлым процессом,
	// навсегда оставались «executing»: карточка показывала идущее действие,
	// которого никто не делает (Q14). Исход неизвестен — повторять его сам
	// Point не вправе, решает человек. Время записи не меняется: иначе старое
	// действие встало бы новее тех, что были после него.
	if _, err = tx.ExecContext(ctx, `UPDATE delivered_app_controls_v2 SET response_json=json_set(response_json,'$.status','unknown_outcome','$.summary',?) WHERE json_extract(response_json,'$.status')='executing' AND (?='' OR quest_id IN (SELECT id FROM quests WHERE workspace_id=?))`,
		deliveredAppInterruptedSummary, workspaceID, workspaceID); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
