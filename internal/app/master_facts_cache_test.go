package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Факты не строят индекс в запросе: открытие истории на большом проекте
// упиралось в 20-секундный таймаут, отмена обрывала сборку, и следующий запрос
// начинал её заново. Индекс достраивается в фоне и отмены запроса не видит.
func TestMasterFactsWarmIndexOutsideRequest(t *testing.T) {
	application := newTestApp(t)
	world := openTestWorld(t, application)
	if err := os.WriteFile(filepath.Join(world.Path, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs, err := application.fs()
	if err != nil {
		t.Fatal(err)
	}
	fs.InvalidateIndex()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	facts := application.computeMasterProjectFacts(ctx)
	if facts.IndexState != "building" {
		t.Fatalf("без готового индекса ждали building, получили %q", facts.IndexState)
	}
	deadline := time.Now().Add(10 * time.Second)
	for fs.IndexStatus().State != "ready" {
		if time.Now().After(deadline) {
			t.Fatalf("фоновая сборка не завершилась: %q", fs.IndexStatus().State)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if facts = application.computeMasterProjectFacts(ctx); facts.IndexState != "ready" || facts.Files == 0 {
		t.Fatalf("после фоновой сборки факты не видят индекс: %+v", facts)
	}
}

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
