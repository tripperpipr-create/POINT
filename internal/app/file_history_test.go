package app

import (
	"context"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestFileHistoryTurnsRunRecordsAroundOneFile(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	world := openTestWorld(t, application)
	ctx := context.Background()
	now := time.Now().UTC()

	// Патчи выбираются по миру через свои прогоны — это и есть изоляция миров,
	// поэтому прогоны обязаны существовать, иначе выборка честно вернёт пусто.
	for _, id := range []string{"run-1", "run-2", "run-3"} {
		if err = application.store.SaveRun(ctx, domain.Run{
			ID: id, WorkspaceID: world.ID, ProfileID: "default", Task: "правка",
			Status: domain.RunCompleted, StartedAt: now.Add(-3 * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Две правки одного файла из разных прогонов плюс правка соседнего файла.
	// Срез обязан собрать первые две и не притащить третью.
	if err = application.store.SavePatch(ctx, domain.PatchProposal{
		ID: "p-old", RunID: "run-1", Path: "main.go", Status: "applied",
		OriginalExisted: true, Original: "package main\n", Proposed: "package main\n// a\n",
		Diff: "@@ -1 +1,2 @@", CreatedAt: now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SavePatch(ctx, domain.PatchProposal{
		ID: "p-new", RunID: "run-2", Path: "main.go", Status: "reverted",
		OriginalExisted: true, Original: "package main\n// a\n", Proposed: "package main\n// b\n",
		Diff: "@@ -1,2 +1,2 @@", CreatedAt: now.Add(-1 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SavePatch(ctx, domain.PatchProposal{
		ID: "p-other", RunID: "run-3", Path: "other.go", Status: "applied",
		OriginalExisted: true, Original: "x", Proposed: "y", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err = application.store.SaveChangeSet(ctx, domain.ChangeSet{
		ID: "cs-1", WorkspaceID: world.ID, ExecutionID: "ex-1", Title: "Правка из песочницы",
		Status: domain.ChangeSetApplied, CreatedAt: now.Add(-30 * time.Minute),
		Items: []domain.ChangeItem{{ID: "it-1", Path: "main.go", Kind: "modify", Diff: "@@"}},
	}); err != nil {
		t.Fatal(err)
	}

	history, err := application.FileHistory(ctx, "main.go")
	if err != nil {
		t.Fatal(err)
	}
	if history.Total != 3 {
		t.Fatalf("ожидалось 3 записи по main.go, получено %d: %+v", history.Total, history.Entries)
	}
	for _, entry := range history.Entries {
		if entry.ID == "p-other" {
			t.Fatal("в историю файла просочилась правка соседнего файла")
		}
	}
	// Новейшее сверху: историю читают с конца.
	if history.Entries[0].ID != "cs-1/it-1" || history.Entries[len(history.Entries)-1].ID != "p-old" {
		t.Fatalf("нарушен обратный хронологический порядок: %+v", history.Entries)
	}
	if history.Applied != 2 || history.Reverted != 1 {
		t.Fatalf("сводка неверна: applied=%d reverted=%d", history.Applied, history.Reverted)
	}

	// Откат предлагается только там, где он возможен: откаченную правку
	// откатить нельзя, и кнопки для неё быть не должно.
	byID := map[string]FileHistoryEntry{}
	for _, entry := range history.Entries {
		byID[entry.ID] = entry
	}
	if !byID["p-old"].Revertible || byID["p-old"].RevertPath != "/api/patches/p-old/revert" {
		t.Fatalf("применённый патч обязан предлагать откат: %+v", byID["p-old"])
	}
	if byID["p-new"].Revertible || byID["p-new"].RevertPath != "" {
		t.Fatalf("уже откаченная правка не может предлагать откат: %+v", byID["p-new"])
	}
	if byID["cs-1/it-1"].RevertPath != "/api/change-sets/cs-1/revert" {
		t.Fatalf("элемент набора откатывается через набор: %+v", byID["cs-1/it-1"])
	}
}

// Путь приходит от клиента, поэтому граница рабочей папки проверяется здесь же.
func TestFileHistoryRejectsPathsOutsideWorkspace(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())

	openTestWorld(t, application)
	ctx := context.Background()

	for _, path := range []string{"../secrets.txt", "..", "nested/../../escape.go"} {
		if _, err = application.FileHistory(ctx, path); err == nil {
			t.Fatalf("путь %q обязан быть отклонён границей рабочей папки", path)
		}
	}
	if _, err = application.FileHistory(ctx, ""); err == nil {
		t.Fatal("пустой путь обязан быть отклонён")
	}
	// Имя с точками внутри — это обычный файл, а не побег. Отклонять его нельзя.
	if _, err = application.FileHistory(ctx, "notes..md"); err != nil {
		t.Fatalf("файл notes..md должен быть допустим: %v", err)
	}
}
