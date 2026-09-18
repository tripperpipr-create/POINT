package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"local-agent-workbench/internal/attachments"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/workspace"
)

type PreviewSourceV2Request struct {
	Kind        string `json:"kind"`
	Label       string `json:"label,omitempty"`
	Content     string `json:"content,omitempty"`
	Path        string `json:"path,omitempty"`
	URL         string `json:"url,omitempty"`
	MediaType   string `json:"mediaType,omitempty"`
	WorkspaceID string `json:"workspaceId,omitempty"`
}

func (a *App) PreviewSourceV2(ctx context.Context, request PreviewSourceV2Request) (domain.SourceSnapshot, error) {
	request.Kind = strings.ToLower(strings.TrimSpace(request.Kind))
	request.Label = strings.TrimSpace(request.Label)
	now := time.Now().UTC()
	id := domain.NewID("source")
	root := filepath.Join(a.dataDir, "hub-v2", "source-snapshots", id)
	snapshot := domain.SourceSnapshot{ID: id, WorkspaceID: strings.TrimSpace(request.WorkspaceID), Kind: request.Kind, Label: request.Label, Warnings: []string{}, CreatedAt: now}

	var err error
	switch request.Kind {
	case "text":
		snapshot, err = previewTextSource(snapshot, request.Content)
	case "image":
		snapshot, err = previewImageSource(snapshot, request.Content, request.MediaType, root)
	case "workspace_file":
		snapshot, err = a.previewWorkspaceFileSource(snapshot, request.Path, root)
	case "local_file":
		snapshot, err = previewLocalFileSource(snapshot, request.Path, root)
	case "url":
		snapshot, err = a.previewURLSource(ctx, snapshot, request.URL, root)
	case "git":
		snapshot, err = a.previewGitSource(ctx, snapshot, request.URL, root)
	default:
		err = errors.New("source kind must be text, image, workspace_file, local_file, url or git")
	}
	if err != nil {
		return domain.SourceSnapshot{}, err
	}
	if err = a.store.SaveSourceSnapshotV2(ctx, snapshot); err != nil {
		return domain.SourceSnapshot{}, err
	}
	return snapshot, nil
}

func previewImageSource(snapshot domain.SourceSnapshot, content, mediaType, root string) (domain.SourceSnapshot, error) {
	extensions := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp"}
	extension := extensions[strings.ToLower(strings.TrimSpace(mediaType))]
	if extension == "" {
		return domain.SourceSnapshot{}, errors.New("image source must be PNG, JPEG or WebP")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(content))
	if err != nil || len(raw) == 0 {
		return domain.SourceSnapshot{}, errors.New("image source content is not valid base64")
	}
	if len(raw) > 16<<20 {
		return domain.SourceSnapshot{}, errors.New("image source exceeds 16 MiB")
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return domain.SourceSnapshot{}, err
	}
	name := "source" + extension
	path := filepath.Join(root, name)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		return domain.SourceSnapshot{}, err
	}
	fs, err := workspace.Open(root)
	if err != nil {
		return domain.SourceSnapshot{}, err
	}
	preview, err := attachments.Resolve(fs, []domain.RunContextInput{{Kind: domain.ContextWorkspaceFile, Label: snapshot.Label, Path: name}})
	if err != nil {
		return domain.SourceSnapshot{}, err
	}
	snapshot.StoragePath = path
	snapshot = snapshotFromContextItem(snapshot, preview.Items[0], preview.Warnings)
	// Image bytes live only in the immutable local artifact. Keeping base64 in
	// SQLite would duplicate a large source and expose it through the metadata API.
	snapshot.ExtractedText = ""
	snapshot.ExtractedBytes = 0
	return snapshot, nil
}

func (a *App) SourceSnapshotV2(ctx context.Context, id string) (domain.SourceSnapshot, error) {
	return a.store.GetSourceSnapshotV2(ctx, id)
}

// RefreshSourceV2 creates a new immutable snapshot from the same approved
// locator. It never rewrites an active WorkOrder; the returned diff is input
// for a new revision and the running quest remains bound to the old digest.
func (a *App) RefreshSourceV2(ctx context.Context, id string) (domain.SourceSnapshotDiff, error) {
	previous, err := a.store.GetSourceSnapshotV2(ctx, strings.TrimSpace(id))
	if err != nil {
		return domain.SourceSnapshotDiff{}, err
	}
	request := PreviewSourceV2Request{Kind: previous.Kind, Label: previous.Label, WorkspaceID: previous.WorkspaceID}
	switch previous.Kind {
	case "url", "git":
		request.URL = sourceFirstNonEmpty(previous.CanonicalURL, previous.Locator)
	default:
		return domain.SourceSnapshotDiff{}, errors.New("only URL and Git snapshots can be refreshed")
	}
	current, err := a.PreviewSourceV2(ctx, request)
	if err != nil {
		return domain.SourceSnapshotDiff{}, err
	}
	added, removed := sourceRequirementLineDiff(previous.ExtractedText, current.ExtractedText, 200)
	return domain.SourceSnapshotDiff{
		Previous: sourceSnapshotRef(previous), Current: sourceSnapshotRef(current),
		Changed: previous.Digest != current.Digest, AddedLines: added, RemovedLines: removed,
	}, nil
}

func sourceSnapshotRef(snapshot domain.SourceSnapshot) domain.SourceSnapshotRef {
	return domain.SourceSnapshotRef{ID: snapshot.ID, Kind: snapshot.Kind, Label: snapshot.Label, Locator: snapshot.Locator, Digest: snapshot.Digest, MediaType: snapshot.MediaType}
}

func sourceRequirementLineDiff(before, after string, limit int) ([]string, []string) {
	left, right := sourceRequirementLines(before), sourceRequirementLines(after)
	leftSet, rightSet := map[string]bool{}, map[string]bool{}
	for _, line := range left {
		leftSet[line] = true
	}
	for _, line := range right {
		rightSet[line] = true
	}
	added, removed := []string{}, []string{}
	for _, line := range right {
		if !leftSet[line] && len(added) < limit {
			added = append(added, line)
		}
	}
	for _, line := range left {
		if !rightSet[line] && len(removed) < limit {
			removed = append(removed, line)
		}
	}
	return added, removed
}

func sourceRequirementLines(value string) []string {
	seen := map[string]bool{}
	lines := []string{}
	for _, raw := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		lines = append(lines, line)
	}
	return lines
}

func previewTextSource(snapshot domain.SourceSnapshot, content string) (domain.SourceSnapshot, error) {
	preview, err := attachments.Resolve(nil, []domain.RunContextInput{{Kind: domain.ContextText, Label: snapshot.Label, Content: content}})
	if err != nil {
		return domain.SourceSnapshot{}, err
	}
	return snapshotFromContextItem(snapshot, preview.Items[0], preview.Warnings), nil
}

func (a *App) previewWorkspaceFileSource(snapshot domain.SourceSnapshot, path, root string) (domain.SourceSnapshot, error) {
	fs, err := a.fs()
	if err != nil {
		return domain.SourceSnapshot{}, errors.New("open the source workspace before attaching a workspace file")
	}
	clean := filepath.ToSlash(strings.TrimSpace(path))
	if clean == "" || workspace.IsSensitive(clean) {
		return domain.SourceSnapshot{}, errors.New("workspace source path is empty or sensitive")
	}
	abs, err := fs.Resolve(clean, false)
	if err != nil {
		return domain.SourceSnapshot{}, err
	}
	snapshot.Locator = clean
	return previewCopiedFile(snapshot, abs, root)
}

func previewLocalFileSource(snapshot domain.SourceSnapshot, path, root string) (domain.SourceSnapshot, error) {
	abs := filepath.Clean(strings.TrimSpace(path))
	if !filepath.IsAbs(abs) {
		return domain.SourceSnapshot{}, errors.New("local source path must be absolute")
	}
	if workspace.IsSensitive(filepath.ToSlash(abs)) {
		return domain.SourceSnapshot{}, errors.New("local source path is sensitive")
	}
	snapshot.Locator = abs
	return previewCopiedFile(snapshot, abs, root)
}

func previewCopiedFile(snapshot domain.SourceSnapshot, sourcePath, root string) (domain.SourceSnapshot, error) {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return domain.SourceSnapshot{}, err
	}
	if info.IsDir() {
		return domain.SourceSnapshot{}, errors.New("source path must be a file")
	}
	if info.Size() > 16<<20 {
		return domain.SourceSnapshot{}, errors.New("source file exceeds 16 MiB")
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return domain.SourceSnapshot{}, err
	}
	name := "source" + strings.ToLower(filepath.Ext(sourcePath))
	destination := filepath.Join(root, name)
	if err = copySnapshotFile(sourcePath, destination); err != nil {
		return domain.SourceSnapshot{}, err
	}
	fs, err := workspace.Open(root)
	if err != nil {
		return domain.SourceSnapshot{}, err
	}
	preview, err := attachments.Resolve(fs, []domain.RunContextInput{{Kind: domain.ContextWorkspaceFile, Label: snapshot.Label, Path: name}})
	if err != nil {
		return domain.SourceSnapshot{}, err
	}
	snapshot.StoragePath = destination
	return snapshotFromContextItem(snapshot, preview.Items[0], preview.Warnings), nil
}

func (a *App) previewURLSource(ctx context.Context, snapshot domain.SourceSnapshot, rawURL, root string) (domain.SourceSnapshot, error) {
	canonical, err := normalizeSourceURL(rawURL)
	if err != nil {
		return domain.SourceSnapshot{}, err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return domain.SourceSnapshot{}, err
	}
	fetcher := a.sourceFetcher
	if fetcher == nil {
		fetcher = defaultSourceFetcher()
	}
	artifact, err := a.snapshotDocument(ctx, fetcher, root, canonical, snapshot.CreatedAt)
	if err != nil {
		return domain.SourceSnapshot{}, err
	}
	snapshot.CanonicalURL, snapshot.Locator = artifact.CanonicalURL, artifact.CanonicalURL
	snapshot.Label = sourceFirstNonEmpty(snapshot.Label, artifact.Title, artifact.CanonicalURL)
	snapshot.Format = artifact.Kind
	snapshot.MediaType = artifact.Provenance.ContentType
	snapshot.ExtractedText = artifact.ExtractedText
	snapshot.Digest = artifact.Provenance.SHA256
	snapshot.SourceBytes = artifact.Provenance.SizeBytes
	snapshot.ExtractedBytes = int64(len(artifact.ExtractedText))
	snapshot.StoragePath = root
	return snapshot, nil
}

func (a *App) previewGitSource(ctx context.Context, snapshot domain.SourceSnapshot, rawURL, root string) (domain.SourceSnapshot, error) {
	canonical, err := normalizeGitSourceURL(rawURL)
	if err != nil {
		return domain.SourceSnapshot{}, err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return domain.SourceSnapshot{}, err
	}
	runner := a.gitRunner
	if runner == nil {
		runner = execGitRunner{}
	}
	repository := filepath.Join(root, "repository")
	cloneCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	_, err = runner.Run(cloneCtx, root, "clone", "--depth", "1", "--no-tags", "--single-branch", "--", canonical, "repository")
	cancel()
	if err != nil {
		return domain.SourceSnapshot{}, fmt.Errorf("repository snapshot failed using installed Git credentials: %w", err)
	}
	artifact, err := snapshotRepository(ctx, runner, repository, canonical, snapshot.CreatedAt)
	if err != nil {
		return domain.SourceSnapshot{}, err
	}
	snapshot.CanonicalURL, snapshot.Locator = canonical, canonical
	snapshot.Label = sourceFirstNonEmpty(snapshot.Label, artifact.Title, canonical)
	snapshot.Format, snapshot.MediaType = "git", artifact.Provenance.ContentType
	snapshot.ExtractedText = artifact.ExtractedText
	snapshot.Digest = artifact.Provenance.SHA256
	snapshot.SourceBytes = artifact.Provenance.SizeBytes
	snapshot.ExtractedBytes = int64(len(artifact.ExtractedText))
	snapshot.StoragePath = repository
	return snapshot, nil
}

func snapshotFromContextItem(snapshot domain.SourceSnapshot, item domain.RunContextItem, warnings []string) domain.SourceSnapshot {
	snapshot.Label = sourceFirstNonEmpty(snapshot.Label, item.Label)
	snapshot.Format, snapshot.MediaType = item.Format, item.MediaType
	snapshot.ExtractedText = security.Redact(item.Content)
	snapshot.Digest = item.Digest
	snapshot.SourceBytes, snapshot.ExtractedBytes = item.SourceSize, item.ExtractedSize
	snapshot.Warnings = append([]string(nil), warnings...)
	return snapshot
}

func copySnapshotFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func normalizeGitSourceURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "-") || strings.ContainsAny(raw, "\r\n") {
		return "", errors.New("invalid Git URL")
	}
	if strings.HasPrefix(raw, "git@") {
		parts := strings.SplitN(raw, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
			return "", errors.New("invalid SSH Git URL")
		}
		return raw, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil && parsed.Scheme != "ssh" {
		return "", errors.New("Git URL must use HTTPS or SSH without embedded credentials")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "ssh" {
		return "", errors.New("Git URL must use HTTPS or SSH")
	}
	parsed.Fragment = ""
	return parsed.String(), nil
}

func sourceFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return "Source"
}
