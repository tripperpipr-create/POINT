package changesets

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/storage"
)

// Применение набора не выносит содержимое за пределы проекта через жёсткую
// ссылку.
//
// Проверка пути здесь бессильна по построению: путь честно внутри проекта, а
// данные снаружи — это один файл на диске. Защищает способ записи: во временный
// файл рядом и переименование поверх, что подменяет запись в каталоге и
// разрывает ссылку. Так пишут workspace.Write и tools/patch.go, а применение
// набора писало на месте — и переписывало внешний файл.
func TestApplyDoesNotWriteThroughHardlink(t *testing.T) {
	const original = "исходное содержимое"

	link := func(t *testing.T, root, secret string) {
		t.Helper()
		target := filepath.Join(root, "inside.txt")
		if runtime.GOOS == "windows" {
			if err := exec.Command("cmd", "/c", "mklink", "/H", target, secret).Run(); err != nil {
				t.Skip("жёсткая ссылка недоступна")
			}
			return
		}
		if err := os.Link(secret, target); err != nil {
			t.Skip("жёсткая ссылка недоступна")
		}
	}

	setup := func(t *testing.T) (string, string, Applier, domain.ChangeSet) {
		t.Helper()
		root, outside := t.TempDir(), t.TempDir()
		secret := filepath.Join(outside, "secret.txt")
		if err := os.WriteFile(secret, []byte(original), 0o600); err != nil {
			t.Fatal(err)
		}
		link(t, root, secret)

		// Защита от холостого хода: ссылка обязана разделять содержимое, иначе
		// проверка пройдёт просто потому, что связи между файлами нет.
		if err := os.WriteFile(secret, []byte("проверка связи"), 0o600); err != nil {
			t.Fatal(err)
		}
		shared, err := os.ReadFile(filepath.Join(root, "inside.txt"))
		if err != nil || string(shared) != "проверка связи" {
			t.Skipf("ссылка не разделяет содержимое (%q, %v)", string(shared), err)
		}
		if err := os.WriteFile(secret, []byte(original), 0o600); err != nil {
			t.Fatal(err)
		}

		db, err := storage.Open(filepath.Join(t.TempDir(), "changes.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		set := domain.ChangeSet{
			ID: domain.NewID("changeset"), WorkspaceID: "ws", Status: domain.ChangeSetPending,
			Items: []domain.ChangeItem{{
				ID: domain.NewID("changeitem"), Path: "inside.txt", Kind: "modify",
				OriginalHash: hashBytes([]byte(original)), OriginalContent: original,
				ProposedContent: "применено наружу", ProposedHash: hashBytes([]byte("применено наружу")),
			}},
		}
		return root, secret, Applier{Store: db}, set
	}

	t.Run("ApplyWithContent", func(t *testing.T) {
		root, secret, applier, set := setup(t)
		if _, err := applier.ApplyWithContent(context.Background(), root, set, map[string]string{
			"inside.txt": "применено наружу",
		}); err != nil {
			t.Fatalf("применение внутри проекта не прошло: %v", err)
		}
		after, err := os.ReadFile(secret)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != original {
			t.Fatalf("внешний файл переписан через жёсткую ссылку: %q", string(after))
		}
	})

	t.Run("Apply", func(t *testing.T) {
		root, secret, applier, set := setup(t)
		ctx := context.Background()
		if err := applier.Store.SaveChangeSet(ctx, set); err != nil {
			t.Fatal(err)
		}
		if _, err := applier.Apply(ctx, root, set.ID); err != nil {
			t.Fatalf("применение внутри проекта не прошло: %v", err)
		}
		after, err := os.ReadFile(secret)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != original {
			t.Fatalf("внешний файл переписан через жёсткую ссылку: %q", string(after))
		}
	})
}
