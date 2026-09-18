package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/workspace"
)

func TestPatchRequiresExpectedOriginal(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, _ := workspace.Open(root)
	manager := NewPatchManager(fs)
	input, _ := json.Marshal(map[string]string{"path": "main.go", "content": "new\n", "reason": "test"})
	result := manager.Execute(context.Background(), input)
	if !result.OK {
		t.Fatalf("proposal failed: %#v", result)
	}
	var proposal domain.PatchProposal
	_ = json.Unmarshal(result.Output, &proposal)
	if err := os.WriteFile(path, []byte("user edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Apply(proposal.ID); !errors.Is(err, ErrPatchConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "user edit\n" {
		t.Fatalf("user content was overwritten: %q", data)
	}
}

func TestAcceptedPatchIsApplied(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("old\n"), 0600)
	fs, _ := workspace.Open(root)
	manager := NewPatchManager(fs)
	input, _ := json.Marshal(map[string]string{"path": "main.go", "content": "new\n", "reason": "test"})
	result := manager.Execute(context.Background(), input)
	var proposal domain.PatchProposal
	_ = json.Unmarshal(result.Output, &proposal)
	applied, err := manager.Apply(proposal.ID)
	if err != nil || applied.Status != "applied" {
		t.Fatalf("apply failed: %#v %v", applied, err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "main.go"))
	if string(data) != "new\n" {
		t.Fatalf("unexpected content %q", data)
	}
}

func TestCompleteContentModeStillCreatesNewFile(t *testing.T) {
	root := t.TempDir()
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewPatchManager(fs)
	input, _ := json.Marshal(map[string]string{"path": "created.go", "content": "package created\n", "reason": "create a new file"})
	result := manager.Execute(context.Background(), input)
	if !result.OK {
		t.Fatalf("new-file proposal failed: %#v", result)
	}
	var proposal domain.PatchProposal
	if err = json.Unmarshal(result.Output, &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.OriginalExisted || proposal.Proposed != "package created\n" || !strings.Contains(proposal.Diff, "+package created") {
		t.Fatalf("new-file proposal=%#v", proposal)
	}
	if _, err = manager.Apply(proposal.ID); err != nil {
		t.Fatal(err)
	}
	emptyInput, _ := json.Marshal(map[string]string{"path": "empty.txt", "content": "", "reason": "create an empty marker"})
	emptyResult := manager.Execute(context.Background(), emptyInput)
	if !emptyResult.OK {
		t.Fatalf("empty-file proposal failed: %#v", emptyResult)
	}
	var emptyProposal domain.PatchProposal
	if err = json.Unmarshal(emptyResult.Output, &emptyProposal); err != nil {
		t.Fatal(err)
	}
	if emptyProposal.Diff == "" || !strings.Contains(emptyProposal.Diff, "--- /dev/null") {
		t.Fatalf("empty creation has no reviewable diff: %#v", emptyProposal)
	}
	if _, err = manager.Apply(emptyProposal.ID); err != nil {
		t.Fatal(err)
	}
	if info, statErr := os.Stat(filepath.Join(root, "empty.txt")); statErr != nil || info.Size() != 0 {
		t.Fatalf("empty file was not created: info=%#v err=%v", info, statErr)
	}
}

func TestPatchRejectsOversizedExistingTargetBeforeReadingIt(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(strings.Repeat("x", 512*1024+1)), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewPatchManager(fs)
	input, _ := json.Marshal(map[string]string{"path": "large.txt", "content": "small\n", "reason": "test bound"})
	result := manager.Execute(context.Background(), input)
	if result.OK || result.Error == nil || result.Error.Code != "content_too_large" {
		t.Fatalf("oversized target result=%#v", result)
	}
}

func TestExactEditsPreserveUnrelatedFileContent(t *testing.T) {
	root := t.TempDir()
	original := "package service\n\nconst untouched = 42\n\nfunc Health() string { return \"old\" }\nfunc Version() string { return \"v1\" }\n"
	if err := os.WriteFile(filepath.Join(root, "service.go"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewPatchManager(fs)
	input, _ := json.Marshal(map[string]any{
		"path": "service.go", "reason": "update two localized values",
		"edits": []map[string]string{
			{"oldText": `func Version() string { return "v1" }`, "newText": `func Version() string { return "v2" }`},
			{"oldText": `func Health() string { return "old" }`, "newText": `func Health() string { return "ok" }`},
		},
	})
	result := manager.Execute(context.Background(), input)
	if !result.OK {
		t.Fatalf("exact edit failed: %#v", result)
	}
	var proposal domain.PatchProposal
	if err = json.Unmarshal(result.Output, &proposal); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(proposal.Proposed, "const untouched = 42") || !strings.Contains(proposal.Proposed, `return "ok"`) || !strings.Contains(proposal.Proposed, `return "v2"`) {
		t.Fatalf("proposal did not preserve and update expected content: %q", proposal.Proposed)
	}
	if strings.Contains(proposal.Proposed, `return "old"`) || strings.Contains(proposal.Proposed, `return "v1"`) {
		t.Fatalf("old localized values remain: %q", proposal.Proposed)
	}
	if _, err = manager.Apply(proposal.ID); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "service.go"))
	if err != nil || string(data) != proposal.Proposed {
		t.Fatalf("applied exact edit=%q err=%v", data, err)
	}
}

func TestExactEditsRejectMissingAmbiguousAndInvalidAnchors(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("same\nsame\nunique\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewPatchManager(fs)
	tests := []struct {
		name string
		body map[string]any
		code string
	}{
		{"ambiguous", map[string]any{"path": "main.go", "reason": "test", "edits": []map[string]string{{"oldText": "same", "newText": "next"}}}, "edit_anchor_ambiguous"},
		{"missing", map[string]any{"path": "main.go", "reason": "test", "edits": []map[string]string{{"oldText": "absent", "newText": "next"}}}, "edit_anchor_missing"},
		{"missing target", map[string]any{"path": "new.go", "reason": "test", "edits": []map[string]string{{"oldText": "old", "newText": "next"}}}, "edit_target_missing"},
		{"both modes", map[string]any{"path": "main.go", "reason": "test", "content": "next", "edits": []map[string]string{{"oldText": "unique", "newText": "next"}}}, "invalid_input"},
		{"empty anchor", map[string]any{"path": "main.go", "reason": "test", "edits": []map[string]string{{"oldText": "", "newText": "next"}}}, "invalid_input"},
		{"no change", map[string]any{"path": "main.go", "reason": "test", "edits": []map[string]string{{"oldText": "unique", "newText": "unique"}}}, "no_changes"},
		{"overlap", map[string]any{"path": "main.go", "reason": "test", "edits": []map[string]string{{"oldText": "same\nsame", "newText": "first"}, {"oldText": "same\nunique", "newText": "second"}}}, "overlapping_edits"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw, _ := json.Marshal(test.body)
			result := manager.Execute(context.Background(), raw)
			if result.OK || result.Error == nil || result.Error.Code != test.code {
				t.Fatalf("result=%#v want code=%s", result, test.code)
			}
		})
	}
}

func TestPatchInputRejectsUnknownFieldsAndExposesBothModes(t *testing.T) {
	fs, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager := NewPatchManager(fs)
	result := manager.Execute(context.Background(), json.RawMessage(`{"path":"new.go","content":"package next\n","reason":"create","unexpected":true}`))
	if result.OK || result.Error == nil || result.Error.Code != "invalid_input" {
		t.Fatalf("unknown field result=%#v", result)
	}
	var schema map[string]any
	if err = json.Unmarshal(manager.Definition().InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if modes, ok := schema["oneOf"].([]any); !ok || len(modes) != 2 {
		t.Fatalf("patch schema modes=%#v", schema["oneOf"])
	}
}

func TestUnifiedDiffUsesBoundedContextForDistantExactEdits(t *testing.T) {
	lines := make([]string, 60)
	for index := range lines {
		lines[index] = fmt.Sprintf("line-%03d", index+1)
	}
	original := strings.Join(lines, "\n") + "\n"
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "long.txt"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewPatchManager(fs)
	input, _ := json.Marshal(map[string]any{
		"path": "long.txt", "reason": "change two distant lines",
		"edits": []map[string]string{
			{"oldText": "line-005", "newText": "line-005\ninserted-after-005"},
			{"oldText": "line-055", "newText": "line-055 changed"},
		},
	})
	result := manager.Execute(context.Background(), input)
	if !result.OK {
		t.Fatalf("proposal failed: %#v", result)
	}
	var proposal domain.PatchProposal
	if err = json.Unmarshal(result.Output, &proposal); err != nil {
		t.Fatal(err)
	}
	if strings.Count(proposal.Diff, "@@") != 4 || !strings.Contains(proposal.Diff, "+inserted-after-005") || !strings.Contains(proposal.Diff, "line-055 changed") {
		t.Fatalf("expected two compact hunks: %s", proposal.Diff)
	}
	if !strings.Contains(proposal.Diff, "@@ -2,7 +2,8 @@") || !strings.Contains(proposal.Diff, "@@ -52,7 +53,7 @@") {
		t.Fatalf("hunk coordinates did not account for inserted line: %s", proposal.Diff)
	}
	if strings.Contains(proposal.Diff, "line-030") || len(proposal.Diff) >= len(original) {
		t.Fatalf("diff included unrelated middle of file (%d bytes): %s", len(proposal.Diff), proposal.Diff)
	}
}

func TestUnifiedDiffDoesNotInventTrailingBlankLine(t *testing.T) {
	diff := unifiedDiff("value.txt", "old\n", "new\n")
	if strings.Contains(diff, "+\n") || strings.Contains(diff, "-\n") || strings.Contains(diff, " \n") {
		t.Fatalf("diff invented a blank line: %q", diff)
	}
	if !strings.Contains(diff, "-old\n") || !strings.Contains(diff, "+new\n") {
		t.Fatalf("diff lost the actual line change: %q", diff)
	}
}

func TestExactEditOnLargeRepeatedFileKeepsReviewDiffBounded(t *testing.T) {
	root := t.TempDir()
	original := strings.Repeat("same\n", 10_000) + "TARGET_UNIQUE\n" + strings.Repeat("same\n", 10_000)
	if err := os.WriteFile(filepath.Join(root, "generated.txt"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewPatchManager(fs)
	input, _ := json.Marshal(map[string]any{
		"path": "generated.txt", "reason": "replace the unique marker",
		"edits": []map[string]string{{"oldText": "TARGET_UNIQUE", "newText": "TARGET_CHANGED"}},
	})
	fullInput, _ := json.Marshal(map[string]any{
		"path": "generated.txt", "reason": "replace the unique marker",
		"content": strings.Replace(original, "TARGET_UNIQUE", "TARGET_CHANGED", 1),
	})
	if len(input)*100 >= len(fullInput) {
		t.Fatalf("exact edit payload=%d bytes, full payload=%d bytes", len(input), len(fullInput))
	}
	result := manager.Execute(context.Background(), input)
	if !result.OK {
		t.Fatalf("large exact edit failed: %#v", result)
	}
	var proposal domain.PatchProposal
	if err = json.Unmarshal(result.Output, &proposal); err != nil {
		t.Fatal(err)
	}
	if len(proposal.Diff) > 2048 || !strings.Contains(proposal.Diff, "-TARGET_UNIQUE") || !strings.Contains(proposal.Diff, "+TARGET_CHANGED") {
		t.Fatalf("large-file review diff is not bounded (%d bytes): %s", len(proposal.Diff), proposal.Diff)
	}
}

func FuzzPatchManagerInputNeverProducesInvalidProposal(f *testing.F) {
	root := f.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc Value() string { return \"old\" }\n"), 0600); err != nil {
		f.Fatal(err)
	}
	fs, err := workspace.Open(root)
	if err != nil {
		f.Fatal(err)
	}
	manager := NewPatchManager(fs)
	f.Add([]byte(`{"path":"main.go","reason":"seed","edits":[{"oldText":"old","newText":"new"}]}`))
	f.Add([]byte(`{"path":"created.go","reason":"seed","content":"package created\n"}`))
	f.Add([]byte(`{"path":"main.go","reason":"seed","content":"x","edits":[]}`))
	f.Add([]byte(`not-json`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		result := manager.Execute(context.Background(), json.RawMessage(raw))
		if !result.OK {
			if result.Error == nil || result.Error.Code == "" {
				t.Fatalf("failed result lacks a structured error: %#v", result)
			}
			return
		}
		var proposal domain.PatchProposal
		if err := json.Unmarshal(result.Output, &proposal); err != nil {
			t.Fatalf("successful result is not a proposal: %v", err)
		}
		if proposal.ID == "" || proposal.Path == "" || len(proposal.Proposed) > maxPatchContentBytes || proposal.OriginalHash == "" || proposal.Diff == "" {
			t.Fatalf("invalid successful proposal: %#v", proposal)
		}
	})
}
