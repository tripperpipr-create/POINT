package app

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"local-agent-workbench/internal/orchestrator"
)

const masterFactsTTL = 30 * time.Second
const masterFactsCapacity = 16

type masterFactsKey struct {
	workspaceID, root, name string
	generation              uint64
}
type masterFactsEntry struct {
	at    time.Time
	used  uint64
	facts orchestrator.ProjectFacts
}
type masterFactsFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	facts   orchestrator.ProjectFacts
}
type masterFactsCache struct {
	mu      sync.Mutex
	entries map[masterFactsKey]masterFactsEntry
	flights map[masterFactsKey]*masterFactsFlight
	used    uint64
}

func (a *App) masterProjectFacts(ctx context.Context) orchestrator.ProjectFacts {
	scope, pinned := ctx.Value(masterScopeKey{}).(masterScope)
	if !pinned {
		a.mu.RLock()
		scope.FS = a.currentFS
		if a.currentWorkspace != nil {
			scope.Workspace = *a.currentWorkspace
		}
		a.mu.RUnlock()
		// Pin the legacy snapshot too: switching projects during preparation must
		// never publish the new project's facts under the old project's key.
		ctx = context.WithValue(ctx, masterScopeKey{}, scope)
	}
	if scope.FS == nil {
		return a.computeMasterProjectFacts(ctx)
	}
	root := filepath.Clean(scope.FS.Root())
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
	}
	key := masterFactsKey{scope.Workspace.ID, root, scope.Workspace.Name, scope.FS.IndexGeneration()}
	return a.masterFacts.get(ctx, key, scope.FS.IndexGeneration, a.computeMasterProjectFacts)
}

// get coalesces preparation per key. Each waiter owns one reference; only the
// departure of the last waiter cancels shared work. Unrelated keys never wait
// on filesystem work under the cache lock.
func (c *masterFactsCache) get(ctx context.Context, key masterFactsKey, generation func() uint64, compute func(context.Context) orchestrator.ProjectFacts) orchestrator.ProjectFacts {
	if ctx.Err() != nil {
		return orchestrator.ProjectFacts{}
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[masterFactsKey]masterFactsEntry)
	}
	if c.flights == nil {
		c.flights = make(map[masterFactsKey]*masterFactsFlight)
	}
	c.used++
	if entry, ok := c.entries[key]; ok && time.Since(entry.at) < masterFactsTTL {
		entry.used = c.used
		c.entries[key] = entry
		facts := cloneMasterFacts(entry.facts)
		c.mu.Unlock()
		return facts
	}
	delete(c.entries, key)
	flight := c.flights[key]
	if flight == nil {
		workCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		flight = &masterFactsFlight{done: make(chan struct{}), cancel: cancel}
		c.flights[key] = flight
		go func() {
			facts := compute(workCtx)
			c.mu.Lock()
			flight.facts = cloneMasterFacts(facts)
			if workCtx.Err() == nil && generation() == key.generation {
				c.used++
				c.entries[key] = masterFactsEntry{time.Now(), c.used, cloneMasterFacts(facts)}
				if len(c.entries) > masterFactsCapacity {
					var oldest masterFactsKey
					used := ^uint64(0)
					for k, entry := range c.entries {
						if entry.used < used {
							oldest, used = k, entry.used
						}
					}
					delete(c.entries, oldest)
				}
			}
			if c.flights[key] == flight {
				delete(c.flights, key)
			}
			close(flight.done)
			c.mu.Unlock()
			cancel()
		}()
	}
	flight.waiters++
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		c.mu.Lock()
		flight.waiters--
		if flight.waiters == 0 {
			flight.cancel()
			if c.flights[key] == flight {
				delete(c.flights, key)
			}
		}
		c.mu.Unlock()
		return orchestrator.ProjectFacts{}
	case <-flight.done:
		return cloneMasterFacts(flight.facts)
	}
}

func cloneMasterFacts(facts orchestrator.ProjectFacts) orchestrator.ProjectFacts {
	clone := func(values []string) []string { return append([]string(nil), values...) }
	facts.Languages, facts.Modules, facts.Entrypoints = clone(facts.Languages), clone(facts.Modules), clone(facts.Entrypoints)
	facts.BuildCommands, facts.TestCommands, facts.KeySymbols = clone(facts.BuildCommands), clone(facts.TestCommands), clone(facts.KeySymbols)
	facts.ActiveQuests, facts.RecentChecks, facts.RecentChangeSets = clone(facts.ActiveQuests), clone(facts.RecentChecks), clone(facts.RecentChangeSets)
	facts.Sources = clone(facts.Sources)
	return facts
}
