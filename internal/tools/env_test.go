package tools

import (
	"strings"
	"testing"
)

func TestSanitizedProcessEnvOmitsSecrets(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "secret-should-not-leak")
	t.Setenv("GH_TOKEN", "ghp_should-not-leak")
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("GOPATH", "/tmp/go")
	env := sanitizedProcessEnv()
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "secret-should-not-leak") || strings.Contains(joined, "ghp_should-not-leak") {
		t.Fatalf("sanitized env leaked secrets: %s", joined)
	}
	foundPath, foundGo := false, false
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			foundPath = true
		}
		if strings.HasPrefix(entry, "GOPATH=") {
			foundGo = true
		}
	}
	if !foundPath || !foundGo {
		t.Fatalf("expected PATH and GOPATH to remain, env=%v", env)
	}
}
