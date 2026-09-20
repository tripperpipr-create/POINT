package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
)

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
	if !loopback || port == "11434" {
		return nil, fmt.Errorf("unexpected network request in internal/app tests: %s", address)
	}
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

func TestMain(m *testing.M) {
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
	os.Exit(m.Run())
}
