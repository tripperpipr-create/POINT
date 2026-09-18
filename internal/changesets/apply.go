package changesets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/workspace"
)

type Store interface {
	SaveChangeSet(ctx context.Context, set domain.ChangeSet) error
	GetChangeSet(ctx context.Context, id string) (domain.ChangeSet, error)
}

type Applier struct {
	Store Store
}

type BuildRequest struct {
	WorkspaceID   string
	ExecutionID   string
	QuestID       string
	Title         string
	WorkspacePath string
	BaselinePath  string
	SandboxPath   string
	DependsOn     []string
}

func (a Applier) BuildFromSandbox(ctx context.Context, req BuildRequest) (domain.ChangeSet, error) {
	manager := &sandbox.Manager{}
	basePath := req.WorkspacePath
	if strings.TrimSpace(req.BaselinePath) != "" {
		basePath = req.BaselinePath
	}
	diffs, err := manager.Diff(ctx, basePath, req.SandboxPath)
	if err != nil {
		return domain.ChangeSet{}, err
	}
	now := time.Now().UTC()
	set := domain.ChangeSet{
		ID: domain.NewID("changeset"), WorkspaceID: req.WorkspaceID, ExecutionID: req.ExecutionID,
		QuestID: req.QuestID, Title: req.Title, Kind: "execution", Status: domain.ChangeSetPending,
		DependsOn: append([]string(nil), req.DependsOn...), CreatedAt: now, UpdatedAt: now,
	}
	if set.Title == "" {
		set.Title = "Execution changes"
	}
	for _, diff := range diffs {
		set.Items = append(set.Items, domain.ChangeItem{
			ID: domain.NewID("changeitem"), Path: diff.Path, Kind: diff.Kind,
			OriginalHash: diff.OriginalHash, ProposedHash: diff.ProposedHash,
			OriginalContent: diff.Original, ProposedContent: diff.Proposed,
			Diff: unifiedDiff(diff.Path, diff.Original, diff.Proposed),
		})
	}
	if len(set.Items) == 0 {
		return set, nil
	}
	if err := a.Store.SaveChangeSet(ctx, set); err != nil {
		return domain.ChangeSet{}, err
	}
	return set, nil
}

type ApplyResult struct {
	ChangeSet domain.ChangeSet `json:"changeSet"`
	Applied   []string         `json:"applied"`
	Conflicts []string         `json:"conflicts"`
}

func (a Applier) Apply(ctx context.Context, workspacePath string, changeSetID string) (ApplyResult, error) {
	set, err := a.Store.GetChangeSet(ctx, changeSetID)
	if err != nil {
		return ApplyResult{}, err
	}
	if set.Status == domain.ChangeSetApplied {
		return ApplyResult{ChangeSet: set}, nil
	}
	if set.Status == domain.ChangeSetRejected || set.Status == domain.ChangeSetReverted || set.Status == domain.ChangeSetSuperseded {
		return ApplyResult{}, fmt.Errorf("change set cannot be applied from status %s", set.Status)
	}
	resolutionByPath := map[string]domain.ConflictResolution{}
	for _, resolution := range set.Resolutions {
		resolutionByPath[resolution.Path] = resolution
	}
	var conflicts []string
	conflictSeen := map[string]bool{}
	markConflict := func(path string) {
		if conflictSeen[path] {
			return
		}
		conflictSeen[path] = true
		conflicts = append(conflicts, path)
		if _, exists := resolutionByPath[path]; exists {
			return
		}
		resolution := domain.ConflictResolution{Strategy: "unresolved", Path: path}
		set.Resolutions = append(set.Resolutions, resolution)
		resolutionByPath[path] = resolution
	}
	operations := make([]fileOperation, 0, len(set.Items))
	for index, item := range set.Items {
		target, targetErr := safeTarget(workspacePath, item.Path)
		if targetErr != nil {
			return ApplyResult{}, targetErr
		}
		before, existed, mode, readErr := readFileState(target)
		if readErr != nil {
			return ApplyResult{}, readErr
		}
		currentHash := ""
		if existed {
			currentHash = hashBytes(before)
		}
		resolution, hasResolution := resolutionByPath[item.Path]
		drifted := false
		switch item.Kind {
		case "create":
			drifted = existed && currentHash != item.ProposedHash
			if existed && !drifted {
				operations = append(operations, fileOperation{index: index, path: item.Path, target: target, kind: "kept", before: before, existed: true, mode: mode})
				continue
			}
		case "modify":
			if existed && item.ProposedHash != "" && currentHash == item.ProposedHash {
				operations = append(operations, fileOperation{index: index, path: item.Path, target: target, kind: "kept", before: before, existed: true, mode: mode})
				continue
			}
			drifted = !existed || item.OriginalHash == "" || currentHash != item.OriginalHash
		case "delete":
			if !existed {
				operations = append(operations, fileOperation{index: index, path: item.Path, target: target, kind: "kept", existed: false})
				continue
			}
			drifted = item.OriginalHash == "" || currentHash != item.OriginalHash
		default:
			return ApplyResult{}, fmt.Errorf("unsupported change kind %q for %s", item.Kind, item.Path)
		}
		if drifted {
			if !hasResolution {
				markConflict(item.Path)
				continue
			}
			switch resolution.Strategy {
			case "keep_ours":
				operations = append(operations, fileOperation{index: index, path: item.Path, target: target, kind: "kept", before: before, existed: existed, mode: mode})
				continue
			case "keep_theirs", "manual":
			default:
				markConflict(item.Path)
				continue
			}
		}
		if hasResolution && resolution.Strategy == "manual" {
			if resolution.Content == nil {
				markConflict(item.Path)
				continue
			}
			operations = append(operations, fileOperation{index: index, path: item.Path, target: target, kind: "write", content: []byte(*resolution.Content), before: before, existed: existed, mode: mode})
			continue
		}
		if item.Kind == "delete" {
			operations = append(operations, fileOperation{index: index, path: item.Path, target: target, kind: "delete", before: before, existed: existed, mode: mode})
			continue
		}
		content, contentErr := exactProposed(item)
		if contentErr != nil {
			return ApplyResult{}, contentErr
		}
		operations = append(operations, fileOperation{index: index, path: item.Path, target: target, kind: "write", content: []byte(content), before: before, existed: existed, mode: mode})
	}
	now := time.Now().UTC()
	set.UpdatedAt = now
	if len(conflicts) > 0 {
		set.Status = domain.ChangeSetConflict
		if err := a.Store.SaveChangeSet(ctx, set); err != nil {
			return ApplyResult{}, err
		}
		return ApplyResult{ChangeSet: set, Conflicts: conflicts}, nil
	}
	applied := make([]string, 0, len(operations))
	completed := make([]fileOperation, 0, len(operations))
	for _, operation := range operations {
		if err := executeFileOperation(operation); err != nil {
			rollbackFileOperations(completed)
			return ApplyResult{}, fmt.Errorf("apply %s: %w", operation.path, err)
		}
		completed = append(completed, operation)
		applied = append(applied, operation.path)
		set.Items[operation.index].AppliedOperation = operation.kind
		if operation.kind == "write" {
			set.Items[operation.index].AppliedHash = hashBytes(operation.content)
		} else if operation.kind == "kept" && operation.existed {
			set.Items[operation.index].AppliedHash = hashBytes(operation.before)
		} else {
			set.Items[operation.index].AppliedHash = ""
		}
	}
	set.Status = domain.ChangeSetApplied
	set.AppliedAt = &now
	if err := a.Store.SaveChangeSet(ctx, set); err != nil {
		rollbackFileOperations(completed)
		return ApplyResult{}, err
	}
	return ApplyResult{ChangeSet: set, Applied: applied}, nil
}

type fileOperation struct {
	index   int
	path    string
	target  string
	kind    string // write | delete | kept
	content []byte
	before  []byte
	existed bool
	mode    os.FileMode
}

func safeTarget(workspacePath, path string) (string, error) {
	fs, err := workspace.Open(workspacePath)
	if err != nil {
		return "", err
	}
	resolved, err := fs.Resolve(path, true)
	if err != nil {
		return "", fmt.Errorf("change path escapes workspace: %q", path)
	}
	return resolved, nil
}

func readFileState(path string) ([]byte, bool, os.FileMode, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, false, 0o644, nil
	}
	if err != nil {
		return nil, false, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, 0, fmt.Errorf("change target is not a regular file: %s", path)
	}
	data, err := os.ReadFile(path)
	return data, true, info.Mode().Perm(), err
}

func exactProposed(item domain.ChangeItem) (string, error) {
	if item.ProposedHash == "" {
		return "", fmt.Errorf("change item %s has no proposed content hash", item.Path)
	}
	if hashBytes([]byte(item.ProposedContent)) == item.ProposedHash {
		return item.ProposedContent, nil
	}
	// Compatibility for change sets created before exact snapshots existed.
	legacy := extractProposed(item.Diff)
	if hashBytes([]byte(legacy)) == item.ProposedHash {
		return legacy, nil
	}
	return "", fmt.Errorf("proposed snapshot hash mismatch for %s", item.Path)
}

func executeFileOperation(operation fileOperation) error {
	switch operation.kind {
	case "kept":
		return nil
	case "delete":
		if !operation.existed {
			return nil
		}
		return os.Remove(operation.target)
	case "write":
		if err := os.MkdirAll(filepath.Dir(operation.target), 0o755); err != nil {
			return err
		}
		mode := operation.mode
		if mode == 0 {
			mode = 0o644
		}
		return writeFileAtomically(operation.target, operation.content, mode)
	default:
		return fmt.Errorf("unsupported file operation %q", operation.kind)
	}
}

// writeFileAtomically пишет во временный файл рядом и переименовывает поверх.
//
// Так же пишут два других места, меняющих файлы проекта (workspace.Write и
// tools/patch.go), а применение набора писало на месте — и это отличие было не
// стилистическим. Проверка пути бессильна против жёсткой ссылки: путь честно
// лежит внутри проекта, а данные — снаружи, потому что это один файл на диске.
// Запись на месте шла по ссылке и переписывала внешний файл; переименование
// подменяет запись в каталоге и ссылку разрывает.
//
// Второе следствие — атомарность: прерванная запись больше не оставляет файл
// пользователя наполовину переписанным.
func writeFileAtomically(target string, content []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(target), ".workbench-apply-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if _, err = tmp.Write(content); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err = tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err = tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err = os.Rename(name, target); err != nil {
		cleanup()
		return err
	}
	return nil
}

func rollbackFileOperations(operations []fileOperation) {
	for index := len(operations) - 1; index >= 0; index-- {
		operation := operations[index]
		if operation.kind == "kept" {
			continue
		}
		if operation.existed {
			_ = os.MkdirAll(filepath.Dir(operation.target), 0o755)
			mode := operation.mode
			if mode == 0 {
				mode = 0o644
			}
			// Тем же способом, что и применение: правило «здесь не пишут на
			// месте» без исключений, иначе третий писатель однажды заведётся
			// снова. Прерванный откат тоже не оставит файл наполовину.
			_ = writeFileAtomically(operation.target, operation.before, mode)
		} else {
			_ = os.Remove(operation.target)
		}
	}
}

func (a Applier) Reject(ctx context.Context, changeSetID string) (domain.ChangeSet, error) {
	set, err := a.Store.GetChangeSet(ctx, changeSetID)
	if err != nil {
		return domain.ChangeSet{}, err
	}
	if set.Status == domain.ChangeSetApplied || set.Status == domain.ChangeSetReverted || set.Status == domain.ChangeSetSuperseded {
		return domain.ChangeSet{}, fmt.Errorf("change set cannot be rejected from status %s", set.Status)
	}
	set.Status = domain.ChangeSetRejected
	set.UpdatedAt = time.Now().UTC()
	if err := a.Store.SaveChangeSet(ctx, set); err != nil {
		return domain.ChangeSet{}, err
	}
	return set, nil
}

// Revert restores the exact pre-apply snapshots after first verifying that no
// applied file has been changed by the user or another execution.
func (a Applier) Revert(ctx context.Context, workspacePath, changeSetID string) (ApplyResult, error) {
	set, err := a.Store.GetChangeSet(ctx, changeSetID)
	if err != nil {
		return ApplyResult{}, err
	}
	if set.Status == domain.ChangeSetReverted {
		return ApplyResult{ChangeSet: set}, nil
	}
	if set.Status != domain.ChangeSetApplied {
		return ApplyResult{}, fmt.Errorf("only an applied change set can be reverted; current status is %s", set.Status)
	}
	operations := make([]fileOperation, 0, len(set.Items))
	for index, item := range set.Items {
		if item.AppliedOperation == "kept" {
			continue
		}
		target, targetErr := safeTarget(workspacePath, item.Path)
		if targetErr != nil {
			return ApplyResult{}, targetErr
		}
		current, existed, mode, readErr := readFileState(target)
		if readErr != nil {
			return ApplyResult{}, readErr
		}
		switch item.AppliedOperation {
		case "write":
			if !existed || item.AppliedHash == "" || hashBytes(current) != item.AppliedHash {
				return ApplyResult{}, fmt.Errorf("cannot revert %s: file changed after change set apply", item.Path)
			}
		case "delete":
			if existed {
				return ApplyResult{}, fmt.Errorf("cannot revert %s: path was recreated after change set apply", item.Path)
			}
		default:
			return ApplyResult{}, fmt.Errorf("cannot revert %s: missing applied operation metadata", item.Path)
		}
		if item.Kind == "create" {
			operations = append(operations, fileOperation{index: index, path: item.Path, target: target, kind: "delete", before: current, existed: existed, mode: mode})
			continue
		}
		if item.OriginalHash == "" || hashBytes([]byte(item.OriginalContent)) != item.OriginalHash {
			return ApplyResult{}, fmt.Errorf("original snapshot hash mismatch for %s", item.Path)
		}
		operations = append(operations, fileOperation{index: index, path: item.Path, target: target, kind: "write", content: []byte(item.OriginalContent), before: current, existed: existed, mode: mode})
	}
	completed := make([]fileOperation, 0, len(operations))
	reverted := make([]string, 0, len(operations))
	for _, operation := range operations {
		if err := executeFileOperation(operation); err != nil {
			rollbackFileOperations(completed)
			return ApplyResult{}, fmt.Errorf("revert %s: %w", operation.path, err)
		}
		completed = append(completed, operation)
		reverted = append(reverted, operation.path)
	}
	set.Status = domain.ChangeSetReverted
	set.UpdatedAt = time.Now().UTC()
	if err := a.Store.SaveChangeSet(ctx, set); err != nil {
		rollbackFileOperations(completed)
		return ApplyResult{}, err
	}
	return ApplyResult{ChangeSet: set, Applied: reverted}, nil
}

type ResolveRequest struct {
	Strategy string  `json:"strategy"`
	Path     string  `json:"path"`
	Content  *string `json:"content,omitempty"`
}

func (a Applier) ResolveConflict(ctx context.Context, changeSetID string, req ResolveRequest) (domain.ChangeSet, error) {
	strategy := strings.TrimSpace(req.Strategy)
	path := strings.TrimSpace(req.Path)
	switch strategy {
	case "keep_ours", "keep_theirs", "manual":
	default:
		return domain.ChangeSet{}, fmt.Errorf("unsupported resolution strategy %q", strategy)
	}
	if path == "" {
		return domain.ChangeSet{}, fmt.Errorf("path is required")
	}
	if strategy == "manual" && req.Content == nil {
		return domain.ChangeSet{}, fmt.Errorf("manual resolution content is required")
	}
	set, err := a.Store.GetChangeSet(ctx, changeSetID)
	if err != nil {
		return domain.ChangeSet{}, err
	}
	if set.Status != domain.ChangeSetConflict {
		return domain.ChangeSet{}, fmt.Errorf("change set is not in conflict status")
	}
	for index, existing := range set.Resolutions {
		if existing.Path == path {
			set.Resolutions[index] = domain.ConflictResolution{Strategy: strategy, Path: path, Content: req.Content}
			set.UpdatedAt = time.Now().UTC()
			if err := a.Store.SaveChangeSet(ctx, set); err != nil {
				return domain.ChangeSet{}, err
			}
			return set, nil
		}
	}
	set.Resolutions = append(set.Resolutions, domain.ConflictResolution{Strategy: strategy, Path: path, Content: req.Content})
	set.UpdatedAt = time.Now().UTC()
	if err := a.Store.SaveChangeSet(ctx, set); err != nil {
		return domain.ChangeSet{}, err
	}
	return set, nil
}

func (a Applier) ApplyWithContent(ctx context.Context, workspacePath string, set domain.ChangeSet, contents map[string]string) (ApplyResult, error) {
	var applied, conflicts []string
	for i, item := range set.Items {
		// Через safeTarget, как и в Apply: путь приходит из набора изменений, то
		// есть от агента. Прямая склейка с корнем проекта не отвергала ни «..»,
		// ни абсолютный путь, ни ссылку наружу — запись и удаление уходили за
		// пределы рабочей копии. Отказ здесь такой же, как в Apply: применение
		// целиком не состоится, а не «частично мимо проекта».
		target, targetErr := safeTarget(workspacePath, item.Path)
		if targetErr != nil {
			return ApplyResult{}, targetErr
		}
		content, ok := contents[item.Path]
		if item.Kind == "delete" {
			if fileExists(target) {
				current, readErr := os.ReadFile(target)
				if readErr == nil && item.OriginalHash != "" && hashBytes(current) != item.OriginalHash {
					conflicts = append(conflicts, item.Path)
					continue
				}
				if err := os.Remove(target); err != nil {
					return ApplyResult{}, err
				}
			}
			applied = append(applied, item.Path)
			continue
		}
		if !ok {
			content = extractProposed(item.Diff)
		}
		if fileExists(target) && item.OriginalHash != "" {
			current, readErr := os.ReadFile(target)
			if readErr == nil && hashBytes(current) != item.OriginalHash {
				conflicts = append(conflicts, item.Path)
				continue
			}
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return ApplyResult{}, err
		}
		if err := writeFileAtomically(target, []byte(content), 0o644); err != nil {
			return ApplyResult{}, err
		}
		set.Items[i].ProposedHash = hashBytes([]byte(content))
		applied = append(applied, item.Path)
	}
	now := time.Now().UTC()
	set.UpdatedAt = now
	if len(conflicts) > 0 {
		set.Status = domain.ChangeSetConflict
	} else {
		set.Status = domain.ChangeSetApplied
		set.AppliedAt = &now
	}
	if err := a.Store.SaveChangeSet(ctx, set); err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{ChangeSet: set, Applied: applied, Conflicts: conflicts}, nil
}

func BuildWithContents(req BuildRequest, diffs []sandbox.DiffEntry) domain.ChangeSet {
	now := time.Now().UTC()
	set := domain.ChangeSet{
		ID: domain.NewID("changeset"), WorkspaceID: req.WorkspaceID, ExecutionID: req.ExecutionID,
		QuestID: req.QuestID, Title: req.Title, Kind: "execution", Status: domain.ChangeSetPending,
		DependsOn: append([]string(nil), req.DependsOn...), CreatedAt: now, UpdatedAt: now,
	}
	if set.Title == "" {
		set.Title = "Execution changes"
	}
	for _, diff := range diffs {
		set.Items = append(set.Items, domain.ChangeItem{
			ID: domain.NewID("changeitem"), Path: diff.Path, Kind: diff.Kind,
			OriginalHash: diff.OriginalHash, ProposedHash: diff.ProposedHash,
			OriginalContent: diff.Original, ProposedContent: diff.Proposed,
			Diff: unifiedDiff(diff.Path, diff.Original, diff.Proposed),
		})
	}
	return set
}

func unifiedDiff(path, original, proposed string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", path, path)
	origLines := strings.Split(original, "\n")
	propLines := strings.Split(proposed, "\n")
	if original == "" && proposed != "" {
		for _, line := range propLines {
			fmt.Fprintf(&b, "+%s\n", line)
		}
		return b.String()
	}
	if proposed == "" && original != "" {
		for _, line := range origLines {
			fmt.Fprintf(&b, "-%s\n", line)
		}
		return b.String()
	}
	max := len(origLines)
	if len(propLines) > max {
		max = len(propLines)
	}
	for i := 0; i < max; i++ {
		var o, p string
		if i < len(origLines) {
			o = origLines[i]
		}
		if i < len(propLines) {
			p = propLines[i]
		}
		if o == p {
			fmt.Fprintf(&b, " %s\n", o)
			continue
		}
		if o != "" || i < len(origLines) {
			fmt.Fprintf(&b, "-%s\n", o)
		}
		if p != "" || i < len(propLines) {
			fmt.Fprintf(&b, "+%s\n", p)
		}
	}
	return b.String()
}

func extractProposed(diff string) string {
	var lines []string
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			continue
		}
		if strings.HasPrefix(line, "+") {
			lines = append(lines, strings.TrimPrefix(line, "+"))
			continue
		}
		if strings.HasPrefix(line, "-") {
			continue
		}
		if strings.HasPrefix(line, " ") {
			lines = append(lines, strings.TrimPrefix(line, " "))
		}
	}
	return strings.Join(lines, "\n")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func hashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
