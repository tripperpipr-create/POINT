package sandbox

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"local-agent-workbench/internal/filepolicy"
)

// Proves the delegation preserves the live/legacy secret boundary before moving its owner.
func TestSharedPolicyPreservesEveryLegacySecretVariant(t *testing.T) {
	for _, key := range []string{"id_rsa", "id_ed25519", "id_ecdsa", "id_dsa", ".env", "credentials", ".npmrc", ".pypirc", "ordinary"} {
		for _, prefix := range []string{"", "deploy_", "prefix/", "prefix\\"} {
			for _, suffix := range []string{"", "_sk", "_deploy", "_parser.go", ".pub", ".txt", ".pem", ".key", ".p12", ".pfx", ".kdbx", ".png", ".dll", ".so", ".json", ".PHP", ".yaml", ".md", ".exe", "\\other", "/other", ". ", "..", " .key"} {
				name := prefix + key + suffix
				if old, next := legacySecretReference(name), filepolicy.CopySensitiveLegacy(name, runtime.GOOS == "windows"); old != next {
					t.Fatalf("secret policy changed for %q: old %v, new %v", name, old, next)
				}
				lower := strings.ToLower(name)
				excluded := false
				for _, ext := range []string{".exe", ".dll", ".so", ".png", ".jpg", ".jpeg"} {
					excluded = excluded || strings.HasSuffix(lower, ext)
				}
				if old, next := lower == ".git" || excluded || legacySecretReference(name), filepolicy.SkipFileLegacy(name, runtime.GOOS == "windows"); old != next {
					t.Fatalf("copy policy changed for %q: old %v, new %v", name, old, next)
				}
			}
		}
	}
}

// Frozen legacy reference is independent of the shared policy under test.
func legacySecretReference(name string) bool {
	base := strings.ToLower(name)
	if runtime.GOOS == "windows" {
		base = strings.TrimRight(base, ". ")
	}
	for _, key := range []string{"id_rsa", "id_ed25519", "id_ecdsa", "id_dsa"} {
		if !strings.Contains(base, key) {
			continue
		}
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		if stem == key || stem == key+"_sk" {
			return true
		}
		for _, ext := range []string{".go", ".py", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx", ".java", ".kt", ".rs", ".rb", ".php", ".cs", ".c", ".h", ".cpp", ".hpp", ".swift", ".sh", ".ps1", ".md", ".txt", ".json", ".yaml", ".yml", ".toml", ".html", ".css", ".sql"} {
			if filepath.Ext(base) == ext {
				return false
			}
		}
		return true
	}
	base = strings.ToLower(filepath.Base(name))
	if runtime.GOOS == "windows" {
		base = strings.TrimRight(base, ". ")
	}
	if base == ".env" || strings.HasPrefix(base, ".env.") || base == ".npmrc" || base == ".pypirc" || base == "credentials" {
		return true
	}
	for _, ext := range []string{".pem", ".key", ".p12", ".pfx", ".kdbx"} {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	return strings.Contains(base, "id_rsa") || strings.Contains(base, "id_ed25519")
}
