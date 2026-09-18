package environment

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestProbeNetworkHostsPackagist(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	bad := ProbeNetworkHosts(ctx, []string{"repo.packagist.org", "github.com"})
	if len(bad) != 0 {
		t.Fatalf("expected packagist/github reachable from host, got %v", bad)
	}
}

func TestProbeNetworkHostsUnreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	bad := ProbeNetworkHosts(ctx, []string{"this-host-does-not-exist.invalid"})
	if len(bad) == 0 {
		t.Fatal("expected unreachable host")
	}
	if !strings.Contains(bad[0], "this-host-does-not-exist.invalid") {
		t.Fatalf("unexpected: %v", bad)
	}
}
