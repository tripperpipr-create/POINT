package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
	"local-agent-workbench/internal/workspace"
)

const maxInspectablePatchBytes = 512 * 1024

type fileObservation struct {
	Key             string
	Revision        int
	AvailableAtStep int
	SHA256          string
	Complete        bool
	Fragment        string
}

type workspaceObservation struct {
	Revision        int
	AvailableAtStep int
}

type observationTracker struct {
	files     map[string][]fileObservation
	workspace map[string]workspaceObservation
}

type patchInspectionState struct {
	Known           bool
	Path            string
	OriginalExisted bool
	SHA256          string
}

type patchInspectionRequirement struct {
	Code              string `json:"code"`
	Path              string `json:"path"`
	RequiredTool      string `json:"requiredTool"`
	WorkspaceRevision int    `json:"workspaceRevision"`
	Message           string `json:"message"`
}

func newObservationTracker(contextItems []domain.RunContextItem) *observationTracker {
	tracker := &observationTracker{files: make(map[string][]fileObservation), workspace: make(map[string]workspaceObservation)}
	for _, item := range contextItems {
		path := normalizeObservationPath(item.Path)
		if path == "" || item.Truncated || item.Content == "" || item.Digest == "" {
			continue
		}
		digest := sha256.Sum256([]byte(item.Content))
		if "sha256:"+hex.EncodeToString(digest[:]) != strings.ToLower(item.Digest) {
			continue
		}
		key := "context:" + item.ID
		tracker.files[path] = append(tracker.files[path], fileObservation{Key: key, Revision: 0, AvailableAtStep: 1, SHA256: hex.EncodeToString(digest[:]), Complete: true})
	}
	return tracker
}

func (t *observationTracker) Observe(call providers.ToolCall, result domain.ToolResult, revision, step int, executionKey string) {
	if t == nil || !toolCallCompletedSuccessfully(result) || executionKey == "" {
		return
	}
	switch call.Name {
	case "read_file":
		var output struct {
			Path      string `json:"path"`
			SHA256    string `json:"sha256"`
			Truncated bool   `json:"truncated"`
		}
		if json.Unmarshal(result.Output, &output) != nil || output.Truncated || !validSHA256(output.SHA256) {
			return
		}
		path := normalizeObservationPath(output.Path)
		if path == "" {
			return
		}
		t.files[path] = append(t.files[path], fileObservation{Key: executionKey, Revision: revision, AvailableAtStep: step + 1, SHA256: strings.ToLower(output.SHA256), Complete: true})
	case "search_code":
		var output struct {
			Chunks []struct {
				Path       string `json:"path"`
				Content    string `json:"content"`
				FileSHA256 string `json:"fileSha256"`
			} `json:"chunks"`
		}
		if json.Unmarshal(result.Output, &output) != nil {
			return
		}
		for _, chunk := range output.Chunks {
			path := normalizeObservationPath(chunk.Path)
			if path == "" || chunk.Content == "" || !validSHA256(chunk.FileSHA256) {
				continue
			}
			t.files[path] = append(t.files[path], fileObservation{
				Key: executionKey, Revision: revision, AvailableAtStep: step + 1,
				SHA256: strings.ToLower(chunk.FileSHA256), Fragment: chunk.Content,
			})
		}
	case "list_files":
		t.workspace[executionKey] = workspaceObservation{Revision: revision, AvailableAtStep: step + 1}
	}
}

func (t *observationTracker) Release(executionKey string) {
	if t == nil || executionKey == "" {
		return
	}
	delete(t.workspace, executionKey)
	for path, items := range t.files {
		filtered := items[:0]
		for _, item := range items {
			if item.Key != executionKey {
				filtered = append(filtered, item)
			}
		}
		if len(filtered) == 0 {
			delete(t.files, path)
		} else {
			t.files[path] = filtered
		}
	}
}

func (t *observationTracker) ReleasePatchTarget(raw json.RawMessage) []string {
	if t == nil {
		return nil
	}
	var input struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(raw, &input) != nil {
		return nil
	}
	path := normalizeObservationPath(input.Path)
	items := t.files[path]
	if len(items) == 0 {
		return nil
	}
	delete(t.files, path)
	keys := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.Key == "" {
			continue
		}
		if _, exists := seen[item.Key]; exists {
			continue
		}
		seen[item.Key] = struct{}{}
		keys = append(keys, item.Key)
	}
	return keys
}

func (t *observationTracker) CheckPatch(fs *workspace.FS, raw json.RawMessage, revision, step int) (patchInspectionState, *patchInspectionRequirement) {
	var input struct {
		Path    string  `json:"path"`
		Content *string `json:"content"`
		Edits   []struct {
			OldText string `json:"oldText"`
		} `json:"edits"`
	}
	if json.Unmarshal(raw, &input) != nil || strings.TrimSpace(input.Path) == "" || workspace.IsSensitive(input.Path) {
		return patchInspectionState{}, nil
	}
	path := normalizeObservationPath(input.Path)
	abs, err := fs.Resolve(input.Path, true)
	if err != nil {
		return patchInspectionState{}, nil
	}
	info, err := os.Stat(abs)
	if os.IsNotExist(err) {
		parentMissing := false
		if parent := filepath.Dir(abs); parent != "" && parent != abs {
			if _, parentErr := os.Stat(parent); os.IsNotExist(parentErr) {
				parentMissing = true
			}
		}
		// Greenfield trees (no src/ yet): any prior successful read/list is enough
		// to create the first file under a missing parent directory.
		if t.hasCurrentWorkspaceView(revision, step) || t.hasCurrentNeighbor(path, revision, step) || (parentMissing && t.hasAnyFileObservation(revision, step)) {
			return patchInspectionState{Known: true, Path: path, OriginalExisted: false}, nil
		}
		return patchInspectionState{}, &patchInspectionRequirement{
			Code: "inspection_required", Path: path, RequiredTool: "list_files", WorkspaceRevision: revision,
			Message: "inspect the current workspace with list_files (or read a neighboring file) in a previous model turn before creating this file",
		}
	}
	if err != nil || info.IsDir() || info.Size() > maxInspectablePatchBytes {
		return patchInspectionState{}, nil
	}
	current, err := fs.Read(input.Path, false)
	if err != nil || current.Truncated || !validSHA256(current.SHA256) {
		return patchInspectionState{}, nil
	}
	state := patchInspectionState{Known: true, Path: path, OriginalExisted: true, SHA256: strings.ToLower(current.SHA256)}
	items := t.files[path]
	isExactEdit := input.Content == nil && len(input.Edits) > 0
	hasAvailable := false
	hasPending := false
	currentItems := make([]fileObservation, 0, len(items))
	for _, item := range items {
		if item.AvailableAtStep > step {
			hasPending = true
			continue
		}
		hasAvailable = true
		if strings.EqualFold(item.SHA256, current.SHA256) {
			currentItems = append(currentItems, item)
		}
	}
	for _, item := range currentItems {
		if item.Complete {
			return state, nil
		}
	}
	if isExactEdit && exactEditAnchorsCovered(input.Edits, currentItems) {
		return state, nil
	}
	requiredTool := "read_file"
	if isExactEdit {
		requiredTool = "search_code"
	}
	if len(currentItems) > 0 {
		return patchInspectionState{}, &patchInspectionRequirement{
			Code: "inspection_scope_required", Path: path, RequiredTool: requiredTool, WorkspaceRevision: revision,
			Message: "the visible indexed fragments do not contain every exact oldText anchor; search for the missing code fragment or read the complete file before proposing the patch",
		}
	}
	if hasPending && !hasAvailable {
		return patchInspectionState{}, &patchInspectionRequirement{
			Code: "inspection_required", Path: path, RequiredTool: requiredTool, WorkspaceRevision: revision,
			Message: "the inspection result is not visible to the model until the next turn; wait for that result before proposing the patch",
		}
	}
	if hasAvailable {
		return patchInspectionState{}, &patchInspectionRequirement{
			Code: "inspection_stale", Path: path, RequiredTool: requiredTool, WorkspaceRevision: revision,
			Message: "the target file changed after the model inspected it; refresh the exact indexed fragment or read the complete current file before proposing a patch",
		}
	}
	return patchInspectionState{}, &patchInspectionRequirement{
		Code: "inspection_required", Path: path, RequiredTool: requiredTool, WorkspaceRevision: revision,
		Message: "inspect the exact target code with search_code or read the complete current file in a previous model turn before proposing the patch",
	}
}

func exactEditAnchorsCovered(edits []struct {
	OldText string `json:"oldText"`
}, observations []fileObservation) bool {
	if len(edits) == 0 {
		return false
	}
	for _, edit := range edits {
		if edit.OldText == "" {
			return false
		}
		covered := false
		for _, observation := range observations {
			if observation.Fragment != "" && strings.Contains(observation.Fragment, edit.OldText) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

func (t *observationTracker) hasCurrentWorkspaceView(revision, step int) bool {
	for _, item := range t.workspace {
		// A list_files from an earlier revision still proves the agent inspected
		// the workspace. Requiring an exact revision match forced a fresh list
		// after every run_command mkdir/write and burned the step budget on
		// inspection_required loops while creating greenfield files.
		if item.Revision <= revision && item.AvailableAtStep <= step {
			return true
		}
	}
	return false
}

func (t *observationTracker) hasCurrentNeighbor(path string, revision, step int) bool {
	parent := filepath.Dir(filepath.FromSlash(path))
	for observedPath, items := range t.files {
		if filepath.Dir(filepath.FromSlash(observedPath)) != parent {
			continue
		}
		for _, item := range items {
			if item.Revision <= revision && item.AvailableAtStep <= step {
				return true
			}
		}
	}
	return false
}

func (t *observationTracker) hasAnyFileObservation(revision, step int) bool {
	for _, items := range t.files {
		for _, item := range items {
			if item.Revision <= revision && item.AvailableAtStep <= step {
				return true
			}
		}
	}
	return false
}

func normalizeObservationPath(value string) string {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(value))))
	if clean == "." || clean == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		clean = strings.ToLower(clean)
	}
	return clean
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
