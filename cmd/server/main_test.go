package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestEnvFallsBackOnlyWhenUnset(t *testing.T) {
	t.Setenv("POINT_TEST_ENV", "")
	if got := env("POINT_TEST_ENV", "по умолчанию"); got != "по умолчанию" {
		t.Fatalf("пустая переменная равнозначна незаданной: %q", got)
	}
	t.Setenv("POINT_TEST_ENV", "задано")
	if got := env("POINT_TEST_ENV", "по умолчанию"); got != "задано" {
		t.Fatalf("заданная переменная сильнее умолчания: %q", got)
	}
}

// Порядок запуска ядра держится на двух причинах, записанных комментариями, и
// обе ломаются одной перестановкой строк.
//
// Слушать надо до OpenWorkspace: проверка здоровья из IDE не должна ждать
// обхода большого дерева, иначе расширение решит, что ядро не поднялось.
// Startup — до того, как порт открыт: он приводит в порядок квесты, брошенные
// остановленным ядром, и без него первый же запрос увидит их «идущими».
//
// Проверка смотрит на форму исходника, потому что поднять настоящий main в
// тесте нельзя: он ждёт сигнала и не возвращается.
func TestStartupOrderKeepsHealthCheckReachable(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	startup := strings.Index(text, "application.Startup(")
	listen := strings.Index(text, "server.ListenAndServe()")
	openWorkspace := strings.Index(text, "application.OpenWorkspace(")
	for name, at := range map[string]int{
		"application.Startup":       startup,
		"server.ListenAndServe":     listen,
		"application.OpenWorkspace": openWorkspace,
	} {
		if at < 0 {
			t.Fatalf("%s не найден — проверка прошла бы вхолостую", name)
		}
	}
	if startup > listen {
		t.Error("Startup обязан пройти до открытия порта: иначе первый запрос увидит брошенные квесты идущими")
	}
	if listen > openWorkspace {
		t.Error("слушать надо до OpenWorkspace: проверка здоровья не должна ждать обхода большого дерева")
	}
}

// Токен доступа читается до того, как что-либо поднято: без него сервер
// поднимать нечем, и падать надо на первом шаге, а не после инициализации.
func TestAPITokenResolvedBeforeApplication(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	token := strings.Index(text, "httpapi.ResolveAPIToken(")
	application := strings.Index(text, "app.New(")
	if token < 0 || application < 0 {
		t.Fatal("не найдены ResolveAPIToken или app.New — проверка прошла бы вхолостую")
	}
	if token > application {
		t.Error("токен разрешается до создания приложения: отказ должен случиться до инициализации, а не после")
	}
}

// Ядро живёт, пока открыт хоть один Point: свежая отметка окна или аренда
// держат его, старая, будущая и чужой файл — нет.
func TestOwnersAliveReadsOnlyFreshWindowMarks(t *testing.T) {
	dir := t.TempDir()
	now := time.UnixMilli(1_800_000_000_000)
	write := func(name string, updatedAt int64) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(fmt.Sprintf(`{"pid":1,"updatedAt":%d}`, updatedAt)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if ownersAlive(dir, now) {
		t.Fatal("пустой каталог подтвердил хозяина")
	}
	if ownersAlive(filepath.Join(dir, "нет"), now) {
		t.Fatal("несуществующий каталог подтвердил хозяина")
	}
	write("host-1.json", now.Add(-time.Minute).UnixMilli())
	write("core-abc.json", now.UnixMilli())
	write("host-2.json", now.Add(time.Hour).UnixMilli())
	if ownersAlive(dir, now) {
		t.Fatal("старая, будущая или чужая отметка подтвердила хозяина")
	}
	write("lease-abc-1.json", now.Add(-5*time.Second).UnixMilli())
	if !ownersAlive(dir, now) {
		t.Fatal("свежая аренда не подтвердила хозяина")
	}
	if err := os.Remove(filepath.Join(dir, "lease-abc-1.json")); err != nil {
		t.Fatal(err)
	}
	write("host-3.json", now.Add(-3*time.Second).UnixMilli())
	if !ownersAlive(dir, now) {
		t.Fatal("свежая отметка окна не подтвердила хозяина")
	}
}

// Без хозяев ядро выходит ровно после grace — не раньше: смена проекта в
// Чертоге на мгновение оставляет каталог без свежих отметок. Шаг проверяется
// без таймера: тест на настоящих тиках пропускал поломку на Windows, где
// таймер успевал сработать уже после проверки.
func TestOwnerCheckWaitsFullGraceWithoutOwners(t *testing.T) {
	dir := t.TempDir()
	start := time.UnixMilli(1_800_000_000_000)
	grace := 30 * time.Second
	if err := os.WriteFile(filepath.Join(dir, "host-7.json"), []byte(fmt.Sprintf(`{"pid":7,"updatedAt":%d}`, start.UnixMilli())), 0o600); err != nil {
		t.Fatal(err)
	}
	lastSeen, gone := ownerCheck(dir, start.Add(10*time.Second), start, grace)
	if gone || !lastSeen.Equal(start.Add(10*time.Second)) {
		t.Fatalf("живое окно: gone=%v lastSeen=%v", gone, lastSeen)
	}
	// Окно закрылось: отметка устарела, но grace от последней встречи не истёк.
	if seen, gone := ownerCheck(dir, start.Add(35*time.Second), lastSeen, grace); gone || !seen.Equal(lastSeen) {
		t.Fatalf("выход раньше grace: gone=%v lastSeen=%v", gone, seen)
	}
	if _, gone := ownerCheck(dir, start.Add(39*time.Second), lastSeen, grace); gone {
		t.Fatal("выход за секунду до grace")
	}
	if _, gone := ownerCheck(dir, start.Add(40*time.Second), lastSeen, grace); !gone {
		t.Fatal("нет выхода ровно по истечении grace")
	}
}

// Сама горутина: без хозяев канал закрывается, при свежей отметке — нет.
// Запас по времени широкий в обе стороны, чтобы не зависеть от таймера ОС.
func TestWatchOwnersClosesWithoutOwnersOnly(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	empty := t.TempDir()
	select {
	case <-watchOwners(ctx, empty, 20*time.Millisecond, 5*time.Millisecond, time.Now, logger):
	case <-time.After(5 * time.Second):
		t.Fatal("ядро без хозяев не вышло")
	}
	owned := t.TempDir()
	if err := os.WriteFile(filepath.Join(owned, "host-9.json"), []byte(fmt.Sprintf(`{"pid":9,"updatedAt":%d}`, time.Now().UnixMilli())), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-watchOwners(ctx, owned, 20*time.Millisecond, 5*time.Millisecond, time.Now, logger):
		t.Fatal("ядро вышло при живом окне")
	case <-time.After(300 * time.Millisecond):
	}
}

// Q14: повторный сигнал во время остановки попадает в журнал с номером.
func TestLaterSignalsAreLogged(t *testing.T) {
	var buffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buffer, nil))
	signals := make(chan os.Signal, 2)
	signals <- syscall.SIGTERM
	close(signals)
	logLaterSignals(signals, time.Now(), logger)
	line := buffer.String()
	for _, want := range []string{"signal during shutdown", "signal=terminated", "count=2", "ppid="} {
		if !strings.Contains(line, want) {
			t.Fatalf("log lacks %q: %s", want, line)
		}
	}
}
