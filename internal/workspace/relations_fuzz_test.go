package workspace

import (
	"reflect"
	"strings"
	"testing"
)

func FuzzImportResolutionStaysInsideIndexedPaths(f *testing.F) {
	f.Add(uint8(0), "./service")
	f.Add(uint8(1), "example.dev/point/internal/auth")
	f.Add(uint8(2), "....outside")
	f.Add(uint8(3), "../../outside")
	f.Add(uint8(0), "C:\\outside\\secret")
	paths := []string{
		"src/api.ts", "src/service.ts", "src/service.test.ts", "internal/auth/token.go",
		"tests/test_service.py", "app/service.py", "outside/secret.ts",
	}
	f.Fuzz(func(t *testing.T, sourceIndex uint8, rawSpec string) {
		spec := strings.ToValidUTF8(rawSpec, "")
		if len(spec) > 4096 {
			return
		}
		source := paths[int(sourceIndex)%len(paths)]
		first := resolveImportTargets(source, spec, paths)
		second := resolveImportTargets(source, spec, paths)
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("nondeterministic import resolution: %v != %v", first, second)
		}
		if len(first) > maxImportTargets {
			t.Fatalf("unbounded targets: %v", first)
		}
		for _, target := range first {
			if target == source || strings.Contains(target, "..") || !containsPath(paths, target) {
				t.Fatalf("unsafe target %q from %q", target, spec)
			}
		}
		imports := extractImportSpecs("src/fuzz.ts", "import value from \""+spec+"\"\n")
		if len(imports) > maxImportsPerFile {
			t.Fatalf("unbounded extracted imports: %d", len(imports))
		}
	})
}

func containsPath(paths []string, target string) bool {
	for _, path := range paths {
		if path == target {
			return true
		}
	}
	return false
}
