package filepolicy

import (
	"strings"
	"testing"
)

func TestVendorDependencyAndSourceAssets(t *testing.T) {
	for _, value := range []string{"vendor/autoload.php", "packages/api/vendor/library.php", "src/node_modules/pkg/index.js", "src/vendor/build/output.js"} {
		if !ExcludedPath(Current, value, false) {
			t.Errorf("dependency included: %s", value)
		}
	}
	for _, value := range []string{"src/styles/vendor/suggestions.min.pcss", "packages/ui/src/scripts/vendor/tool.js"} {
		if ExcludedPath(Current, value, false) {
			t.Errorf("source resource excluded: %s", value)
		}
	}
	if !SkipDirectoryPath(Legacy, "src/vendor", true) {
		t.Fatal("legacy audit changed")
	}
}

func TestVersionedPortablePolicy(t *testing.T) {
	for _, name := range []string{"vendor", "node_modules", ".vscode", ".point", "out", "target", "coverage", "dist", "build", ".next", ".venv"} {
		if !SkipDirectory(Current, name, false) || !SkipDirectory(Current, name, true) {
			t.Errorf("portable rules differ for %s", name)
		}
	}
	if SkipDirectory(Legacy, "vendor", false) || !SkipDirectory(Legacy, "vendor", true) {
		t.Fatal("legacy copy/audit compatibility changed")
	}
	for _, name := range []string{".env", ".env.local", ".npmrc", "credentials", "deploy_id_rsa", "id_ecdsa_sk", "cert.pem", "image.PNG", "lib.so"} {
		if !SkipFile(name) {
			t.Errorf("included %s", name)
		}
	}
	for _, name := range []string{"main.go", "id_rsa_parser.go", "package-lock.json", "component.vue", "payload.bin"} {
		if SkipFile(name) {
			t.Errorf("excluded source %s", name)
		}
	}
}
func TestPortableNamesAndFolding(t *testing.T) {
	for _, p := range []string{"../x", "/absolute", "a\\b", "a//b", "CON.txt", "COM¹.txt", "LPT²", strings.Repeat("я", 128), "a/NUL", "a:name", "trailing. ", "a/../b", "", "bad\x00name"} {
		if ValidatePath(p) == nil {
			t.Errorf("accepted unsafe name %q", p)
		}
	}
	for _, p := range []string{"src/проверка.go", "a/file name.txt", "empty", "café.txt"} {
		if err := ValidatePath(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	if Fold("K/É") != Fold("k/e\u0301") {
		t.Fatal("Unicode or canonical alias not detected")
	}
}
