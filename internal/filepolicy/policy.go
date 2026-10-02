// Package filepolicy owns versioned portable-file rules shared by snapshots and Docker workspaces.
package filepolicy

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const Current = "portable-v2"
const Legacy = "legacy-v1"

var legacyCopyDirs = set(".git", "node_modules", ".cache", "dist", "build", ".venv", "venv", "__pycache__", ".idea", ".vscode")
var legacyAuditDirs = set(".git", "node_modules", "vendor", ".cache", "dist", "build", "out", ".idea", ".point", "target", ".venv", "venv", "__pycache__", ".next", ".turbo", "coverage")
var portableDirs = func() map[string]bool {
	m := set()
	for k := range legacyCopyDirs {
		m[k] = true
	}
	for k := range legacyAuditDirs {
		m[k] = true
	}
	m[".gocache"] = true
	m[".tmp"] = true
	return m
}()
var sourceExts = set(".go", ".py", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx", ".java", ".kt", ".rs", ".rb", ".php", ".cs", ".c", ".h", ".cpp", ".hpp", ".swift", ".sh", ".ps1", ".md", ".txt", ".json", ".yaml", ".yml", ".toml", ".html", ".css", ".sql")

func set(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, name := range names {
		m[name] = true
	}
	return m
}

func ValidVersion(version string) bool {
	return version == Current || version == Legacy || version == ""
}
func SkipDirectory(version, name string, audit bool) bool {
	key := strings.ToLower(name)
	if version == Current {
		return portableDirs[key]
	}
	if audit {
		return legacyAuditDirs[key]
	}
	return legacyCopyDirs[key]
}

// SkipDirectoryPath distinguishes dependency vendor directories from source assets.
// A vendor directory underneath src belongs to the portable source tree; package-
// root vendor directories remain excluded, including nested PHP/Go packages.
func SkipDirectoryPath(version, relative string, audit bool) bool {
	parts := strings.Split(strings.ToLower(relative), "/")
	name := parts[len(parts)-1]
	if version == Current && name == "vendor" {
		for _, parent := range parts[:len(parts)-1] {
			if parent == "src" {
				return false
			}
		}
	}
	return SkipDirectory(version, name, audit)
}

func ExcludedPath(version, relative string, directory bool) bool {
	parts := strings.Split(relative, "/")
	limit := len(parts)
	if !directory {
		limit--
	}
	for i := 1; i <= limit; i++ {
		if SkipDirectoryPath(version, strings.Join(parts[:i], "/"), false) {
			return true
		}
	}
	return !directory && SkipFile(path.Base(relative))
}
func SkipFile(name string) bool {
	name = strings.ToLower(name)
	if name == ".git" || Sensitive(name) {
		return true
	}
	switch path.Ext(name) {
	case ".exe", ".dll", ".so", ".png", ".jpg", ".jpeg":
		return true
	}
	return false
}

// Sensitive recognizes key names without excluding source files such as id_rsa_parser.go.
// Windows aliases are recognized on every platform so transfers have one policy.
func Sensitive(name string) bool {
	return CopySensitiveLegacy(path.Base(strings.ReplaceAll(name, "\\", "/")), true)
}

// Legacy callers choose Windows alias handling to preserve their historical behavior.
func CopySensitiveLegacy(name string, windowsAliases bool) bool {
	base := strings.ToLower(name)
	if windowsAliases {
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
		return !sourceExts[filepath.Ext(base)]
	}
	return AuditSensitiveLegacy(name, windowsAliases)
}

func AuditSensitiveLegacy(name string, windowsAliases bool) bool {
	base := strings.ToLower(filepath.Base(name))
	if windowsAliases {
		base = strings.TrimRight(base, ". ")
	}
	if strings.Contains(base, "id_rsa") || strings.Contains(base, "id_ed25519") {
		return true
	}
	if base == ".env" || strings.HasPrefix(base, ".env.") || base == ".npmrc" || base == ".pypirc" || base == "credentials" {
		return true
	}
	for _, ext := range []string{".pem", ".key", ".p12", ".pfx", ".kdbx"} {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	return false
}

func SkipFileLegacy(name string, windowsAliases bool) bool {
	lower := strings.ToLower(name)
	if lower == ".git" {
		return true
	}
	switch path.Ext(lower) {
	case ".exe", ".dll", ".so", ".png", ".jpg", ".jpeg":
		return true
	}
	return CopySensitiveLegacy(name, windowsAliases)
}

// ValidatePath rejects names that cannot round-trip through Windows, Linux and macOS.
func ValidatePath(value string) error {
	if value == "" || !utf8.ValidString(value) || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return fmt.Errorf("invalid portable path %q", value)
	}
	for _, component := range strings.Split(value, "/") {
		if len(component) > 255 {
			return fmt.Errorf("portable filename exceeds filesystem limit %q", value)
		}
		if component == "" || component == "." || component == ".." || strings.TrimRight(component, ". ") != component {
			return fmt.Errorf("unrepresentable portable path %q", value)
		}
		for _, r := range component {
			if r < 32 || strings.ContainsRune("<>:\"|?*", r) {
				return fmt.Errorf("unrepresentable portable path %q", value)
			}
		}
		stem := strings.ToUpper(strings.SplitN(component, ".", 2)[0])
		if len([]rune(stem)) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && strings.ContainsRune("¹²³", []rune(stem)[3]) {
			return fmt.Errorf("reserved portable path %q", value)
		}
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || (len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9') {
			return fmt.Errorf("reserved portable path %q", value)
		}
	}
	return nil
}

// Fold uses Unicode simple folding, not locale-dependent casing.
func Fold(value string) string {
	return strings.Map(func(r rune) rune {
		min := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < min {
				min = next
			}
		}
		return min
	}, norm.NFC.String(value))
}
