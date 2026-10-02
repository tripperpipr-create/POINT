package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
	"unicode/utf8"

	"local-agent-workbench/internal/filepolicy"
	"local-agent-workbench/internal/sandboxsync"
)

const (
	maxSnapshotFiles         = 5000
	maxSnapshotFileBytes     = 512 * 1024
	maxSnapshotContentBytes  = 32 * 1024 * 1024
	maxSnapshotHashFileBytes = 8 * 1024 * 1024
	maxSnapshotHashBytes     = 64 * 1024 * 1024
	maxSnapshotSkippedPaths  = 200
)

type SnapshotEntry struct {
	Fingerprint string
	Content     string
	Revertible  bool
	// Size и ModTimeNano — метаданные файла на момент чтения. По ним
	// следующий снимок узнаёт неизменённый файл, не читая его заново.
	Size        int64
	ModTimeNano int64
}

type TextSnapshot struct {
	// StartedAt — когда начался обход. Файл, изменённый позже или почти
	// одновременно, следующий снимок перечитывает (правило «racy git»).
	StartedAt     time.Time
	Files         map[string]SnapshotEntry
	ScannedFiles  int
	CapturedBytes int64
	HashedBytes   int64
	Complete      bool
	SkippedPaths  []string
}

type SnapshotChange struct {
	Path            string
	OriginalExisted bool
	Original        string
	Proposed        string
	Revertible      bool
	Reason          string
}

// CaptureTextSnapshot records a bounded view of regular workspace files.
// UTF-8 files within the per-file and aggregate limits retain exact contents
// for rollback. Other files retain a metadata fingerprint so mutations remain
// visible without placing binary or unbounded data into the audit database.
func (f *FS) CaptureTextSnapshot(ctx context.Context) (TextSnapshot, error) {
	return f.CaptureTextSnapshotFrom(ctx, nil)
}

// snapshotRacyWindow — насколько раньше начала прежнего снимка должен быть
// изменён файл, чтобы его содержимое можно было взять из прежнего снимка.
// Запись в ту же секунду, что и прежний обход, могла не сдвинуть mtime на
// файловой системе с грубым временем, поэтому такие файлы перечитываются.
const snapshotRacyWindow = 2 * time.Second

// CaptureTextSnapshotFrom — тот же снимок, но текстовый файл, у которого
// размер и mtime совпали с прежним снимком, не перечитывается: его
// содержимое берётся из prev. Движок снимает рабочую область до и после
// каждой команды, и прежде каждый снимок читал до 32 МиБ заново —
// по полторы секунды на команду в квесте 30.09.
func (f *FS) CaptureTextSnapshotFrom(ctx context.Context, prev *TextSnapshot) (TextSnapshot, error) {
	if f.FileRules == filepolicy.Current {
		manifest, err := sandboxsync.Scan(ctx, f.root, f.FileRules)
		if err != nil {
			return TextSnapshot{}, err
		}
		return f.CaptureManifestSnapshot(ctx, manifest, prev)
	}
	snapshot := TextSnapshot{StartedAt: time.Now(), Files: make(map[string]SnapshotEntry), Complete: true, SkippedPaths: []string{}}
	err := filepath.WalkDir(f.root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			snapshot.Complete = false
			appendSkippedPath(&snapshot, relativeSnapshotPath(f.root, path))
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != f.root && filepolicy.SkipDirectory(f.FileRules, entry.Name(), true) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if f.FileRules == filepolicy.Current && filepolicy.SkipFile(entry.Name()) {
			return nil
		}
		if snapshot.ScannedFiles >= maxSnapshotFiles {
			snapshot.Complete = false
			appendSkippedPath(&snapshot, relativeSnapshotPath(f.root, path))
			return filepath.SkipAll
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		snapshot.ScannedFiles++
		relative := relativeSnapshotPath(f.root, path)
		if prev != nil {
			if old, ok := prev.Files[relative]; ok && old.unchangedSince(info, prev.StartedAt) && snapshot.CapturedBytes+info.Size() <= maxSnapshotContentBytes {
				snapshot.CapturedBytes += int64(len(old.Content))
				snapshot.Files[relative] = old
				return nil
			}
		}
		if IsSensitive(relative) {
			snapshot.Files[relative] = SnapshotEntry{Fingerprint: opaqueFingerprint(path, info, &snapshot)}
			appendSkippedPath(&snapshot, relative)
			return nil
		}
		if info.Size() > maxSnapshotFileBytes || snapshot.CapturedBytes+info.Size() > maxSnapshotContentBytes {
			snapshot.Files[relative] = SnapshotEntry{Fingerprint: opaqueFingerprint(path, info, &snapshot)}
			appendSkippedPath(&snapshot, relative)
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			snapshot.Complete = false
			snapshot.Files[relative] = SnapshotEntry{Fingerprint: metadataFingerprint(info)}
			appendSkippedPath(&snapshot, relative)
			return nil
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxSnapshotFileBytes+1))
		_ = file.Close()
		if readErr != nil || len(data) > maxSnapshotFileBytes {
			snapshot.Complete = false
			snapshot.Files[relative] = SnapshotEntry{Fingerprint: metadataFingerprint(info)}
			appendSkippedPath(&snapshot, relative)
			return nil
		}
		snapshot.CapturedBytes += int64(len(data))
		fingerprint := contentFingerprint(data)
		if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
			snapshot.Files[relative] = SnapshotEntry{Fingerprint: fingerprint}
			appendSkippedPath(&snapshot, relative)
			return nil
		}
		snapshot.Files[relative] = SnapshotEntry{Fingerprint: fingerprint, Content: string(data), Revertible: true, Size: info.Size(), ModTimeNano: info.ModTime().UnixNano()}
		return nil
	})
	return snapshot, err
}

// unchangedSince — текстовый файл из прежнего снимка можно взять как есть:
// размер и mtime те же, а изменён он заведомо раньше начала того снимка.
func (e SnapshotEntry) unchangedSince(info os.FileInfo, started time.Time) bool {
	if !e.Revertible || e.ModTimeNano == 0 || started.IsZero() {
		return false
	}
	modified := info.ModTime()
	return e.Size == info.Size() && e.ModTimeNano == modified.UnixNano() && modified.Before(started.Add(-snapshotRacyWindow))
}

func opaqueFingerprint(path string, info os.FileInfo, snapshot *TextSnapshot) string {
	if info.Size() < 0 || info.Size() > maxSnapshotHashFileBytes || snapshot.HashedBytes+info.Size() > maxSnapshotHashBytes {
		return metadataFingerprint(info)
	}
	file, err := os.Open(path)
	if err != nil {
		return metadataFingerprint(info)
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(hasher, io.LimitReader(file, maxSnapshotHashFileBytes+1))
	_ = file.Close()
	if copyErr != nil || written != info.Size() {
		return metadataFingerprint(info)
	}
	snapshot.HashedBytes += written
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil))
}

func DiffTextSnapshots(before, after TextSnapshot) []SnapshotChange {
	paths := make(map[string]struct{}, len(before.Files)+len(after.Files))
	for path := range before.Files {
		paths[path] = struct{}{}
	}
	for path := range after.Files {
		paths[path] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	changes := make([]SnapshotChange, 0)
	for _, path := range ordered {
		original, originalExists := before.Files[path]
		proposed, proposedExists := after.Files[path]
		if originalExists == proposedExists && original.Fingerprint == proposed.Fingerprint {
			continue
		}
		revertible := (!originalExists || original.Revertible) && (!proposedExists || proposed.Revertible)
		reason := ""
		if !revertible {
			reason = "binary, oversized, unreadable, or outside the bounded content budget"
		}
		changes = append(changes, SnapshotChange{
			Path: path, OriginalExisted: originalExists, Original: original.Content,
			Proposed: proposed.Content, Revertible: revertible, Reason: reason,
		})
	}
	return changes
}

func contentFingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func metadataFingerprint(info os.FileInfo) string {
	return fmt.Sprintf("metadata:%d:%d", info.Size(), info.ModTime().UTC().UnixNano())
}

func relativeSnapshotPath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}

func appendSkippedPath(snapshot *TextSnapshot, path string) {
	if len(snapshot.SkippedPaths) < maxSnapshotSkippedPaths {
		snapshot.SkippedPaths = append(snapshot.SkippedPaths, path)
	}
}
