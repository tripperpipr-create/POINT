package changesets_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"local-agent-workbench/internal/changesets"
	"local-agent-workbench/internal/filepolicy"
	"local-agent-workbench/internal/storage"
)

func TestPortableChangeSetRetainsSourceVendorAndExcludesDependencies(t *testing.T) {
	base, mirror := t.TempDir(), t.TempDir()
	write := func(root, path, value string) {
		t.Helper()
		target := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write(base, "src/styles/vendor/source.css", "old\r\n")
	write(mirror, "src/styles/vendor/source.css", "new\r\n")
	write(mirror, "vendor/package/library.php", "dependency")
	write(mirror, "out/compiled.js", "build output")
	store, err := storage.Open(filepath.Join(t.TempDir(), "changes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	set, err := (changesets.Applier{Store: store}).BuildFromSandbox(context.Background(), changesets.BuildRequest{WorkspaceID: "portable", ExecutionID: "execution", WorkspacePath: base, SandboxPath: mirror, FileRulesVersion: filepolicy.Current})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Items) != 1 || set.Items[0].Path != "src/styles/vendor/source.css" || set.Items[0].OriginalContent != "old\r\n" || set.Items[0].ProposedContent != "new\r\n" {
		t.Fatalf("portable change set: %+v", set.Items)
	}
}
