package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pmezard/go-difflib/difflib"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/workspace"
)

var ErrPatchConflict = errors.New("file changed after the patch was proposed")

const (
	maxPatchContentBytes  = 512 * 1024
	maxPatchEdits         = 64
	maxPatchArgumentBytes = 1024 * 1024
)

type patchTextEdit struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

type patchInput struct {
	Path    string     `json:"path"`
	Content *string    `json:"content,omitempty"`
	Edits   patchEdits `json:"edits,omitempty"`
	Reason  string     `json:"reason"`
}

// patchEdits принимает список правок и массивом, и строкой, внутри которой
// лежит тот же массив.
//
// Схема объявляет массив, и формально строка — нарушение. Но на живом прогоне
// Qwen3.6 трижды подряд прислала edits строкой, каждый раз получала отказ
// разбора и потратила на подбор формата три шага из двадцати пяти, ничего за
// них не сделав. Вложенный JSON строкой — известная манера моделей среднего
// размера, и отвергать её значит платить за чужую привычку шагами человека.
//
// Снисходительность строго ограничена формой: содержимое разбирается тем же
// типом и проходит все те же проверки — якоря, пределы, число правок. Принять
// строку и принять что попало — разные вещи.
type patchEdits []patchTextEdit

func (e *patchEdits) UnmarshalJSON(raw []byte) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var inner string
		if err := json.Unmarshal(trimmed, &inner); err != nil {
			return err
		}
		trimmed = bytes.TrimSpace([]byte(inner))
	}
	var items []patchTextEdit
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return err
	}
	*e = items
	return nil
}

type resolvedTextEdit struct {
	Start   int
	End     int
	NewText string
}

type PatchManager struct {
	FS        *workspace.FS
	mu        sync.RWMutex
	proposals map[string]*domain.PatchProposal
}

func NewPatchManager(fs *workspace.FS) *PatchManager {
	return &PatchManager{FS: fs, proposals: make(map[string]*domain.PatchProposal)}
}

func (m *PatchManager) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{Name: "propose_patch", Description: "Propose a reviewed UTF-8 file change. For a small change to an existing file, prefer edits with exact unique oldText anchors and replacement newText. For a new file or coherent rewrite, provide the complete content. Use exactly one mode. The user must accept the resulting diff before it is written.", InputSchema: schema(`{"type":"object","properties":{"path":{"type":"string","minLength":1},"content":{"type":"string","maxLength":524288},"edits":{"type":"array","minItems":1,"maxItems":64,"items":{"type":"object","properties":{"oldText":{"type":"string","minLength":1,"maxLength":524288},"newText":{"type":"string","maxLength":524288}},"required":["oldText","newText"],"additionalProperties":false}},"reason":{"type":"string","minLength":1}},"required":["path","reason"],"oneOf":[{"required":["content"]},{"required":["edits"]}],"additionalProperties":false}`)}
}

func (m *PatchManager) ValidateArguments(raw json.RawMessage) *domain.ToolResult {
	_, invalid := decodePatchInput(raw)
	return invalid
}

func (m *PatchManager) Execute(_ context.Context, raw json.RawMessage) domain.ToolResult {
	input, invalid := decodePatchInput(raw)
	if invalid != nil {
		return *invalid
	}
	if workspace.IsSensitive(input.Path) {
		return Fail("sensitive_path", workspace.ErrSensitive.Error())
	}
	abs, err := m.FS.Resolve(input.Path, true)
	if err != nil {
		return Fail("invalid_path", err.Error())
	}
	// Короткое имя (`ENV~1`) проходит проверку по присланному имени, а
	// разрешается в настоящий `.env`: секретность решает разрешённый путь.
	if workspace.IsSensitive(abs) {
		return Fail("sensitive_path", workspace.ErrSensitive.Error())
	}
	original := ""
	originalExisted := false
	if info, statErr := os.Stat(abs); statErr == nil {
		originalExisted = true
		if info.IsDir() {
			return Fail("invalid_path", "patch target is a directory")
		}
		if info.Size() > maxPatchContentBytes {
			return Fail("content_too_large", "existing patch target exceeds 512 KiB")
		}
		data, readErr := os.ReadFile(abs)
		if readErr != nil {
			return Fail("read_failed", readErr.Error())
		}
		if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
			return Fail("binary_file", workspace.ErrBinary.Error())
		}
		original = string(data)
	} else if !os.IsNotExist(statErr) {
		return Fail("read_failed", statErr.Error())
	}
	proposed := ""
	if input.Content != nil {
		proposed = *input.Content
	} else {
		if !originalExisted {
			return FailWithHint("edit_target_missing", "exact edits require an existing file; provide complete content when creating a file", "inspect with list_files or a neighboring read_file first, then create the file with the content field instead of edits")
		}
		resolved := make([]resolvedTextEdit, 0, len(input.Edits))
		for index, edit := range input.Edits {
			occurrences := strings.Count(original, edit.OldText)
			switch occurrences {
			case 0:
				return FailWithHint("edit_anchor_missing", fmt.Sprintf("edit %d oldText does not occur in the current file; read the file again and use an exact current fragment", index+1), "call read_file or search_code on this path in a new turn, copy an exact current substring into oldText, then propose the patch again")
			case 1:
				start := strings.Index(original, edit.OldText)
				resolved = append(resolved, resolvedTextEdit{Start: start, End: start + len(edit.OldText), NewText: edit.NewText})
			default:
				return FailWithHint("edit_anchor_ambiguous", fmt.Sprintf("edit %d oldText occurs %d times; include more surrounding text so the anchor is unique", index+1, occurrences), "expand oldText with neighboring lines until it matches exactly once, or switch to a complete-content rewrite after a full read_file")
			}
		}
		sort.Slice(resolved, func(i, j int) bool { return resolved[i].Start < resolved[j].Start })
		for index := 1; index < len(resolved); index++ {
			if resolved[index].Start < resolved[index-1].End {
				return Fail("overlapping_edits", "exact edit anchors must not overlap; combine the overlapping changes into one unique replacement")
			}
		}
		var builder strings.Builder
		estimatedSize := len(original)
		for _, edit := range resolved {
			estimatedSize += len(edit.NewText) - (edit.End - edit.Start)
		}
		if estimatedSize > maxPatchContentBytes {
			return Fail("content_too_large", "edited patch content exceeds 512 KiB")
		}
		builder.Grow(estimatedSize)
		cursor := 0
		for _, edit := range resolved {
			builder.WriteString(original[cursor:edit.Start])
			builder.WriteString(edit.NewText)
			cursor = edit.End
		}
		builder.WriteString(original[cursor:])
		proposed = builder.String()
		if proposed == original {
			return Fail("no_changes", "the proposed exact edits do not change the file")
		}
		proposal := m.storeProposal(input.Path, original, proposed, originalExisted, unifiedDiffFromEdits(input.Path, original, proposed, resolved))
		return OK(proposal)
	}
	if proposed == original && originalExisted {
		return Fail("no_changes", "the proposed patch does not change the file")
	}
	diff := unifiedDiff(input.Path, original, proposed)
	if !originalExisted {
		diff = unifiedCreationDiff(input.Path, proposed)
	}
	proposal := m.storeProposal(input.Path, original, proposed, originalExisted, diff)
	return OK(proposal)
}

func (m *PatchManager) storeProposal(path, original, proposed string, originalExisted bool, diff string) *domain.PatchProposal {
	proposal := &domain.PatchProposal{ID: domain.NewID("patch"), SourceTool: "propose_patch", Path: filepath.ToSlash(path), OriginalHash: hash(original), OriginalExisted: originalExisted, Original: original, Proposed: proposed, Diff: diff, Status: "pending", CreatedAt: time.Now().UTC()}
	m.mu.Lock()
	m.proposals[proposal.ID] = proposal
	m.mu.Unlock()
	return proposal
}

func decodePatchInput(raw json.RawMessage) (patchInput, *domain.ToolResult) {
	if len(raw) > maxPatchArgumentBytes {
		result := Fail("arguments_too_large", "patch arguments exceed 1 MiB")
		return patchInput{}, &result
	}
	var input patchInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		result := FailWithHint("invalid_input", fmt.Sprintf("invalid patch input: %v", err), "pass path, reason, and exactly one of content or edits; extra fields are rejected")
		return patchInput{}, &result
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		result := Fail("invalid_input", "patch input must contain exactly one JSON object")
		return patchInput{}, &result
	}
	if strings.TrimSpace(input.Path) == "" || strings.TrimSpace(input.Reason) == "" {
		result := FailWithHint("invalid_input", "path and reason are required", "use a workspace-relative path and a short reason grounded in the current task")
		return patchInput{}, &result
	}
	usesContent := input.Content != nil
	usesEdits := input.Edits != nil
	if usesContent == usesEdits {
		result := FailWithHint("invalid_input", "provide exactly one patch mode: content or edits", "for a small change send edits with unique oldText anchors; for a new file or rewrite send content")
		return patchInput{}, &result
	}
	if usesContent {
		if len(*input.Content) > maxPatchContentBytes {
			result := Fail("content_too_large", "patch content exceeds 512 KiB")
			return patchInput{}, &result
		}
		return input, nil
	}
	if len(input.Edits) == 0 {
		result := Fail("invalid_input", "edits must contain at least one exact replacement")
		return patchInput{}, &result
	}
	if len(input.Edits) > maxPatchEdits {
		result := Fail("too_many_edits", "a patch may contain at most 64 exact edits")
		return patchInput{}, &result
	}
	totalBytes := 0
	for index, edit := range input.Edits {
		if edit.OldText == "" {
			result := Fail("invalid_input", fmt.Sprintf("edit %d oldText must not be empty", index+1))
			return patchInput{}, &result
		}
		if len(edit.OldText) > maxPatchContentBytes || len(edit.NewText) > maxPatchContentBytes {
			result := Fail("content_too_large", fmt.Sprintf("edit %d exceeds 512 KiB", index+1))
			return patchInput{}, &result
		}
		totalBytes += len(edit.OldText) + len(edit.NewText)
		if totalBytes > maxPatchArgumentBytes {
			result := Fail("arguments_too_large", "combined exact edits exceed 1 MiB")
			return patchInput{}, &result
		}
	}
	return input, nil
}

// RecordAppliedChange registers an already-authorized exact text mutation made
// by an executable tool. It does not touch the filesystem; the command's own
// approval is the authorization and the resulting record enables safe rollback.
func (m *PatchManager) RecordAppliedChange(runID, approvalID, sourceTool string, change workspace.SnapshotChange) (*domain.PatchProposal, error) {
	if !change.Revertible {
		return nil, errors.New("workspace change is not exactly revertible")
	}
	proposal := &domain.PatchProposal{
		ID: domain.NewID("patch"), RunID: runID, ApprovalID: approvalID, SourceTool: sourceTool,
		Path: filepath.ToSlash(change.Path), OriginalHash: hash(change.Original), OriginalExisted: change.OriginalExisted,
		Original: change.Original, Proposed: change.Proposed, Diff: unifiedDiff(change.Path, change.Original, change.Proposed),
		Status: "applied", CreatedAt: time.Now().UTC(),
	}
	m.mu.Lock()
	m.proposals[proposal.ID] = proposal
	m.mu.Unlock()
	copy := *proposal
	return &copy, nil
}

func (m *PatchManager) Attach(id, runID, approvalID string) (*domain.PatchProposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.proposals[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	p.RunID, p.ApprovalID = runID, approvalID
	copy := *p
	return &copy, nil
}

func (m *PatchManager) Get(id string) (*domain.PatchProposal, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.proposals[id]
	if !ok {
		return nil, false
	}
	copy := *p
	return &copy, true
}

func (m *PatchManager) Apply(id string) (*domain.PatchProposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.proposals[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	if p.Status != "pending" {
		return nil, fmt.Errorf("patch is already %s", p.Status)
	}
	abs, err := m.FS.Resolve(p.Path, true)
	if err != nil {
		return nil, err
	}
	if workspace.IsSensitive(abs) {
		return nil, workspace.ErrSensitive
	}
	current := ""
	mode := os.FileMode(0644)
	if info, statErr := os.Stat(abs); statErr == nil {
		mode = info.Mode().Perm()
		data, readErr := os.ReadFile(abs)
		if readErr != nil {
			return nil, readErr
		}
		current = string(data)
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	}
	if hash(current) != p.OriginalHash {
		return nil, ErrPatchConflict
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".workbench-patch-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err = tmp.WriteString(p.Proposed); err != nil {
		_ = tmp.Close()
		cleanup()
		return nil, err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return nil, err
	}
	if err = tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return nil, err
	}
	if err = tmp.Close(); err != nil {
		cleanup()
		return nil, err
	}
	if err = os.Rename(tmpName, abs); err != nil {
		// Windows does not replace an existing destination. Move the verified original aside and roll back on failure.
		backup := tmpName + ".original"
		if backupErr := os.Rename(abs, backup); backupErr != nil {
			cleanup()
			return nil, err
		}
		if err = os.Rename(tmpName, abs); err != nil {
			_ = os.Rename(backup, abs)
			cleanup()
			return nil, err
		}
		_ = os.Remove(backup)
	}
	p.Status = "applied"
	m.FS.InvalidateIndex()
	copy := *p
	return &copy, nil
}

func (m *PatchManager) Reject(id string) (*domain.PatchProposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.proposals[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	if p.Status != "pending" {
		return nil, fmt.Errorf("patch is already %s", p.Status)
	}
	p.Status = "rejected"
	copy := *p
	return &copy, nil
}

func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func unifiedDiff(path, old, next string) string {
	normalized := filepath.ToSlash(path)
	result, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A: diffLines(old), B: diffLines(next),
		FromFile: "a/" + normalized, ToFile: "b/" + normalized,
		Context: 3, Eol: "\n",
	})
	if err != nil {
		return fmt.Sprintf("--- a/%s\n+++ b/%s\n", normalized, normalized)
	}
	return result
}

func unifiedCreationDiff(path, content string) string {
	normalized := filepath.ToSlash(path)
	result, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A: nil, B: diffLines(content), FromFile: "/dev/null", ToFile: "b/" + normalized, Context: 3, Eol: "\n",
	})
	if err != nil || result == "" {
		return fmt.Sprintf("--- /dev/null\n+++ b/%s\n@@ -0,0 +0,0 @@\n", normalized)
	}
	return result
}

type compactDiffHunk struct {
	OldStart int
	OldEnd   int
	NewStart int
	NewEnd   int
}

func unifiedDiffFromEdits(path, old, next string, edits []resolvedTextEdit) string {
	oldLines, nextLines := diffLines(old), diffLines(next)
	hunks := make([]compactDiffHunk, 0, len(edits))
	delta := 0
	for _, edit := range edits {
		newStartOffset := edit.Start + delta
		newEndOffset := newStartOffset + len(edit.NewText)
		oldStart, oldEnd := changedLineRange(old, edit.Start, edit.End)
		newStart, newEnd := changedLineRange(next, newStartOffset, newEndOffset)
		hunk := compactDiffHunk{
			OldStart: max(0, oldStart-3), OldEnd: min(len(oldLines), oldEnd+3),
			NewStart: max(0, newStart-3), NewEnd: min(len(nextLines), newEnd+3),
		}
		if len(hunks) > 0 && (hunk.OldStart <= hunks[len(hunks)-1].OldEnd || hunk.NewStart <= hunks[len(hunks)-1].NewEnd) {
			previous := &hunks[len(hunks)-1]
			previous.OldEnd = max(previous.OldEnd, hunk.OldEnd)
			previous.NewEnd = max(previous.NewEnd, hunk.NewEnd)
		} else {
			hunks = append(hunks, hunk)
		}
		delta += len(edit.NewText) - (edit.End - edit.Start)
	}

	normalized := filepath.ToSlash(path)
	var out strings.Builder
	fmt.Fprintf(&out, "--- a/%s\n+++ b/%s\n", normalized, normalized)
	for _, hunk := range hunks {
		fmt.Fprintf(&out, "@@ -%s +%s @@\n", formatUnifiedRange(hunk.OldStart, hunk.OldEnd), formatUnifiedRange(hunk.NewStart, hunk.NewEnd))
		oldChunk, newChunk := oldLines[hunk.OldStart:hunk.OldEnd], nextLines[hunk.NewStart:hunk.NewEnd]
		var matcher *difflib.SequenceMatcher
		if len(oldChunk)+len(newChunk) <= 4000 {
			matcher = difflib.NewMatcherWithJunk(oldChunk, newChunk, false, nil)
		} else {
			matcher = difflib.NewMatcher(oldChunk, newChunk)
		}
		for _, operation := range matcher.GetOpCodes() {
			switch operation.Tag {
			case 'e':
				writeDiffLines(&out, " ", oldChunk[operation.I1:operation.I2])
			case 'r':
				writeDiffLines(&out, "-", oldChunk[operation.I1:operation.I2])
				writeDiffLines(&out, "+", newChunk[operation.J1:operation.J2])
			case 'd':
				writeDiffLines(&out, "-", oldChunk[operation.I1:operation.I2])
			case 'i':
				writeDiffLines(&out, "+", newChunk[operation.J1:operation.J2])
			}
		}
	}
	return out.String()
}

func changedLineRange(value string, start, end int) (int, int) {
	startLine := strings.Count(value[:start], "\n")
	endLine := strings.Count(value[:end], "\n")
	if end > start && value[end-1] != '\n' {
		endLine++
	}
	if end == start {
		if start < len(value) || (start > 0 && value[start-1] != '\n') {
			endLine = startLine + 1
		}
	}
	return startLine, endLine
}

func formatUnifiedRange(start, end int) string {
	beginning := start + 1
	length := end - start
	if length == 0 {
		beginning--
	}
	if length == 1 {
		return fmt.Sprintf("%d", beginning)
	}
	return fmt.Sprintf("%d,%d", beginning, length)
}

func writeDiffLines(out *strings.Builder, prefix string, lines []string) {
	for _, line := range lines {
		out.WriteString(prefix)
		out.WriteString(line)
	}
}

func diffLines(value string) []string {
	if value == "" {
		return nil
	}
	lines := strings.SplitAfter(value, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	} else {
		lines[len(lines)-1] += "\n"
	}
	return lines
}
