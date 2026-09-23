package storage

import (
	"context"
	"database/sql"
	"fmt"
)

// Снос квеста со всеми следами — в отличие от DeleteQuest, который убирает
// карточку и оставляет хронику как доказательство сделанного.
//
// Это разные операции, а не два режима одной. DeleteQuest бережёт прошлое
// проекта: удалённый квест оставляет прогоны, события и улики, и интерфейс
// показывает их разделом «запуски без квеста». PurgeQuest отвечает на другой
// запрос человека — «убрать это целиком и не вспоминать»: он сносит всё, что
// без квеста не имеет смысла, вместе с самим следом работы.
//
// Расходы остаются намеренно. Токены и деньги списаны на самом деле, и стереть
// запись о них — значит заставить статистику проекта врать о потраченном.
// В списке они становятся расходом без квеста, и это честно.
//
// Порядок удаления не косметика: строки-потомки выбираются подзапросами по
// исполнениям, прогонам и наборам правок, поэтому сами исполнения, прогоны и
// наборы уходят последними. Переставив их выше, мы оставили бы сирот, которых
// уже не найти ни по одному идентификатору.
//
// Таблицы перечислены поимённо, а не подобраны по имени колонки: удаление,
// которое само решает, что ему сносить, однажды снесёт лишнее. За полнотой
// списка следит TestPurgeQuestCoversEveryQuestScopedTable — он читает схему и
// падает на новой таблице с quest_id, которой нет ни в списке, ни в исключениях.
var questPurgeScopedTables = []string{
	"budget_reservations",
	"delivered_app_controls_v2",
	"egress_asks",
	"intake_sessions",
	"learning_principles",
	"master_evidence_signals",
	"milestone_runtimes_v2",
	"quest_links",
	"quest_replans",
	"quest_tool_leases",
	"team_events",
	"work_order_approvals_v2",
	"work_order_completion_gates_v2",
	"writer_leases_v2",
}

// Таблицы с quest_id, которые снос не трогает, и причина для каждой.
var questPurgeKeptTables = map[string]string{
	"usage_records": "расход списан на самом деле; без записи статистика проекта врёт о потраченном",
	// Ниже — таблицы, которые снос удаляет своим запросом, а не общим списком:
	// у них есть и другие ключи, по которым надо взять строки без quest_id.
	"change_sets": "удаляются своим запросом вместе с элементами правок",
	"executions":  "удаляются последними — по ним выбираются прогоны",
	"flow_runs":   "удаляются последними — по ним выбираются события узлов",
}

// Журналы, которые снос обойдёт, потому что база запрещает их удалять.
//
// Это не забытые таблицы и не список пожеланий: у каждой стоит триггер
// BEFORE DELETE, и снос, который их тронет, не вернёт ошибку человеку — он
// упадёт всей транзакцией, и квест останется на месте. Неизменность здесь и
// есть смысл записи: улика, которую можно стереть, ничего не доказывает, а
// журнал решений по квесту отвечает на вопрос «кто это остановил и когда».
//
// Строки остаются, но сиротами: прогоны и исполнения, по которым их показывают,
// снос уносит, и в интерфейсе они не всплывут. За соответствием списка живым
// триггерам следит TestPurgeQuestSkipsExactlyTheImmutableJournals.
var questPurgeJournalTables = map[string]string{
	"events":                             "хроника прогона: append-only журнал, триггер events_no_delete",
	"evidence_bundles":                   "улики готовности квеста, триггер evidence_bundles_no_delete",
	"work_order_quest_control_events_v2": "журнал решений по квесту, триггер work_order_quest_controls_no_delete_v2",
	"task_brief_revisions":               "подписанные версии задания, триггер task_brief_revisions_immutable_delete",
}

// PurgeQuest сносит квест проекта со всеми следами и возвращает, чего и сколько
// удалено. Счётчик — не украшение отчёта: человек нажал одну кнопку и обязан
// увидеть, что именно за ней исчезло.
func (s *SQLite) PurgeQuest(ctx context.Context, workspaceID, questID string) (map[string]int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var flowID string
	if err = tx.QueryRowContext(ctx, `SELECT flow_id FROM quests WHERE id=? AND workspace_id=?`, questID, workspaceID).Scan(&flowID); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("квест %q не найден в открытом проекте", questID)
		}
		return nil, err
	}
	// Подквест сносится своим вызовом: у него своя схема, свои прогоны и своё
	// место в отчёте. Здесь — только отказ, чтобы родитель не унёс потомка молча.
	var children int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM quests WHERE workspace_id=? AND parent_id=?`, workspaceID, questID).Scan(&children); err != nil {
		return nil, err
	}
	if children > 0 {
		return nil, fmt.Errorf("у квеста остались подквесты (%d) — снесите их первыми", children)
	}

	counts := map[string]int{}
	exec := func(table, statement string, args ...any) error {
		result, execErr := tx.ExecContext(ctx, statement, args...)
		if execErr != nil {
			return fmt.Errorf("%s: %w", table, execErr)
		}
		affected, _ := result.RowsAffected()
		if affected > 0 {
			counts[table] += int(affected)
		}
		return nil
	}

	const (
		runsOfQuest       = `SELECT run_id FROM executions WHERE quest_id=? AND run_id<>''`
		executionsOfQuest = `SELECT id FROM executions WHERE quest_id=?`
		setsOfQuest       = `SELECT id FROM change_sets WHERE quest_id=?`
	)

	// 1. Потомки наборов правок и прогонов — пока сами наборы и прогоны на месте.
	if err = exec("change_items", `DELETE FROM change_items WHERE change_set_id IN (`+setsOfQuest+`)`, questID); err != nil {
		return nil, err
	}
	for _, table := range []string{"approvals", "patches", "run_checkpoints", "learning_signals", "skill_outcomes"} {
		if err = exec(table, `DELETE FROM `+table+` WHERE run_id IN (`+runsOfQuest+`)`, questID); err != nil {
			return nil, err
		}
	}
	if err = exec("sandboxes", `DELETE FROM sandboxes WHERE execution_id IN (`+executionsOfQuest+`)`, questID); err != nil {
		return nil, err
	}
	// 2. Строки, у которых квест — один из ключей, но не единственный.
	if err = exec("budget_reservations", `DELETE FROM budget_reservations WHERE quest_id=? OR run_id IN (`+runsOfQuest+`) OR execution_id IN (`+executionsOfQuest+`)`,
		questID, questID, questID); err != nil {
		return nil, err
	}
	if err = exec("egress_asks", `DELETE FROM egress_asks WHERE quest_id=? OR run_id IN (`+runsOfQuest+`)`, questID, questID); err != nil {
		return nil, err
	}

	// 3. Прогоны агента. Границу проекта держим и здесь: идентификатор,
	// пришедший из чужого окна, не должен доставать до соседнего мира.
	if err = exec("runs", `DELETE FROM runs WHERE workspace_id=? AND id IN (`+runsOfQuest+`)`, workspaceID, questID); err != nil {
		return nil, err
	}

	// 4. То, по чему выбирались потомки.
	for _, table := range []string{"change_sets", "executions", "flow_runs"} {
		if err = exec(table, `DELETE FROM `+table+` WHERE quest_id=?`, questID); err != nil {
			return nil, err
		}
	}

	// 5. Остальное, что адресовано прямо квесту.
	for _, table := range questPurgeScopedTables {
		if err = exec(table, `DELETE FROM `+table+` WHERE quest_id=?`, questID); err != nil {
			return nil, err
		}
	}
	if err = exec("agent_prep_chains", `DELETE FROM agent_prep_chains WHERE parent_quest_id=? OR prep_quest_id=?`, questID, questID); err != nil {
		return nil, err
	}

	// 6. Предложение, из которого квест вырос. Оно связано схемой: у карточки
	if err = forgetMasterExamplesTx(ctx, tx, `workspace_id=? AND (json_extract(payload,'$.questId')=? OR (?<>'' AND (json_extract(payload,'$.flowId')=? OR json_extract(payload,'$.proposalId') IN (SELECT id FROM quest_proposals WHERE workspace_id=? AND flow_id=?))))`, workspaceID, questID, flowID, flowID, workspaceID, flowID); err != nil {
		return nil, err
	}
	// в разговоре нет поля quest_id, и без этого шага в ленте остаётся дверь в
	// пустоту — «Квест запущен» без единого прогона за ней.
	if flowID != "" {
		if err = exec("quest_proposals", `DELETE FROM quest_proposals WHERE workspace_id=? AND flow_id=?`, workspaceID, flowID); err != nil {
			return nil, err
		}
	}

	if err = exec("quests", `DELETE FROM quests WHERE id=? AND workspace_id=?`, questID, workspaceID); err != nil {
		return nil, err
	}
	if counts["quests"] != 1 {
		return nil, fmt.Errorf("квест %q не найден в открытом проекте", questID)
	}

	// 7. Схема уходит за последней ссылкой на себя. Живые прогоны проверять
	// больше нечем и незачем: они удалены этой же транзакцией.
	if flowID != "" {
		if err = exec("flows", `
DELETE FROM flows
WHERE id=? AND workspace_id=?
  AND NOT EXISTS (SELECT 1 FROM quests WHERE workspace_id=? AND flow_id=?)`, flowID, workspaceID, workspaceID, flowID); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return counts, nil
}
