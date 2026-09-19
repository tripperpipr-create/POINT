// Построение и обновление индекса.
//
// Полная сборка идёт по дереву, обновление — по изменившимся путям. Обе
// упираются в пределы из indexLimits и умеют закончиться частичным индексом.
package workspace

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

func (f *FS) BuildIndex(ctx context.Context) (IndexStatus, error) {
	return f.buildIndex(ctx, defaultIndexLimits)
}

func (f *FS) buildIndex(ctx context.Context, limits indexLimits) (IndexStatus, error) {
	started := time.Now()
	slog.Info("index build start", "root", f.root)
	limits = normalizeIndexLimits(limits)
	index := &projectIndex{tokens: map[string][]int{}, files: map[string]indexedFileMetadata{}, imports: map[string][]string{}, related: map[string][]RelatedFile{}, relatedKeys: map[string]map[string]bool{}, language: map[string]int{}, dirs: map[string]int{}}
	index.status.MaxEntries = limits.MaxEntries
	index.status.MaxFiles = limits.MaxFiles
	index.status.MaxBytes = limits.MaxBytes
	index.status.MaxChunks = limits.MaxChunks
	visitedEntries := 0
	type walkedPath struct {
		absolute string
		relative string
	}
	const batchSize = 256
	pending := make([]walkedPath, 0, batchSize)
	processPending := func() (bool, error) {
		if len(pending) == 0 {
			return false, nil
		}
		prepared := make([]preparedIndexFile, len(pending))
		workerCount := min(runtime.GOMAXPROCS(0), 16)
		workerCount = min(workerCount, len(pending))
		jobs := make(chan int)
		var wait sync.WaitGroup
		wait.Add(workerCount)
		for worker := 0; worker < workerCount; worker++ {
			go func() {
				defer wait.Done()
				for item := range jobs {
					if ctx.Err() != nil {
						continue
					}
					path := pending[item]
					content, info, readErr := f.readIndexCandidateFile(path.absolute, path.relative)
					if readErr == nil {
						prepared[item] = preparedIndexFile{relative: path.relative, info: info, content: content}
					} else {
						prepared[item].skip = true
					}
				}
			}()
		}
		for item := range pending {
			jobs <- item
		}
		close(jobs)
		wait.Wait()
		if err := ctx.Err(); err != nil {
			return false, err
		}
		for _, item := range prepared {
			if item.skip {
				continue
			}
			if err := addIndexedContent(index, item.relative, item.info, item.content, true); err != nil {
				var limitErr *indexLimitError
				if errors.As(err, &limitErr) {
					setIndexPartial(index, limitErr.reason)
					pending = pending[:0]
					return true, nil
				}
			}
		}
		pending = pending[:0]
		return false, nil
	}
	err := filepath.WalkDir(f.root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path != f.root {
			visitedEntries++
			if visitedEntries > limits.MaxEntries {
				setIndexPartial(index, "entries")
				return fs.SkipAll
			}
		}
		if entry.IsDir() {
			if path != f.root && isIndexExcludedDir(entry.Name()) {
				return filepath.SkipDir
			}
			if path != f.root {
				info, infoErr := entry.Info()
				if infoErr != nil || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if entry.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 || IsSensitive(entry.Name()) {
			return nil
		}
		relative, err := filepath.Rel(f.root, path)
		if err != nil {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if !likelyTextPath(path) {
			return nil
		}
		pending = append(pending, walkedPath{absolute: path, relative: relative})
		if len(pending) == batchSize {
			stop, processErr := processPending()
			if processErr != nil {
				return processErr
			}
			if stop {
				return fs.SkipAll
			}
		}
		return nil
	})
	if err == nil && !index.status.Partial {
		_, err = processPending()
	}
	if err != nil {
		slog.Error("index build failed", "root", f.root, "error", err, "duration_ms", time.Since(started).Milliseconds())
		return IndexStatus{State: "error"}, err
	}
	buildRelationshipGraph(index)
	index.status.State = "ready"
	index.status.Mode = "full"
	index.status.Chunks = len(index.chunks)
	index.status.Symbols = len(index.symbols)
	index.status.Languages = cloneLanguageCounts(index.language)
	index.status.BuiltAt = time.Now().UTC()
	index.status.DurationMs = time.Since(started).Milliseconds()
	slot := f.indexSlot()
	slot.mu.Lock()
	slot.index = index
	status := index.status
	slot.mu.Unlock()
	slog.Info("index build done", "root", f.root, "files", status.Files, "chunks", status.Chunks, "symbols", status.Symbols, "partial", status.Partial, "limit_reason", status.LimitReason, "duration_ms", status.DurationMs)
	return status, nil
}

const indexUpdateLimit = 48

type preparedIndexFile struct {
	relative string
	info     os.FileInfo
	content  FileContent
	skip     bool
}

func (f *FS) UpdateIndex(ctx context.Context, changed, deleted []string) (IndexStatus, error) {
	changed = uniqueIndexPaths(changed)
	deleted = uniqueIndexPaths(deleted)
	if len(changed)+len(deleted) == 0 {
		return f.IndexStatus(), nil
	}
	slot := f.indexSlot()
	slot.mu.Lock()
	if slot.index == nil {
		slot.mu.Unlock()
		return f.BuildIndex(ctx)
	}
	deleted = expandDeletedIndexPaths(slot.index, deleted)
	knownSHA := map[string]string{}
	for _, relative := range changed {
		if meta, ok := slot.index.files[relative]; ok {
			knownSHA[relative] = meta.IndexedSHA256
		}
	}
	if len(changed)+len(deleted) > indexUpdateLimit {
		slot.mu.Unlock()
		return f.BuildIndex(ctx)
	}
	slot.mu.Unlock()

	started := time.Now()
	prepared := make([]preparedIndexFile, 0, len(changed))
	for _, relative := range changed {
		if err := ctx.Err(); err != nil {
			return IndexStatus{State: "error"}, err
		}
		info, err := f.statIndexedPath(relative)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				deleted = append(deleted, relative)
			}
			continue
		}
		if info.IsDir() {
			continue
		}
		content, err := f.readContent(relative, false, false)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrBinary) || errors.Is(err, ErrSensitive) || errors.Is(err, ErrExcluded) {
				deleted = append(deleted, relative)
			}
			continue
		}
		if sha := knownSHA[relative]; sha != "" && sha == indexedContentDigest(content) {
			prepared = append(prepared, preparedIndexFile{relative: relative, skip: true})
			continue
		}
		prepared = append(prepared, preparedIndexFile{relative: relative, info: info, content: content})
	}
	deleted = uniqueIndexPaths(deleted)
	applyChanged := 0
	for _, item := range prepared {
		if !item.skip {
			applyChanged++
		}
	}
	if applyChanged == 0 && len(deleted) == 0 {
		slot.mu.Lock()
		if slot.index != nil {
			next := *slot.index
			next.status = slot.index.status
			next.status.State = "ready"
			next.status.Mode = "incremental"
			next.status.Languages = cloneLanguageCounts(slot.index.status.Languages)
			slot.index = &next
			status := next.status
			slot.mu.Unlock()
			return status, nil
		}
		slot.mu.Unlock()
		return f.IndexStatus(), nil
	}

	slot.mu.Lock()
	if slot.index == nil {
		slot.mu.Unlock()
		return f.BuildIndex(ctx)
	}
	// COW clone skips token/related maps — rebuildIndexDerived fills them.
	index := cloneProjectIndexForUpdate(slot.index)
	drop := append([]string{}, deleted...)
	for _, item := range prepared {
		if !item.skip {
			drop = append(drop, item.relative)
		}
	}
	dropIndexedPaths(index, drop)
	for _, item := range prepared {
		if item.skip {
			continue
		}
		if err := addIndexedContent(index, item.relative, item.info, item.content, false); err != nil {
			var limitErr *indexLimitError
			if errors.As(err, &limitErr) {
				setIndexPartial(index, limitErr.reason)
				continue
			}
			status := slot.index.status
			slot.mu.Unlock()
			return status, err
		}
	}
	rebuildIndexDerived(index)
	index.status.Mode = "incremental"
	index.status.BuiltAt = time.Now().UTC()
	index.status.DurationMs = time.Since(started).Milliseconds()
	slot.index = index
	status := index.status
	slot.mu.Unlock()
	return status, nil
}

func (f *FS) statIndexedPath(relative string) (os.FileInfo, error) {
	abs, err := f.Resolve(relative, false)
	if err != nil {
		return nil, err
	}
	return os.Stat(abs)
}

func addIndexedContent(index *projectIndex, relative string, info os.FileInfo, content FileContent, liveDerived bool) error {
	if info == nil || info.IsDir() {
		return nil
	}
	lines := scanLines(content.Content)
	incomingChunks := indexChunkCount(len(lines))
	limits := limitsFromStatus(index.status)
	if len(index.files) >= limits.MaxFiles {
		return &indexLimitError{reason: "files"}
	}
	if index.indexedBytes+int64(len(content.Content)) > limits.MaxBytes {
		return &indexLimitError{reason: "bytes"}
	}
	if len(index.chunks)+incomingChunks > limits.MaxChunks {
		return &indexLimitError{reason: "chunks"}
	}
	indexedBytes := int64(len(content.Content))
	indexedDigest := indexedContentDigest(content)
	index.files[relative] = indexedFileMetadata{Size: info.Size(), ModifiedUnixNano: info.ModTime().UnixNano(), SHA256: content.SHA256, IndexedSHA256: indexedDigest, IndexedBytes: indexedBytes}
	index.indexedBytes += indexedBytes
	index.imports[relative] = extractImportSpecs(relative, content.Content)
	language := languageFor(relative)
	directory := indexDirectoryLabel(relative)
	if liveDerived {
		index.language[language]++
		index.dirs[directory]++
		index.status.Files++
		index.status.ApproxBytes = index.indexedBytes
	}
	for start := 0; start < len(lines); start += 48 {
		end := start + 64
		if end > len(lines) {
			end = len(lines)
		}
		body := strings.Join(lines[start:end], "")
		symbols := extractSymbols(lines[start:end])
		chunk := IndexedChunk{Path: relative, StartLine: start + 1, EndLine: end, Language: language, Symbols: symbols, Content: body, FileSHA256: content.SHA256, IndexedSHA256: indexedDigest}
		chunkID := len(index.chunks)
		index.chunks = append(index.chunks, chunk)
		if liveDerived {
			for _, token := range expandedTokens(relative + "\n" + strings.Join(symbols, " ") + "\n" + body) {
				index.tokens[token] = append(index.tokens[token], chunkID)
			}
			index.symbols = append(index.symbols, symbols...)
		}
		if end == len(lines) {
			break
		}
	}
	return nil
}

func indexChunkCount(lineCount int) int {
	count := 0
	for start := 0; start < lineCount; start += 48 {
		count++
		if start+64 >= lineCount {
			break
		}
	}
	return count
}

func dropIndexedPaths(index *projectIndex, paths []string) {
	remove := map[string]bool{}
	for _, path := range paths {
		if path = strings.TrimSpace(path); path != "" {
			remove[path] = true
		}
	}
	if len(remove) == 0 {
		return
	}
	kept := index.chunks[:0]
	for _, chunk := range index.chunks {
		if !remove[chunk.Path] {
			kept = append(kept, chunk)
		}
	}
	index.chunks = kept
	for path := range remove {
		if meta, ok := index.files[path]; ok {
			index.indexedBytes -= meta.IndexedBytes
			if index.indexedBytes < 0 {
				index.indexedBytes = 0
			}
		}
		delete(index.files, path)
		delete(index.imports, path)
	}
}

func rebuildIndexDerived(index *projectIndex) {
	index.tokens = map[string][]int{}
	index.symbols = index.symbols[:0]
	index.language = map[string]int{}
	index.dirs = map[string]int{}
	index.related = map[string][]RelatedFile{}
	index.relatedKeys = map[string]map[string]bool{}
	index.indexedBytes = 0
	for path, meta := range index.files {
		index.language[languageFor(path)]++
		index.dirs[indexDirectoryLabel(path)]++
		index.indexedBytes += meta.IndexedBytes
	}
	for chunkID, chunk := range index.chunks {
		for _, token := range expandedTokens(chunk.Path + "\n" + strings.Join(chunk.Symbols, " ") + "\n" + chunk.Content) {
			index.tokens[token] = append(index.tokens[token], chunkID)
		}
		index.symbols = append(index.symbols, chunk.Symbols...)
	}
	buildRelationshipGraph(index)
	index.status.State = "ready"
	index.status.Files = len(index.files)
	index.status.Chunks = len(index.chunks)
	index.status.Symbols = len(index.symbols)
	index.status.ApproxBytes = index.indexedBytes
	index.status.Languages = cloneLanguageCounts(index.language)
}

func indexDirectoryLabel(relative string) string {
	directory := filepath.ToSlash(filepath.Dir(relative))
	if directory == "." {
		return "корень"
	}
	return directory
}

func uniqueIndexPaths(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = filepath.ToSlash(strings.TrimSpace(value))
		value = strings.TrimPrefix(value, "./")
		if value == "" || HasParentDirSegment(value) || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

// cloneProjectIndexForUpdate copies the durable file/chunk maps for a COW
// incremental update. Token and relationship maps are rebuilt afterwards, so
// they are left empty here to avoid a full deep copy on every small edit.
func cloneProjectIndexForUpdate(src *projectIndex) *projectIndex {
	if src == nil {
		return nil
	}
	dst := &projectIndex{
		status:       src.status,
		indexedBytes: src.indexedBytes,
		chunks:       append([]IndexedChunk(nil), src.chunks...),
		files:        make(map[string]indexedFileMetadata, len(src.files)),
		imports:      make(map[string][]string, len(src.imports)),
	}
	dst.status.Languages = cloneLanguageCounts(src.status.Languages)
	for key, value := range src.files {
		dst.files[key] = value
	}
	for key, value := range src.imports {
		dst.imports[key] = append([]string(nil), value...)
	}
	return dst
}

func expandDeletedIndexPaths(index *projectIndex, deleted []string) []string {
	if index == nil || len(deleted) == 0 {
		return deleted
	}
	extra := append([]string{}, deleted...)
	for _, path := range deleted {
		prefix := strings.TrimSuffix(path, "/") + "/"
		for existing := range index.files {
			if existing == path || strings.HasPrefix(existing, prefix) {
				extra = append(extra, existing)
			}
		}
	}
	return uniqueIndexPaths(extra)
}
