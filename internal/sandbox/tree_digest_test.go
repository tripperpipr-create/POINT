package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Отпечаток обязан совпадать у песочницы и её чистой копии и меняться от
// любой правки переносимого файла. То, что копия не переносит (node_modules,
// сборка, секреты), на него не влияет: приёмка этого не видит.
func TestTreeDigestMatchesTheCarriedCopyAndNothingElse(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src, map[string]string{
		"package.json": `{"name":"a"}`, "src/main.js": "console.log(1)\n",
		"node_modules/x/index.js": "junk", "dist/app.js": "built", ".env": "SECRET=1",
	})
	if err := os.MkdirAll(filepath.Join(src, "out-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	base, err := TreeDigest(src)
	if err != nil {
		t.Fatal(err)
	}
	copyRoot := filepath.Join(t.TempDir(), "copy")
	if err = CopyCarried(src, copyRoot); err != nil {
		t.Fatal(err)
	}
	if copied, _ := TreeDigest(copyRoot); copied != base {
		t.Fatalf("clean copy digest %s differs from the sandbox digest %s", copied, base)
	}
	if _, err = os.Stat(filepath.Join(copyRoot, "node_modules")); err == nil {
		t.Fatal("clean copy carried node_modules")
	}
	writeTree(t, src, map[string]string{"node_modules/y/index.js": "more", "dist/other.js": "x", ".env": "SECRET=2"})
	if same, _ := TreeDigest(src); same != base {
		t.Fatal("digest depends on files the next sandbox never sees")
	}
	writeTree(t, src, map[string]string{"src/main.js": "console.log(2)\n"})
	changed, _ := TreeDigest(src)
	if changed == base {
		t.Fatal("digest ignored a source change")
	}
	if err = os.Remove(filepath.Join(src, "out-dir")); err != nil {
		t.Fatal(err)
	}
	if without, _ := TreeDigest(src); without == changed {
		t.Fatal("digest ignored a removed directory")
	}
}
