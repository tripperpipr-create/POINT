package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLikelyTextPathSkipsOpenForKnownExtensions(t *testing.T) {
	root := t.TempDir()
	goPath := filepath.Join(root, "main.go")
	binPath := filepath.Join(root, "blob.png")
	if err := os.WriteFile(goPath, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte{0x89, 0x50, 0x4e, 0x47, 0, 1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	if !likelyTextPath(goPath) {
		t.Fatal("known Go source should be treated as text without sniffing failure")
	}
	if likelyTextPath(binPath) {
		t.Fatal("known PNG should be rejected as binary")
	}
	unknown := filepath.Join(root, "notes.weirdlang")
	if err := os.WriteFile(unknown, []byte("hello notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !likelyTextPath(unknown) {
		t.Fatal("unknown but utf8 file should pass content sniff")
	}
}

func TestSelectRelevantChunksSkipsPartialWhenExactHitsAreRich(t *testing.T) {
	index := &projectIndex{
		chunks: make([]IndexedChunk, 12),
		tokens: map[string][]int{
			"widget": {0, 1, 2, 3, 4, 5, 6, 7},
		},
	}
	for i := range index.chunks {
		index.chunks[i] = IndexedChunk{Path: "f.go", Content: "widget body", StartLine: i + 1, EndLine: i + 1}
	}
	// Flood vocabulary with tokens that contain "widget" as a substring — a naive
	// partial scan would score all of them. With the exact-hit floor these must
	// not be required for a successful search.
	for i := 0; i < 2000; i++ {
		token := "xwidget" + strings.Repeat("z", i%7)
		index.tokens[token] = []int{i % 12}
	}
	result := selectRelevantChunks(index, "widget", []string{"widget"}, 4, 4000)
	if result.CandidateChunks == 0 {
		t.Fatal("exact hits should still rank widget chunks")
	}
	for _, chunk := range result.Chunks {
		if chunk.Score < 8 {
			t.Fatalf("expected exact-match score, got %#v", chunk)
		}
	}
}

func TestExactCompactLookupDoesNotExpandBroadComponentPostings(t *testing.T) {
	broad := make([]int, 10_000)
	for index := range broad {
		broad[index] = index
	}
	index := &projectIndex{
		chunks: make([]IndexedChunk, len(broad)),
		tokens: map[string][]int{
			"performancemarker00042": {42},
			"performance":            broad,
			"marker":                 broad,
		},
	}
	ids := collectIndexCandidateIDs(index, "PerformanceMarker00042", "performancemarker00042")
	if len(ids) != 1 || ids[0] != 42 {
		t.Fatalf("exact identifier should not expand common component postings: %v", ids)
	}
}

func TestParallelIndexPreparationPreservesWalkOrder(t *testing.T) {
	root := t.TempDir()
	for index := 0; index < 300; index++ {
		content := fmt.Sprintf("package ordered\n\nfunc StableOrder%03d() {}\n", index)
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file-%03d.go", index)), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	filesystem, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = filesystem.BuildIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := filesystem.peekReadyIndex()
	firstOrder := make([]string, len(first.chunks))
	for index, chunk := range first.chunks {
		firstOrder[index] = chunk.Path + ":" + strings.Join(chunk.Symbols, ",")
	}
	filesystem.InvalidateIndex()
	if _, err = filesystem.BuildIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := filesystem.peekReadyIndex()
	if len(firstOrder) != len(second.chunks) {
		t.Fatalf("chunk count changed across deterministic builds: %d != %d", len(firstOrder), len(second.chunks))
	}
	for index, chunk := range second.chunks {
		actual := chunk.Path + ":" + strings.Join(chunk.Symbols, ",")
		if actual != firstOrder[index] {
			t.Fatalf("chunk %d order changed: %q != %q", index, actual, firstOrder[index])
		}
	}
}

func BenchmarkSelectRelevantChunksExactRich(b *testing.B) {
	index := &projectIndex{
		chunks: make([]IndexedChunk, 64),
		tokens: map[string][]int{"widget": make([]int, 16)},
	}
	for i := range index.chunks {
		index.chunks[i] = IndexedChunk{Path: "f.go", Content: "widget body", StartLine: i + 1, EndLine: i + 1}
	}
	for i := range index.tokens["widget"] {
		index.tokens["widget"][i] = i
	}
	for i := 0; i < 8000; i++ {
		index.tokens["xwidget"+strings.Repeat("a", i%11)] = []int{i % 64}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = selectRelevantChunks(index, "widget", []string{"widget"}, 8, 8000)
	}
}

func TestProjectMapUsesReadySnapshotWithoutRebuild(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fs.BuildIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := fs.IndexStatus()
	project, err := fs.ProjectMap(context.Background(), 40)
	if err != nil {
		t.Fatal(err)
	}
	after := fs.IndexStatus()
	if project.Status.Files != before.Files || after.Mode != before.Mode {
		t.Fatalf("ProjectMap changed index unexpectedly: before=%#v after=%#v map=%#v", before, after, project.Status)
	}
}
