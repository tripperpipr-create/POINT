package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// ollamaDials считает попытки тестов достучаться до ollama разработчика.
// Отказ подключения тест проглатывал и оставался зелёным, а обращение к
// настоящей модели жило незамеченным; теперь любая такая попытка роняет пакет.
var ollamaDials atomic.Int64

func appTestDialGuard(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("unexpected network request in internal/app tests: %s", address)
	}
	host = strings.TrimSpace(host)
	loopback := strings.EqualFold(host, "localhost")
	if parsed := net.ParseIP(host); parsed != nil && parsed.IsLoopback() {
		loopback = true
	}
	if port == "11434" {
		ollamaDials.Add(1)
	}
	if !loopback || port == "11434" {
		return nil, fmt.Errorf("unexpected network request in internal/app tests: %s", address)
	}
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

func TestMain(m *testing.M) {
	// Поддельный MCP-сервер — этот же тестовый бинарь (mcp_servers_test.go).
	if mode := os.Getenv("POINT_FAKE_MCP"); mode != "" {
		runFakeMCPServer(mode)
		os.Exit(0)
	}
	baseTransport := http.DefaultTransport
	guardedTransport := baseTransport.(*http.Transport).Clone()
	guardedTransport.DialContext = appTestDialGuard
	http.DefaultTransport = guardedTransport
	defer func() { http.DefaultTransport = baseTransport }()
	// Package tests historically assume isolated sandbox copies. Live workspace
	// is the Point IDE default (extension sets POINT_LIVE_WORKSPACE=1); opt into
	// it per test when covering Cursor-style writes.
	if os.Getenv("POINT_LIVE_WORKSPACE") == "" {
		_ = os.Setenv("POINT_LIVE_WORKSPACE", "0")
	}
	if os.Getenv("POINT_FILE_ISOLATION") == "" {
		_ = os.Setenv("POINT_FILE_ISOLATION", "sandbox")
	}
	sandboxes, err := os.MkdirTemp("", "point-app-sandboxes-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = os.Setenv("POINT_SANDBOX_ROOT", sandboxes)
	code := m.Run()
	_ = os.RemoveAll(sandboxes)
	// Одну попытку делает сам TestOrdinaryTestsRejectDeveloperOllamaAndAllowManagedFake —
	// он проверяет сторож; всё сверх неё — тест, который идёт к настоящей модели.
	if dials := ollamaDials.Load(); dials > expectedOllamaDials.Load() {
		fmt.Fprintf(os.Stderr, "internal/app tests dialed developer Ollama on 127.0.0.1:11434 %d time(s); use useRefusingTestModel or an httptest model instead\n", dials-expectedOllamaDials.Load())
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// expectedOllamaDials — попытки, которые тест сторожа делает намеренно.
var expectedOllamaDials atomic.Int64
