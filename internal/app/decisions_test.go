package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func openTestWorld(t *testing.T, application *App) domain.Workspace {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	view, err := application.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	return view.Workspace
}

func TestDecisionsOrdersByWaitingTimeAndDescribesResolution(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()

	queue, err := application.Decisions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if queue.Total != 0 {
		t.Fatalf("ожидалась пустая очередь, получено %d", queue.Total)
	}
	if queue.Items == nil {
		t.Fatal("Items должен быть пустым массивом, а не nil: клиент рендерит его напрямую")
	}

	now := time.Now().UTC()
	// Свежее предложение и старый набор изменений: старший обязан встать первым
	// независимо от порядка добавления.
	if err = application.store.SaveQuestProposal(ctx, domain.QuestProposal{
		ID: "qp-new", WorkspaceID: world.ID, Title: "Свежее предложение",
		Status: "pending", CreatedAt: now.Add(-1 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveChangeSet(ctx, domain.ChangeSet{
		ID: "cs-old", WorkspaceID: world.ID, Title: "Старый набор",
		Status: domain.ChangeSetPending, CreatedAt: now.Add(-30 * time.Minute),
		Items: []domain.ChangeItem{{Path: "main.go"}},
	}); err != nil {
		t.Fatal(err)
	}

	queue, err = application.Decisions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if queue.Total != 2 {
		t.Fatalf("ожидалось 2 решения, получено %d", queue.Total)
	}
	if queue.Items[0].ID != "cs-old" {
		t.Fatalf("первым должен стоять тот, кто ждёт дольше; получен %s", queue.Items[0].ID)
	}
	if queue.OldestMs < int64(29*time.Minute/time.Millisecond) {
		t.Fatalf("OldestMs должен отражать самое долгое ожидание, получено %d", queue.OldestMs)
	}
	if queue.ByKind["change-set"] != 1 || queue.ByKind["quest"] != 1 {
		t.Fatalf("разбивка по типам неверна: %v", queue.ByKind)
	}

	// Каждый элемент обязан нести способ решения: иначе клиенту пришлось бы
	// держать собственный switch по типам, и новый источник ломал бы UI.
	for _, item := range queue.Items {
		if item.Resolve.Path == "" {
			t.Fatalf("%s не описывает, как его решать", item.ID)
		}
	}
	if queue.Items[0].Resolve.Path != "/api/change-sets/cs-old/apply" {
		t.Fatalf("неверный путь применения набора: %s", queue.Items[0].Resolve.Path)
	}
	if queue.Items[0].Resolve.Reject != "/api/change-sets/cs-old/reject" {
		t.Fatalf("неверный путь отклонения набора: %s", queue.Items[0].Resolve.Reject)
	}
}

func TestDecisionsKeepModifiedDraftsUntilTheyAreApplied(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()
	now := time.Now().UTC()
	if err = application.store.SaveQuestProposal(ctx, domain.QuestProposal{
		ID: "qp-modified", WorkspaceID: world.ID, Title: "Изменённый квест",
		Status: "modified", CreatedAt: now.Add(-2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveCompanionActionProposal(ctx, domain.CompanionActionProposal{
		ID: "action-modified", WorkspaceID: world.ID, Kind: domain.CompanionActionCreateAgent,
		Title: "Изменённый агент", Status: "modified", CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	queue, err := application.Decisions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if queue.Total != 2 {
		t.Fatalf("изменённые черновики исчезли до подтверждения: %+v", queue.Items)
	}
	byID := map[string]Decision{}
	for _, item := range queue.Items {
		byID[item.ID] = item
	}
	if byID["qp-modified"].Resolve.Path != "/api/quest-proposals/decide" {
		t.Fatalf("изменённый квест нельзя запустить: %+v", byID["qp-modified"])
	}
	if byID["action-modified"].Resolve.Path != "/api/companion/actions/decide" {
		t.Fatalf("изменённого агента нельзя применить: %+v", byID["action-modified"])
	}
}

func TestDecisionsMarksConflictsBlockingAndProposalsNot(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()
	now := time.Now().UTC()

	if err = application.store.SaveChangeSet(ctx, domain.ChangeSet{
		ID: "cs-conflict", WorkspaceID: world.ID, Title: "Расхождение веток",
		Status: domain.ChangeSetConflict, CreatedAt: now.Add(-5 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveQuestProposal(ctx, domain.QuestProposal{
		ID: "qp-1", WorkspaceID: world.ID, Title: "Предложение",
		Status: "pending", CreatedAt: now.Add(-9 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	queue, err := application.Decisions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if queue.Blocking != 1 {
		t.Fatalf("простаивать должен только конфликт, насчитано %d", queue.Blocking)
	}
	byID := map[string]Decision{}
	for _, item := range queue.Items {
		byID[item.ID] = item
	}
	if !byID["cs-conflict"].Blocking {
		t.Fatal("конфликт слияния останавливает флоу и обязан считаться блокирующим")
	}
	if byID["qp-1"].Blocking {
		t.Fatal("предложение квеста никого не блокирует: работа ещё не начата")
	}
	if byID["cs-conflict"].Risk != "HIGH" {
		t.Fatalf("конфликт обязан быть высокого риска, получено %s", byID["cs-conflict"].Risk)
	}
	if byID["cs-conflict"].Resolve.Path != "/api/change-sets/cs-conflict/resolve" {
		t.Fatalf("конфликт решается разбором, а не применением: %s", byID["cs-conflict"].Resolve.Path)
	}
}

// Изоляция миров — контракт, который очередь обязана соблюдать так же, как
// bootstrap: решение из соседней папки не должно всплыть в текущей.
func TestDecisionsAreScopedToCurrentWorld(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	ctx := context.Background()
	first := openTestWorld(t, application)
	if err = application.store.SaveQuestProposal(ctx, domain.QuestProposal{
		ID: "qp-first", WorkspaceID: first.ID, Title: "Из первого мира",
		Status: "pending", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	second := openTestWorld(t, application)
	if second.ID == first.ID {
		t.Fatal("тест бессмысленен: миры получили один идентификатор")
	}

	queue, err := application.Decisions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if queue.Total != 0 {
		t.Fatalf("решение соседнего мира просочилось в текущий: %+v", queue.Items)
	}
}

// Прогон, ждущий подтверждения, обязан быть в очереди решений.
//
// Движок при запросе разрешения ставит прогону статус waiting_approval и
// блокируется (agent/engine.go, awaitApproval). Очередь же собирала подтверждения
// только у прогонов в статусах running и paused — то есть пропускала ровно те,
// которые ждут человека. Агент стоял, а экран «всё, что ждёт вашего решения»
// показывал пустоту: работа замирала молча.
func TestDecisionsIncludeRunsWaitingForApproval(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	world := openTestWorld(t, application)
	ctx := context.Background()
	now := time.Now().UTC()

	run := domain.Run{
		ID: "run-waiting", WorkspaceID: world.ID, AgentID: "agent-1",
		Status: domain.RunWaiting, Task: "Обновить схему", StartedAt: now.Add(-3 * time.Minute),
	}
	if err = application.store.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveApproval(ctx, domain.Approval{
		ID: "ap-waiting", RunID: run.ID, AgentID: run.AgentID, ToolName: "run_command",
		Reason: "запуск команды", Status: domain.ApprovalPending, CreatedAt: now.Add(-2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	queue, err := application.Decisions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range queue.Items {
		if item.ID == "ap-waiting" {
			found = true
			if item.Kind != DecisionApproval {
				t.Fatalf("подтверждение попало в очередь не тем видом: %s", item.Kind)
			}
			if strings.TrimSpace(item.Resolve.Path) == "" {
				t.Fatal("решение без пути разрешения — принять его будет нечем")
			}
		}
	}
	if !found {
		t.Fatalf("прогон в статусе waiting_approval не дошёл до очереди: %+v", queue.Items)
	}
}

// Флоу, остановленный на подтверждении узла, обязан быть в очереди.
//
// Тот же дефект уровнем выше: flowruntime выставляет прогону флоу статус
// waiting_approval, а очередь отбирала только running и paused. Соседний код
// (orchestration.go) этот статус учитывает — два места расходились, и ждущий
// флоу пропадал из очереди.
func TestDecisionsIncludeFlowRunsWaitingForApproval(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	world := openTestWorld(t, application)
	ctx := context.Background()
	now := time.Now().UTC()

	if err = application.store.SaveFlowRun(ctx, domain.FlowRun{
		ID: "fr-waiting", FlowID: "fl-1", WorkspaceID: world.ID,
		Status: domain.RunWaiting, StartedAt: now.Add(-4 * time.Minute),
		NodeStates: map[string]domain.FlowNodeState{
			"n2": {Status: "waiting_approval", StartedAt: &now},
		},
	}); err != nil {
		t.Fatal(err)
	}

	queue, err := application.Decisions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range queue.Items {
		if item.FlowRunID == "fr-waiting" && item.NodeID == "n2" {
			found = true
			if strings.TrimSpace(item.Resolve.Path) == "" {
				t.Fatal("узел флоу без пути разрешения — принять его будет нечем")
			}
		}
	}
	if !found {
		t.Fatalf("флоу в статусе waiting_approval не дошёл до очереди: %+v", queue.Items)
	}
}

// Число в очереди склоняется. «1 файлов» — тот же дефект, что я вычищал в
// интерфейсе, только живший в ядре: текст собирается здесь, и интерфейс его
// уже не исправит.
func TestDecisionsCountFilesInProperForm(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	world := openTestWorld(t, application)
	ctx := context.Background()
	now := time.Now().UTC()

	one := domain.ChangeSet{
		ID: "cs-one", WorkspaceID: world.ID, Title: "Один файл", Status: domain.ChangeSetPending,
		CreatedAt: now, Items: []domain.ChangeItem{{ID: "i1", Path: "a.go", Kind: "modify"}},
	}
	two := domain.ChangeSet{
		ID: "cs-two", WorkspaceID: world.ID, Title: "Два файла", Status: domain.ChangeSetPending,
		CreatedAt: now, Items: []domain.ChangeItem{
			{ID: "i2", Path: "b.go", Kind: "modify"}, {ID: "i3", Path: "c.go", Kind: "modify"},
		},
	}
	for _, set := range []domain.ChangeSet{one, two} {
		if err = application.store.SaveChangeSet(ctx, set); err != nil {
			t.Fatal(err)
		}
	}

	queue, err := application.Decisions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]string{}
	for _, item := range queue.Items {
		byID[item.ID] = item.Detail
	}
	if byID["cs-one"] != "1 файл" {
		t.Fatalf("один файл описан как %q", byID["cs-one"])
	}
	if byID["cs-two"] != "2 файла" {
		t.Fatalf("два файла описаны как %q", byID["cs-two"])
	}
}

// Каждый элемент очереди обязан нести путь разрешения.
//
// Очередь существует, чтобы работу можно было разблокировать. Элемент без пути
// рисуется живой кнопкой, которая молча ничего не делает: человек жмёт, работа
// стоит, и никто об этом не сообщает.
func TestEveryDecisionCarriesAResolvePath(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	world := openTestWorld(t, application)
	ctx := context.Background()
	now := time.Now().UTC()

	// По одному представителю от каждого источника очереди.
	run := domain.Run{ID: "run-1", WorkspaceID: world.ID, AgentID: "a1", Status: domain.RunWaiting, StartedAt: now}
	if err = application.store.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveApproval(ctx, domain.Approval{
		ID: "ap-1", RunID: run.ID, AgentID: "a1", ToolName: "run_command",
		Status: domain.ApprovalPending, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveChangeSet(ctx, domain.ChangeSet{
		ID: "cs-1", WorkspaceID: world.ID, Title: "Правка", Status: domain.ChangeSetPending,
		CreatedAt: now, Items: []domain.ChangeItem{{ID: "i1", Path: "a.go", Kind: "modify"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveFlowRun(ctx, domain.FlowRun{
		ID: "fr-1", FlowID: "fl-1", WorkspaceID: world.ID, Status: domain.RunWaiting, StartedAt: now,
		NodeStates: map[string]domain.FlowNodeState{"n1": {Status: "waiting_approval", StartedAt: &now}},
	}); err != nil {
		t.Fatal(err)
	}

	queue, err := application.Decisions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue.Items) == 0 {
		t.Fatal("очередь пуста — проверять нечего, фикстура не сработала")
	}
	for _, item := range queue.Items {
		if strings.TrimSpace(item.Resolve.Path) == "" {
			t.Fatalf("решение %s (%s) без пути разрешения: принять его будет нечем", item.ID, item.Kind)
		}
		if strings.TrimSpace(item.Resolve.Accept) == "" {
			t.Fatalf("решение %s (%s) без кода согласия", item.ID, item.Kind)
		}
	}
}

// Элемент без отметки времени не должен захватывать верх очереди.
//
// Очередь обещает «дольше всех ждущий — первым». Отметка времени приходит из
// хранилища, и в повреждённой или старой записи её может не быть вовсе. Тогда
// now.Sub(нулевое время) даёт около двух тысяч лет: такая запись навсегда
// встаёт первой, отодвигая настоящую срочную работу, а на экране показывает
// «17532000ч». Неизвестное время — это не «ждёт дольше всех».
func TestDecisionWithoutTimestampDoesNotHijackQueue(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	world := openTestWorld(t, application)
	ctx := context.Background()
	now := time.Now().UTC()

	run := domain.Run{ID: "run-1", WorkspaceID: world.ID, AgentID: "a1", Status: domain.RunWaiting, StartedAt: now}
	if err = application.store.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	// Настоящая работа ждёт десять минут.
	if err = application.store.SaveApproval(ctx, domain.Approval{
		ID: "ap-real", RunID: run.ID, AgentID: "a1", ToolName: "run_command",
		Status: domain.ApprovalPending, CreatedAt: now.Add(-10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	// А у этой записи отметки времени нет.
	if err = application.store.SaveChangeSet(ctx, domain.ChangeSet{
		ID: "cs-broken", WorkspaceID: world.ID, Title: "Без даты", Status: domain.ChangeSetPending,
		Items: []domain.ChangeItem{{ID: "i1", Path: "a.go", Kind: "modify"}},
	}); err != nil {
		t.Fatal(err)
	}
	// А эта создана «в будущем»: часы машины прыгнули назад после поправки NTP.
	// Ожидание тогда выходит отрицательным, и запись уезжает в конец очереди.
	if err = application.store.SaveChangeSet(ctx, domain.ChangeSet{
		ID: "cs-future", WorkspaceID: world.ID, Title: "Из будущего", Status: domain.ChangeSetPending,
		CreatedAt: now.Add(2 * time.Hour),
		Items:     []domain.ChangeItem{{ID: "i2", Path: "b.go", Kind: "modify"}},
	}); err != nil {
		t.Fatal(err)
	}

	queue, err := application.Decisions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue.Items) < 2 {
		t.Fatalf("в очереди %d элементов — проверка прошла бы вхолостую", len(queue.Items))
	}
	if queue.Items[0].ID != "ap-real" {
		t.Errorf("верх очереди занял %q вместо реально ждущего ap-real", queue.Items[0].ID)
	}
	for _, item := range queue.Items {
		if item.WaitingMs < 0 {
			t.Errorf("решение %s ждёт отрицательное время: %d", item.ID, item.WaitingMs)
		}
		if item.WaitingMs > int64(365*24*time.Hour/time.Millisecond) {
			t.Errorf("решение %s ждёт %d мс — это не время ожидания, а нулевая дата", item.ID, item.WaitingMs)
		}
	}
}
