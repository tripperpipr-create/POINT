package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIgnoreRulesFollowGitSemantics(t *testing.T) {
	root := t.TempDir()
	write := func(name, text string) {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "# comment\n*.log\n!keep.log\n/source/upload\ncache/\n**/tmp/**\ndocs/*.bak\n")
	write("lk/.gitignore", "dist\n")
	ignore := newProjectIgnore(root)
	cases := []struct {
		path  string
		dir   bool
		want  bool
		label string
	}{
		{"app.log", false, true, "glob at any depth"},
		{"deep/x/app.log", false, true, "glob in subfolder"},
		{"keep.log", false, false, "negation"},
		{"source/upload", true, true, "anchored"},
		{"other/source/upload", true, false, "anchored only at root"},
		{"bitrix/cache", true, true, "dir-only pattern on dir"},
		{"bitrix/cache", false, false, "dir-only pattern on file"},
		{"a/tmp/b/c.txt", false, true, "double star"},
		{"docs/x.bak", false, true, "anchored glob"},
		{"docs/sub/x.bak", false, false, "single star stays in one folder"},
		{"lk/dist", true, true, "nested .gitignore"},
		{"dist", true, false, "nested rule does not leak upwards"},
		{"source/api.php", false, false, "code stays"},
	}
	for _, item := range cases {
		if got := ignore.matches(item.path, item.dir); got != item.want {
			t.Errorf("%s: %s dir=%v → %v, want %v", item.label, item.path, item.dir, got, item.want)
		}
	}
	if !ignore.ignored("bitrix/cache/x.php", false) {
		t.Fatal("file inside an ignored folder must be ignored")
	}
}

// Индекс cf-bitrix 02.10.2026: 492 файла из docker/ и ни одного из source/.
// Без git — те же правила: .gitignore, .pointignore и тома СУБД.
func TestIndexSkipsIgnoredAndDatabaseVolumesWithoutGit(t *testing.T) {
	root := t.TempDir()
	write := func(name, text string) {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "docker/logs/\n")
	write(".pointignore", "dumps/\n")
	write("source/api.php", "<?php function documentsApi() { return '/api/documents'; }\n")
	write("docker/logs/access.json", `{"path":"/api/documents"}`+"\n")
	write("docker/mysql/ibdata1", "data")
	write("docker/mysql/cf/table.txt", "/api/documents\n")
	write("dumps/full.sql", "INSERT '/api/documents';\n")
	write("docker/compose.yml", "services: {}\n")
	fs, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	status, err := fs.BuildIndex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	files := fs.indexSlot().index.files
	for _, unwanted := range []string{"docker/logs/access.json", "docker/mysql/cf/table.txt", "dumps/full.sql"} {
		if _, ok := files[unwanted]; ok {
			t.Fatalf("%s indexed: %+v", unwanted, status)
		}
	}
	for _, wanted := range []string{"source/api.php", "docker/compose.yml"} {
		if _, ok := files[wanted]; !ok {
			t.Fatalf("%s missing from index", wanted)
		}
	}
	write("docker/logs/new.json", "{}\n")
	if _, err = fs.UpdateIndex(context.Background(), []string{"docker/logs/new.json"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := fs.indexSlot().index.files["docker/logs/new.json"]; ok {
		t.Fatal("incremental update brought an ignored file back")
	}
	matches, _, _, err := fs.SearchWithStats(context.Background(), SearchOptions{Query: "/api/documents"})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, match := range matches {
		paths = append(paths, match.Path)
	}
	if strings.Join(paths, ",") != "source/api.php" {
		t.Fatalf("search without git found %v", paths)
	}
}
