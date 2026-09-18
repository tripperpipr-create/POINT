package cache

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type Cache interface {
	Get(context.Context, string) ([]byte, bool, error)
	Set(context.Context, string, []byte, time.Duration) error
	DeletePrefix(context.Context, string) error
	Close() error
}

type entry struct {
	value   []byte
	expires time.Time
}
type Memory struct {
	mu    sync.RWMutex
	items map[string]entry
}

func NewMemory() *Memory { return &Memory{items: make(map[string]entry)} }
func (m *Memory) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.RLock()
	item, ok := m.items[key]
	m.mu.RUnlock()
	if !ok {
		return nil, false, nil
	}
	if time.Now().After(item.expires) {
		m.mu.Lock()
		delete(m.items, key)
		m.mu.Unlock()
		return nil, false, nil
	}
	return append([]byte(nil), item.value...), true, nil
}
func (m *Memory) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	m.items[key] = entry{value: append([]byte(nil), value...), expires: time.Now().Add(ttl)}
	m.mu.Unlock()
	return nil
}
func (m *Memory) DeletePrefix(_ context.Context, prefix string) error {
	m.mu.Lock()
	for key := range m.items {
		if strings.HasPrefix(key, prefix) {
			delete(m.items, key)
		}
	}
	m.mu.Unlock()
	return nil
}
func (m *Memory) Close() error { return nil }

type Redis struct{ client *redis.Client }

func NewRedis(ctx context.Context, address string) (*Redis, error) {
	client := redis.NewClient(&redis.Options{Addr: address, Password: os.Getenv("REDIS_PASSWORD")})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return &Redis{client: client}, nil
}
func (r *Redis) Get(ctx context.Context, key string) ([]byte, bool, error) {
	value, err := r.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	return value, err == nil, err
}
func (r *Redis) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return r.client.Set(ctx, key, value, ttl).Err()
}
func (r *Redis) DeletePrefix(ctx context.Context, prefix string) error {
	var cursor uint64
	for {
		keys, next, err := r.client.Scan(ctx, cursor, prefix+"*", 100).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err = r.client.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		cursor = next
		if cursor == 0 {
			return nil
		}
	}
}
func (r *Redis) Close() error { return r.client.Close() }

func FromEnvironment(ctx context.Context) (Cache, error) {
	address := strings.TrimSpace(os.Getenv("REDIS_ADDR"))
	if address == "" {
		return NewMemory(), nil
	}
	return NewRedis(ctx, address)
}
