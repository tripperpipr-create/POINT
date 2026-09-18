package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type sourceSnapshotFetcher struct{ result FetchedSource }

func (f sourceSnapshotFetcher) Fetch(context.Context, string) (FetchedSource, error) {
	return f.result, nil
}

type sequenceSourceSnapshotFetcher struct {
	results []FetchedSource
	index   int
}

func (f *sequenceSourceSnapshotFetcher) Fetch(context.Context, string) (FetchedSource, error) {
	result := f.results[f.index]
	if f.index < len(f.results)-1 {
		f.index++
	}
	return result, nil
}

func TestPreviewLocalFileCreatesImmutableCopy(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	dataDir, sourceDir := t.TempDir(), t.TempDir()
	sourcePath := filepath.Join(sourceDir, "requirements.md")
	if err := os.WriteFile(sourcePath, []byte("# Version one\nBuild the API."), 0600); err != nil {
		t.Fatal(err)
	}
	application, err := New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })

	snapshot, err := application.PreviewSourceV2(context.Background(), PreviewSourceV2Request{Kind: "local_file", Path: sourcePath})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Digest == "" || !strings.Contains(snapshot.ExtractedText, "Build the API") || snapshot.StoragePath == "" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	if err = os.WriteFile(sourcePath, []byte("# Version two\nChanged."), 0600); err != nil {
		t.Fatal(err)
	}
	stored, err := application.SourceSnapshotV2(context.Background(), snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := os.ReadFile(stored.StoragePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(retained), "Version one") || stored.Digest != snapshot.Digest {
		t.Fatalf("snapshot changed with origin: stored=%#v content=%q", stored, retained)
	}
}

func TestPreviewURLPersistsFetchedDigestAndRedactsContent(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	fetcher := sourceSnapshotFetcher{result: FetchedSource{
		CanonicalURL: "https://docs.example.test/spec.md",
		ContentType:  "text/markdown",
		Body:         []byte("# Spec\nAPI_KEY=super-secret-value\nCreate endpoint."),
	}}
	application, err := New(t.TempDir(), WithSourceFetcher(fetcher))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })

	snapshot, err := application.PreviewSourceV2(context.Background(), PreviewSourceV2Request{Kind: "url", URL: "https://docs.example.test/spec.md"})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Digest == "" || snapshot.CanonicalURL != fetcher.result.CanonicalURL {
		t.Fatalf("unexpected URL snapshot: %#v", snapshot)
	}
	if strings.Contains(snapshot.ExtractedText, "super-secret-value") {
		t.Fatalf("secret leaked into extracted text: %q", snapshot.ExtractedText)
	}
}

func TestRefreshURLCreatesNewSnapshotAndRequirementDiff(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	fetcher := &sequenceSourceSnapshotFetcher{results: []FetchedSource{
		{CanonicalURL: "https://docs.example.test/spec.md", ContentType: "text/markdown", Body: []byte("# Spec\nCreate API\n")},
		{CanonicalURL: "https://docs.example.test/spec.md", ContentType: "text/markdown", Body: []byte("# Spec\nCreate API\nAdd tests\n")},
	}}
	application, err := New(t.TempDir(), WithSourceFetcher(fetcher))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	previous, err := application.PreviewSourceV2(context.Background(), PreviewSourceV2Request{Kind: "url", URL: fetcher.results[0].CanonicalURL})
	if err != nil {
		t.Fatal(err)
	}
	diff, err := application.RefreshSourceV2(context.Background(), previous.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Changed || diff.Previous.ID != previous.ID || diff.Current.ID == previous.ID || len(diff.AddedLines) != 1 || diff.AddedLines[0] != "Add tests" {
		t.Fatalf("unexpected refresh diff: %#v", diff)
	}
	stored, err := application.SourceSnapshotV2(context.Background(), previous.ID)
	if err != nil || stored.Digest != previous.Digest || strings.Contains(stored.ExtractedText, "Add tests") {
		t.Fatalf("refresh mutated the approved snapshot: %#v err=%v", stored, err)
	}
}

func TestPreviewImageStoresImmutableBytesWithoutBase64InMetadata(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	// Valid 1x1 PNG; the request carries base64 only across the intake boundary.
	encoded := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := application.PreviewSourceV2(context.Background(), PreviewSourceV2Request{Kind: "image", Label: "Макет", Content: encoded, MediaType: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := application.SourceSnapshotV2(context.Background(), snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := os.ReadFile(stored.StoragePath)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Kind != "image" || stored.MediaType != "image/png" || stored.Digest == "" || stored.ExtractedText != "" || stored.ExtractedBytes != 0 || !bytes.Equal(artifact, raw) {
		t.Fatalf("image snapshot metadata/artifact mismatch: %#v", stored)
	}
}

func TestNormalizeGitSourceURLAllowsCredentialManagersButNotEmbeddedHTTPSSecrets(t *testing.T) {
	for _, accepted := range []string{"https://github.com/acme/project.git", "ssh://git@github.com/acme/project.git", "git@github.com:acme/project.git"} {
		if _, err := normalizeGitSourceURL(accepted); err != nil {
			t.Fatalf("expected %q to be accepted: %v", accepted, err)
		}
	}
	for _, rejected := range []string{"https://token@github.com/acme/project.git", "file:///tmp/project", "--upload-pack=evil", "https://github.com/a.git\n--config=x"} {
		if _, err := normalizeGitSourceURL(rejected); err == nil {
			t.Fatalf("expected %q to be rejected", rejected)
		}
	}
}

func TestNormalizeDocumentURLRejectsLocalAndPrivateTargets(t *testing.T) {
	for _, rejected := range []string{"https://localhost/spec", "https://service.local/spec", "https://127.0.0.1/spec", "https://10.0.0.5/spec", "https://[::1]/spec"} {
		if _, err := normalizeSourceURL(rejected); err == nil {
			t.Fatalf("expected %q to be rejected", rejected)
		}
	}
	if _, err := normalizeSourceURL("https://docs.example.com/spec"); err != nil {
		t.Fatalf("public HTTPS URL rejected: %v", err)
	}
}
