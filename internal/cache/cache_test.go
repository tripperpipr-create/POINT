package cache

import (
	"context"
	"testing"
	"time"
)

func TestMemoryTTLAndPrefixInvalidation(t *testing.T) {
	ctx := context.Background()
	c := NewMemory()
	if err := c.Set(ctx, "workspace:a:file", []byte("one"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := c.Set(ctx, "other", []byte("two"), time.Minute); err != nil {
		t.Fatal(err)
	}
	value, ok, _ := c.Get(ctx, "workspace:a:file")
	if !ok || string(value) != "one" {
		t.Fatalf("bad cached value %q", value)
	}
	if err := c.DeletePrefix(ctx, "workspace:a:"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ = c.Get(ctx, "workspace:a:file"); ok {
		t.Fatal("prefix was not invalidated")
	}
	if _, ok, _ = c.Get(ctx, "other"); !ok {
		t.Fatal("unrelated cache entry was removed")
	}
	_ = c.Set(ctx, "short", []byte("x"), time.Nanosecond)
	time.Sleep(time.Millisecond)
	if _, ok, _ = c.Get(ctx, "short"); ok {
		t.Fatal("expired value returned")
	}
}
