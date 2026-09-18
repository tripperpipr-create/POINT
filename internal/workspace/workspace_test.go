package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBuildIndexSkipsArtifactDirs(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc KeepMe() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"dist", "build", "node_modules", ".cache", ".gocache", ".tmp", "target", ".yarn", ".mypy_cache", ".svelte-kit"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "noise.js"), []byte("export const noise = 1\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	status, err := fs.BuildIndex(context.Background())
	if err != nil || status.State != "ready" || status.Files != 1 {
		t.Fatalf("expected only main.go indexed, status=%#v err=%v", status, err)
	}
	// Artifact paths remain readable for tools even when not indexed.
	if _, err = fs.Read("dist/noise.js", false); err != nil {
		t.Fatalf("dist should still be readable: %v", err)
	}
}

func TestResolveRejectsTraversalAndSensitiveRead(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=secret"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fs.Resolve("../outside", true); !errors.Is(err, ErrOutsideWorkspace) {
		t.Fatalf("expected traversal rejection, got %v", err)
	}
	if _, err = fs.Read(".env", false); !errors.Is(err, ErrSensitive) {
		t.Fatalf("expected sensitive rejection, got %v", err)
	}
	content, err := fs.Read("main.go", false)
	if err != nil || content.Numbered != "     1 | package main\n" {
		t.Fatalf("unexpected read: %#v %v", content, err)
	}
	if content.SHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte("package main\n"))) {
		t.Fatalf("read digest=%q", content.SHA256)
	}
}

func TestNormalizeIncomingPathAcceptsAbsoluteInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(root, "src", "main.go")
	content, err := fs.Read(abs, false)
	if err != nil {
		t.Fatalf("absolute inside workspace should resolve: %v", err)
	}
	if content.Path != "src/main.go" {
		t.Fatalf("display path=%q", content.Path)
	}
	quoted, err := fs.Read(`"`+filepath.ToSlash(filepath.Join("src", "main.go"))+`"`, false)
	if err != nil || quoted.Path != "src/main.go" {
		t.Fatalf("quoted relative path: %#v %v", quoted, err)
	}
	outside := filepath.Join(filepath.Dir(root), "outside.go")
	if _, err = fs.Read(outside, false); !errors.Is(err, ErrOutsideWorkspace) {
		t.Fatalf("expected outside rejection, got %v", err)
	}
}

func TestNormalizeIncomingPathWindowsRootRelative(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows drive-relative /path handling")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	content, err := fs.Read("/main.go", false)
	if err != nil || content.Path != "main.go" {
		t.Fatalf("windows /main.go: %#v %v", content, err)
	}
}

func TestListPathListsSubdirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "root.txt"), []byte("root\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := fs.ListPath(context.Background(), "src", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 1 || tree[0].Path != "src/main.go" || tree[0].IsDir {
		t.Fatalf("subdir listing=%#v", tree)
	}
}

func TestListPathMissingDirectoryReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := fs.ListPath(context.Background(), "src", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 0 {
		t.Fatalf("missing dir listing=%#v", tree)
	}
}

func TestListPathAllowsExplicitVendorPackage(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "vendor", "systemeio", "test-for-candidates", "src")
	if err := os.MkdirAll(pkg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "Pay.php"), []byte("<?php\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	rootTree, err := fs.ListPath(context.Background(), ".", 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range rootTree {
		if node.Name == "vendor" {
			t.Fatalf("root listing should omit vendor: %#v", rootTree)
		}
	}
	tree, err := fs.ListPath(context.Background(), "vendor/systemeio/test-for-candidates", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) == 0 {
		t.Fatal("expected vendor package listing")
	}
	content, err := fs.Read("vendor/systemeio/test-for-candidates/src/Pay.php", false)
	if err != nil || !strings.Contains(content.Numbered, "<?php") {
		t.Fatalf("vendor read=%#v err=%v", content, err)
	}
}

func TestTruncatedReadDoesNotClaimACompleteDigest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(strings.Repeat("x", 1024*1024+1)), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	content, err := fs.Read("large.txt", false)
	if err != nil || !content.Truncated || content.SHA256 != "" {
		t.Fatalf("truncated read=%#v err=%v", content, err)
	}
}

func TestLocalCodeIndexRanksRelevantChunksAndInvalidatesOnWrite(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "auth"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "auth", "token.go"), []byte("package auth\n\nfunc RotateRefreshToken() error { return nil }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Example\n\nA small service.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	status, err := fs.BuildIndex(context.Background())
	if err != nil || status.State != "ready" || status.Files != 2 || status.Chunks < 2 {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	project, err := fs.ProjectMap(context.Background(), 20)
	if err != nil || project.FilesByLanguage["Go"] != 1 {
		t.Fatalf("project map=%#v err=%v", project, err)
	}
	chunks, err := fs.RelevantContext(context.Background(), "rotate refresh token", 3, 4000)
	if err != nil || len(chunks) == 0 || chunks[0].Path != "internal/auth/token.go" || chunks[0].FileSHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte("package auth\n\nfunc RotateRefreshToken() error { return nil }\n"))) {
		t.Fatalf("chunks=%#v err=%v", chunks, err)
	}
	lookup := fs.LookupIndex("RotateRefreshToken", 10)
	if lookup.Status.State != "ready" || len(lookup.Hits) == 0 || lookup.Hits[0].Name != "RotateRefreshToken" || lookup.Hits[0].Path != "internal/auth/token.go" || lookup.Hits[0].Kind != "symbol" {
		t.Fatalf("index lookup=%#v", lookup)
	}
	if strings.Contains(lookup.Hits[0].Snippet, "\n") {
		t.Fatalf("IDE hit must stay compact: %#v", lookup.Hits[0])
	}
	empty := fs.LookupIndex("", 10)
	if empty.Status.State != "ready" || len(empty.Hits) != 0 {
		t.Fatalf("empty lookup should not invent hits: %#v", empty)
	}
	if _, err = fs.Write("internal/auth/token.go", "package auth\n\nfunc RotateAccessToken() error { return nil }\n"); err != nil {
		t.Fatal(err)
	}
	if fs.IndexStatus().State != "stale" {
		t.Fatalf("index status after write=%#v", fs.IndexStatus())
	}
}

func TestUpdateIndexRefreshesOneFileWithoutDroppingTheRest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "keep.go"), []byte("package keep\n\nfunc KeepMarker() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "touch.go"), []byte("package touch\n\nfunc OldTouch() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fs.BuildIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "touch.go"), []byte("package touch\n\nfunc NewTouchSymbol() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	status, err := fs.UpdateIndex(context.Background(), []string{"touch.go"}, nil)
	if err != nil || status.State != "ready" || status.Files != 2 {
		t.Fatalf("update status=%#v err=%v", status, err)
	}
	keep := fs.LookupIndex("KeepMarker", 8)
	if len(keep.Hits) == 0 || keep.Hits[0].Path != "keep.go" {
		t.Fatalf("keep.go disappeared after incremental update: %#v", keep)
	}
	fresh := fs.LookupIndex("NewTouchSymbol", 8)
	if len(fresh.Hits) == 0 || fresh.Hits[0].Path != "touch.go" {
		t.Fatalf("changed symbol missing: %#v", fresh)
	}
	stale := fs.LookupIndex("OldTouch", 8)
	for _, hit := range stale.Hits {
		if hit.Name == "OldTouch" {
			t.Fatalf("stale symbol survived incremental update: %#v", stale)
		}
	}
	if _, err = fs.UpdateIndex(context.Background(), nil, []string{"touch.go"}); err != nil {
		t.Fatal(err)
	}
	if removed := fs.LookupIndex("NewTouchSymbol", 8); len(removed.Hits) != 0 {
		t.Fatalf("deleted file stayed indexed: %#v", removed)
	}
}

func TestHasParentDirSegment(t *testing.T) {
	if !HasParentDirSegment("../secret.go") || !HasParentDirSegment("pkg/../x.go") || !HasParentDirSegment("..") {
		t.Fatal("parent segments must be rejected")
	}
	if HasParentDirSegment("notes..md") || HasParentDirSegment("nested/notes..md") || HasParentDirSegment("ok.go") {
		t.Fatal("dot-dot filenames must stay allowed")
	}
}

func TestRelevantChunksTreatMissingDigestAsStale(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := fs.relevantChunksCurrent([]RelevantChunk{{IndexedChunk: IndexedChunk{Path: "main.go"}}})
	if err != nil || ok {
		t.Fatalf("empty digest should be stale: ok=%v err=%v", ok, err)
	}
	ok, err = fs.relevantChunksCurrent([]RelevantChunk{{IndexedChunk: IndexedChunk{Path: "main.go", FileSHA256: "abcd"}}})
	if err != nil || ok {
		t.Fatalf("short digest should be stale: ok=%v err=%v", ok, err)
	}
}

func TestConcurrentSearchAndUpdateIndex(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc AlphaMarker() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fs.BuildIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 8)
	for i := 0; i < 4; i++ {
		go func() {
			_, searchErr := fs.SearchContext(context.Background(), "AlphaMarker", 8, 4000)
			done <- searchErr
		}()
		go func() {
			_, updateErr := fs.UpdateIndex(context.Background(), []string{"a.go"}, nil)
			done <- updateErr
		}()
	}
	for i := 0; i < 8; i++ {
		if err = <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestUniqueIndexPathsKeepsDotDotFilename(t *testing.T) {
	kept := uniqueIndexPaths([]string{"notes..md", "ok.go", "../secret.go", "pkg/../x.go", "./nested/notes..md"})
	joined := strings.Join(kept, ",")
	if !strings.Contains(joined, "notes..md") || !strings.Contains(joined, "nested/notes..md") || !strings.Contains(joined, "ok.go") {
		t.Fatalf("dot-dot filename dropped: %v", kept)
	}
	for _, path := range kept {
		if path == "../secret.go" || path == "pkg/../x.go" {
			t.Fatalf("parent path leaked: %v", kept)
		}
	}
}

func TestUpdateIndexSkipsUnchangedContent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "stable.go"), []byte("package stable\n\nfunc StableMarker() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fs.BuildIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, err := fs.UpdateIndex(context.Background(), []string{"stable.go"}, nil)
	if err != nil || status.State != "ready" || status.Mode != "incremental" {
		t.Fatalf("unchanged update=%#v err=%v", status, err)
	}
	hits := fs.LookupIndex("StableMarker", 8)
	if len(hits.Hits) == 0 || hits.Hits[0].Path != "stable.go" {
		t.Fatalf("unchanged file lost its symbol: %#v", hits)
	}
}

func TestSearchContextUsesReadySnapshotWithoutForcedRebuild(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ready.go"), []byte("package ready\n\nfunc ReadySearchMarker() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fs.BuildIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, err := fs.SearchContext(context.Background(), "ReadySearchMarker", 4, 4000)
	if err != nil || len(first.Chunks) == 0 {
		t.Fatalf("first search=%#v err=%v", first, err)
	}
	second, err := fs.SearchContext(context.Background(), "ReadySearchMarker", 4, 4000)
	if err != nil || len(second.Chunks) == 0 || second.Chunks[0].Path != "ready.go" {
		t.Fatalf("snapshot search=%#v err=%v", second, err)
	}
	if mode := fs.IndexStatus().Mode; mode != "full" && mode != "incremental" {
		t.Fatalf("unexpected index mode after snapshot search: %#v", fs.IndexStatus())
	}
}

func TestUpdateIndexRemovesDeletedDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg", "inner"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "keep.go"), []byte("package keep\n\nfunc KeepDirMarker() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "inner", "gone.go"), []byte("package inner\n\nfunc GoneDirMarker() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fs.BuildIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = os.RemoveAll(filepath.Join(root, "pkg")); err != nil {
		t.Fatal(err)
	}
	status, err := fs.UpdateIndex(context.Background(), nil, []string{"pkg"})
	if err != nil || status.State != "ready" || status.Files != 1 {
		t.Fatalf("directory delete status=%#v err=%v", status, err)
	}
	if gone := fs.LookupIndex("GoneDirMarker", 8); len(gone.Hits) != 0 {
		t.Fatalf("deleted directory stayed indexed: %#v", gone)
	}
	if keep := fs.LookupIndex("KeepDirMarker", 8); len(keep.Hits) == 0 {
		t.Fatalf("sibling file disappeared after directory delete: %#v", keep)
	}
}

func TestSearchContextIncrementallyIndexesExternalEdit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "keep.go"), []byte("package keep\n\nfunc KeepSearchMarker() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "touch.go"), []byte("package touch\n\nfunc OldSearchMarker() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fs.BuildIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "touch.go"), []byte("package touch\n\nfunc FreshSearchMarker() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := fs.SearchContext(context.Background(), "FreshSearchMarker", 4, 4000)
	if err != nil || len(result.Chunks) == 0 || !strings.Contains(result.Chunks[0].Content, "FreshSearchMarker") {
		t.Fatalf("search did not pick up the edit: %#v err=%v", result, err)
	}
	if fs.IndexStatus().Mode != "incremental" {
		t.Fatalf("search refreshed with a full rebuild: %#v", fs.IndexStatus())
	}
	keep := fs.LookupIndex("KeepSearchMarker", 8)
	if len(keep.Hits) == 0 {
		t.Fatalf("search refresh dropped the other file: %#v", keep)
	}
}

func TestLookupIndexDoesNotRebuildAndStaysInsideCurrentTree(t *testing.T) {
	root := t.TempDir()
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	before := fs.LookupIndex("Anything", 8)
	if before.Status.State != "not_built" || len(before.Hits) != 0 {
		t.Fatalf("lookup before build=%#v", before)
	}
	if err = os.WriteFile(filepath.Join(root, "keep.go"), []byte("package keep\n\nfunc KeepMarker() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = fs.BuildIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	hits := fs.LookupIndex("KeepMarker", 8)
	if len(hits.Hits) == 0 || hits.Hits[0].Path != "keep.go" {
		t.Fatalf("expected keep.go hit, got %#v", hits)
	}
}

func TestCodeIndexRefreshesAfterExternalModifyCreateAndDelete(t *testing.T) {
	root := t.TempDir()
	mainPath := filepath.Join(root, "main.go")
	if err := os.WriteFile(mainPath, []byte("package main\n\nfunc OriginalMarker() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fs.BuildIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(mainPath, []byte("package main\n\nfunc GalacticNebulaMarker() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	chunks, err := fs.RelevantContext(context.Background(), "galactic nebula marker", 2, 4000)
	if err != nil || len(chunks) == 0 || !strings.Contains(chunks[0].Content, "GalacticNebulaMarker") || !validIndexSHA256(chunks[0].FileSHA256) {
		t.Fatalf("modified external file was not reindexed: chunks=%#v err=%v", chunks, err)
	}
	createdPath := filepath.Join(root, "created.go")
	if err = os.WriteFile(createdPath, []byte("package main\n\nfunc CometTrailFeature() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	chunks, err = fs.RelevantContext(context.Background(), "comet trail feature", 2, 4000)
	if err != nil || len(chunks) == 0 || chunks[0].Path != "created.go" {
		t.Fatalf("new external file was not indexed: chunks=%#v err=%v", chunks, err)
	}
	if err = os.Remove(createdPath); err != nil {
		t.Fatal(err)
	}
	chunks, err = fs.RelevantContext(context.Background(), "galactic nebula marker", 2, 4000)
	if err != nil || len(chunks) == 0 || chunks[0].Path != "main.go" || fs.IndexStatus().Files != 1 {
		t.Fatalf("deleted external file remained indexed: status=%#v chunks=%#v err=%v", fs.IndexStatus(), chunks, err)
	}
}

func TestRelevantContextTruncatesOnUTF8Boundary(t *testing.T) {
	root := t.TempDir()
	content := "marker " + strings.Repeat("я", 1000) + "\n"
	if err := os.WriteFile(filepath.Join(root, "unicode.txt"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := fs.RelevantContext(context.Background(), "marker", 1, 1000)
	if err != nil || len(chunks) != 1 || len(chunks[0].Content) > 1000 || !utf8.ValidString(chunks[0].Content) {
		t.Fatalf("UTF-8 bounded chunk=%#v err=%v", chunks, err)
	}
}

func TestRelevantContextPreservesCRLFForExactAnchors(t *testing.T) {
	root := t.TempDir()
	content := "package windows\r\n\r\nfunc CRLFQuestMarker() string {\r\n\treturn \"sealed\"\r\n}\r\n"
	if err := os.WriteFile(filepath.Join(root, "windows.go"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := fs.RelevantContext(context.Background(), "CRLFQuestMarker", 1, 4000)
	if err != nil || len(chunks) != 1 || chunks[0].Content != content {
		t.Fatalf("CRLF indexed chunk=%#v err=%v", chunks, err)
	}
	if chunks[0].FileSHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(content))) {
		t.Fatalf("CRLF digest=%q", chunks[0].FileSHA256)
	}
}

func TestRelevantContextDetectsSameMetadataContentChangeByDigest(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "same.go")
	original := "package same\n\nfunc SharedMarker() string { return \"old\" }\n"
	updated := "package same\n\nfunc SharedMarker() string { return \"new\" }\n"
	if len(original) != len(updated) {
		t.Fatal("fixture must preserve byte size")
	}
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := fs.RelevantContext(context.Background(), "SharedMarker", 1, 4000)
	if err != nil || len(first) != 1 {
		t.Fatalf("initial chunks=%#v err=%v", first, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	second, err := fs.RelevantContext(context.Background(), "SharedMarker", 1, 4000)
	if err != nil || len(second) != 1 || !strings.Contains(second[0].Content, `return "new"`) || second[0].FileSHA256 == first[0].FileSHA256 {
		t.Fatalf("same-metadata change was not refreshed: first=%#v second=%#v err=%v", first, second, err)
	}
}

func TestRelevantContextPrioritizesExactSymbolOverDocumentationPhrase(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "guide.md"), []byte("# Resolve session token\n\nresolve session token resolve session token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "session.go"), []byte("package session\n\nfunc ResolveSessionToken() string { return \"ok\" }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := fs.RelevantContext(context.Background(), "resolve session token", 2, 4000)
	if err != nil || len(chunks) != 2 {
		t.Fatalf("chunks=%#v err=%v", chunks, err)
	}
	if chunks[0].Path != "session.go" || !containsAll(chunks[0].MatchedTokens, "resolve", "session", "token") {
		t.Fatalf("exact declaration was not ranked first: %#v", chunks)
	}
}

func TestRelevantContextDiversifiesFilesAndRemovesOverlappingChunks(t *testing.T) {
	root := t.TempDir()
	var large strings.Builder
	large.WriteString("package alpha\n")
	for line := 0; line < 220; line++ {
		fmt.Fprintf(&large, "var questTarget%03d = \"quest target\"\n", line)
	}
	if err := os.WriteFile(filepath.Join(root, "alpha.go"), []byte(large.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "beta.go"), []byte("package beta\n\nfunc QuestTarget() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	diverse, err := fs.RelevantContext(context.Background(), "quest target", 2, 16*1024)
	if err != nil || len(diverse) != 2 || diverse[0].Path == diverse[1].Path {
		t.Fatalf("top results did not cover distinct files: %#v err=%v", diverse, err)
	}
	if err = os.Remove(filepath.Join(root, "beta.go")); err != nil {
		t.Fatal(err)
	}
	nonOverlapping, err := fs.RelevantContext(context.Background(), "quest target", 4, 32*1024)
	if err != nil || len(nonOverlapping) < 2 {
		t.Fatalf("single-file chunks=%#v err=%v", nonOverlapping, err)
	}
	for left := 0; left < len(nonOverlapping); left++ {
		for right := left + 1; right < len(nonOverlapping); right++ {
			if nonOverlapping[left].Path == nonOverlapping[right].Path && nonOverlapping[left].StartLine <= nonOverlapping[right].EndLine && nonOverlapping[right].StartLine <= nonOverlapping[left].EndLine {
				t.Fatalf("overlapping chunks waste context: %#v and %#v", nonOverlapping[left], nonOverlapping[right])
			}
		}
	}
}

func TestRelevantContextDoesNotSacrificeStrongMatchesForWeakFileDiversity(t *testing.T) {
	root := t.TempDir()
	var strong strings.Builder
	strong.WriteString("package strong\n")
	strong.WriteString("func ResolveSessionToken() {}\n")
	strong.WriteString(strings.Repeat("// unrelated spacer\n", 110))
	strong.WriteString("func ResolveSessionToken() {}\n")
	if err := os.WriteFile(filepath.Join(root, "strong.go"), []byte(strong.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "weak.md"), []byte("token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := fs.RelevantContext(context.Background(), "ResolveSessionToken", 2, 16*1024)
	if err != nil || len(chunks) != 2 || chunks[0].Path != "strong.go" || chunks[1].Path != "strong.go" {
		t.Fatalf("strong matches were displaced by weak diversity: %#v err=%v", chunks, err)
	}
}

func TestSearchContextReportsImportsImportersAndTestsWithoutLoadingTheirCode(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"src/service.ts":      "export function CreateSession() { return 'ok' }\n",
		"src/api.ts":          "import { CreateSession } from './service'\nexport const handler = CreateSession\n",
		"src/service.test.ts": "import { CreateSession } from './service'\ntest('session', () => CreateSession())\n",
		"src/unrelated.ts":    "import React from 'react'\nexport const unrelated = true\n",
	}
	for relative, content := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	search, err := fs.SearchContextWithRelations(context.Background(), "CreateSession", 1, 2400, true)
	if err != nil || len(search.Chunks) != 1 || search.Chunks[0].Path != "src/service.ts" {
		t.Fatalf("search=%#v err=%v", search, err)
	}
	if !hasRelatedFile(search.RelatedFiles, "src/api.ts", "imported_by") || !hasRelatedFile(search.RelatedFiles, "src/service.test.ts", "test") || hasRelatedPath(search.RelatedFiles, "src/unrelated.ts") {
		t.Fatalf("related files=%#v", search.RelatedFiles)
	}
	for _, related := range search.RelatedFiles {
		if related.FileSHA256 == "" {
			t.Fatalf("relationship lacks freshness digest: %#v", related)
		}
	}
	encodedRelations, err := json.Marshal(search.RelatedFiles)
	if err != nil || strings.Contains(string(encodedRelations), `"content"`) {
		t.Fatalf("relationship metadata leaked code: %s err=%v", encodedRelations, err)
	}
	withoutRelations, err := fs.SearchContextWithRelations(context.Background(), "CreateSession", 1, 2400, false)
	if err != nil || len(withoutRelations.RelatedFiles) != 0 {
		t.Fatalf("disabled relationships=%#v err=%v", withoutRelations.RelatedFiles, err)
	}
}

func TestSearchContextResolvesGoModuleImportsAndFilenameTests(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                      "module example.dev/point\n\ngo 1.25\n",
		"internal/auth/token.go":      "package auth\n\nfunc RotateToken() {}\n",
		"internal/auth/token_test.go": "package auth\n\nfunc TestRotateToken() { RotateToken() }\n",
		"cmd/api/main.go":             "package main\n\nimport \"example.dev/point/internal/auth\"\n\nfunc main() { auth.RotateToken() }\n",
	}
	for relative, content := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	search, err := fs.SearchContextWithRelations(context.Background(), "RotateToken", 1, 2400, true)
	if err != nil || len(search.Chunks) != 1 || search.Chunks[0].Path != "internal/auth/token.go" {
		t.Fatalf("search=%#v err=%v", search, err)
	}
	if !hasRelatedFile(search.RelatedFiles, "cmd/api/main.go", "imported_by") || !hasRelatedFile(search.RelatedFiles, "internal/auth/token_test.go", "test") {
		t.Fatalf("Go relationships=%#v", search.RelatedFiles)
	}
}

func TestSearchContextResolvesPythonImportsAndRejectsExternalOrEscapingSpecs(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"app/service.py":        "def issue_token():\n    return 'ok'\n",
		"app/api.py":            "from app.service import issue_token\n",
		"tests/test_service.py": "from app.service import issue_token\n",
		"app/unsafe.py":         "from ....outside import secret\nimport requests\n",
		"outside.py":            "def secret():\n    return 'hidden'\n",
	}
	for relative, content := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	search, err := fs.SearchContextWithRelations(context.Background(), "issue_token", 1, 2400, true)
	if err != nil || len(search.Chunks) != 1 || search.Chunks[0].Path != "app/service.py" {
		t.Fatalf("search=%#v err=%v", search, err)
	}
	if !hasRelatedFile(search.RelatedFiles, "app/api.py", "imported_by") || !hasRelatedFile(search.RelatedFiles, "tests/test_service.py", "test") || hasRelatedPath(search.RelatedFiles, "app/unsafe.py") {
		t.Fatalf("Python relationships=%#v", search.RelatedFiles)
	}
	unsafeSearch, err := fs.SearchContextWithRelations(context.Background(), "app/unsafe.py", 1, 2400, true)
	if err != nil || len(unsafeSearch.Chunks) != 1 || hasRelatedPath(unsafeSearch.RelatedFiles, "outside.py") {
		t.Fatalf("escaping Python relationship=%#v err=%v", unsafeSearch, err)
	}
}

func TestRelatedFilesRefreshAfterSameMetadataExternalImportChange(t *testing.T) {
	root := t.TempDir()
	service := "export function StableService() {}\n"
	apiOriginal := "import { StableService } from './service'\nStableService()\n"
	apiUpdated := "import { StableService } from './ignored'\nStableService()\n"
	if len(apiOriginal) != len(apiUpdated) {
		t.Fatal("fixture must preserve size")
	}
	for name, content := range map[string]string{"service.ts": service, "ignored.ts": "export function IgnoredService() {}\n", "api.ts": apiOriginal} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := fs.SearchContextWithRelations(context.Background(), "StableService", 1, 2400, true)
	if err != nil || !hasRelatedPath(first.RelatedFiles, "api.ts") {
		t.Fatalf("initial relationships=%#v err=%v", first.RelatedFiles, err)
	}
	apiPath := filepath.Join(root, "api.ts")
	info, err := os.Stat(apiPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(apiPath, []byte(apiUpdated), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(apiPath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	second, err := fs.SearchContextWithRelations(context.Background(), "StableService", 1, 2400, true)
	if err != nil || hasRelatedPath(second.RelatedFiles, "api.ts") {
		t.Fatalf("stale relationship survived same-metadata edit: %#v err=%v", second.RelatedFiles, err)
	}
}

func TestRelatedFilesAreDeterministicallyCapped(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "service.ts"), []byte("export function SharedService() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 25; index++ {
		content := "import { SharedService } from './service'\nSharedService()\n"
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("consumer-%02d.ts", index)), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	search, err := fs.SearchContextWithRelations(context.Background(), "SharedService", 1, 2400, true)
	if err != nil || len(search.RelatedFiles) != 20 || !search.RelatedFilesTruncated {
		t.Fatalf("related cap=%d truncated=%v err=%v", len(search.RelatedFiles), search.RelatedFilesTruncated, err)
	}
	for index, related := range search.RelatedFiles {
		expected := fmt.Sprintf("consumer-%02d.ts", index)
		if related.Path != expected {
			t.Fatalf("related[%d]=%#v want %s", index, related, expected)
		}
	}
}

func TestImportExtractionIsBoundedPerFile(t *testing.T) {
	var source strings.Builder
	for index := 0; index < maxImportsPerFile+50; index++ {
		fmt.Fprintf(&source, "import value%03d from './module-%03d'\n", index, index)
	}
	imports := extractImportSpecs("large.ts", source.String())
	if len(imports) != maxImportsPerFile || imports[0] != "./module-000" || imports[len(imports)-1] != "./module-255" {
		t.Fatalf("bounded imports=%d first=%q last=%q", len(imports), imports[0], imports[len(imports)-1])
	}
}

func hasRelatedFile(items []RelatedFile, path, relation string) bool {
	for _, item := range items {
		if item.Path == path && item.Relation == relation {
			return true
		}
	}
	return false
}

func hasRelatedPath(items []RelatedFile, path string) bool {
	for _, item := range items {
		if item.Path == path {
			return true
		}
	}
	return false
}

func containsAll(items []string, expected ...string) bool {
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		seen[item] = true
	}
	for _, item := range expected {
		if !seen[item] {
			return false
		}
	}
	return true
}

func TestRestoreAgentChangeRefusesToOverwriteLaterEdit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "value.txt")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fs.Write("value.txt", "agent"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("user"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = fs.RestoreAgentChange("value.txt", "agent", "original", true); err == nil {
		t.Fatal("rollback overwrote a later user edit")
	}
	if data, readErr := os.ReadFile(path); readErr != nil || string(data) != "user" {
		t.Fatalf("later edit changed: %q err=%v", data, readErr)
	}
	if err = os.WriteFile(path, []byte("agent"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = fs.RestoreAgentChange("value.txt", "agent", "original", true); err != nil {
		t.Fatal(err)
	}
}

func TestResolveRejectsEscapingSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require Windows developer mode")
	}
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fs.Resolve("escape/file.txt", true); !errors.Is(err, ErrOutsideWorkspace) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestListAndSearchSkipSecrets(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "visible.txt"), []byte("needle here"), 0600)
	_ = os.WriteFile(filepath.Join(root, ".env"), []byte("needle secret"), 0600)
	_ = os.WriteFile(filepath.Join(root, "binary.bin"), []byte{0, 1, 2, 3}, 0600)
	fs, _ := Open(root)
	tree, err := fs.List(context.Background(), 3)
	if err != nil || len(tree) != 1 || tree[0].Name != "visible.txt" {
		t.Fatalf("bad tree: %#v, %v", tree, err)
	}
	matches, err := fs.Search(context.Background(), "needle", 20)
	if err != nil || len(matches) != 1 || matches[0].Path != "visible.txt" {
		t.Fatalf("bad matches: %#v, %v", matches, err)
	}
}

func TestWriteAtomicallySavesSafeText(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package old\n"), 0640); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	written, err := fs.Write("main.go", "package main\n")
	if err != nil {
		t.Fatal(err)
	}
	if written.Content != "package main\n" || written.Size != int64(len("package main\n")) {
		t.Fatalf("unexpected written file: %#v", written)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0640 {
		t.Fatalf("file mode changed: %o", info.Mode().Perm())
	}
}

func TestWriteRejectsUnsafeContentAndPaths(t *testing.T) {
	fs, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fs.Write("../escape.txt", "no"); !errors.Is(err, ErrOutsideWorkspace) {
		t.Fatalf("expected workspace escape rejection, got %v", err)
	}
	if _, err = fs.Write(".env", "TOKEN=secret"); !errors.Is(err, ErrSensitive) {
		t.Fatalf("expected sensitive file rejection, got %v", err)
	}
	if _, err = fs.Write("bad.txt", string([]byte{0xff})); !errors.Is(err, ErrBinary) {
		t.Fatalf("expected binary content rejection, got %v", err)
	}
}

// Файл длиннее лимита чтения приходит обрезанным, и отпечатка файла у него нет.
// Пока свежесть куска сверяли по этому пустому отпечатку, кусок всегда числился
// устаревшим: поиск переиндексировал файл, снова получал пустой отпечаток и на
// третьей попытке отдавал "project changed while indexed context was being
// prepared" — на любой запрос, задевший такой файл.
func TestSearchContextAnswersFromOversizedFile(t *testing.T) {
	root := t.TempDir()
	filler := strings.Repeat("// filler line pushing this file past the read cap\n", 30000)
	if err := os.WriteFile(filepath.Join(root, "big.go"), []byte("package big\n\nfunc HugeSearchMarker() {}\n"+filler), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fs.BuildIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := fs.SearchContext(context.Background(), "HugeSearchMarker", 4, 4000)
	if err != nil || len(result.Chunks) == 0 || result.Chunks[0].Path != "big.go" {
		t.Fatalf("oversized file search=%#v err=%v", result, err)
	}
	// Обрезанный файл сверяется по отпечатку прочитанной части, поэтому правка
	// внутри неё обязана дойти до поиска.
	if err = os.WriteFile(filepath.Join(root, "big.go"), []byte("package big\n\nfunc FreshHugeMarker() {}\n"+filler), 0600); err != nil {
		t.Fatal(err)
	}
	edited, err := fs.SearchContext(context.Background(), "FreshHugeMarker", 4, 4000)
	if err != nil || len(edited.Chunks) == 0 || !strings.Contains(edited.Chunks[0].Content, "FreshHugeMarker") {
		t.Fatalf("edit inside the indexed prefix was lost: %#v err=%v", edited, err)
	}
}

// Переход через лимит чтения в обе стороны.
//
// Отпечаток индексируемого содержимого у файла до лимита — это отпечаток файла,
// а после — отпечаток прочитанной части. Значит на самой границе он меняется не
// потому, что файл стал другим, и обе стороны обязаны сойтись за один проход:
// иначе поиск снова упирался бы в потолок попыток, как до починки.
func TestSearchContextSurvivesCrossingTheReadCap(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "border.go")
	filler := strings.Repeat("// filler line pushing this file past the read cap\n", 30000)
	if err := os.WriteFile(path, []byte("package border\n\nfunc SmallBorderMarker() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fs.BuildIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	small, err := fs.SearchContext(context.Background(), "SmallBorderMarker", 4, 4000)
	if err != nil || len(small.Chunks) == 0 {
		t.Fatalf("маленький файл не найден: %#v err=%v", small, err)
	}

	// Файл перерос лимит: отпечаток файла пропал, остался отпечаток куска.
	if err = os.WriteFile(path, []byte("package border\n\nfunc GrownBorderMarker() {}\n"+filler), 0600); err != nil {
		t.Fatal(err)
	}
	grown, err := fs.SearchContext(context.Background(), "GrownBorderMarker", 4, 4000)
	if err != nil || len(grown.Chunks) == 0 {
		t.Fatalf("после перехода за лимит поиск сломался: %#v err=%v", grown, err)
	}

	// И обратно: файл снова целиком читается, отпечаток опять файловый.
	if err = os.WriteFile(path, []byte("package border\n\nfunc ShrunkBorderMarker() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	shrunk, err := fs.SearchContext(context.Background(), "ShrunkBorderMarker", 4, 4000)
	if err != nil || len(shrunk.Chunks) == 0 {
		t.Fatalf("после возврата под лимит поиск сломался: %#v err=%v", shrunk, err)
	}
	// Поиск нечёткий: на запрос про исчезнувший маркер он вернёт соседние куски
	// по общим словам. Проверяется не пустота выдачи, а то, что самого текста в
	// индексе не осталось.
	stale, _ := fs.SearchContext(context.Background(), "GrownBorderMarker", 4, 4000)
	for _, chunk := range stale.Chunks {
		if strings.Contains(chunk.Content, "GrownBorderMarker") {
			t.Fatalf("исчезнувший маркер остался в индексе: %s", chunk.Content)
		}
	}
}
