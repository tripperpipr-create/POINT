package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
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
	mu         sync.Mutex
	index      *projectIndex
	generation uint64
	// warming закрывается, когда фоновая сборка закончится; пока он не nil,
	// вторая сборка на тот же корень не запускается.
	warming chan struct{}
}

// WarmIndexInBackground запускает полную сборку индекса отдельно от вызвавшего
// запроса: его отмена сборку не обрывает. Если сборка уже идёт, возвращает её
// канал, если индекс готов — nil. done вызывается только у запустившего.
func (f *FS) WarmIndexInBackground(timeout time.Duration, done func(IndexStatus, error)) <-chan struct{} {
	slot := f.indexSlot()
	slot.mu.Lock()
	if slot.warming != nil {
		finished := slot.warming
		slot.mu.Unlock()
		return finished
	}
	if slot.index != nil && slot.index.status.State == "ready" {
		slot.mu.Unlock()
		return nil
	}
	finished := make(chan struct{})
	slot.warming = finished
	slot.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		status, err := f.BuildIndex(ctx)
		slot.mu.Lock()
		slot.warming = nil
		slot.mu.Unlock()
		close(finished)
		if done != nil {
			done(status, err)
		}
	}()
	return finished
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
//
// Проверяется только существование, поэтому хватает Lstat по пути внутри
// корня, без полного Resolve со сверкой ссылок: на Windows тот стоил ~1 мс на
// файл, и каждый search_code в cf-bitrix (16 тысяч файлов в индексе) ждал
// 15–16 с (замер 03.10). Файлы проверяются параллельно.
func (f *FS) missingIndexedPaths(index *projectIndex) []string {
	if index == nil || len(index.files) == 0 {
		return nil
	}
	paths := make([]string, 0, len(index.files))
	for relative := range index.files {
		paths = append(paths, relative)
	}
	workers := min(8, max(1, len(paths)/256))
	found := make([][]string, workers)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for index := worker; index < len(paths); index += workers {
				if _, err := os.Lstat(filepath.Join(f.root, filepath.FromSlash(paths[index]))); errors.Is(err, os.ErrNotExist) {
					found[worker] = append(found[worker], paths[index])
				}
			}
		}(worker)
	}
	wg.Wait()
	missing := make([]string, 0)
	for _, part := range found {
		missing = append(missing, part...)
	}
	return missing
}

func (f *FS) indexSlot() *indexSlot {
	root := f.indexRoot
	if root == "" {
		root = f.root
	}
	value, _ := indexes.LoadOrStore(root, &indexSlot{})
	return value.(*indexSlot)
}

// IndexGeneration identifies published snapshots and invalidations within this process.
// It is shared by FS instances opened on the same root, and is not an API field.
func (f *FS) IndexGeneration() uint64 {
	slot := f.indexSlot()
	slot.mu.Lock()
	defer slot.mu.Unlock()
	return slot.generation
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
	if f.MutationHook != nil {
		f.MutationHook()
	}
	slot := f.indexSlot()
	slot.mu.Lock()
	defer slot.mu.Unlock()
	slot.generation++
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
