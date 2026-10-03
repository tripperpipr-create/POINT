package gitflow

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// RepositoryLock serializes Point writes to a worktree, including quest delivery.
// Git's own index.lock still guards changes made by external processes.
var repositoryLocks sync.Map

func LockRepository(ctx context.Context, runner Runner, dir string) func() {
	root, err := run(ctx, runner, dir, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		root = dir
	}
	root, _ = filepath.Abs(root)
	root = filepath.Clean(root)
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
	}
	value, _ := repositoryLocks.LoadOrStore(root, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}
