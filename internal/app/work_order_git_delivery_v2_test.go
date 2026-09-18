package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestCreateWorkOrderSquashCommitV2CommitsOnlyApprovedFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	repository := t.TempDir()
	if output, err := exec.Command("git", "-C", repository, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(repository, "result.txt"), []byte("ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitID, err := createWorkOrderSquashCommitV2(context.Background(), repository, "quest-42", "evidence-42", []string{"result.txt"})
	if err != nil || len(commitID) != 40 {
		t.Fatalf("commit=%q err=%v", commitID, err)
	}
	message, err := exec.Command("git", "-C", repository, "log", "-1", "--pretty=%B").CombinedOutput()
	if err != nil || !strings.Contains(string(message), "Point Quest quest-42") || !strings.Contains(string(message), "EvidenceBundle: evidence-42") {
		t.Fatalf("unexpected commit message: %q err=%v", message, err)
	}
	status, err := exec.Command("git", "-C", repository, "status", "--porcelain").CombinedOutput()
	if err != nil || len(status) != 0 {
		t.Fatalf("delivery did not leave approved files clean: %q err=%v", status, err)
	}
}

func TestCreateWorkOrderSquashCommitV2RefusesUnrelatedWorkspaceDrift(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	repository := t.TempDir()
	if output, err := exec.Command("git", "-C", repository, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	for name, value := range map[string]string{"result.txt": "ready\n", "external.txt": "user change\n"} {
		if err := os.WriteFile(filepath.Join(repository, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := createWorkOrderSquashCommitV2(context.Background(), repository, "quest", "evidence", []string{"result.txt"}); err == nil || !strings.Contains(err.Error(), "outside the approved delivery") {
		t.Fatalf("unrelated drift was not rejected: %v", err)
	}
}

func TestWorkOrderChangeSetsRollbackAsOneDeliveryOnConflict(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	workspacePath := t.TempDir()
	view, err := application.OpenWorkspace(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(workspacePath, "existing.txt"), []byte("user version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	root := domain.Quest{ID: "quest-atomic", WorkspaceID: view.Workspace.ID, Title: "Atomic", Kind: "project", Status: domain.QuestApplying, CreatedAt: now, UpdatedAt: now}
	if err = application.store.SaveQuest(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	created := "first applied\n"
	proposed := "agent version\n"
	sets := []domain.ChangeSet{
		{ID: "set-first", WorkspaceID: view.Workspace.ID, QuestID: root.ID, Title: "first", Status: domain.ChangeSetPending, CreatedAt: now, UpdatedAt: now,
			Items: []domain.ChangeItem{{ID: "first", Path: "created.txt", Kind: "create", ProposedContent: created, ProposedHash: deliveryHashV2(created)}}},
		{ID: "set-second", WorkspaceID: view.Workspace.ID, QuestID: root.ID, Title: "second", Status: domain.ChangeSetPending, CreatedAt: now.Add(time.Millisecond), UpdatedAt: now.Add(time.Millisecond),
			Items: []domain.ChangeItem{{ID: "second", Path: "existing.txt", Kind: "modify", OriginalHash: deliveryHashV2("different baseline\n"), ProposedContent: proposed, ProposedHash: deliveryHashV2(proposed)}}},
	}
	for _, set := range sets {
		if err = application.store.SaveChangeSet(context.Background(), set); err != nil {
			t.Fatal(err)
		}
	}
	order := domain.WorkOrder{WorkspaceID: view.Workspace.ID, Workspace: domain.WorkspacePlan{Path: workspacePath}, Delivery: domain.DeliveryPolicy{CommitMode: "none"}}
	if _, _, err = application.applyWorkOrderChangeSetsV2(context.Background(), order, root, "evidence"); err == nil {
		t.Fatal("conflicting second change set did not fail the delivery")
	}
	if _, err = os.Stat(filepath.Join(workspacePath, "created.txt")); !os.IsNotExist(err) {
		t.Fatalf("first change set remained partially delivered: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(workspacePath, "existing.txt"))
	if err != nil || string(data) != "user version\n" {
		t.Fatalf("external file changed despite conflict: %q err=%v", data, err)
	}
}

func deliveryHashV2(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// Проект бывает подпапкой репозитория: так открывают монорепозиторий. Пути
// доставки при этом относительны проекта, а git печатает их от корня — без
// поправки на префикс честная доставка обвиняла себя же в чужом изменении.
func TestCreateWorkOrderSquashCommitV2DeliversFromRepositorySubdirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	repository := t.TempDir()
	if output, err := exec.Command("git", "-C", repository, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	project := filepath.Join(repository, "apps", "web")
	if err := os.MkdirAll(filepath.Join(project, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "src", "result.txt"), []byte("ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitID, err := createWorkOrderSquashCommitV2(context.Background(), project, "quest-7", "evidence-7", []string{"src/result.txt"})
	if err != nil || len(commitID) != 40 {
		t.Fatalf("commit=%q err=%v", commitID, err)
	}
	files, err := exec.Command("git", "-C", repository, "show", "--name-only", "--pretty=", "HEAD").CombinedOutput()
	if err != nil || strings.TrimSpace(string(files)) != "apps/web/src/result.txt" {
		t.Fatalf("commit content=%q err=%v", files, err)
	}

	// Чужое изменение вне проекта остаётся отказом: договор доставки не знает
	// «своей» подпапки, он знает утверждённый список файлов.
	if err = os.WriteFile(filepath.Join(repository, "external.txt"), []byte("user change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(project, "src", "second.txt"), []byte("more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = createWorkOrderSquashCommitV2(context.Background(), project, "quest-8", "evidence-8", []string{"src/second.txt"}); err == nil || !strings.Contains(err.Error(), "outside the approved delivery") {
		t.Fatalf("чужое изменение принято: %v", err)
	}
}
