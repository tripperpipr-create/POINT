package workspace

import (
	"errors"
	"os"
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
