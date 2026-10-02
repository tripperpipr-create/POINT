package sandbox

import (
	"local-agent-workbench/internal/domain"
	"testing"
)

func TestVerificationContextBindsEngineAndVisibleEnvironment(t *testing.T) {
	r := domain.SandboxRecord{Backend: "docker", BackendVersion: "27", StorageMode: "volume", SandboxdDigest: "helper"}
	base := VerificationContextDigest(r)
	other := r
	other.Backend = "embedded-moby"
	if base == VerificationContextDigest(other) {
		t.Fatal("cross-engine reuse")
	}
	other = r
	other.BackendVersion = "28"
	if base == VerificationContextDigest(other) {
		t.Fatal("version ignored")
	}
	for name, mutate := range map[string]func(*domain.SandboxRecord){
		"helper":  func(value *domain.SandboxRecord) { value.SandboxdDigest = "another-helper" },
		"storage": func(value *domain.SandboxRecord) { value.StorageMode = "bind" },
	} {
		other = r
		mutate(&other)
		if base == VerificationContextDigest(other) {
			t.Fatalf("%s ignored", name)
		}
	}
	for name, value := range map[string]string{"POINT_SANDBOX_MEMORY": "3g", "POINT_SANDBOX_CPUS": "1", "POINT_SANDBOX_PIDS": "32", "POINT_SANDBOX_USER": "10002:10002"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, value)
			if base == VerificationContextDigest(r) {
				t.Fatalf("resource/security setting %s ignored", name)
			}
		})
	}
	t.Setenv("GOFLAGS", "-tags=changed")
	if base == VerificationContextDigest(r) {
		t.Fatal("visible environment ignored")
	}
	changed := VerificationContextDigest(r)
	t.Setenv("POINT_LLMUX_API_KEY", "not-a-real-key")
	if changed != VerificationContextDigest(r) {
		t.Fatal("secret affected verification identity")
	}
}
