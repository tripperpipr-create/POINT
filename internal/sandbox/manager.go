package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pmezard/go-difflib/difflib"

	"local-agent-workbench/internal/osproc"
	"local-agent-workbench/internal/domain"
)

// Manager creates isolated execution workspaces (git worktree or filtered copy).
type Manager struct {
	Root string
}

// Capabilities describes guarantees provided by a sandbox backend. Callers
// must use these flags instead of treating every backend as an OS boundary.
type Capabilities struct {
	Backend                       string   `json:"backend"`
	Version                       string   `json:"version,omitempty"`
	APIVersion                    string   `json:"apiVersion,omitempty"`
	Image                         string   `json:"image,omitempty"`
	ImageDigest                   string   `json:"imageDigest,omitempty"`
	LiveWorkspaceIsolation        bool     `json:"liveWorkspaceIsolation"`
	ProcessIsolation              bool     `json:"processIsolation"`
	NetworkIsolation              bool     `json:"networkIsolation"`
	SecretEnvironmentSanitization bool     `json:"secretEnvironmentSanitization"`
	SymlinkIsolation              bool     `json:"symlinkIsolation"`
	StrongOSBoundary              bool     `json:"strongOsBoundary"`
	Notes                         []string `json:"notes,omitempty"`
}

// Backend is the replaceable execution-workspace boundary used by Agent Hub.
// A future container/VM implementation can satisfy the same lifecycle while
// reporting stronger, independently verifiable capabilities.
type Backend interface {
	Create(context.Context, CreateRequest) (domain.SandboxRecord, error)
	Diff(context.Context, string, string) ([]DiffEntry, error)
	Close(context.Context, domain.SandboxRecord, string) error
	Capabilities() Capabilities
}

var _ Backend = (*Manager)(nil)

func (*Manager) Capabilities() Capabilities {
	return Capabilities{
		Backend:                       "filtered-copy",
		Version:                       "1",
		LiveWorkspaceIsolation:        true,
		ProcessIsolation:              false,
		NetworkIsolation:              false,
		SecretEnvironmentSanitization: true,
		SymlinkIsolation:              true,
		StrongOSBoundary:              false,
		Notes: []string{
			"Approved commands run as the Point process user.",
			"Network command filtering is defense in depth, not an egress boundary.",
			"Clean Git repositories may use a detached worktree; dirty repositories use a filtered copy of the current files.",
		},
	}
}

type CreateRequest struct {
	WorkspaceID    string
	WorkspacePath  string
	ExecutionID    string
	PreferWorktree bool
	// LiveWorkspace makes Path the open project. Tools write live files;
	// BaselinePath still holds an immutable snapshot for Change Set diffs.
	LiveWorkspace        bool
	SeedPath             string
	ParentSandboxID      string
	ParentExecutionID    string
	BaselineChangeSetIDs []string
}

type DiffEntry struct {
	Path         string
	Kind         string // create | modify | delete
	OriginalHash string
	ProposedHash string
	Original     string
	Proposed     string
}

// MergeSeed is one completed branch head participating in a deterministic
// Flow join. Paths remain server-side; only bounded conflict metadata is
// exposed to clients.
type MergeSeed struct {
	ExecutionID string
	SandboxID   string
	Path        string
}

type MergeResolution struct {
	Path        string  `json:"path"`
	Strategy    string  `json:"strategy"` // use_parent | manual
	ExecutionID string  `json:"executionId,omitempty"`
	Content     *string `json:"content,omitempty"`
	Delete      bool    `json:"delete,omitempty"`
}

type MergeCandidate struct {
	ExecutionID string `json:"executionId"`
	Kind        string `json:"kind"`
	Hash        string `json:"hash,omitempty"`
	proposed    string
}

type MergeConflict struct {
	Path       string           `json:"path"`
	Candidates []MergeCandidate `json:"candidates"`
}

type MergeRequest struct {
	WorkspaceID          string
	ExecutionID          string
	BasePath             string
	Seeds                []MergeSeed
	Resolutions          []MergeResolution
	BaselineChangeSetIDs []string
}

type MergeResult struct {
	Record    domain.SandboxRecord `json:"record"`
	Conflicts []MergeConflict      `json:"conflicts,omitempty"`
	Paths     []string             `json:"paths,omitempty"`
}

type mergeTextEdit struct {
	start       int
	end         int
	replacement []string
}

func splitMergeLines(value string) []string {
	if value == "" {
		return nil
	}
	lines := strings.SplitAfter(value, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func mergeTextEditsOverlap(left, right mergeTextEdit) bool {
	leftInsert := left.start == left.end
	rightInsert := right.start == right.end
	if leftInsert && rightInsert {
		return left.start == right.start
	}
	if leftInsert {
		return left.start >= right.start && left.start <= right.end
	}
	if rightInsert {
		return right.start >= left.start && right.start <= left.end
	}
	return left.start < right.end && right.start < left.end
}

// mergeNonOverlappingText performs a conservative line-based three-way
// merge. It succeeds only when every branch edit maps to a disjoint base
// range. Identical edits are deduplicated; overlapping replacements remain an
// explicit user conflict instead of relying on marker text or branch order.
func mergeNonOverlappingText(base string, candidates []MergeCandidate) (string, bool) {
	baseLines := splitMergeLines(base)
	edits := make([]mergeTextEdit, 0)
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if candidate.Kind != "modify" {
			return "", false
		}
		proposedLines := splitMergeLines(candidate.proposed)
		matcher := difflib.NewMatcher(baseLines, proposedLines)
		for _, opcode := range matcher.GetOpCodes() {
			if opcode.Tag == 'e' {
				continue
			}
			replacement := append([]string(nil), proposedLines[opcode.J1:opcode.J2]...)
			key := fmt.Sprintf("%d:%d:%s", opcode.I1, opcode.I2, strings.Join(replacement, ""))
			if seen[key] {
				continue
			}
			seen[key] = true
			edits = append(edits, mergeTextEdit{start: opcode.I1, end: opcode.I2, replacement: replacement})
		}
	}
	for left := 0; left < len(edits); left++ {
		for right := left + 1; right < len(edits); right++ {
			if mergeTextEditsOverlap(edits[left], edits[right]) {
				return "", false
			}
		}
	}
	sort.Slice(edits, func(i, j int) bool {
		if edits[i].start == edits[j].start {
			return edits[i].end > edits[j].end
		}
		return edits[i].start > edits[j].start
	})
	result := append([]string(nil), baseLines...)
	for _, edit := range edits {
		updated := make([]string, 0, len(result)-(edit.end-edit.start)+len(edit.replacement))
		updated = append(updated, result[:edit.start]...)
		updated = append(updated, edit.replacement...)
		updated = append(updated, result[edit.end:]...)
		result = updated
	}
	return strings.Join(result, ""), true
}

// Merge creates a writable sandbox from multiple completed branch heads.
// Branches that touch disjoint paths (or produce byte-identical content) are
// combined automatically. Divergent outcomes for the same path are returned
// as structured conflicts; no partial sandbox is retained until every
// conflict has an explicit resolution.
func (m *Manager) Merge(ctx context.Context, req MergeRequest) (MergeResult, error) {
	if strings.TrimSpace(req.BasePath) == "" {
		return MergeResult{}, fmt.Errorf("merge base path is required")
	}
	if len(req.Seeds) < 2 {
		return MergeResult{}, fmt.Errorf("sandbox merge requires at least two branch seeds")
	}
	if err := os.MkdirAll(m.Root, 0o755); err != nil {
		return MergeResult{}, err
	}
	id := domain.NewID("sandbox")
	target := filepath.Join(m.Root, id)
	baseline := target + "-baseline"
	cleanup := func() {
		_ = os.RemoveAll(target)
		_ = os.RemoveAll(baseline)
	}
	if err := copyFiltered(req.BasePath, target); err != nil {
		cleanup()
		return MergeResult{}, fmt.Errorf("copy merge base: %w", err)
	}

	baseFiles, err := listTextFiles(req.BasePath)
	if err != nil {
		cleanup()
		return MergeResult{}, fmt.Errorf("read merge base: %w", err)
	}
	byPath := map[string][]MergeCandidate{}
	parentExecutionIDs := make([]string, 0, len(req.Seeds))
	parentSandboxIDs := make([]string, 0, len(req.Seeds))
	seenExecutions := map[string]bool{}
	for _, seed := range req.Seeds {
		seed.ExecutionID = strings.TrimSpace(seed.ExecutionID)
		if seed.ExecutionID == "" || strings.TrimSpace(seed.Path) == "" {
			cleanup()
			return MergeResult{}, fmt.Errorf("merge seed execution and path are required")
		}
		if seenExecutions[seed.ExecutionID] {
			cleanup()
			return MergeResult{}, fmt.Errorf("duplicate merge seed execution %s", seed.ExecutionID)
		}
		seenExecutions[seed.ExecutionID] = true
		parentExecutionIDs = append(parentExecutionIDs, seed.ExecutionID)
		parentSandboxIDs = append(parentSandboxIDs, seed.SandboxID)
		diffs, err := m.Diff(ctx, req.BasePath, seed.Path)
		if err != nil {
			cleanup()
			return MergeResult{}, fmt.Errorf("diff merge seed %s: %w", seed.ExecutionID, err)
		}
		for _, diff := range diffs {
			byPath[diff.Path] = append(byPath[diff.Path], MergeCandidate{
				ExecutionID: seed.ExecutionID, Kind: diff.Kind, Hash: diff.ProposedHash, proposed: diff.Proposed,
			})
		}
	}

	resolutionByPath := map[string]MergeResolution{}
	for _, resolution := range req.Resolutions {
		path := filepath.ToSlash(strings.TrimSpace(resolution.Path))
		if path == "" {
			cleanup()
			return MergeResult{}, fmt.Errorf("merge resolution path is required")
		}
		if _, exists := resolutionByPath[path]; exists {
			cleanup()
			return MergeResult{}, fmt.Errorf("duplicate merge resolution for %s", path)
		}
		resolution.Path = path
		resolutionByPath[path] = resolution
	}

	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	conflicts := make([]MergeConflict, 0)
	chosen := make(map[string]MergeCandidate, len(paths))
	for _, path := range paths {
		candidates := byPath[path]
		unique := make([]MergeCandidate, 0, len(candidates))
		seenOutcomes := map[string]bool{}
		for _, candidate := range candidates {
			var key string
			if candidate.Kind == "delete" {
				key = "delete"
			} else {
				key = "write:" + candidate.Hash
			}
			if !seenOutcomes[key] {
				seenOutcomes[key] = true
				unique = append(unique, candidate)
			}
		}
		if len(unique) == 1 {
			chosen[path] = unique[0]
			continue
		}
		if base, exists := baseFiles[path]; exists {
			if content, ok := mergeNonOverlappingText(base, unique); ok {
				chosen[path] = MergeCandidate{Kind: "modify", Hash: hashText(content), proposed: content}
				continue
			}
		}
		resolution, resolved := resolutionByPath[path]
		if resolved {
			switch strings.TrimSpace(resolution.Strategy) {
			case "use_parent":
				for _, candidate := range candidates {
					if candidate.ExecutionID == strings.TrimSpace(resolution.ExecutionID) {
						chosen[path] = candidate
						break
					}
				}
				if _, ok := chosen[path]; !ok {
					cleanup()
					return MergeResult{}, fmt.Errorf("resolution for %s references a non-candidate execution", path)
				}
			case "manual":
				if resolution.Delete {
					chosen[path] = MergeCandidate{Kind: "delete"}
				} else {
					if resolution.Content == nil {
						cleanup()
						return MergeResult{}, fmt.Errorf("manual merge resolution content is required for %s", path)
					}
					if len(*resolution.Content) > 1024*1024 {
						cleanup()
						return MergeResult{}, fmt.Errorf("manual merge resolution for %s exceeds 1 MiB", path)
					}
					chosen[path] = MergeCandidate{Kind: "modify", Hash: hashText(*resolution.Content), proposed: *resolution.Content}
				}
			default:
				cleanup()
				return MergeResult{}, fmt.Errorf("unsupported merge resolution strategy %q", resolution.Strategy)
			}
			continue
		}
		publicCandidates := make([]MergeCandidate, len(candidates))
		copy(publicCandidates, candidates)
		for index := range publicCandidates {
			publicCandidates[index].proposed = ""
		}
		conflicts = append(conflicts, MergeConflict{Path: path, Candidates: publicCandidates})
	}
	if len(conflicts) > 0 {
		cleanup()
		return MergeResult{Conflicts: conflicts, Paths: paths}, nil
	}
	for _, path := range paths {
		candidate := chosen[path]
		targetPath := filepath.Join(target, filepath.FromSlash(path))
		if candidate.Kind == "delete" {
			if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
				cleanup()
				return MergeResult{}, fmt.Errorf("merge delete %s: %w", path, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			cleanup()
			return MergeResult{}, err
		}
		if err := os.WriteFile(targetPath, []byte(candidate.proposed), 0o644); err != nil {
			cleanup()
			return MergeResult{}, fmt.Errorf("merge write %s: %w", path, err)
		}
	}
	if err := copyFiltered(target, baseline); err != nil {
		cleanup()
		return MergeResult{}, fmt.Errorf("snapshot merged sandbox baseline: %w", err)
	}
	record := domain.SandboxRecord{
		ID: id, WorkspaceID: req.WorkspaceID, ExecutionID: req.ExecutionID, Kind: "merge-copy",
		Backend: "filtered-copy", BackendVersion: "1",
		Path: target, BaselinePath: baseline, ParentSandboxIDs: parentSandboxIDs,
		ParentExecutionIDs: parentExecutionIDs, BaselineChangeSetIDs: append([]string(nil), req.BaselineChangeSetIDs...),
		CreatedAt: time.Now().UTC(),
	}
	return MergeResult{Record: record, Paths: paths}, nil
}

func (m *Manager) Create(ctx context.Context, req CreateRequest) (domain.SandboxRecord, error) {
	if strings.TrimSpace(req.WorkspacePath) == "" {
		return domain.SandboxRecord{}, fmt.Errorf("workspace path is required")
	}
	if err := os.MkdirAll(m.Root, 0o755); err != nil {
		return domain.SandboxRecord{}, err
	}
	id := domain.NewID("sandbox")
	if req.LiveWorkspace {
		return m.createLive(ctx, req, id)
	}
	target := filepath.Join(m.Root, id)
	record := domain.SandboxRecord{
		ID: id, WorkspaceID: req.WorkspaceID, ExecutionID: req.ExecutionID,
		Backend: "filtered-copy", BackendVersion: "1",
		Path: target, ParentSandboxID: req.ParentSandboxID, ParentExecutionID: req.ParentExecutionID,
		BaselineChangeSetIDs: append([]string(nil), req.BaselineChangeSetIDs...),
		CreatedAt:            time.Now().UTC(),
	}
	if strings.TrimSpace(req.ParentSandboxID) != "" {
		record.ParentSandboxIDs = []string{req.ParentSandboxID}
	}
	if strings.TrimSpace(req.ParentExecutionID) != "" {
		record.ParentExecutionIDs = []string{req.ParentExecutionID}
	}
	// A sequential Flow stage starts from an immutable snapshot of its single
	// completed predecessor. Keep a second filtered copy as the stage baseline:
	// its Change Set then contains only this stage's delta while the live
	// workspace remains untouched.
	if seedPath := strings.TrimSpace(req.SeedPath); seedPath != "" {
		info, statErr := os.Stat(seedPath)
		if statErr != nil {
			return domain.SandboxRecord{}, fmt.Errorf("inspect sandbox seed: %w", statErr)
		}
		if !info.IsDir() {
			return domain.SandboxRecord{}, fmt.Errorf("sandbox seed must be a directory")
		}
		record.Kind = "copy"
		record.BaselinePath = target + "-baseline"
		if err := copyFiltered(seedPath, record.BaselinePath); err != nil {
			_ = os.RemoveAll(record.BaselinePath)
			return domain.SandboxRecord{}, fmt.Errorf("copy sandbox baseline: %w", err)
		}
		if err := copyFiltered(seedPath, target); err != nil {
			_ = os.RemoveAll(record.BaselinePath)
			_ = os.RemoveAll(target)
			return domain.SandboxRecord{}, fmt.Errorf("copy sandbox seed: %w", err)
		}
		return record, nil
	}
	// Every execution gets an immutable baseline, including the first node in a
	// Flow. Without it, a user edit made while the agent is running would change
	// the meaning of the eventual Change Set. A clean worktree is copied after
	// creation; the copy backend snapshots the live workspace once and then
	// seeds the writable sandbox from that exact snapshot.
	if req.PreferWorktree && isGitRepo(req.WorkspacePath) && gitWorkspaceClean(ctx, req.WorkspacePath) {
		record.Kind = "worktree"
		commit, err := gitHead(ctx, req.WorkspacePath)
		if err != nil {
			return domain.SandboxRecord{}, err
		}
		record.BaseCommit = commit
		if err := runGit(ctx, req.WorkspacePath, "worktree", "add", "--detach", target, commit); err != nil {
			record.Kind = "copy"
			record.BaseCommit = ""
			record.BaselinePath = target + "-baseline"
			if copyErr := copyFiltered(req.WorkspacePath, record.BaselinePath); copyErr != nil {
				return domain.SandboxRecord{}, fmt.Errorf("worktree failed (%v); copy failed: %w", err, copyErr)
			}
			if copyErr := copyFiltered(record.BaselinePath, target); copyErr != nil {
				_ = os.RemoveAll(record.BaselinePath)
				_ = os.RemoveAll(target)
				return domain.SandboxRecord{}, fmt.Errorf("seed copied sandbox: %w", copyErr)
			}
			return record, nil
		}
		record.BaselinePath = target + "-baseline"
		if err := copyFiltered(target, record.BaselinePath); err != nil {
			_ = runGit(ctx, req.WorkspacePath, "worktree", "remove", "--force", target)
			_ = os.RemoveAll(target)
			_ = os.RemoveAll(record.BaselinePath)
			return domain.SandboxRecord{}, fmt.Errorf("snapshot worktree baseline: %w", err)
		}
		return record, nil
	}
	record.Kind = "copy"
	record.BaselinePath = target + "-baseline"
	if err := copyFiltered(req.WorkspacePath, record.BaselinePath); err != nil {
		return domain.SandboxRecord{}, err
	}
	if err := copyFiltered(record.BaselinePath, target); err != nil {
		_ = os.RemoveAll(record.BaselinePath)
		_ = os.RemoveAll(target)
		return domain.SandboxRecord{}, fmt.Errorf("seed copied sandbox: %w", err)
	}
	return record, nil
}

func (m *Manager) Close(ctx context.Context, record domain.SandboxRecord, workspacePath string) error {
	if record.Kind == "live" {
		if strings.TrimSpace(record.BaselinePath) != "" {
			if err := m.validateManagedPath(record.BaselinePath); err != nil {
				return err
			}
			return os.RemoveAll(record.BaselinePath)
		}
		return nil
	}
	if err := m.validateManagedPath(record.Path); err != nil {
		return err
	}
	if strings.TrimSpace(record.BaselinePath) != "" {
		if err := m.validateManagedPath(record.BaselinePath); err != nil {
			return err
		}
	}
	if record.Kind == "worktree" && isGitRepo(workspacePath) {
		_ = runGit(ctx, workspacePath, "worktree", "remove", "--force", record.Path)
	}
	if err := os.RemoveAll(record.Path); err != nil {
		return err
	}
	if strings.TrimSpace(record.BaselinePath) != "" {
		return os.RemoveAll(record.BaselinePath)
	}
	return nil
}

// ManagedRoot exposes the exact deletion boundary for retention jobs. It is
// intentionally not inferred from persisted sandbox paths.
func (m *Manager) ManagedRoot() string { return filepath.Clean(m.Root) }

func (m *Manager) validateManagedPath(path string) error {
	root, err := filepath.Abs(strings.TrimSpace(m.Root))
	if err != nil || strings.TrimSpace(m.Root) == "" {
		return fmt.Errorf("sandbox managed root is unavailable")
	}
	target, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil || strings.TrimSpace(path) == "" {
		return fmt.Errorf("sandbox cleanup path is unavailable")
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("refuse sandbox cleanup outside managed root: %s", target)
	}
	return nil
}

// createLive keeps Path on the open workspace and snapshots a baseline for
// Change Set diffs and revert. SeedPath (sequential Flow) becomes the baseline
// source so the stage delta is measured from the predecessor head.
func (m *Manager) createLive(_ context.Context, req CreateRequest, id string) (domain.SandboxRecord, error) {
	workspacePath := filepath.Clean(req.WorkspacePath)
	info, err := os.Stat(workspacePath)
	if err != nil {
		return domain.SandboxRecord{}, fmt.Errorf("inspect live workspace: %w", err)
	}
	if !info.IsDir() {
		return domain.SandboxRecord{}, fmt.Errorf("live workspace must be a directory")
	}
	baseline := filepath.Join(m.Root, id+"-baseline")
	seed := strings.TrimSpace(req.SeedPath)
	if seed == "" {
		seed = workspacePath
	}
	if err := copyFiltered(seed, baseline); err != nil {
		_ = os.RemoveAll(baseline)
		return domain.SandboxRecord{}, fmt.Errorf("snapshot live baseline: %w", err)
	}
	record := domain.SandboxRecord{
		ID: id, WorkspaceID: req.WorkspaceID, ExecutionID: req.ExecutionID,
		Kind: "live", Backend: "filtered-copy", BackendVersion: "1",
		Path: workspacePath, BaselinePath: baseline,
		ParentSandboxID: req.ParentSandboxID, ParentExecutionID: req.ParentExecutionID,
		BaselineChangeSetIDs: append([]string(nil), req.BaselineChangeSetIDs...),
		CreatedAt:            time.Now().UTC(),
	}
	if strings.TrimSpace(req.ParentSandboxID) != "" {
		record.ParentSandboxIDs = []string{req.ParentSandboxID}
	}
	if strings.TrimSpace(req.ParentExecutionID) != "" {
		record.ParentExecutionIDs = []string{req.ParentExecutionID}
	}
	return record, nil
}

func (m *Manager) Diff(_ context.Context, basePath, sandboxPath string) ([]DiffEntry, error) {
	baseFiles, err := listTextFiles(basePath)
	if err != nil {
		return nil, err
	}
	sandboxFiles, err := listTextFiles(sandboxPath)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	var diffs []DiffEntry
	for path, proposed := range sandboxFiles {
		seen[path] = struct{}{}
		original, exists := baseFiles[path]
		if !exists {
			diffs = append(diffs, DiffEntry{
				Path: path, Kind: "create", ProposedHash: hashText(proposed), Proposed: proposed,
			})
			continue
		}
		if original == proposed {
			continue
		}
		diffs = append(diffs, DiffEntry{
			Path: path, Kind: "modify", OriginalHash: hashText(original), ProposedHash: hashText(proposed),
			Original: original, Proposed: proposed,
		})
	}
	for path, original := range baseFiles {
		if _, ok := seen[path]; ok {
			continue
		}
		diffs = append(diffs, DiffEntry{
			Path: path, Kind: "delete", OriginalHash: hashText(original), Original: original,
		})
	}
	sort.Slice(diffs, func(i, j int) bool { return diffs[i].Path < diffs[j].Path })
	return diffs, nil
}

func isGitRepo(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".git"))
	if err == nil && info != nil {
		// Directory for normal repos; file (gitdir:) for linked worktrees.
		return true
	}
	// Fallback when .git is unusual but Git still recognizes the work tree.
	cmd := osproc.Command("git", "-C", path, "rev-parse", "--is-inside-work-tree")
	out, err := cmd.CombinedOutput()
	return err == nil && strings.EqualFold(strings.TrimSpace(string(out)), "true")
}

func gitHead(ctx context.Context, path string) (string, error) {
	out, err := osproc.CommandContext(ctx, "git", "-C", path, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func gitWorkspaceClean(ctx context.Context, path string) bool {
	out, err := osproc.CommandContext(ctx, "git", "-C", path, "status", "--porcelain=v1", "--untracked-files=all").CombinedOutput()
	return err == nil && strings.TrimSpace(string(out)) == ""
}

func runGit(ctx context.Context, path string, args ...string) error {
	cmd := osproc.CommandContext(ctx, "git", append([]string{"-C", path}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

var skippedDirectories = map[string]struct{}{
	// vendor/ is intentionally NOT skipped: Composer PHP stages inherit the
	// bootstrap tip and must keep installed packages (autoload + libraries).
	".git": {}, "node_modules": {}, ".cache": {}, "dist": {}, "build": {},
	".venv": {}, "venv": {}, "__pycache__": {}, ".idea": {}, ".vscode": {},
}

func copyFiltered(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		base := d.Name()
		if d.IsDir() {
			if shouldSkipDirectory(base) {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if shouldSkipFile(base) {
			return nil
		}
		return copyFile(path, filepath.Join(dst, rel))
	})
}

func shouldSkipFile(name string) bool {
	lower := strings.ToLower(name)
	// Linked Git worktrees use a .git *file* instead of a directory. Copying it
	// would make an otherwise isolated child sandbox point back to the parent
	// worktree's administrative directory.
	if lower == ".git" {
		return true
	}
	if strings.HasSuffix(lower, ".exe") || strings.HasSuffix(lower, ".dll") || strings.HasSuffix(lower, ".so") {
		return true
	}
	if strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg") {
		return true
	}
	if lower == ".env" || strings.HasPrefix(lower, ".env.") || strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".key") {
		return true
	}
	return false
}

func shouldSkipDirectory(name string) bool {
	_, skip := skippedDirectories[name]
	return skip
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	mode := fs.FileMode(0o644)
	if info, statErr := in.Stat(); statErr == nil && info.Mode().IsRegular() {
		mode = info.Mode().Perm()
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

func listTextFiles(root string) (map[string]string, error) {
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if shouldSkipDirectory(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || shouldSkipFile(d.Name()) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !isMostlyText(data) {
			return nil
		}
		result[rel] = string(data)
		return nil
	})
	return result, err
}

func isMostlyText(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	sample := data
	if len(sample) > 2048 {
		sample = sample[:2048]
	}
	nul := 0
	for _, b := range sample {
		if b == 0 {
			nul++
		}
	}
	return nul == 0
}

func hashText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
