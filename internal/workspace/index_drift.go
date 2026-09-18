// Расхождение индекса с диском и его подтягивание перед запросом.
package workspace

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
)

func (f *FS) snapshotIndexFiles() map[string]indexedFileMetadata {
	slot := f.indexSlot()
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.index == nil {
		return nil
	}
	out := make(map[string]indexedFileMetadata, len(slot.index.files))
	for path, meta := range slot.index.files {
		out[path] = meta
	}
	return out
}

func (f *FS) collectIndexDrift(ctx context.Context, files map[string]indexedFileMetadata, maxEntries int) (changed, deleted []string, partial bool, err error) {
	if files == nil {
		return nil, nil, false, nil
	}
	if maxEntries <= 0 {
		maxEntries = defaultIndexLimits.MaxEntries
	}
	seen := make(map[string]struct{}, len(files))
	visitedEntries := 0
	err = filepath.WalkDir(f.root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if walkErr := ctx.Err(); walkErr != nil {
			return walkErr
		}
		if path != f.root {
			visitedEntries++
			if visitedEntries > maxEntries {
				partial = true
				return fs.SkipAll
			}
		}
		if entry.IsDir() {
			if path != f.root && isIndexExcludedDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || IsSensitive(entry.Name()) || !likelyTextPath(path) {
			return nil
		}
		relative, relErr := filepath.Rel(f.root, path)
		if relErr != nil {
			return nil
		}
		relative = filepath.ToSlash(relative)
		seen[relative] = struct{}{}
		indexed, exists := files[relative]
		if !exists {
			changed = append(changed, relative)
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			changed = append(changed, relative)
			return nil
		}
		if indexed.Size != info.Size() || indexed.ModifiedUnixNano != info.ModTime().UnixNano() {
			changed = append(changed, relative)
		}
		return nil
	})
	if err != nil {
		return nil, nil, false, err
	}
	if partial {
		return uniqueIndexPaths(changed), nil, true, nil
	}
	for path := range files {
		if _, ok := seen[path]; !ok {
			deleted = append(deleted, path)
		}
	}
	return uniqueIndexPaths(changed), uniqueIndexPaths(deleted), false, nil
}

func (f *FS) ensureFreshIndex(ctx context.Context) (*projectIndex, error) {
	slot := f.indexSlot()
	slot.mu.Lock()
	index := slot.index
	slot.mu.Unlock()
	if index == nil {
		if _, err := f.BuildIndex(ctx); err != nil {
			return nil, err
		}
		slot.mu.Lock()
		defer slot.mu.Unlock()
		return slot.index, nil
	}
	if index.status.Partial {
		if index.status.State == "stale" {
			if _, err := f.buildIndex(ctx, limitsFromStatus(index.status)); err != nil {
				return nil, err
			}
			slot.mu.Lock()
			defer slot.mu.Unlock()
			return slot.index, nil
		}
		return index, nil
	}
	changed, deleted, driftPartial, err := f.collectIndexDrift(ctx, f.snapshotIndexFiles(), index.status.MaxEntries)
	if err != nil {
		return nil, err
	}
	if driftPartial {
		if _, err = f.buildIndex(ctx, limitsFromStatus(index.status)); err != nil {
			return nil, err
		}
		slot.mu.Lock()
		defer slot.mu.Unlock()
		return slot.index, nil
	}
	if len(changed)+len(deleted) > 0 {
		if _, err = f.UpdateIndex(ctx, changed, deleted); err != nil {
			return nil, err
		}
	} else if index.status.State == "stale" {
		slot.mu.Lock()
		if slot.index != nil {
			next := *slot.index
			next.status = slot.index.status
			next.status.State = "ready"
			if next.status.Mode == "" {
				next.status.Mode = "incremental"
			}
			next.status.Languages = cloneLanguageCounts(slot.index.status.Languages)
			slot.index = &next
		}
		slot.mu.Unlock()
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	return slot.index, nil
}

func (f *FS) ensureIndex(ctx context.Context) (*projectIndex, error) {
	return f.ensureFreshIndex(ctx)
}
