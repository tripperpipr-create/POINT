package workspace

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIndexGenerationTracksPublicationAndInvalidation(t *testing.T) {
	root := t.TempDir()
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	assertAdvance := func(before uint64) {
		t.Helper()
		if fs.IndexGeneration() <= before {
			t.Fatal("generation did not advance")
		}
	}
	before := fs.IndexGeneration()
	fs.InvalidateIndex()
	assertAdvance(before)
	file := filepath.Join(root, "main.go")
	if err = os.WriteFile(file, []byte("package sample\nfunc First() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before = fs.IndexGeneration()
	if _, err = fs.BuildIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertAdvance(before)
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.IndexGeneration() != fs.IndexGeneration() {
		t.Fatal("generation is FS-pointer-scoped")
	}
	before = fs.IndexGeneration()
	if err = os.WriteFile(file, []byte("package sample\nfunc Second() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = fs.UpdateIndex(context.Background(), []string{"main.go"}, nil); err != nil {
		t.Fatal(err)
	}
	assertAdvance(before)
	before = fs.IndexGeneration()
	fs.InvalidateIndex()
	assertAdvance(before)
	if runtime.GOOS == "windows" {
		lower, e := Open(strings.ToLower(root))
		if e != nil {
			t.Fatal(e)
		}
		if lower.indexSlot() != fs.indexSlot() {
			t.Fatal("case variants use different generations")
		}
	}
	if err = os.WriteFile(filepath.Join(root, "other.go"), []byte("package sample\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before = fs.IndexGeneration()
	if status, e := fs.buildIndex(context.Background(), indexLimits{MaxFiles: 1, MaxEntries: 10, MaxBytes: 1024, MaxChunks: 1}); e != nil || !status.Partial {
		t.Fatal("partial publication missing")
	}
	assertAdvance(before)
}
