package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
	"local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
)

func TestPatchObservationRequiresVisibleExactReadAndDetectsStaleness(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	tracker := newObservationTracker(nil)
	patch := json.RawMessage(`{"path":"main.go","content":"package main\n","reason":"test"}`)
	if _, requirement := tracker.CheckPatch(fs, patch, 0, 1); requirement == nil || requirement.Code != "inspection_required" {
		t.Fatalf("blind patch requirement=%#v", requirement)
	}
	readResult := tools.ReadFile{FS: fs}.Execute(t.Context(), json.RawMessage(`{"path":"main.go"}`))
	tracker.Observe(providers.ToolCall{Name: "read_file"}, readResult, 0, 1, "read-key")
	if _, requirement := tracker.CheckPatch(fs, patch, 0, 1); requirement == nil || requirement.Code != "inspection_required" {
		t.Fatalf("same-turn read became visible: %#v", requirement)
	}
	state, requirement := tracker.CheckPatch(fs, patch, 0, 2)
	if requirement != nil || !state.Known || !state.OriginalExisted || !validSHA256(state.SHA256) {
		t.Fatalf("visible read state=%#v requirement=%#v", state, requirement)
	}
	if err = os.WriteFile(path, []byte("package main\n// user edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, requirement = tracker.CheckPatch(fs, patch, 0, 2); requirement == nil || requirement.Code != "inspection_stale" {
		t.Fatalf("external edit was not stale: %#v", requirement)
	}
	if err = os.WriteFile(path, []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if state, requirement = tracker.CheckPatch(fs, patch, 1, 2); requirement != nil || !state.Known {
		t.Fatalf("unrelated workspace revision invalidated identical target: state=%#v requirement=%#v", state, requirement)
	}
	tracker.Release("read-key")
	if _, requirement = tracker.CheckPatch(fs, patch, 0, 2); requirement == nil || requirement.Code != "inspection_required" {
		t.Fatalf("released read remained usable: %#v", requirement)
	}
}

func TestNewFileRequiresPriorWorkspaceViewAndRechecksCreationRace(t *testing.T) {
	root := t.TempDir()
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	tracker := newObservationTracker(nil)
	patch := json.RawMessage(`{"path":"new.go","content":"package newfile\n","reason":"test"}`)
	if _, requirement := tracker.CheckPatch(fs, patch, 0, 1); requirement == nil || requirement.RequiredTool != "list_files" {
		t.Fatalf("new file requirement=%#v", requirement)
	}
	listResult := tools.ListFiles{FS: fs}.Execute(t.Context(), json.RawMessage(`{"maxDepth":3}`))
	tracker.Observe(providers.ToolCall{Name: "list_files"}, listResult, 0, 1, "list-key")
	if _, requirement := tracker.CheckPatch(fs, patch, 0, 1); requirement == nil {
		t.Fatal("same-turn workspace view became visible")
	}
	state, requirement := tracker.CheckPatch(fs, patch, 0, 2)
	if requirement != nil || !state.Known || state.OriginalExisted {
		t.Fatalf("new file state=%#v requirement=%#v", state, requirement)
	}
	// A later workspace revision (e.g. after mkdir via run_command) must not
	// invalidate the prior list_files for greenfield file creation.
	state, requirement = tracker.CheckPatch(fs, patch, 1, 3)
	if requirement != nil || !state.Known || state.OriginalExisted {
		t.Fatalf("stale-revision workspace view should still allow new files: state=%#v requirement=%#v", state, requirement)
	}
	if err = os.WriteFile(filepath.Join(root, "new.go"), []byte("user-created\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, requirement = tracker.CheckPatch(fs, patch, 0, 2); requirement == nil || requirement.RequiredTool != "read_file" {
		t.Fatalf("creation race did not require exact read: %#v", requirement)
	}
}

func TestExactAttachedWorkspaceContextSeedsObservation(t *testing.T) {
	content := "package attached\n"
	digest := sha256.Sum256([]byte(content))
	tracker := newObservationTracker([]domain.RunContextItem{{
		ID: "ctx", Kind: domain.ContextWorkspaceFile, Path: "attached.go", Content: content,
		Digest: "sha256:" + hex.EncodeToString(digest[:]), Size: int64(len(content)), SourceSize: int64(len(content)),
	}})
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "attached.go"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	state, requirement := tracker.CheckPatch(fs, json.RawMessage(`{"path":"attached.go"}`), 0, 1)
	if requirement != nil || !state.Known {
		t.Fatalf("attached context state=%#v requirement=%#v", state, requirement)
	}
	redacted := newObservationTracker([]domain.RunContextItem{{ID: "secret", Path: "attached.go", Content: "[REDACTED]", Digest: "sha256:" + hex.EncodeToString(digest[:])}})
	if _, requirement = redacted.CheckPatch(fs, json.RawMessage(`{"path":"attached.go"}`), 0, 1); requirement == nil {
		t.Fatal("redacted attachment incorrectly counted as exact observation")
	}
}

func TestIndexedFragmentAuthorizesOnlyCoveredExactEditsAndRefreshesAfterExternalChange(t *testing.T) {
	root := t.TempDir()
	var source bytes.Buffer
	source.WriteString("package generated\n\n")
	source.WriteString(strings.Repeat("// filler\n", 18_000))
	target := `func RareIndexedTarget() string { return "old" }`
	source.WriteString(target + "\n")
	source.WriteString(strings.Repeat("// trailer\n", 18_000))
	path := filepath.Join(root, "generated.go")
	if err := os.WriteFile(path, source.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	tracker := newObservationTracker(nil)
	searchArguments := json.RawMessage(`{"query":"RareIndexedTarget","max_chunks":1,"max_chars":4096}`)
	searchResult := tools.SearchCode{FS: fs}.Execute(t.Context(), searchArguments)
	if !searchResult.OK || len(searchResult.Output) >= source.Len()/10 {
		t.Fatalf("indexed result is not token-bounded: ok=%v output=%d source=%d error=%#v", searchResult.OK, len(searchResult.Output), source.Len(), searchResult.Error)
	}
	tracker.Observe(providers.ToolCall{Name: "search_code"}, searchResult, 0, 1, "indexed-read-1")
	patch := json.RawMessage(`{"path":"generated.go","reason":"localized indexed edit","edits":[{"oldText":"func RareIndexedTarget() string { return \"old\" }","newText":"func RareIndexedTarget() string { return \"ok\" }"}]}`)
	if _, requirement := tracker.CheckPatch(fs, patch, 0, 1); requirement == nil || requirement.Code != "inspection_required" {
		t.Fatalf("same-turn indexed fragment became visible: %#v", requirement)
	}
	state, requirement := tracker.CheckPatch(fs, patch, 0, 2)
	if requirement != nil || !state.Known || !state.OriginalExisted {
		t.Fatalf("visible indexed fragment state=%#v requirement=%#v", state, requirement)
	}
	fullRewrite := json.RawMessage(`{"path":"generated.go","reason":"unsafe rewrite","content":"package replaced\n"}`)
	if _, requirement = tracker.CheckPatch(fs, fullRewrite, 0, 2); requirement == nil || requirement.Code != "inspection_scope_required" || requirement.RequiredTool != "read_file" {
		t.Fatalf("fragment authorized a full rewrite: %#v", requirement)
	}
	uncovered := json.RawMessage(`{"path":"generated.go","reason":"unseen edit","edits":[{"oldText":"package generated","newText":"package changed"}]}`)
	if _, requirement = tracker.CheckPatch(fs, uncovered, 0, 2); requirement == nil || requirement.Code != "inspection_scope_required" || requirement.RequiredTool != "search_code" {
		t.Fatalf("fragment authorized an unseen anchor: %#v", requirement)
	}
	updated := append(append([]byte(nil), source.Bytes()...), []byte("// external user edit\n")...)
	if err = os.WriteFile(path, updated, 0600); err != nil {
		t.Fatal(err)
	}
	if _, requirement = tracker.CheckPatch(fs, patch, 0, 2); requirement == nil || requirement.Code != "inspection_stale" || requirement.RequiredTool != "search_code" {
		t.Fatalf("external edit did not stale indexed evidence: %#v", requirement)
	}
	if keys := tracker.ReleasePatchTarget(patch); len(keys) != 1 || keys[0] != "indexed-read-1" {
		t.Fatalf("stale indexed key release=%v", keys)
	}
	refreshed := tools.SearchCode{FS: fs}.Execute(t.Context(), searchArguments)
	if !refreshed.OK || bytes.Equal(refreshed.Output, searchResult.Output) {
		t.Fatalf("external edit did not refresh indexed output: old=%s new=%s error=%#v", searchResult.Output, refreshed.Output, refreshed.Error)
	}
	tracker.Observe(providers.ToolCall{Name: "search_code"}, refreshed, 0, 3, "indexed-read-2")
	if _, requirement = tracker.CheckPatch(fs, patch, 0, 4); requirement != nil {
		t.Fatalf("refreshed indexed fragment was not accepted: %#v", requirement)
	}
}
