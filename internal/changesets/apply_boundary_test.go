package changesets

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/storage"
)

// Применение набора изменений не пишет за пределы проекта.
//
// Путь каждого файла приходит из набора, то есть от агента. Apply пропускает
// его через safeTarget, а ApplyWithContent склеивал путь с корнем напрямую:
// «../» и абсолютный путь не отвергались, и запись — а для delete и удаление —
// уходила наружу. Функция пока никем не вызывается, но незамеченная дыра ждёт
// того, кто её подключит.
//
// Проверяем мир, а не текст ошибки: снаружи не должно появиться ничего.
func TestApplyRefusesPathsOutsideWorkspace(t *testing.T) {
	ctx := context.Background()

	for _, entry := range []struct {
		имя  string
		путь string
	}{
		{"выход через ..", filepath.Join("..", "escaped.txt")},
		{"выход через вложенный ..", filepath.Join("sub", "..", "..", "escaped.txt")},
	} {
		t.Run(entry.имя, func(t *testing.T) {
			parent := t.TempDir()
			workspacePath := filepath.Join(parent, "project")
			if err := os.MkdirAll(workspacePath, 0o755); err != nil {
				t.Fatal(err)
			}
			db, err := storage.Open(filepath.Join(t.TempDir(), "changes.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			applier := Applier{Store: db}

			set := domain.ChangeSet{
				ID: domain.NewID("changeset"), WorkspaceID: "ws", Title: "побег",
				Status: domain.ChangeSetPending,
				Items: []domain.ChangeItem{{
					ID: domain.NewID("changeitem"), Path: entry.путь, Kind: "modify",
				}},
			}

			escaped := filepath.Join(parent, "escaped.txt")

			if _, err = applier.ApplyWithContent(ctx, workspacePath, set, map[string]string{
				entry.путь: "агент записал наружу",
			}); err == nil {
				t.Error("ApplyWithContent принял путь за пределы проекта")
			}
			if _, statErr := os.Stat(escaped); statErr == nil {
				t.Fatalf("ApplyWithContent создал файл вне проекта: %s", escaped)
			}

			// Apply работает по идентификатору: набор нужно положить в хранилище.
			if err = db.SaveChangeSet(ctx, set); err != nil {
				t.Fatal(err)
			}
			if _, err = applier.Apply(ctx, workspacePath, set.ID); err == nil {
				t.Error("Apply принял путь за пределы проекта")
			}
			if _, statErr := os.Stat(escaped); statErr == nil {
				t.Fatalf("Apply создал файл вне проекта: %s", escaped)
			}
		})
	}
}

// Защита от холостого хода: обычный путь внутри проекта обязан проходить.
// Иначе проверки выше срабатывали бы просто потому, что применение не работает.
func TestApplyWithContentWritesInsideWorkspace(t *testing.T) {
	ctx := context.Background()
	workspacePath := t.TempDir()
	db, err := storage.Open(filepath.Join(t.TempDir(), "changes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	applier := Applier{Store: db}

	set := domain.ChangeSet{
		ID: domain.NewID("changeset"), WorkspaceID: "ws", Title: "обычная правка",
		Status: domain.ChangeSetPending,
		Items: []domain.ChangeItem{{
			ID: domain.NewID("changeitem"), Path: "inside.txt", Kind: "modify",
		}},
	}
	if _, err = applier.ApplyWithContent(ctx, workspacePath, set, map[string]string{
		"inside.txt": "внутри проекта",
	}); err != nil {
		t.Fatalf("применение внутри проекта не прошло: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(workspacePath, "inside.txt"))
	if err != nil {
		t.Fatalf("файл внутри проекта не создан: %v", err)
	}
	if string(content) != "внутри проекта" {
		t.Fatalf("содержимое не то: %q", string(content))
	}
}
