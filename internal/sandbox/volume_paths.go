package sandbox

import (
	"fmt"
	"path/filepath"
	"strings"

	"local-agent-workbench/internal/sandboxsync"
)

// ResolveProcessDirectory validates a volume cwd without requiring transient
// dependency/build directories to exist in the portable host mirror.
func (b *ContainerBackend) ResolveProcessDirectory(root, value string) (string, error) {
	base, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" || value == "." {
		return base, nil
	}
	target := filepath.FromSlash(value)
	if !filepath.IsAbs(target) {
		target = filepath.Join(base, target)
	}
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return base, nil
	}
	return sandboxsync.Resolve(base, filepath.ToSlash(rel))
}

func volumeContainerPaths(root, cwd string) (string, string, error) {
	base, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	if cwd == "" {
		cwd = base
	}
	rel, err := filepath.Rel(base, cwd)
	if err != nil {
		return "", "", err
	}
	if rel == "." {
		return base, "/workspace", nil
	}
	if _, err := sandboxsync.Resolve(base, filepath.ToSlash(rel)); err != nil {
		return "", "", err
	}
	return base, "/workspace/" + filepath.ToSlash(rel), nil
}

func workspaceProgram(root, program string) (string, error) {
	if !filepath.IsAbs(program) {
		return program, nil
	}
	rel, err := filepath.Rel(root, program)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return program, nil // Absolute image programs remain image-local.
	}
	if rel == "." {
		return "", fmt.Errorf("process program points to the workspace directory")
	}
	return "/workspace/" + filepath.ToSlash(rel), nil
}
