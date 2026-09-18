package app

import (
	"context"
	"encoding/json"
	"time"
)

func (a *App) CurrentWorkspaceID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.currentWorkspace == nil {
		return ""
	}
	return a.currentWorkspace.ID
}

func (a *App) cachePrefix() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.currentWorkspace == nil {
		return "workspace:none:"
	}
	return "workspace:" + a.currentWorkspace.ID + ":"
}

func (a *App) cacheGet(key string, target any) bool {
	data, ok, err := a.cache.Get(context.Background(), a.cachePrefix()+key)
	return err == nil && ok && json.Unmarshal(data, target) == nil
}

func (a *App) cacheSet(key string, value any, ttl time.Duration) {
	data, err := json.Marshal(value)
	if err == nil {
		_ = a.cache.Set(context.Background(), a.cachePrefix()+key, data, ttl)
	}
}
