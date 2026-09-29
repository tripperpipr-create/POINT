package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/workspace"
)

// Квест 29.09: package.json с CRLF, модель прислала якорь с LF (read_file
// концов строк не показывает) — неудачный патч и ход на od -c.
func TestExactEditMatchesCRLFFileWithLFAnchor(t *testing.T) {
	root := t.TempDir()
	original := "{\r\n  \"devDependencies\": {\r\n    \"sass\": \"^1.69.5\",\r\n    \"typescript\": \"^5.3.3\"\r\n  }\r\n}\r\n"
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	fs, _ := workspace.Open(root)
	manager := NewPatchManager(fs)
	raw, _ := json.Marshal(map[string]any{"path": "package.json", "reason": "deps", "edits": []map[string]string{{
		"oldText": "    \"sass\": \"^1.69.5\",\n    \"typescript\": \"^5.3.3\"",
		"newText": "    \"sass\": \"^1.69.5\",\n    \"ssh2\": \"^1.16.0\",\n    \"typescript\": \"^5.3.3\"",
	}}})
	result := manager.Execute(context.Background(), raw)
	if !result.OK {
		t.Fatalf("LF anchor did not match CRLF file: %#v", result.Error)
	}
	var proposal domain.PatchProposal
	_ = json.Unmarshal(result.Output, &proposal)
	want := "{\r\n  \"devDependencies\": {\r\n    \"sass\": \"^1.69.5\",\r\n    \"ssh2\": \"^1.16.0\",\r\n    \"typescript\": \"^5.3.3\"\r\n  }\r\n}\r\n"
	if proposal.Proposed != want {
		t.Fatalf("line endings not preserved:\n%q\nwant\n%q", proposal.Proposed, want)
	}
}

func TestMatchFileLineEndingsKeepsAmbiguousAndExactAnchors(t *testing.T) {
	// Уже точный якорь не трогается.
	if old, repl := MatchFileLineEndings("a\r\nb\r\n", "a\r\nb", "x"); old != "a\r\nb" || repl != "x" {
		t.Fatalf("exact anchor changed: %q %q", old, repl)
	}
	// Переведённый якорь встречается дважды — перевод не засчитывается.
	if old, _ := MatchFileLineEndings("a\r\nb\r\na\r\nb\r\n", "a\nb", "x"); old != "a\nb" {
		t.Fatalf("ambiguous anchor converted: %q", old)
	}
	// LF-файл и CRLF-якорь — перевод в обратную сторону.
	if old, repl := MatchFileLineEndings("a\nb\n", "a\r\nb", "c\r\nd"); old != "a\nb" || repl != "c\nd" {
		t.Fatalf("CRLF anchor not converted for LF file: %q %q", old, repl)
	}
	// Однострочный якорь от концов строк не зависит.
	if old, _ := MatchFileLineEndings("a\r\n", "zzz", ""); old != "zzz" {
		t.Fatalf("single-line anchor changed: %q", old)
	}
}
