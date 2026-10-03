package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

// Закрытый квест не держит живых следов (TODO Q14).
//
// Статус квеста наряда пишут одни места, а этапы Flow, milestone и фазу
// запуска — другие. После остановки ядра или позднего колбэка у завершённого
// или отменённого квеста оставались milestone `running`, фаза запуска и узлы
// `waiting_approval`: карточка и полоса «Нужно ваше решение» показывали
// работу, которой никто не делает. Восстановление мира и отмена закрывают их
// вместе со статусом квеста.

// closedWorkOrderQuestStatusesV2 — исходы, после которых квест не
// продолжается. `blocked` сюда не входит: он возобновляется новой проверкой
// окружения и подбирает свой milestone.
var closedWorkOrderQuestStatusesV2 = []domain.QuestStatus{domain.QuestCompleted, domain.QuestNeedsReview, domain.QuestFailed, domain.QuestCancelled}

// closeClosedQuestResidueV2 закрывает следы закрытых квестов нарядов одного
// мира. Чужой мир не трогается: его квесты ведёт его ядро.
func (s *SQLite) closeClosedQuestResidueV2(ctx context.Context, workspaceID string) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(closedWorkOrderQuestStatusesV2)), ",")
	args := []any{workspaceID}
	for _, status := range closedWorkOrderQuestStatusesV2 {
		args = append(args, status)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT quest.id,quest.status FROM quests quest JOIN work_order_approvals_v2 approval ON approval.quest_id=quest.id
WHERE quest.workspace_id=? AND quest.status IN (`+placeholders+`)`, args...)
	if err != nil {
		return err
	}
	type closedQuest struct {
		id     string
		status domain.QuestStatus
	}
	var quests []closedQuest
	for rows.Next() {
		var quest closedQuest
		if err = rows.Scan(&quest.id, &quest.status); err != nil {
			rows.Close()
			return err
		}
		quests = append(quests, quest)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, quest := range quests {
		tx, txErr := s.beginTx(ctx)
		if txErr != nil {
			return txErr
		}
		if err = closeWorkOrderQuestResidueV2Tx(ctx, tx, quest.id, quest.status, now); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// closeWorkOrderQuestResidueV2Tx закрывает всё живое под закрытым квестом:
// фазу запуска, milestone, писательскую аренду, незавершённые этапы-квесты и
// узлы его прогонов Flow.
func closeWorkOrderQuestResidueV2Tx(ctx context.Context, tx *sql.Tx, questID string, status domain.QuestStatus, now time.Time) error {
	formatted := formatTime(now)
	if _, err := tx.ExecContext(ctx, `UPDATE quests SET status=?,controller_state=?,updated_at=?,finished_at=? WHERE parent_id=? AND flow_node_id<>'' AND status NOT IN ('completed','needs_review','blocked','failed','cancelled')`,
		domain.QuestCancelled, string(domain.QuestCancelled), formatted, formatted, questID); err != nil {
		return err
	}
	if err := reconcileCompletedWorkOrderGateV2Tx(ctx, tx, questID, status, now); err != nil {
		return err
	}
	return closeQuestFlowRunsV2Tx(ctx, tx, questID, status, now)
}

// closeQuestFlowRunsV2Tx закрывает незавершённые узлы прогонов квеста, а
// живой или прерванный прогон — по исходу квеста.
func closeQuestFlowRunsV2Tx(ctx context.Context, tx *sql.Tx, questID string, status domain.QuestStatus, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,status,node_states FROM flow_runs WHERE quest_id=?`, questID)
	if err != nil {
		return err
	}
	type flowRunRow struct{ run domain.FlowRun }
	var runs []flowRunRow
	for rows.Next() {
		var row flowRunRow
		var states string
		if err = rows.Scan(&row.run.ID, &row.run.Status, &states); err != nil {
			rows.Close()
			return err
		}
		if strings.TrimSpace(states) != "" {
			if err = json.Unmarshal([]byte(states), &row.run.NodeStates); err != nil {
				rows.Close()
				return err
			}
		}
		runs = append(runs, row)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	reason := "Квест закрыт (" + string(status) + "); этап не исполнялся"
	closedStatus := domain.RunInterrupted
	if status == domain.QuestCancelled {
		reason, closedStatus = "Квест отменён; этап не исполнялся", domain.RunCancelled
	}
	formatted := formatTime(now)
	for _, row := range runs {
		nodesClosed := row.run.CloseUnfinishedNodes(reason, now)
		live := false
		switch row.run.Status {
		case domain.RunRunning, domain.RunWaiting, domain.RunPaused, domain.RunPending:
			live = true
		}
		if !nodesClosed && !live {
			continue
		}
		next := row.run.Status
		if live {
			next = closedStatus
		}
		if _, err = tx.ExecContext(ctx, `UPDATE flow_runs SET status=?,node_states=?,finished_at=COALESCE(NULLIF(finished_at,''),?) WHERE id=?`,
			next, marshalJSON(row.run.NodeStates), formatted, row.run.ID); err != nil {
			return err
		}
	}
	return nil
}
