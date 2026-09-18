package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// IndexStatus is intentionally small enough to include in every bootstrap
// response. The index itself never leaves the local process.
type IndexStatus struct {
	State       string         `json:"state"`
	Mode        string         `json:"mode,omitempty"` // full | incremental
	Partial     bool           `json:"partial"`
	LimitReason string         `json:"limitReason,omitempty"` // entries | files | bytes | chunks
	MaxEntries  int            `json:"maxEntries,omitempty"`
	MaxFiles    int            `json:"maxFiles,omitempty"`
	MaxBytes    int64          `json:"maxBytes,omitempty"`
	MaxChunks   int            `json:"maxChunks,omitempty"`
	Files       int            `json:"files"`
	Chunks      int            `json:"chunks"`
	Symbols     int            `json:"symbols"`
	Languages   map[string]int `json:"languages,omitempty"`
	BuiltAt     time.Time      `json:"builtAt,omitempty"`
	DurationMs  int64          `json:"durationMs"`
	ApproxBytes int64          `json:"approxBytes"`
}

type IndexedChunk struct {
	Path       string   `json:"path"`
	StartLine  int      `json:"startLine"`
	EndLine    int      `json:"endLine"`
	Language   string   `json:"language"`
	Symbols    []string `json:"symbols,omitempty"`
	Content    string   `json:"content"`
	FileSHA256 string   `json:"fileSha256,omitempty"`
	// IndexedSHA256 — отпечаток того, что реально попало в индекс.
	//
	// Для файла целиком он совпадает с FileSHA256. Файл длиннее лимита чтения
	// приходит обрезанным, и FileSHA256 у него пуст: отдавать наружу отпечаток
	// куска как отпечаток файла нельзя, по нему сверяют исходник перед
	// правкой. Свежесть индекса при этом сверять надо, поэтому отпечаток
	// прочитанного куска живёт отдельным полем и наружу не уходит.
	IndexedSHA256 string `json:"-"`
}

type RelevantChunk struct {
	IndexedChunk
	Score         int      `json:"score"`
	MatchedTokens []string `json:"matchedTokens,omitempty"`
}

type ContextSearchResult struct {
	Chunks                []RelevantChunk `json:"chunks"`
	CandidateChunks       int             `json:"candidateChunks"`
	ReturnedChunks        int             `json:"returnedChunks"`
	UsedChars             int             `json:"usedChars"`
	MaxChars              int             `json:"maxChars"`
	MaxChunks             int             `json:"maxChunks"`
	Truncated             bool            `json:"truncated"`
	RelatedFiles          []RelatedFile   `json:"relatedFiles,omitempty"`
	RelatedFilesTruncated bool            `json:"relatedFilesTruncated,omitempty"`
}

type RelatedFile struct {
	Path       string `json:"path"`
	Relation   string `json:"relation"`
	Via        string `json:"via,omitempty"`
	FileSHA256 string `json:"fileSha256"`
}

type ProjectMap struct {
	FilesByLanguage map[string]int `json:"filesByLanguage"`
	TopDirectories  []string       `json:"topDirectories"`
	Symbols         []string       `json:"symbols"`
	Status          IndexStatus    `json:"status"`
}

// IndexHit is a compact navigation result for the IDE. It never includes full chunk text.
type IndexHit struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Language string `json:"language,omitempty"`
	Score    int    `json:"score"`
	Snippet  string `json:"snippet,omitempty"`
}

type IndexSearchResult struct {
	Query  string      `json:"query"`
	Status IndexStatus `json:"status"`
	Hits   []IndexHit  `json:"hits"`
}

type projectIndex struct {
	status       IndexStatus
	indexedBytes int64
	chunks       []IndexedChunk
	tokens       map[string][]int
	files        map[string]indexedFileMetadata
	imports      map[string][]string
	related      map[string][]RelatedFile
	relatedKeys  map[string]map[string]bool
	language     map[string]int
	dirs         map[string]int
	symbols      []string
}

type indexedFileMetadata struct {
	Size             int64
	ModifiedUnixNano int64
	SHA256           string
	IndexedSHA256    string
	IndexedBytes     int64
}

var indexes sync.Map // workspace root -> *indexSlot

type indexSlot struct {
	mu    sync.Mutex
	index *projectIndex
}

// peekReadyIndex returns the current ready index without a filesystem drift walk.
// Callers that may miss brand-new files should fall back to ensureFreshIndex.
func (f *FS) peekReadyIndex() *projectIndex {
	slot := f.indexSlot()
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.index == nil || slot.index.status.State != "ready" {
		return nil
	}
	return slot.index
}

// missingIndexedPaths stats known indexed files and returns paths that disappeared.
// Cheaper than a full-tree drift walk when a ready snapshot already answered the query.
func (f *FS) missingIndexedPaths(index *projectIndex) []string {
	if index == nil || len(index.files) == 0 {
		return nil
	}
	missing := make([]string, 0)
	for relative := range index.files {
		if _, err := f.statIndexedPath(relative); errors.Is(err, os.ErrNotExist) {
			missing = append(missing, relative)
		}
	}
	return missing
}

func (f *FS) indexSlot() *indexSlot {
	value, _ := indexes.LoadOrStore(f.root, &indexSlot{})
	return value.(*indexSlot)
}

func (f *FS) IndexStatus() IndexStatus {
	slot := f.indexSlot()
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.index == nil {
		return IndexStatus{State: "not_built"}
	}
	status := slot.index.status
	if len(status.Languages) == 0 && len(slot.index.language) > 0 {
		status.Languages = cloneLanguageCounts(slot.index.language)
	}
	return status
}

func (f *FS) InvalidateIndex() {
	slot := f.indexSlot()
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.index == nil {
		return
	}
	next := *slot.index
	next.status = slot.index.status
	next.status.State = "stale"
	next.status.Languages = cloneLanguageCounts(slot.index.status.Languages)
	slot.index = &next
}

type indexLimits struct {
	MaxEntries int
	MaxFiles   int
	MaxBytes   int64
	MaxChunks  int
}

var defaultIndexLimits = indexLimits{
	MaxEntries: 200_000,
	MaxFiles:   60_000,
	MaxBytes:   256 * 1024 * 1024,
	MaxChunks:  150_000,
}

type indexLimitError struct{ reason string }

func (e *indexLimitError) Error() string { return "index capacity reached: " + e.reason }

func normalizeIndexLimits(limits indexLimits) indexLimits {
	if limits.MaxEntries <= 0 {
		limits.MaxEntries = defaultIndexLimits.MaxEntries
	}
	if limits.MaxFiles <= 0 {
		limits.MaxFiles = defaultIndexLimits.MaxFiles
	}
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = defaultIndexLimits.MaxBytes
	}
	if limits.MaxChunks <= 0 {
		limits.MaxChunks = defaultIndexLimits.MaxChunks
	}
	return limits
}

func limitsFromStatus(status IndexStatus) indexLimits {
	return normalizeIndexLimits(indexLimits{
		MaxEntries: status.MaxEntries,
		MaxFiles:   status.MaxFiles,
		MaxBytes:   status.MaxBytes,
		MaxChunks:  status.MaxChunks,
	})
}

func setIndexPartial(index *projectIndex, reason string) {
	index.status.Partial = true
	if index.status.LimitReason == "" {
		index.status.LimitReason = reason
	}
}

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

func (f *FS) appendIndexedPath(index *projectIndex, relative string, info os.FileInfo, liveDerived bool) error {
	if info == nil || info.IsDir() {
		return nil
	}
	content, err := f.readContent(relative, false, false)
	if err != nil {
		return err
	}
	return addIndexedContent(index, relative, info, content, liveDerived)
}

func (f *FS) appendWalkedIndexPath(index *projectIndex, absolute, relative string, info os.FileInfo, liveDerived bool) error {
	if info == nil || !info.Mode().IsRegular() {
		return nil
	}
	content, err := f.readWalkedIndexFile(absolute, relative, info)
	if err != nil {
		return err
	}
	return addIndexedContent(index, relative, info, content, liveDerived)
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

func cloneProjectIndex(src *projectIndex) *projectIndex {
	if src == nil {
		return nil
	}
	dst := cloneProjectIndexForUpdate(src)
	dst.tokens = make(map[string][]int, len(src.tokens))
	dst.related = make(map[string][]RelatedFile, len(src.related))
	dst.relatedKeys = make(map[string]map[string]bool, len(src.relatedKeys))
	dst.language = cloneLanguageCounts(src.language)
	dst.dirs = cloneLanguageCounts(src.dirs)
	dst.symbols = append([]string(nil), src.symbols...)
	for key, value := range src.tokens {
		dst.tokens[key] = append([]int(nil), value...)
	}
	for key, value := range src.related {
		dst.related[key] = append([]RelatedFile(nil), value...)
	}
	for key, value := range src.relatedKeys {
		inner := make(map[string]bool, len(value))
		for nested, flag := range value {
			inner[nested] = flag
		}
		dst.relatedKeys[key] = inner
	}
	return dst
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

func (f *FS) ProjectMap(ctx context.Context, maxSymbols int) (ProjectMap, error) {
	// Prefer the ready snapshot: companion and Hub call this often and a full-tree
	// drift walk is unnecessary when the extension already keeps the index fresh.
	index := f.peekReadyIndex()
	var err error
	if index == nil {
		index, err = f.ensureIndex(ctx)
		if err != nil {
			return ProjectMap{}, err
		}
	}
	if maxSymbols <= 0 || maxSymbols > 200 {
		maxSymbols = 80
	}
	type counted struct {
		value string
		count int
	}
	directories := make([]counted, 0, len(index.dirs))
	for value, count := range index.dirs {
		directories = append(directories, counted{value, count})
	}
	sort.Slice(directories, func(i, j int) bool { return directories[i].count > directories[j].count })
	topDirectories := make([]string, 0, min(20, len(directories)))
	for _, item := range directories[:min(20, len(directories))] {
		topDirectories = append(topDirectories, item.value)
	}
	symbols := uniqueStrings(index.symbols)
	if len(symbols) > maxSymbols {
		symbols = symbols[:maxSymbols]
	}
	return ProjectMap{FilesByLanguage: cloneLanguageCounts(index.language), TopDirectories: topDirectories, Symbols: symbols, Status: index.status}, nil
}

// LookupIndex ranks the already-built index for IDE navigation. It never starts a rebuild.
func (f *FS) LookupIndex(query string, limit int) IndexSearchResult {
	query = strings.TrimSpace(query)
	result := IndexSearchResult{Query: query, Hits: []IndexHit{}}
	if limit <= 0 || limit > 80 {
		limit = 40
	}
	slot := f.indexSlot()
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.index == nil {
		result.Status = IndexStatus{State: "not_built"}
		return result
	}
	result.Status = slot.index.status
	if query == "" {
		return result
	}
	result.Hits = rankIndexHits(slot.index, query, limit)
	return result
}

func rankIndexHits(index *projectIndex, query string, limit int) []IndexHit {
	compactQuery := compactToken(query)
	phrase := strings.ToLower(query)
	seen := map[string]bool{}
	hits := make([]IndexHit, 0, limit)
	for _, chunkID := range collectIndexCandidateIDs(index, query, compactQuery) {
		if chunkID < 0 || chunkID >= len(index.chunks) {
			continue
		}
		chunk := index.chunks[chunkID]
		snippet := indexSnippet(chunk.Content)
		pathScore := 0
		if compactQuery != "" && compactToken(filepath.Base(chunk.Path)) == compactQuery {
			pathScore = 80
		} else if compactQuery != "" && strings.Contains(compactToken(filepath.Base(chunk.Path)), compactQuery) {
			pathScore = 36
		} else if len([]rune(phrase)) >= 3 && strings.Contains(strings.ToLower(chunk.Path), phrase) {
			pathScore = 24
		}
		matchedSymbol := false
		for _, symbol := range chunk.Symbols {
			score := 0
			compactSymbol := compactToken(symbol)
			switch {
			case compactQuery != "" && compactSymbol == compactQuery:
				score = 100
			case compactQuery != "" && strings.HasPrefix(compactSymbol, compactQuery):
				score = 72
			case len([]rune(compactQuery)) >= 3 && strings.Contains(compactSymbol, compactQuery):
				score = 40
			}
			if score == 0 {
				continue
			}
			matchedSymbol = true
			addIndexHit(&hits, seen, IndexHit{
				Kind: "symbol", Name: symbol, Path: chunk.Path, Line: chunk.StartLine,
				Language: chunk.Language, Score: score + pathScore, Snippet: snippet,
			})
		}
		contentScore := 0
		if len([]rune(phrase)) >= 3 && strings.Contains(strings.ToLower(chunk.Content), phrase) {
			contentScore = 18
		}
		if !matchedSymbol && (pathScore > 0 || contentScore > 0) {
			name := filepath.Base(chunk.Path)
			if len(chunk.Symbols) > 0 {
				name = chunk.Symbols[0]
			}
			addIndexHit(&hits, seen, IndexHit{
				Kind: "chunk", Name: name, Path: chunk.Path, Line: chunk.StartLine,
				Language: chunk.Language, Score: pathScore + contentScore, Snippet: snippet,
			})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].Path != hits[j].Path {
			return hits[i].Path < hits[j].Path
		}
		return hits[i].Line < hits[j].Line
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

func collectIndexCandidateIDs(index *projectIndex, query, compactQuery string) []int {
	// An exact compact token is emitted for complete identifiers such as
	// RotateRefreshToken. Prefer its posting list over broad component tokens
	// (rotate/refresh/token), which otherwise turn a precise lookup into a
	// full-index scan on large projects.
	if compactQuery != "" {
		if exact := index.tokens[compactQuery]; len(exact) > 0 {
			ids := append([]int(nil), exact...)
			sort.Ints(ids)
			return ids
		}
	}
	seen := map[int]bool{}
	add := func(ids []int) {
		for _, id := range ids {
			if id >= 0 && id < len(index.chunks) {
				seen[id] = true
			}
		}
	}
	for _, token := range expandedTokens(query) {
		add(index.tokens[token])
	}
	if compactQuery != "" {
		add(index.tokens[compactQuery])
	}
	if len(seen) < 24 && len(compactQuery) >= 3 {
		for token, ids := range index.tokens {
			if strings.HasPrefix(token, compactQuery) || (len(compactQuery) >= 4 && strings.Contains(token, compactQuery)) {
				add(ids)
				if len(seen) >= 400 {
					break
				}
			}
		}
	}
	ids := make([]int, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return ids
}

func addIndexHit(hits *[]IndexHit, seen map[string]bool, hit IndexHit) {
	key := hit.Kind + "\n" + hit.Path + "\n" + hit.Name + "\n" + strconv.Itoa(hit.Line)
	if seen[key] {
		return
	}
	seen[key] = true
	*hits = append(*hits, hit)
}

func indexSnippet(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		runes := []rune(line)
		if len(runes) > 160 {
			return string(runes[:160]) + "…"
		}
		return line
	}
	return ""
}

func cloneLanguageCounts(source map[string]int) map[string]int {
	if len(source) == 0 {
		return nil
	}
	out := make(map[string]int, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func (f *FS) RelevantContext(ctx context.Context, query string, maxChunks, maxChars int) ([]RelevantChunk, error) {
	result, err := f.SearchContext(ctx, query, maxChunks, maxChars)
	return result.Chunks, err
}

func (f *FS) SearchContext(ctx context.Context, query string, maxChunks, maxChars int) (ContextSearchResult, error) {
	return f.SearchContextWithRelations(ctx, query, maxChunks, maxChars, false)
}

func (f *FS) SearchContextWithRelations(ctx context.Context, query string, maxChunks, maxChars int, includeRelated bool) (ContextSearchResult, error) {
	if len(query) > 4096 {
		return ContextSearchResult{}, errors.New("context query exceeds 4096 UTF-8 bytes")
	}
	queryTokens := expandedTokens(query)
	if len(queryTokens) == 0 {
		return ContextSearchResult{}, errors.New("context query is empty")
	}
	if len(queryTokens) > 64 {
		return ContextSearchResult{}, errors.New("context query has more than 64 meaningful terms; refine it to a symbol, path, or focused concept")
	}
	if maxChunks <= 0 || maxChunks > 20 {
		maxChunks = 6
	}
	if maxChars <= 0 || maxChars > 64*1024 {
		maxChars = 16 * 1024
	}
	// Attempt 0: search the ready snapshot without a full-tree drift walk.
	// Hits with current digests short-circuit the expensive freshen. Misses and
	// stale digests fall through to ensureFreshIndex / targeted UpdateIndex.
	for attempt := 0; attempt < 3; attempt++ {
		var index *projectIndex
		var err error
		freshened := false
		switch {
		case attempt == 0:
			index = f.peekReadyIndex()
			if index == nil {
				index, err = f.ensureFreshIndex(ctx)
				freshened = true
			}
		default:
			index, err = f.ensureFreshIndex(ctx)
			freshened = true
		}
		if err != nil {
			return ContextSearchResult{}, err
		}
		if index == nil {
			return ContextSearchResult{}, errors.New("project index is unavailable")
		}
		search := selectRelevantChunks(index, query, queryTokens, maxChunks, maxChars)
		if includeRelated {
			search.RelatedFiles, search.RelatedFilesTruncated = selectRelatedFiles(index, search.Chunks, 20)
		}
		if attempt == 0 && !freshened && search.CandidateChunks == 0 {
			// Likely a brand-new file/symbol — walk once, then retry.
			continue
		}
		current, err := f.relevantChunksCurrent(search.Chunks)
		if err != nil {
			return ContextSearchResult{}, err
		}
		if current && includeRelated {
			current, err = f.relatedFilesCurrent(search.RelatedFiles)
			if err != nil {
				return ContextSearchResult{}, err
			}
		}
		if current {
			if !freshened {
				if missing := f.missingIndexedPaths(index); len(missing) > 0 {
					if _, err = f.UpdateIndex(ctx, nil, missing); err != nil {
						return ContextSearchResult{}, err
					}
					continue
				}
			}
			return search, nil
		}
		stale := make([]string, 0, len(search.Chunks)+len(search.RelatedFiles))
		for _, chunk := range search.Chunks {
			stale = append(stale, chunk.Path)
		}
		for _, related := range search.RelatedFiles {
			stale = append(stale, related.Path)
		}
		if len(stale) == 0 {
			return search, nil
		}
		if _, err = f.UpdateIndex(ctx, stale, nil); err != nil {
			return ContextSearchResult{}, err
		}
	}
	return ContextSearchResult{}, errors.New("project changed while indexed context was being prepared; retry the search")
}

func (f *FS) relatedFilesCurrent(files []RelatedFile) (bool, error) {
	for _, related := range files {
		if !validIndexSHA256(related.FileSHA256) {
			return false, nil
		}
		content, err := f.Read(related.Path, false)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		if !strings.EqualFold(content.SHA256, related.FileSHA256) {
			return false, nil
		}
	}
	return true, nil
}

func selectRelevantChunks(index *projectIndex, query string, queryTokens []string, maxChunks, maxChars int) ContextSearchResult {
	scores := map[int]int{}
	matches := map[int]map[string]bool{}
	const (
		partialExactHitFloor = 8  // skip vocab partial scan when exact hits are plentiful
		partialChunkCap      = 64 // cap partial matches per query token
	)
	for _, token := range queryTokens {
		exactChunkIDs := index.tokens[token]
		rarity := 0
		if len(exactChunkIDs) > 0 {
			rarity = min(24, (len(index.chunks)*8)/(len(exactChunkIDs)+1))
		}
		for _, chunkID := range exactChunkIDs {
			scores[chunkID] += 8 + rarity
			if matches[chunkID] == nil {
				matches[chunkID] = map[string]bool{}
			}
			matches[chunkID][token] = true
		}
		// Partial token scan is O(vocab). Skip when the exact posting list is
		// already rich enough, or the token is too short to be discriminative.
		if len(token) < 4 || len(exactChunkIDs) >= partialExactHitFloor {
			continue
		}
		partialChunkIDs := map[int]bool{}
		for indexedToken, chunkIDs := range index.tokens {
			if len(partialChunkIDs) >= partialChunkCap {
				break
			}
			if indexedToken != token && strings.Contains(indexedToken, token) {
				for _, chunkID := range chunkIDs {
					partialChunkIDs[chunkID] = true
					if len(partialChunkIDs) >= partialChunkCap {
						break
					}
				}
			}
		}
		for chunkID := range partialChunkIDs {
			scores[chunkID] += 2
			if matches[chunkID] == nil {
				matches[chunkID] = map[string]bool{}
			}
			matches[chunkID][token] = true
		}
	}
	queryPhrase := strings.ToLower(strings.TrimSpace(query))
	compactQuery := compactToken(query)
	for chunkID := range scores {
		chunk := index.chunks[chunkID]
		if len([]rune(queryPhrase)) >= 3 && strings.Contains(strings.ToLower(chunk.Content), queryPhrase) {
			scores[chunkID] += 24
		}
		path := strings.ToLower(chunk.Path)
		if strings.Contains(path, queryPhrase) || (compactQuery != "" && strings.Contains(compactToken(filepath.Base(path)), compactQuery)) {
			scores[chunkID] += 28
		}
		for _, symbol := range chunk.Symbols {
			compactSymbol := compactToken(symbol)
			switch {
			case compactQuery != "" && compactSymbol == compactQuery:
				scores[chunkID] += 64
			case len([]rune(compactQuery)) >= 4 && strings.Contains(compactSymbol, compactQuery):
				scores[chunkID] += 20
			}
		}
	}
	result := make([]RelevantChunk, 0, len(scores))
	for chunkID, score := range scores {
		if chunkID >= 0 && chunkID < len(index.chunks) {
			matched := make([]string, 0, len(matches[chunkID]))
			for _, token := range queryTokens {
				if matches[chunkID][token] {
					matched = append(matched, token)
				}
			}
			result = append(result, RelevantChunk{IndexedChunk: index.chunks[chunkID], Score: score, MatchedTokens: matched})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Score != result[j].Score {
			return result[i].Score > result[j].Score
		}
		if result[i].Path != result[j].Path {
			return result[i].Path < result[j].Path
		}
		return result[i].StartLine < result[j].StartLine
	})
	search := ContextSearchResult{Chunks: make([]RelevantChunk, 0, min(maxChunks, len(result))), CandidateChunks: len(result), MaxChars: maxChars, MaxChunks: maxChunks}
	type lineRange struct{ start, end int }
	selectedRanges := make(map[string][]lineRange)
	selectedPerPath := make(map[string]int)
	remainingChunks := append([]RelevantChunk(nil), result...)
	for len(remainingChunks) > 0 {
		if len(search.Chunks) >= maxChunks || search.UsedChars >= maxChars {
			search.Truncated = true
			break
		}
		bestIndex, bestAdjustedScore := -1, -1
		for index, candidate := range remainingChunks {
			overlaps := false
			for _, selected := range selectedRanges[candidate.Path] {
				if candidate.StartLine <= selected.end && selected.start <= candidate.EndLine {
					overlaps = true
					break
				}
			}
			if overlaps {
				continue
			}
			adjustedScore := candidate.Score
			if count := selectedPerPath[candidate.Path]; count > 0 {
				adjustedScore = candidate.Score * 3 / (3 + count)
			}
			if adjustedScore > bestAdjustedScore {
				bestIndex, bestAdjustedScore = index, adjustedScore
			}
		}
		if bestIndex < 0 {
			search.Truncated = len(remainingChunks) > 0
			break
		}
		chunk := remainingChunks[bestIndex]
		remainingChunks = append(remainingChunks[:bestIndex], remainingChunks[bestIndex+1:]...)
		remaining := maxChars - search.UsedChars
		if len(chunk.Content) > remaining {
			chunk.Content = utf8Prefix(chunk.Content, remaining)
			chunk.EndLine = chunk.StartLine + strings.Count(chunk.Content, "\n")
			search.Truncated = true
		}
		if chunk.Content == "" {
			search.Truncated = true
			break
		}
		search.UsedChars += len(chunk.Content)
		search.Chunks = append(search.Chunks, chunk)
		selectedPerPath[chunk.Path]++
		selectedRanges[chunk.Path] = append(selectedRanges[chunk.Path], lineRange{start: chunk.StartLine, end: chunk.EndLine})
		for index := len(remainingChunks) - 1; index >= 0; index-- {
			candidate := remainingChunks[index]
			if candidate.Path != chunk.Path {
				continue
			}
			if candidate.StartLine <= chunk.EndLine && chunk.StartLine <= candidate.EndLine {
				remainingChunks = append(remainingChunks[:index], remainingChunks[index+1:]...)
				search.Truncated = true
			}
		}
	}
	search.ReturnedChunks = len(search.Chunks)
	if search.ReturnedChunks < search.CandidateChunks {
		search.Truncated = true
	}
	return search
}

// relevantChunksCurrent сверяет выбранные куски с диском по отпечатку того,
// что индексировалось. Сверять по FileSHA256 нельзя: у файла длиннее лимита
// чтения он пуст с обеих сторон, такой кусок навсегда считался бы устаревшим,
// и поиск контекста упирался бы в потолок попыток вместо ответа.
func (f *FS) relevantChunksCurrent(chunks []RelevantChunk) (bool, error) {
	checked := make(map[string]string)
	for _, chunk := range chunks {
		if !validIndexSHA256(chunk.IndexedSHA256) {
			return false, nil
		}
		if digest, exists := checked[chunk.Path]; exists {
			if !strings.EqualFold(digest, chunk.IndexedSHA256) {
				return false, nil
			}
			continue
		}
		content, err := f.Read(chunk.Path, false)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		current := indexedContentDigest(content)
		if !strings.EqualFold(current, chunk.IndexedSHA256) {
			return false, nil
		}
		checked[chunk.Path] = current
	}
	return true, nil
}

// indexedContentDigest — отпечаток прочитанного содержимого. У целого файла это
// отпечаток файла и есть; у обрезанного — отпечаток той части, которая попала
// в индекс.
func indexedContentDigest(content FileContent) string {
	if validIndexSHA256(content.SHA256) {
		return strings.ToLower(content.SHA256)
	}
	digest := sha256.Sum256([]byte(content.Content))
	return hex.EncodeToString(digest[:])
}

func validIndexSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}

func utf8Prefix(value string, limit int) string {
	if limit >= len(value) {
		return value
	}
	if limit <= 0 {
		return ""
	}
	for limit > 0 && !utf8.ValidString(value[:limit]) {
		limit--
	}
	return value[:limit]
}

func scanLines(value string) []string {
	if value == "" {
		return nil
	}
	lines := strings.SplitAfter(value, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func expandedTokens(value string) []string {
	seen := map[string]bool{}
	result := make([]string, 0)
	add := func(value string) {
		value = strings.ToLower(strings.Trim(value, "_"))
		if len([]rune(value)) < 3 || seen[value] {
			return
		}
		seen[value] = true
		result = append(result, value)
	}
	parts := strings.FieldsFunc(value, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' })
	for _, part := range parts {
		add(part)
		for _, segment := range strings.FieldsFunc(part, func(r rune) bool { return r == '_' }) {
			for _, identifierPart := range identifierParts(segment) {
				add(identifierPart)
			}
		}
	}
	return result
}

func identifierParts(value string) []string {
	runes := []rune(value)
	if len(runes) == 0 {
		return nil
	}
	result := make([]string, 0, 4)
	start := 0
	for index := 1; index < len(runes); index++ {
		previous, current := runes[index-1], runes[index]
		nextIsLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
		boundary := unicode.IsLower(previous) && unicode.IsUpper(current)
		boundary = boundary || (unicode.IsUpper(previous) && unicode.IsUpper(current) && nextIsLower)
		boundary = boundary || (unicode.IsLetter(previous) && unicode.IsDigit(current)) || (unicode.IsDigit(previous) && unicode.IsLetter(current))
		if boundary {
			result = append(result, string(runes[start:index]))
			start = index
		}
	}
	result = append(result, string(runes[start:]))
	return result
}

func compactToken(value string) string {
	var result strings.Builder
	for _, char := range strings.ToLower(value) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			result.WriteRune(char)
		}
	}
	return result.String()
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func extractSymbols(lines []string) []string {
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		prefixes := []string{"func ", "type ", "class ", "interface ", "def ", "function ", "export function ", "export class "}
		for _, prefix := range prefixes {
			if strings.HasPrefix(trimmed, prefix) {
				value := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
				if cut := strings.IndexAny(value, "({:< =\t"); cut >= 0 {
					value = value[:cut]
				}
				if value != "" {
					result = append(result, value)
				}
				break
			}
		}
	}
	return result
}

var languageByExt = map[string]string{
	".go": "Go", ".js": "JavaScript", ".mjs": "JavaScript", ".cjs": "JavaScript",
	".ts": "TypeScript", ".tsx": "TypeScript", ".jsx": "JavaScript",
	".py": "Python", ".rs": "Rust", ".java": "Java", ".kt": "Kotlin", ".cs": "C#",
	".cpp": "C++", ".c": "C", ".h": "C/C++", ".rb": "Ruby", ".php": "PHP",
	".sql": "SQL", ".md": "Markdown", ".json": "JSON", ".yaml": "YAML", ".yml": "YAML",
}

func languageFor(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if value := languageByExt[ext]; value != "" {
		return value
	}
	if ext == "" {
		return "Text"
	}
	return strings.TrimPrefix(ext, ".")
}
