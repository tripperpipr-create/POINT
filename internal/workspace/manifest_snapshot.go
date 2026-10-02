package workspace

import (
	"bytes"
	"context"
	"os"
	"time"
	"unicode/utf8"

	"local-agent-workbench/internal/sandboxsync"
)

// CaptureManifestSnapshot uses an authenticated portable manifest instead of walking
// the Windows mirror. Cached exact content is reused only by SHA-256 identity.
func (f *FS) CaptureManifestSnapshot(ctx context.Context, manifest sandboxsync.Manifest, previous *TextSnapshot) (TextSnapshot, error) {
	s := TextSnapshot{StartedAt: time.Now(), Files: map[string]SnapshotEntry{}, Complete: true, SkippedPaths: []string{}}
	for _, e := range manifest.Entries {
		if err := ctx.Err(); err != nil {
			return s, err
		}
		if e.Directory {
			continue
		}
		if s.ScannedFiles >= maxSnapshotFiles {
			s.Complete = false
			appendSkippedPath(&s, e.Path)
			continue
		}
		s.ScannedFiles++
		fingerprint := "sha256:" + e.Hash
		if previous != nil {
			if old, ok := previous.Files[e.Path]; ok && old.Fingerprint == fingerprint && s.CapturedBytes+int64(len(old.Content)) <= maxSnapshotContentBytes {
				s.Files[e.Path] = old
				s.CapturedBytes += int64(len(old.Content))
				continue
			}
		}
		entry := SnapshotEntry{Fingerprint: fingerprint, Size: e.Size}
		if e.Size > maxSnapshotFileBytes || s.CapturedBytes+e.Size > maxSnapshotContentBytes {
			appendSkippedPath(&s, e.Path)
			s.Files[e.Path] = entry
			continue
		}
		p, err := sandboxsync.Resolve(f.Root(), e.Path)
		if err != nil {
			s.Complete = false
			appendSkippedPath(&s, e.Path)
			s.Files[e.Path] = entry
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			s.Complete = false
			appendSkippedPath(&s, e.Path)
			s.Files[e.Path] = entry
			continue
		}
		if contentFingerprint(data) != fingerprint {
			s.Complete = false
			appendSkippedPath(&s, e.Path)
			s.Files[e.Path] = entry
			continue
		}
		s.CapturedBytes += int64(len(data))
		if bytes.IndexByte(data, 0) < 0 && utf8.Valid(data) {
			entry.Content = string(data)
			entry.Revertible = true
		} else {
			appendSkippedPath(&s, e.Path)
		}
		s.Files[e.Path] = entry
	}
	return s, nil
}
