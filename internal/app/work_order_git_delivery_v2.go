package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"

	"local-agent-workbench/internal/osproc"
	"path/filepath"
	"strings"
)

// createWorkOrderSquashCommitV2 creates exactly one commit from the files
// delivered by this WorkOrder. It refuses unrelated workspace drift and never
// uses `git add -A` without an explicit path list.
func createWorkOrderSquashCommitV2(ctx context.Context, workspacePath, questID, evidenceID string, changedFiles []string) (string, error) {
	if len(changedFiles) == 0 {
		return "", errors.New("squash delivery has no changed files")
	}
	inside, err := runGitV2(ctx, workspacePath, nil, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(inside) != "true" {
		return "", errors.New("squash delivery requires a Git workspace")
	}
	// Пути доставки относительны папки проекта, а `status --porcelain` всегда
	// печатает их от корня репозитория. Совпадают они только когда проект и
	// есть корень; открытая подпапка монорепозитория без этой поправки давала
	// «изменение вне утверждённой доставки» на собственных же файлах.
	prefix, err := runGitV2(ctx, workspacePath, nil, "rev-parse", "--show-prefix")
	if err != nil {
		return "", fmt.Errorf("locate delivery path inside the repository: %w", err)
	}
	prefix = strings.TrimSpace(prefix)
	approved := make(map[string]bool, len(changedFiles))
	paths := make([]string, 0, len(changedFiles))
	for _, item := range changedFiles {
		clean := filepath.Clean(strings.TrimSpace(item))
		if clean == "." || clean == ".." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("unsafe changed path %q", item)
		}
		normalized := filepath.ToSlash(clean)
		approved[prefix+normalized] = true
		paths = append(paths, normalized)
	}
	status, err := runGitBytesV2(ctx, workspacePath, nil, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return "", fmt.Errorf("inspect Git delivery target: %w", err)
	}
	for _, record := range bytes.Split(status, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		if len(record) < 4 || record[0] == 'R' || record[1] == 'R' || record[0] == 'C' || record[1] == 'C' {
			return "", errors.New("Git workspace contains an unsupported external rename/copy")
		}
		path := filepath.ToSlash(string(record[3:]))
		if !approved[path] {
			return "", fmt.Errorf("Git workspace changed outside the approved delivery: %s", path)
		}
	}
	args := append([]string{"add", "-A", "--"}, paths...)
	if _, err = runGitV2(ctx, workspacePath, nil, args...); err != nil {
		return "", fmt.Errorf("stage approved delivery files: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if _, headErr := runGitV2(context.Background(), workspacePath, nil, "rev-parse", "--verify", "HEAD"); headErr == nil {
			_, _ = runGitV2(context.Background(), workspacePath, nil, append([]string{"reset", "--mixed", "HEAD", "--"}, paths...)...)
		} else {
			_, _ = runGitV2(context.Background(), workspacePath, nil, append([]string{"rm", "--cached", "-r", "--ignore-unmatch", "--"}, paths...)...)
		}
	}()
	environment := []string{
		"GIT_AUTHOR_NAME=Point", "GIT_AUTHOR_EMAIL=point@localhost",
		"GIT_COMMITTER_NAME=Point", "GIT_COMMITTER_EMAIL=point@localhost",
	}
	message := "Point Quest " + strings.TrimSpace(questID) + "\n\nEvidenceBundle: " + strings.TrimSpace(evidenceID)
	if _, err = runGitV2(ctx, workspacePath, environment, "commit", "--no-gpg-sign", "-m", message); err != nil {
		return "", fmt.Errorf("create approved squash commit: %w", err)
	}
	commitID, err := runGitV2(ctx, workspacePath, nil, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(commitID) == "" {
		return "", errors.New("read squash commit revision")
	}
	committed = true
	return strings.TrimSpace(commitID), nil
}

func runGitV2(ctx context.Context, workspacePath string, extraEnv []string, args ...string) (string, error) {
	output, err := runGitBytesV2(ctx, workspacePath, extraEnv, args...)
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func runGitBytesV2(ctx context.Context, workspacePath string, extraEnv []string, args ...string) ([]byte, error) {
	command := osproc.CommandContext(ctx, "git", append([]string{"-C", workspacePath}, args...)...)
	command.Env = append(os.Environ(), extraEnv...)
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if len(message) > 2048 {
			message = message[:2048]
		}
		if message != "" {
			return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, message)
		}
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return output, nil
}
