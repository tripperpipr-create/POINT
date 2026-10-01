package app

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/observability"
)

// questCacheBackend — бэкенд, держащий кэши пакетных менеджеров на квест
// (sandbox/cache_volumes.go).
type questCacheBackend interface {
	CacheScopes(ctx context.Context) ([]string, error)
	RemoveCacheVolumes(ctx context.Context, scope string) error
}

// cleanupFinishedQuestCaches снимает тома кэша квестов, которые закончены или
// удалены. База общая для миров, поэтому решение принимается только по
// состоянию самого квеста: живой квест соседнего мира не теряет кэш, а
// законченному он больше не нужен ни в каком мире.
func (a *App) cleanupFinishedQuestCaches(ctx context.Context) {
	backend, ok := a.sandboxBackend.(questCacheBackend)
	if !ok || a.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	scopes, err := backend.CacheScopes(ctx)
	if err != nil {
		observability.From(ctx).Warn("quest cache scan failed", "error", err)
		return
	}
	for _, scope := range scopes {
		quest, questErr := a.store.GetQuest(ctx, scope)
		switch {
		case errors.Is(questErr, sql.ErrNoRows):
		case questErr != nil:
			continue
		case !domain.IsTerminalQuestStatus(quest.Status):
			continue
		}
		if err = backend.RemoveCacheVolumes(ctx, scope); err != nil {
			observability.From(ctx).Warn("quest cache cleanup failed", "quest_id", scope, "error", err)
		}
	}
}
