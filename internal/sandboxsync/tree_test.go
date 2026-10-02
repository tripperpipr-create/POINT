package sandboxsync

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"local-agent-workbench/internal/filepolicy"
)

func TestTransferExactBytesAndContentHashIgnoresMtime(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	ctx := context.Background()
	files := map[string][]byte{"CRLF.txt": []byte("hello\r\n"), "папка/данные.bin": {0, 255, 1, 2}, "large.txt": bytes.Repeat([]byte("x"), 1024*1024), "vendor/pkg.php": []byte("excluded"), ".env": []byte("secret")}
	for p, data := range files {
		target := filepath.Join(src, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	m, err := Scan(ctx, src, filepolicy.Current)
	if err != nil {
		t.Fatal(err)
	}
	if err = ValidateManifest(m); err != nil {
		t.Fatal(err)
	}
	delta := Changes(Manifest{}, m)
	for i := range delta {
		delta[i], err = ReadChange(src, delta[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = Apply(dst, delta); err != nil {
		t.Fatal(err)
	}
	if err = Apply(dst, delta); err != nil {
		t.Fatal("replay", err)
	}
	copy, err := Scan(ctx, dst, filepolicy.Current)
	if err != nil || m.Digest != copy.Digest {
		t.Fatalf("copy digest: %v", err)
	}
	for p, data := range files {
		if p == ".env" || p == "vendor/pkg.php" {
			continue
		}
		actual, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(p)))
		if err != nil || !reflect.DeepEqual(data, actual) {
			t.Fatalf("bytes %s: %v", p, err)
		}
	}
	p := filepath.Join(src, "CRLF.txt")
	original, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, []byte("other\r\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(p, time.Now(), original.ModTime()); err != nil {
		t.Fatal(err)
	}
	next, err := Scan(ctx, src, filepolicy.Current)
	if err != nil || m.Digest == next.Digest {
		t.Fatal("trusted mtime instead of content")
	}
}

func TestTransferRejectsLinksTraversalInvalidHashAndManifest(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err == nil {
		if _, err = Resolve(root, "link/escape"); err == nil {
			t.Fatal("followed link")
		}
	}
	for _, c := range []Change{{Entry: Entry{Path: "../escape"}}, {Entry: Entry{Path: ".env"}}, {Entry: Entry{Path: "vendor/pkg"}}, {Entry: Entry{Path: "file", Hash: "bad", Size: 1}, Data: []byte("x")}} {
		if Apply(root, []Change{c}) == nil {
			t.Fatalf("accepted unsafe change %+v", c.Entry)
		}
	}
	for _, entries := range [][]Entry{{{Path: "a/child", Directory: true}}, {{Path: "Readme", Directory: true}, {Path: "README", Directory: true}}, {{Path: "a", Directory: true}, {Path: "a", Directory: true}}, {{Path: "node_modules", Directory: true}}} {
		m := Manifest{Rules: filepolicy.Current, Entries: entries, Digest: Digest(entries)}
		if ValidateManifest(m) == nil {
			t.Fatalf("accepted invalid manifest %+v", entries)
		}
	}
}
