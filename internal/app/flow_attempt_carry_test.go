package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
)

// Q11 (E6): повтор этапа начинал с исходника и терял сделанное до обрыва.
// Изменения прерванной попытки переносятся в новую песочницу от того же
// исходника; файл, который в новой песочнице уже другой, не трогается.
func TestRetryCarriesInterruptedAttemptCandidate(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	backend := &sandbox.Manager{Root: t.TempDir()}
	application, err := New(t.TempDir(), WithSandboxBackend(backend))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	ctx := context.Background()
	world := openTestWorld(t, application)
	for name, content := range map[string]string{"a.txt": "one\n", "b.txt": "keep\n", "c.txt": "gone\n"} {
		if err = os.WriteFile(filepath.Join(world.Path, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	attempt := func(id string) (domain.ExecutionInstance, domain.SandboxRecord) {
		record, createErr := backend.Create(ctx, sandbox.CreateRequest{WorkspaceID: world.ID, WorkspacePath: world.Path, ExecutionID: id})
		if createErr != nil {
			t.Fatal(createErr)
		}
		if createErr = application.store.SaveSandbox(ctx, record); createErr != nil {
			t.Fatal(createErr)
		}
		execution := domain.ExecutionInstance{ID: id, WorkspaceID: world.ID, SandboxID: record.ID, Status: domain.RunFailed, StartedAt: time.Now().UTC()}
		if createErr = application.store.SaveExecution(ctx, execution); createErr != nil {
			t.Fatal(createErr)
		}
		return execution, record
	}
	write := func(root, name, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(name))
		if mkdirErr := os.MkdirAll(filepath.Dir(path), 0o700); mkdirErr != nil {
			t.Fatal(mkdirErr)
		}
		if writeErr := os.WriteFile(path, []byte(content), 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	_, interrupted := attempt("exec-interrupted")
	write(interrupted.Path, "a.txt", "two\n")
	write(interrupted.Path, "b.txt", "first\n")
	write(interrupted.Path, "d/new.txt", "found the blocker\n")
	if err = os.Remove(filepath.Join(interrupted.Path, "c.txt")); err != nil {
		t.Fatal(err)
	}
	retry, next := attempt("exec-retry")
	write(next.Path, "b.txt", "mine\n")

	carry := application.carryPreviousAttemptV2(ctx, "exec-interrupted", retry)
	if carry.Reason != "" || !slices.Contains(carry.Files, "a.txt") || !slices.Contains(carry.Files, "d/new.txt") || !slices.Contains(carry.Skipped, "b.txt") {
		t.Fatalf("перенос: %+v", carry)
	}
	read := func(name string) string {
		data, _ := os.ReadFile(filepath.Join(next.Path, filepath.FromSlash(name)))
		return string(data)
	}
	if read("a.txt") != "two\n" || read("d/new.txt") != "found the blocker\n" || read("b.txt") != "mine\n" {
		t.Fatalf("песочница повтора: a=%q new=%q b=%q", read("a.txt"), read("d/new.txt"), read("b.txt"))
	}
	if _, statErr := os.Stat(filepath.Join(next.Path, "c.txt")); !os.IsNotExist(statErr) {
		t.Fatal("удаление прерванной попытки не перенесено")
	}
	// Исходник общий, поэтому набор изменений повтора включает перенесённое.
	diffs, err := backend.Diff(ctx, next.BaselinePath, next.Path)
	if err != nil {
		t.Fatal(err)
	}
	changed := map[string]string{}
	for _, entry := range diffs {
		changed[entry.Path] = entry.Kind
	}
	if changed["a.txt"] != "modify" || changed["d/new.txt"] != "create" || changed["c.txt"] != "delete" {
		t.Fatalf("набор изменений повтора без перенесённого: %v", changed)
	}
	if note := attemptCarryNote(carry); !strings.Contains(note, "a.txt") || !strings.Contains(note, "b.txt") {
		t.Fatalf("исполнитель не узнает о переносе: %q", note)
	}

	// Другой исходник — ничего не переносится.
	other, otherRecord := attempt("exec-other-base")
	otherRecord.ParentExecutionID = "exec-upstream"
	if err = application.store.SaveSandbox(ctx, otherRecord); err != nil {
		t.Fatal(err)
	}
	if refused := application.carryPreviousAttemptV2(ctx, "exec-interrupted", other); refused.Reason == "" || len(refused.Files) != 0 {
		t.Fatalf("перенос между разными исходниками: %+v", refused)
	}
}
