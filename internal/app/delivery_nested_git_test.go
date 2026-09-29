package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

type recordingGitRunner struct{ calls [][]string }

func (r *recordingGitRunner) Run(_ context.Context, _ string, arguments ...string) ([]byte, error) {
	r.calls = append(r.calls, arguments)
	return nil, nil
}

// Папка без Git в корне с Git-проектами внутри: `git init` накрыл бы их
// чужим репозиторием. Доставка отказывает, а не создаёт .git.
func TestDeliveryBranchRefusesFolderWithNestedRepositories(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"cf-pages", "cf-vue-apps"} {
		if err := os.MkdirAll(filepath.Join(root, name, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runner := &recordingGitRunner{}
	err := ensureDeliveryBranch(context.Background(), runner, domain.DeliveryTarget{WorkspacePath: root, Branch: "point/task"})
	if err == nil || !strings.Contains(err.Error(), "cf-pages, cf-vue-apps") {
		t.Fatalf("delivery into a multi-repo folder allowed: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("git ran in the folder: %v", runner.calls)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".git")); !os.IsNotExist(statErr) {
		t.Fatal("repository created over nested projects")
	}
}
