package gitflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func workbenchRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	identity(t, root)
	write(t, filepath.Join(root, "a.txt"), "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "initial")
	return root
}
func TestWorkbenchCommitUsesOnlyIndex(t *testing.T) {
	root := workbenchRepo(t)
	write(t, filepath.Join(root, "a.txt"), "ONE\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\n")
	git(t, root, "add", "a.txt")
	write(t, filepath.Join(root, "a.txt"), "ONE\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nNINE\n")
	s, e := ReadSnapshot(context.Background(), ExecRunner{}, root)
	if e != nil {
		t.Fatal(e)
	}
	if len(s.Changes) != 2 || s.Changes[0].Area != "staged" || s.Changes[1].Area != "working" {
		t.Fatalf("partial file: %+v", s.Changes)
	}
	result, e := Execute(context.Background(), ExecRunner{}, root, Command{Action: "commit", Message: "partial", Revision: s.Revision})
	if e != nil {
		t.Fatal(e)
	}
	if got := git(t, root, "show", result.Commit+":a.txt"); strings.Contains(got, "NINE") || !strings.Contains(got, "ONE") {
		t.Fatal(got)
	}
	if status := git(t, root, "status", "--porcelain"); !strings.Contains(status, "M a.txt") {
		t.Fatal(status)
	}
}
func TestQuestCommitPreservesStagedBlobs(t *testing.T) {
	root := workbenchRepo(t)
	write(t, filepath.Join(root, "a.txt"), "prepared by user\n")
	git(t, root, "add", "a.txt")
	write(t, filepath.Join(root, "a.txt"), "quest result\n")
	write(t, filepath.Join(root, "foreign.txt"), "other staged file\n")
	git(t, root, "add", "foreign.txt")
	before := git(t, root, "show", ":a.txt")
	commit, e := Commit(context.Background(), ExecRunner{}, root, []string{"a.txt"}, "quest")
	if e != nil {
		t.Fatal(e)
	}
	if got := git(t, root, "show", commit+":a.txt"); got != "quest result" {
		t.Fatal(got)
	}
	if got := git(t, root, "show", ":a.txt"); got != before {
		t.Fatalf("staged user blob changed: %s", got)
	}
	if got := git(t, root, "diff", "--cached", "--name-only"); !strings.Contains(got, "foreign.txt") {
		t.Fatal(got)
	}
}
func TestWorkbenchRejectsEditorSaveWithSameStatus(t *testing.T) {
	root := workbenchRepo(t)
	write(t, filepath.Join(root, "a.txt"), "edit one\n")
	s, e := ReadSnapshot(context.Background(), ExecRunner{}, root)
	if e != nil {
		t.Fatal(e)
	}
	write(t, filepath.Join(root, "a.txt"), "edit two\n")
	_, e = Execute(context.Background(), ExecRunner{}, root, Command{Action: "stage", Paths: []string{"a.txt"}, Revision: s.Revision})
	if e != ErrStaleRevision {
		t.Fatalf("stale change accepted: %v", e)
	}
	if got := git(t, root, "diff", "--cached"); got != "" {
		t.Fatal(got)
	}
}
func TestWorkbenchStagePatchAndUnstage(t *testing.T) {
	root := workbenchRepo(t)
	write(t, filepath.Join(root, "a.txt"), "ONE\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nNINE\n")
	s, _ := ReadSnapshot(context.Background(), ExecRunner{}, root)
	patch := "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1,4 +1,4 @@\n-one\n+ONE\n two\n three\n four\n"
	result, e := Execute(context.Background(), ExecRunner{}, root, Command{Action: "stagePatch", Patch: patch, Revision: s.Revision})
	if e != nil {
		t.Fatal(e)
	}
	if got := git(t, root, "show", ":a.txt"); strings.Contains(got, "NINE") || !strings.Contains(got, "ONE") {
		t.Fatal(got)
	}
	_, e = Execute(context.Background(), ExecRunner{}, root, Command{Action: "unstagePatch", Patch: patch, Revision: result.Snapshot.Revision})
	if e != nil {
		t.Fatal(e)
	}
	if got := git(t, root, "diff", "--cached"); got != "" {
		t.Fatal(got)
	}
}
func TestWorkbenchPathsAndUnbornUnicode(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	identity(t, root)
	name := "файл с пробелами.txt"
	write(t, filepath.Join(root, name), "hello\n")
	s, e := ReadSnapshot(context.Background(), ExecRunner{}, root)
	if e != nil {
		t.Fatal(e)
	}
	if len(s.Changes) != 1 || s.Changes[0].Path != name {
		t.Fatal(s)
	}
	_, e = Execute(context.Background(), ExecRunner{}, root, Command{Action: "stage", Paths: []string{"../outside"}, Revision: s.Revision})
	if e == nil {
		t.Fatal("escape accepted")
	}
	r, e := Execute(context.Background(), ExecRunner{}, root, Command{Action: "stage", Paths: []string{name}, Revision: s.Revision})
	if e != nil {
		t.Fatal(e)
	}
	write(t, filepath.Join(root, name), "edited after staging\n")
	r.Snapshot, e = ReadSnapshot(context.Background(), ExecRunner{}, root)
	if e != nil {
		t.Fatal(e)
	}
	_, e = Execute(context.Background(), ExecRunner{}, root, Command{Action: "unstage", Paths: []string{name}, Revision: r.Snapshot.Revision})
	if e != nil {
		t.Fatal(e)
	}
	if content, err := os.ReadFile(filepath.Join(root, name)); err != nil || string(content) != "edited after staging\n" {
		t.Fatalf("working file changed: %q %v", content, err)
	}
	if _, e = os.Stat(filepath.Join(root, name)); e != nil {
		t.Fatal(e)
	}
}
func TestWorkbenchHistoryParents(t *testing.T) {
	root := workbenchRepo(t)
	git(t, root, "switch", "-q", "-c", "feature")
	write(t, filepath.Join(root, "b.txt"), "b")
	git(t, root, "add", "b.txt")
	git(t, root, "commit", "-q", "-m", "feature")
	git(t, root, "switch", "-q", "main")
	git(t, root, "merge", "--no-ff", "-m", "merge", "feature")
	commits, e := History(context.Background(), ExecRunner{}, root, HistoryQuery{})
	if e != nil {
		t.Fatal(e)
	}
	if len(commits) != 3 || len(commits[0].Parents) != 2 {
		t.Fatalf("graph: %+v", commits)
	}
}

func TestWorkbenchRevisionIncludesBranch(t *testing.T) {
	root := workbenchRepo(t)
	ctx := context.Background()
	s, e := ReadSnapshot(ctx, ExecRunner{}, root)
	if e != nil {
		t.Fatal(e)
	}
	git(t, root, "switch", "-c", "another")
	_, e = Execute(ctx, ExecRunner{}, root, Command{Action: "createTag", Name: "stale", Revision: s.Revision})
	if e != ErrStaleRevision {
		t.Fatalf("same-head branch change accepted: %v", e)
	}
}
func TestWorkbenchCommitSurvivesFailedPush(t *testing.T) {
	root := workbenchRepo(t)
	ctx := context.Background()
	git(t, root, "remote", "add", "origin", filepath.Join(t.TempDir(), "missing.git"))
	write(t, filepath.Join(root, "a.txt"), "committed\n")
	git(t, root, "add", "a.txt")
	s, e := ReadSnapshot(ctx, ExecRunner{}, root)
	if e != nil {
		t.Fatal(e)
	}
	v, e := Execute(ctx, ExecRunner{}, root, Command{Action: "commitAndPush", Message: "saved", Remote: "origin", Revision: s.Revision})
	if e == nil || v.Commit == "" {
		t.Fatalf("result: %+v %v", v, e)
	}
	head := git(t, root, "rev-parse", "HEAD")
	if head != v.Commit {
		t.Fatal("commit lost")
	}
	_, e = Execute(ctx, ExecRunner{}, root, Command{Action: "push", Remote: "origin", Revision: v.Snapshot.Revision})
	if e == nil || git(t, root, "rev-parse", "HEAD") != head {
		t.Fatal("retry created or lost commit")
	}
}

func TestWorkbenchRenameBinaryTagAndStash(t *testing.T) {
	root := workbenchRepo(t)
	ctx := context.Background()
	git(t, root, "mv", "a.txt", "новое имя.txt")
	write(t, filepath.Join(root, "binary.dat"), "\x00\x01\x02")
	git(t, root, "add", "binary.dat")
	s, e := ReadSnapshot(ctx, ExecRunner{}, root)
	if e != nil {
		t.Fatal(e)
	}
	renamed := false
	for _, change := range s.Changes {
		renamed = renamed || (change.OriginalPath == "a.txt" && change.Path == "новое имя.txt")
	}
	if !renamed {
		t.Fatalf("rename missing: %+v", s.Changes)
	}
	v, e := Execute(ctx, ExecRunner{}, root, Command{Action: "commit", Message: "rename and binary", Revision: s.Revision})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(git(t, root, "show", "--numstat", v.Commit), "binary.dat") {
		t.Fatal("binary not committed")
	}
	v, e = Execute(ctx, ExecRunner{}, root, Command{Action: "createTag", Name: "v-test", Revision: v.Snapshot.Revision})
	if e != nil {
		t.Fatal(e)
	}
	v, e = Execute(ctx, ExecRunner{}, root, Command{Action: "switchBranch", Ref: "v-test", Revision: v.Snapshot.Revision})
	if e != nil || !v.Snapshot.Detached {
		t.Fatalf("tag: %+v %v", v, e)
	}
	write(t, filepath.Join(root, "новое имя.txt"), "dirty\n")
	s, e = ReadSnapshot(ctx, ExecRunner{}, root)
	if e != nil {
		t.Fatal(e)
	}
	v, e = Execute(ctx, ExecRunner{}, root, Command{Action: "stashPush", Revision: s.Revision})
	if e != nil || len(v.Snapshot.Changes) != 0 {
		t.Fatalf("stash %+v %v", v, e)
	}
	v, e = Execute(ctx, ExecRunner{}, root, Command{Action: "stashApply", Ref: "stash@{0}", Revision: v.Snapshot.Revision})
	if e != nil || len(v.Snapshot.Changes) == 0 {
		t.Fatalf("stash apply %+v %v", v, e)
	}
	v, e = Execute(ctx, ExecRunner{}, root, Command{Action: "discardTracked", Paths: []string{"новое имя.txt"}, Confirmed: true, Revision: v.Snapshot.Revision})
	if e != nil || len(v.Snapshot.Changes) != 0 {
		t.Fatalf("discard %+v %v", v, e)
	}
}
func TestWorkbenchConflictAndRebaseAbort(t *testing.T) {
	root := workbenchRepo(t)
	ctx := context.Background()
	git(t, root, "switch", "-c", "feature")
	write(t, filepath.Join(root, "a.txt"), "feature\n")
	git(t, root, "add", "a.txt")
	git(t, root, "commit", "-m", "feature")
	git(t, root, "switch", "main")
	write(t, filepath.Join(root, "a.txt"), "main\n")
	git(t, root, "add", "a.txt")
	git(t, root, "commit", "-m", "main")
	git(t, root, "switch", "feature")
	s, e := ReadSnapshot(ctx, ExecRunner{}, root)
	if e != nil {
		t.Fatal(e)
	}
	v, e := Execute(ctx, ExecRunner{}, root, Command{Action: "rebase", Ref: "main", Confirmed: true, Revision: s.Revision})
	if e == nil || v.Snapshot.Operation != "rebase" {
		t.Fatalf("expected unfinished rebase %+v %v", v, e)
	}
	conflict := false
	for _, c := range v.Snapshot.Changes {
		conflict = conflict || c.Area == "conflict"
	}
	if !conflict {
		t.Fatal("no conflict")
	}
	_, e = Commit(ctx, ExecRunner{}, root, []string{"a.txt"}, "quest")
	if e == nil {
		t.Fatal("quest wrote during rebase")
	}
	v, e = Execute(ctx, ExecRunner{}, root, Command{Action: "abort", Confirmed: true, Revision: v.Snapshot.Revision})
	if e != nil || v.Snapshot.Operation != "" || len(v.Snapshot.Changes) != 0 {
		t.Fatalf("abort %+v %v", v, e)
	}
}
