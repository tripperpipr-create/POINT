package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sort"

	"local-agent-workbench/internal/domain"
)

// v2 explicitly permits compiler/test binaries in the bounded, nosuid/nodev
// temporary filesystem. Engine defaults previously disagreed on execution.
const SecurityProfileVersion = "point-container-security-v2"

// Only execution-visible, sanitized values participate; no secrets are logged.
func VerificationContextDigest(r domain.SandboxRecord) string {
	env := containerEnvironment(os.Environ())
	sort.Strings(env)
	data, _ := json.Marshal(struct {
		Backend, Version, Helper, Storage, Security string
		Memory, CPUs, PIDs, User                    string
		Environment                                 []string
	}{r.Backend, r.BackendVersion, r.SandboxdDigest, r.StorageMode, SecurityProfileVersion,
		os.Getenv("POINT_SANDBOX_MEMORY"), os.Getenv("POINT_SANDBOX_CPUS"), os.Getenv("POINT_SANDBOX_PIDS"), os.Getenv("POINT_SANDBOX_USER"), env})
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
