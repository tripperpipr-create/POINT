package storage

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func purgeStore(t *testing.T) *SQLite {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "purge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func tablesWithColumn(t *testing.T, store *SQLite, column string) []string {
	t.Helper()
	rows, err := store.db.QueryContext(context.Background(),
		`SELECT m.name FROM sqlite_master m JOIN pragma_table_info(m.name) p WHERE m.type='table' AND p.name=? ORDER BY m.name`, column)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return names
}

// Затвор полноты. Снос читает список таблиц, а таблицы заводят и после него: без
// этой проверки новая таблица с quest_id молча пережила бы удаление и осталась
// строкой, на которую больше ничто не ссылается. Проверка читает схему, а не
// исходник, поэтому не слепнет от переезда кода.
func TestPurgeQuestCoversEveryQuestScopedTable(t *testing.T) {
	store := purgeStore(t)
	known := map[string]bool{}
	for _, table := range questPurgeScopedTables {
		known[table] = true
	}
	for table := range questPurgeKeptTables {
		known[table] = true
	}
	for table := range questPurgeJournalTables {
		known[table] = true
	}
	var missed []string
	tables := tablesWithColumn(t, store, "quest_id")
	if len(tables) < 10 {
		t.Fatalf("схема отдала %d таблиц с quest_id — проверка смотрит не туда", len(tables))
	}
	for _, table := range tables {
		if !known[table] {
			missed = append(missed, table)
		}
	}
	if len(missed) > 0 {
		sort.Strings(missed)
		t.Fatalf("таблицы с quest_id не названы ни в сносе, ни в исключениях: %s", strings.Join(missed, ", "))
	}
	// Список исключений обязан оставаться списком живых таблиц: удалённая
	// таблица, забытая в нём, прячет опечатку в имени соседней.
	for table := range questPurgeKeptTables {
		if !containsString(tables, table) {
			t.Fatalf("исключение %q не существует в схеме", table)
		}
	}
	for _, table := range questPurgeScopedTables {
		if !containsString(tables, table) {
			t.Fatalf("снос адресован таблице %q, которой нет в схеме", table)
		}
	}
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func countRows(t *testing.T, store *SQLite, table, where string, args ...any) int {
	t.Helper()
	var count int
	if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table+` WHERE `+where, args...).Scan(&count); err != nil {
		t.Fatalf("%s: %v", table, err)
	}
	return count
}

// Полный след одного квеста: схема, прогон схемы, исполнение, прогон агента,
// его события и правки, набор правок, предложение и расход.
func seedPurgeQuest(t *testing.T, store *SQLite, workspaceID, questID string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	flowID, flowRunID, runID, executionID, setID, proposalID := "flow-"+questID, "flowrun-"+questID, "run-"+questID, "exec-"+questID, "set-"+questID, "qp-"+questID
	if err := store.SaveWorkspace(ctx, domain.Workspace{ID: workspaceID, Name: "Мир", Path: t.TempDir(), OpenedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveFlow(ctx, domain.FlowGraph{ID: flowID, WorkspaceID: workspaceID, Name: "Схема", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveQuest(ctx, domain.Quest{ID: questID, WorkspaceID: workspaceID, Title: "Квест", Status: domain.QuestActive, FlowID: flowID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveFlowRun(ctx, domain.FlowRun{ID: flowRunID, FlowID: flowID, WorkspaceID: workspaceID, QuestID: questID, Status: domain.RunCompleted, StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRun(ctx, domain.Run{ID: runID, WorkspaceID: workspaceID, Task: "работа", Status: domain.RunCompleted, StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveExecution(ctx, domain.ExecutionInstance{ID: executionID, WorkspaceID: workspaceID, QuestID: questID, FlowRunID: flowRunID, RunID: runID, Status: domain.RunCompleted, StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, domain.Event{ID: "ev-" + questID, RunID: runID, WorkspaceID: workspaceID, QuestID: questID, Type: domain.EventRunStarted, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	// Событие без quest_id: так пишутся первые шаги прогона. По одному quest_id
	// оно бы уцелело, и хроника удалённого квеста осталась бы в базе.
	if err := store.Append(ctx, domain.Event{ID: "ev-early-" + questID, RunID: runID, WorkspaceID: workspaceID, Type: domain.EventRunStarted, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveChangeSet(ctx, domain.ChangeSet{
		ID: setID, WorkspaceID: workspaceID, QuestID: questID, ExecutionID: executionID, Title: "Правки",
		Status: domain.ChangeSetApplied, CreatedAt: now, UpdatedAt: now,
		Items: []domain.ChangeItem{{ID: "ci-" + questID, Path: "a.go", Kind: "modify"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveQuestProposal(ctx, domain.QuestProposal{ID: proposalID, WorkspaceID: workspaceID, Title: "Предложение", Status: "started", FlowID: flowID, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertUsageRecord(ctx, domain.UsageRecord{
		ID: "usage-" + questID, WorkspaceID: workspaceID, QuestID: questID, ExecutionID: executionID,
		TotalTokens: 1200, Outcome: "usage_reported", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeQuestRemovesEveryTraceButSpend(t *testing.T) {
	store := purgeStore(t)
	ctx := context.Background()
	seedPurgeQuest(t, store, "ws-1", "q-1")
	// Соседний квест того же мира: снос обязан остановиться на границе одного.
	seedPurgeQuestNeighbour(t, store, "ws-1", "q-2")

	counts, err := store.PurgeQuest(ctx, "ws-1", "q-1")
	if err != nil {
		t.Fatal(err)
	}
	if counts["quests"] != 1 || counts["change_items"] != 1 || counts["runs"] != 1 {
		t.Fatalf("отчёт не сходится со снесённым: %v", counts)
	}
	for _, check := range []struct{ table, where string }{
		{"quests", "id='q-1'"},
		{"flows", "id='flow-q-1'"},
		{"flow_runs", "quest_id='q-1'"},
		{"executions", "quest_id='q-1'"},
		{"runs", "id='run-q-1'"},
		{"change_sets", "quest_id='q-1'"},
		{"change_items", "change_set_id='set-q-1'"},
		{"quest_proposals", "flow_id='flow-q-1'"},
	} {
		if left := countRows(t, store, check.table, check.where); left != 0 {
			t.Fatalf("%s: после сноса осталось %d строк (%s)", check.table, left, check.where)
		}
	}
	if left := countRows(t, store, "usage_records", "quest_id='q-1'"); left != 1 {
		t.Fatalf("расход снесён вместе с квестом: осталось %d записей", left)
	}
	// Хроника прогона остаётся: её удаление запрещено триггером, и снос обязан
	// не спотыкаться об это, а обходить. Сиротой она человеку не видна —
	// прогона, по которому её показывают, больше нет.
	if left := countRows(t, store, "events", "run_id='run-q-1'"); left != 2 {
		t.Fatalf("снос тронул неизменяемый журнал событий: осталось %d из 2", left)
	}
	// Соседний квест не задет ни одной строкой.
	for _, check := range []struct {
		table, where string
		want         int
	}{
		{"quests", "id='q-2'", 1},
		{"flows", "id='flow-q-2'", 1},
		{"executions", "quest_id='q-2'", 1},
		{"runs", "id='run-q-2'", 1},
		{"change_sets", "quest_id='q-2'", 1},
		{"quest_proposals", "flow_id='flow-q-2'", 1},
	} {
		if left := countRows(t, store, check.table, check.where); left != check.want {
			t.Fatalf("%s: у соседнего квеста %d строк вместо %d — снос перешёл границу", check.table, left, check.want)
		}
	}
}

func seedPurgeQuestNeighbour(t *testing.T, store *SQLite, workspaceID, questID string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	flowID := "flow-" + questID
	if err := store.SaveFlow(ctx, domain.FlowGraph{ID: flowID, WorkspaceID: workspaceID, Name: "Схема", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveQuest(ctx, domain.Quest{ID: questID, WorkspaceID: workspaceID, Title: "Сосед", Status: domain.QuestActive, FlowID: flowID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRun(ctx, domain.Run{ID: "run-" + questID, WorkspaceID: workspaceID, Task: "работа", Status: domain.RunCompleted, StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveExecution(ctx, domain.ExecutionInstance{ID: "exec-" + questID, WorkspaceID: workspaceID, QuestID: questID, RunID: "run-" + questID, Status: domain.RunCompleted, StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ev-" + questID, "ev-early-" + questID} {
		if err := store.Append(ctx, domain.Event{ID: id, RunID: "run-" + questID, WorkspaceID: workspaceID, Type: domain.EventRunStarted, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveChangeSet(ctx, domain.ChangeSet{
		ID: "set-" + questID, WorkspaceID: workspaceID, QuestID: questID, ExecutionID: "exec-" + questID,
		Title: "Правки", Status: domain.ChangeSetApplied, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveQuestProposal(ctx, domain.QuestProposal{ID: "qp-" + questID, WorkspaceID: workspaceID, Title: "Предложение", Status: "started", FlowID: flowID, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeQuestRefusesForeignWorkspaceAndSubquests(t *testing.T) {
	store := purgeStore(t)
	ctx := context.Background()
	seedPurgeQuest(t, store, "ws-1", "q-1")
	if _, err := store.PurgeQuest(ctx, "ws-other", "q-1"); err == nil {
		t.Fatal("снос достал квест чужого проекта")
	}
	if left := countRows(t, store, "quests", "id='q-1'"); left != 1 {
		t.Fatal("отказ по чужому миру всё равно удалил квест")
	}
	now := time.Now().UTC()
	if err := store.SaveQuest(ctx, domain.Quest{ID: "q-child", WorkspaceID: "ws-1", ParentID: "q-1", Title: "Подквест", Status: domain.QuestActive, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PurgeQuest(ctx, "ws-1", "q-1"); err == nil || !strings.Contains(err.Error(), "подквест") {
		t.Fatalf("родитель снесён вместе с подквестом молча: %v", err)
	}
}

// Затвор неизменности. Снос обходит журналы не по вкусу автора, а потому что их
// защищает база. Проверка читает триггеры: таблица из списка обхода без живого
// триггера значит, что запрет сняли и данные молча переживают удаление; таблица
// из списка сноса с триггером значит, что снос упадёт всей транзакцией — квест
// останется на месте, а человек прочтёт «events are immutable».
func TestPurgeQuestSkipsExactlyTheImmutableJournals(t *testing.T) {
	store := purgeStore(t)
	rows, err := store.db.QueryContext(context.Background(),
		`SELECT tbl_name FROM sqlite_master WHERE type='trigger' AND sql LIKE '%BEFORE DELETE%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	protected := map[string]bool{}
	for rows.Next() {
		var table string
		if err = rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		protected[table] = true
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(protected) == 0 {
		t.Fatal("в схеме не нашлось ни одного запрета на удаление — проверка смотрит не туда")
	}
	for table := range questPurgeJournalTables {
		if !protected[table] {
			t.Fatalf("%q обходится как неизменяемая, но запрета на удаление у неё нет — строки переживают снос молча", table)
		}
	}
	purged := append([]string{"change_sets", "executions", "flow_runs", "runs", "change_items", "quest_proposals", "agent_prep_chains"}, questPurgeScopedTables...)
	for _, table := range purged {
		if protected[table] {
			t.Fatalf("снос удаляет %q, а база это запрещает — упадёт вся транзакция вместе с квестом", table)
		}
	}
}
