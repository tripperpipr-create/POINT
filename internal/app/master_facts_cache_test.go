package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Кэш фактов отдаёт копию: Situation дописывает в срезы фактов квесты и
// проверки хода, и без копии они уезжали бы в следующий ход чужими.
func TestMasterFactsCacheReturnsIndependentCopies(t *testing.T) {
	application := newTestApp(t)
	world := openTestWorld(t, application)
	if err := os.WriteFile(filepath.Join(world.Path, "package.json"), []byte(`{"scripts":{"test":"vitest"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first := application.masterProjectFacts(ctx)
	first.ActiveQuests = append(first.ActiveQuests, "чужой квест")
	first.Sources = append(first.Sources[:0], "подменённый источник")
	second := application.masterProjectFacts(ctx)
	if len(second.ActiveQuests) != 0 {
		t.Fatalf("дописанное одним ходом уехало в другой: %q", second.ActiveQuests)
	}
	for _, source := range second.Sources {
		if source == "подменённый источник" {
			t.Fatal("кэш отдал общий срез источников")
		}
	}
	if second.Name != world.Name {
		t.Fatalf("факты другого проекта: %q", second.Name)
	}
}
