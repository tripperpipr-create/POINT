package orchestrator

import "testing"

func TestPathScopesOverlap(t *testing.T) {
	if !pathScopesOverlap([]string{"internal/api"}, []string{"internal/api/server.go"}) {
		t.Fatal("nested ownership was not detected")
	}
	if pathScopesOverlap([]string{"internal/api"}, []string{"internal/ui"}) {
		t.Fatal("independent ownership was reported as overlap")
	}
}
