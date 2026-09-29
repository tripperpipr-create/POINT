package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerRuntimeVersionsIntegration(t *testing.T) {
	if os.Getenv("POINT_SANDBOX_DOCKER_TEST") != "1" {
		t.Skip("Docker image integration is opt-in")
	}
	b := NewContainerBackend(filepath.Join(t.TempDir(), "sandboxes"))
	if image := strings.TrimSpace(os.Getenv("POINT_SANDBOX_IMAGE")); image != "" {
		b.Image = image
	}
	if err := b.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	image, digest, err := b.resolveRuntimeImage(context.Background(), "", RuntimeRequirements{
		ID: "node", RequiredCommands: []string{"node", "npm"},
		ToolVersions:    map[string]string{"node": "20", "npm": "10.9.0"},
		CandidateImages: []string{"point-agent-sandbox-node20:1.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if image != "point-agent-sandbox-node20:1.0.0" || digest == "" {
		t.Fatalf("image=%q digest=%q", image, digest)
	}
	_, _, err = b.resolveRuntimeImage(context.Background(), "", RuntimeRequirements{
		ID: "node", RequiredCommands: []string{"node"}, ToolVersions: map[string]string{"node": "99"},
	})
	if !errors.Is(err, ErrRuntimeVersionUnavailable) {
		t.Fatalf("missing image error=%v", err)
	}
}
