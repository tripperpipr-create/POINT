package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildIndexStopsAtConfiguredCapacity(t *testing.T) {
	tests := []struct {
		name   string
		limits indexLimits
		reason string
	}{
		{name: "entries", limits: indexLimits{MaxEntries: 1, MaxFiles: 10, MaxBytes: 1 << 20, MaxChunks: 10}, reason: "entries"},
		{name: "files", limits: indexLimits{MaxEntries: 100, MaxFiles: 1, MaxBytes: 1 << 20, MaxChunks: 10}, reason: "files"},
		{name: "bytes", limits: indexLimits{MaxEntries: 100, MaxFiles: 10, MaxBytes: 24, MaxChunks: 10}, reason: "bytes"},
		{name: "chunks", limits: indexLimits{MaxEntries: 100, MaxFiles: 10, MaxBytes: 1 << 20, MaxChunks: 1}, reason: "chunks"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeIndexLimitFixture(t, root, "a.go", "package fixture\nvar Alpha = 1\n")
			writeIndexLimitFixture(t, root, "b.go", "package fixture\nvar Beta = 2\n")
			filesystem, err := Open(root)
			if err != nil {
				t.Fatal(err)
			}

			status, err := filesystem.buildIndex(context.Background(), test.limits)
			if err != nil {
				t.Fatal(err)
			}
			if status.State != "ready" || !status.Partial || status.LimitReason != test.reason {
				t.Fatalf("bounded status=%#v", status)
			}
			if status.Files > test.limits.MaxFiles || status.ApproxBytes > test.limits.MaxBytes || status.Chunks > test.limits.MaxChunks {
				t.Fatalf("index crossed its capacity: %#v", status)
			}
			if status.MaxEntries != test.limits.MaxEntries || status.MaxFiles != test.limits.MaxFiles || status.MaxBytes != test.limits.MaxBytes || status.MaxChunks != test.limits.MaxChunks {
				t.Fatalf("reported limits differ from applied limits: %#v", status)
			}
		})
	}
}

func TestPartialIndexMissDoesNotRepeatFullWalk(t *testing.T) {
	root := t.TempDir()
	writeIndexLimitFixture(t, root, "a.go", "package fixture\nvar IndexedAlpha = 1\n")
	writeIndexLimitFixture(t, root, "b.go", "package fixture\nvar ExcludedZebra = 2\n")
	filesystem, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	status, err := filesystem.buildIndex(context.Background(), indexLimits{MaxEntries: 100, MaxFiles: 1, MaxBytes: 1 << 20, MaxChunks: 10})
	if err != nil || !status.Partial {
		t.Fatalf("initial partial index=%#v err=%v", status, err)
	}

	result, err := filesystem.SearchContext(context.Background(), "ExcludedZebra", 4, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Chunks) != 0 {
		t.Fatalf("partial index invented an out-of-bound hit: %#v", result.Chunks)
	}
	after := filesystem.IndexStatus()
	if after.BuiltAt != status.BuiltAt || !after.Partial || after.LimitReason != "files" {
		t.Fatalf("a miss rebuilt the same partial index: before=%#v after=%#v", status, after)
	}
}

func TestIncrementalUpdateCannotGrowPastPartialIndexLimit(t *testing.T) {
	root := t.TempDir()
	writeIndexLimitFixture(t, root, "a.go", "package fixture\nvar Alpha = 1\n")
	writeIndexLimitFixture(t, root, "b.go", "package fixture\nvar Beta = 2\n")
	filesystem, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	status, err := filesystem.buildIndex(context.Background(), indexLimits{MaxEntries: 100, MaxFiles: 1, MaxBytes: 1 << 20, MaxChunks: 10})
	if err != nil || !status.Partial || status.Files != 1 {
		t.Fatalf("initial partial index=%#v err=%v", status, err)
	}
	writeIndexLimitFixture(t, root, "z.go", "package fixture\nvar NewMarker = 3\n")
	status, err = filesystem.UpdateIndex(context.Background(), []string{"z.go"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Partial || status.Files != 1 || status.LimitReason != "files" {
		t.Fatalf("incremental update crossed or forgot the limit: %#v", status)
	}
}

func writeIndexLimitFixture(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
