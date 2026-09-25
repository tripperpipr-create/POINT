package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"local-agent-workbench/internal/orchestrator"
	"local-agent-workbench/internal/workspace"
)

// Факты проекта для Мастера кэшируются ненадолго.
//
// Их сборка — это карта индекса, обход дерева на три уровня и чтение
// манифестов сборки, и шла она синхронно в каждом старте хода и в каждом
// открытии истории разговора: человек ждал её раньше, чем модель получала
// вопрос. Ключ — состояние индекса: перестроенный индекс сбрасывает кэш сразу,
// а срок жизни ловит то, чего индекс не видит, — новые манифесты и каталоги.
const masterFactsTTL = 30 * time.Second

type masterFactsCache struct {
	mu    sync.Mutex
	fs    *workspace.FS
	key   string
	at    time.Time
	facts orchestrator.ProjectFacts
}

func (a *App) masterProjectFacts(ctx context.Context) orchestrator.ProjectFacts {
	a.mu.RLock()
	fs := a.currentFS
	name := ""
	if a.currentWorkspace != nil {
		name = a.currentWorkspace.Name
	}
	a.mu.RUnlock()
	if fs == nil {
		return a.computeMasterProjectFacts(ctx)
	}
	status := fs.IndexStatus()
	key := fmt.Sprintf("%s|%s|%s|%d|%d", name, status.State, status.BuiltAt.UTC().Format(time.RFC3339Nano), status.Files, status.Symbols)
	cache := &a.masterFacts
	cache.mu.Lock()
	if cache.fs == fs && cache.key == key && time.Since(cache.at) < masterFactsTTL {
		facts := cloneMasterFacts(cache.facts)
		cache.mu.Unlock()
		return facts
	}
	cache.mu.Unlock()
	facts := a.computeMasterProjectFacts(ctx)
	cache.mu.Lock()
	cache.fs, cache.key, cache.at, cache.facts = fs, key, time.Now(), cloneMasterFacts(facts)
	cache.mu.Unlock()
	return facts
}

// Кэш отдаёт копию: срезы фактов общие, и дописанный кем-то источник уехал бы
// в чужой ход.
func cloneMasterFacts(facts orchestrator.ProjectFacts) orchestrator.ProjectFacts {
	clone := func(values []string) []string { return append([]string(nil), values...) }
	facts.Languages, facts.Modules, facts.Entrypoints = clone(facts.Languages), clone(facts.Modules), clone(facts.Entrypoints)
	facts.BuildCommands, facts.TestCommands, facts.KeySymbols = clone(facts.BuildCommands), clone(facts.TestCommands), clone(facts.KeySymbols)
	facts.ActiveQuests, facts.RecentChecks, facts.RecentChangeSets = clone(facts.ActiveQuests), clone(facts.RecentChecks), clone(facts.RecentChangeSets)
	facts.Sources = clone(facts.Sources)
	return facts
}
