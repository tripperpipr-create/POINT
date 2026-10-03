package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/orchestrator"
	"local-agent-workbench/internal/workspace"
)

func waitFacts(t *testing.T, result <-chan orchestrator.ProjectFacts) orchestrator.ProjectFacts {
	t.Helper()
	select {
	case facts := <-result:
		return facts
	case <-time.After(5 * time.Second):
		t.Fatal("preparation did not finish")
		return orchestrator.ProjectFacts{}
	}
}
func awaitFactsWaiters(t *testing.T, cache *masterFactsCache, key masterFactsKey, count int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		cache.mu.Lock()
		flight := cache.flights[key]
		actual := 0
		if flight != nil {
			actual = flight.waiters
		}
		cache.mu.Unlock()
		if actual == count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("waiters did not join shared preparation")
}

func TestMasterFactsCoalescesWithoutBlockingOtherProjects(t *testing.T) {
	var cache masterFactsCache
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	compute := func(ctx context.Context) orchestrator.ProjectFacts {
		calls.Add(1)
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return orchestrator.ProjectFacts{Sources: []string{"tree"}}
	}
	key := masterFactsKey{workspaceID: "a"}
	generation := func() uint64 { return 0 }
	owner, cancel := context.WithCancel(context.Background())
	first, second := make(chan orchestrator.ProjectFacts, 1), make(chan orchestrator.ProjectFacts, 1)
	go func() { first <- cache.get(owner, key, generation, compute) }()
	<-started
	go func() { second <- cache.get(context.Background(), key, generation, compute) }()
	awaitFactsWaiters(t, &cache, key, 2)
	other := cache.get(context.Background(), masterFactsKey{workspaceID: "b"}, generation, func(context.Context) orchestrator.ProjectFacts { return orchestrator.ProjectFacts{Name: "b"} })
	if other.Name != "b" {
		t.Fatal(other)
	}
	cancel()
	waitFacts(t, first)
	close(release)
	facts := waitFacts(t, second)
	facts.Sources[0] = "mutated"
	cached := cache.get(context.Background(), key, generation, compute)
	if calls.Load() != 1 || cached.Sources[0] != "tree" {
		t.Fatalf("calls=%d facts=%+v", calls.Load(), cached)
	}
}

func TestMasterFactsCancelledWorkAndGenerationChangeAreNotCached(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			var cache masterFactsCache
			var generation atomic.Uint64
			var calls atomic.Int32
			started, release := make(chan struct{}), make(chan struct{})
			compute := func(ctx context.Context) orchestrator.ProjectFacts {
				calls.Add(1)
				close(started)
				if cancelled {
					<-ctx.Done()
				} else {
					<-release
				}
				return orchestrator.ProjectFacts{Name: "old"}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan orchestrator.ProjectFacts, 1)
			key := masterFactsKey{workspaceID: "a"}
			go func() { result <- cache.get(ctx, key, generation.Load, compute) }()
			<-started
			cache.mu.Lock()
			flight := cache.flights[key]
			cache.mu.Unlock()
			if cancelled {
				cancel()
			} else {
				generation.Add(1)
				close(release)
			}
			waitFacts(t, result)
			select {
			case <-flight.done:
			case <-time.After(5 * time.Second):
				t.Fatal("cancelled preparation still running")
			}
			cache.mu.Lock()
			count := len(cache.entries)
			cache.mu.Unlock()
			if count != 0 {
				t.Fatal("obsolete work was cached")
			}
			next := cache.get(context.Background(), masterFactsKey{workspaceID: "a", generation: generation.Load()}, generation.Load, func(context.Context) orchestrator.ProjectFacts {
				calls.Add(1)
				return orchestrator.ProjectFacts{Name: "new"}
			})
			if next.Name != "new" || calls.Load() != 2 {
				t.Fatalf("%+v calls=%d", next, calls.Load())
			}
		})
	}
}

func TestMasterFactsTTLAndLRU(t *testing.T) {
	var cache masterFactsCache
	var calls int
	generation := func() uint64 { return 0 }
	compute := func(context.Context) orchestrator.ProjectFacts {
		calls++
		return orchestrator.ProjectFacts{Name: fmt.Sprint(calls)}
	}
	key := func(i int) masterFactsKey { return masterFactsKey{workspaceID: fmt.Sprint(i)} }
	for i := 0; i < 16; i++ {
		cache.get(context.Background(), key(i), generation, compute)
	}
	cache.get(context.Background(), key(0), generation, compute)
	cache.get(context.Background(), key(16), generation, compute)
	cache.mu.Lock()
	_, oldest := cache.entries[key(1)]
	_, recent := cache.entries[key(0)]
	entry := cache.entries[key(0)]
	entry.at = time.Now().Add(-masterFactsTTL)
	cache.entries[key(0)] = entry
	size := len(cache.entries)
	cache.mu.Unlock()
	if oldest || !recent || size != 16 {
		t.Fatalf("oldest=%v recent=%v size=%d", oldest, recent, size)
	}
	cache.get(context.Background(), key(0), generation, compute)
	if calls != 18 {
		t.Fatalf("TTL did not recompute: %d", calls)
	}
}

func TestMasterFactsPinnedWorkspaceReusesAcrossFSInstances(t *testing.T) {
	a := newTestApp(t)
	w := openTestWorld(t, a)
	ctx, err := a.WithMasterWorkspace(context.Background(), w.ID)
	if err != nil {
		t.Fatal(err)
	}
	fs := ctx.Value(masterScopeKey{}).(masterScope).FS
	if _, err = fs.BuildIndex(ctx); err != nil {
		t.Fatal(err)
	}
	first := a.masterProjectFacts(ctx)
	if first.Name != w.Name {
		t.Fatal(first)
	}
	a.masterFacts.mu.Lock()
	var key masterFactsKey
	var before time.Time
	for k, e := range a.masterFacts.entries {
		key, before = k, e.at
	}
	a.masterFacts.mu.Unlock()
	reopened, err := workspace.Open(w.Path)
	if err != nil {
		t.Fatal(err)
	}
	ctx = context.WithValue(ctx, masterScopeKey{}, masterScope{Workspace: w, FS: reopened})
	second := a.masterProjectFacts(ctx)
	a.masterFacts.mu.Lock()
	after := a.masterFacts.entries[key].at
	a.masterFacts.mu.Unlock()
	if second.Name != w.Name || before.IsZero() || !before.Equal(after) {
		t.Fatal("pinned v2 request prepared facts again")
	}
	reopened.InvalidateIndex()
	a.masterProjectFacts(ctx)
	a.masterFacts.mu.Lock()
	usedNew := false
	for k := range a.masterFacts.entries {
		if k.generation != key.generation {
			usedNew = true
		}
	}
	a.masterFacts.mu.Unlock()
	// Background build can change generation during preparation; a subsequent
	// stable request must populate its own generation, never the original one.
	if !usedNew {
		a.masterProjectFacts(ctx)
		a.masterFacts.mu.Lock()
		for k := range a.masterFacts.entries {
			if k.generation != key.generation {
				usedNew = true
			}
		}
		a.masterFacts.mu.Unlock()
	}
	if !usedNew {
		t.Fatal("index invalidation reused old facts")
	}
}

func BenchmarkMasterProjectFacts10000(b *testing.B) {
	root := b.TempDir()
	for dir := 0; dir < 100; dir++ {
		path := filepath.Join(root, fmt.Sprintf("module%03d", dir))
		if err := os.Mkdir(path, 0700); err != nil {
			b.Fatal(err)
		}
		for file := 0; file < 100; file++ {
			if err := os.WriteFile(filepath.Join(path, fmt.Sprintf("file%03d.go", file)), []byte("package sample\nfunc Entry() {}\n"), 0600); err != nil {
				b.Fatal(err)
			}
		}
	}
	fs, err := workspace.Open(root)
	if err != nil {
		b.Fatal(err)
	}
	status, err := fs.BuildIndex(context.Background())
	if err != nil || status.Files != 10000 {
		b.Fatalf("synthetic index: %+v %v", status, err)
	}
	a := &App{currentWorkspace: &domain.Workspace{ID: "benchmark", Name: "synthetic", Path: root}, currentFS: fs}
	ctx := context.WithValue(context.Background(), masterScopeKey{}, masterScope{Workspace: *a.currentWorkspace, FS: fs})
	for _, mode := range []string{"cold", "repeat", "after_index_update"} {
		b.Run(mode, func(b *testing.B) {
			a.masterProjectFacts(ctx)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if mode != "repeat" {
					b.StopTimer()
					if mode == "cold" {
						a.masterFacts.mu.Lock()
						clear(a.masterFacts.entries)
						a.masterFacts.mu.Unlock()
					} else {
						if err := os.WriteFile(filepath.Join(root, "module000", "file000.go"), []byte(fmt.Sprintf("package sample\nfunc Entry%d() {}\n", i)), 0600); err != nil {
							b.Fatal(err)
						}
						if _, err := fs.UpdateIndex(ctx, []string{"module000/file000.go"}, nil); err != nil {
							b.Fatal(err)
						}
					}
					b.StartTimer()
				}
				facts := a.masterProjectFacts(ctx)
				if facts.Files != 10000 {
					b.Fatal(facts.Files)
				}
			}
		})
	}
}
