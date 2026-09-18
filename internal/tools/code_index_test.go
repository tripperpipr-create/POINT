package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/workspace"
)

func TestSearchCodeReportsBoundedRetrievalEvidence(t *testing.T) {
	root := t.TempDir()
	var source strings.Builder
	for line := 0; line < 240; line++ {
		fmt.Fprintf(&source, "retrieval marker line %03d\n", line)
	}
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(source.String()), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	result := SearchCode{FS: fs}.Execute(t.Context(), json.RawMessage(`{"query":"retrieval marker","max_chunks":1,"max_chars":1000}`))
	if !result.OK {
		t.Fatalf("search failed: %#v", result.Error)
	}
	var output struct {
		Chunks          []workspace.RelevantChunk `json:"chunks"`
		CandidateChunks int                       `json:"candidateChunks"`
		ReturnedChunks  int                       `json:"returnedChunks"`
		UsedChars       int                       `json:"usedChars"`
		MaxChars        int                       `json:"maxChars"`
		MaxChunks       int                       `json:"maxChunks"`
		Truncated       bool                      `json:"truncated"`
	}
	if err = json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Chunks) != 1 || output.ReturnedChunks != 1 || output.CandidateChunks <= output.ReturnedChunks || output.UsedChars != len(output.Chunks[0].Content) || output.UsedChars > 1000 || output.MaxChars != 1000 || output.MaxChunks != 1 || !output.Truncated {
		t.Fatalf("retrieval evidence=%#v", output)
	}
}

func TestSearchCodeRejectsUnboundedQueriesBeforeIndexing(t *testing.T) {
	fs, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool := SearchCode{FS: fs}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"query":"ok","max_chunks":21}`),
		json.RawMessage(`{"query":"ok","max_chars":999}`),
		json.RawMessage(`{"query":"` + strings.Repeat("x", 4097) + `"}`),
	} {
		result := tool.Execute(t.Context(), raw)
		if result.OK || result.Error == nil || result.Error.Code != "invalid_input" {
			t.Fatalf("unbounded query was accepted: %#v", result)
		}
	}
	if fs.IndexStatus().State != "not_built" {
		t.Fatalf("invalid query built the index: %#v", fs.IndexStatus())
	}
}

func TestSearchCodeReturnsRelationshipMetadataWithoutRelatedContents(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "service.ts"), []byte("export function ForgeArtifact() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "api.ts"), []byte("import { ForgeArtifact } from './service'\nForgeArtifact()\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	result := SearchCode{FS: fs}.Execute(t.Context(), json.RawMessage(`{"query":"ForgeArtifact","max_chunks":1,"max_chars":2400,"include_related":true}`))
	if !result.OK {
		t.Fatalf("search failed: %#v", result.Error)
	}
	var output struct {
		RelatedFiles []workspace.RelatedFile `json:"relatedFiles"`
	}
	if err = json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if len(output.RelatedFiles) != 1 || output.RelatedFiles[0].Path != "api.ts" || output.RelatedFiles[0].Relation != "imported_by" || output.RelatedFiles[0].FileSHA256 == "" {
		t.Fatalf("relationships=%#v", output.RelatedFiles)
	}
	encoded, _ := json.Marshal(output.RelatedFiles)
	if strings.Contains(string(encoded), `"content"`) {
		t.Fatalf("related code leaked: %s", encoded)
	}
}
