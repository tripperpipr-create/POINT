package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// nestedReposForTest — папка без Git с двумя вложенными репозиториями, как
// cf-pages/ и cf-vue-apps/ в E1/E2: у каждого один начальный коммит.
func nestedReposForTest(t *testing.T) (string, map[string]string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	root := t.TempDir()
	heads := map[string]string{}
	for _, repo := range []string{"cf-pages", "cf-vue-apps"} {
		dir := filepath.Join(root, repo)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"}} {
			if output, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v: %s", args, err, output)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(repo+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"add", "README.md"}, {"commit", "-q", "-m", "init"}} {
			if output, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v: %s", args, err, output)
			}
		}
		heads[repo] = gitHeadForTest(t, dir)
	}
	return root, heads
}

func gitHeadForTest(t *testing.T, dir string) string {
	t.Helper()
	output, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse: %v: %s", err, output)
	}
	return strings.TrimSpace(string(output))
}

func writeDeliveryFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNestedDeliveryCommitsEachRepositoryItsOwnFiles(t *testing.T) {
	root, heads := nestedReposForTest(t)
	if repos, ok := cleanNestedGitRepos(root); !ok || strings.Join(repos, ",") != "cf-pages,cf-vue-apps" {
		t.Fatalf("clean nested repositories not recognised: %v %v", repos, ok)
	}
	writeDeliveryFile(t, root, "cf-pages/.gitlab-ci.yml", "stages: [deploy]\n")
	writeDeliveryFile(t, root, "cf-vue-apps/deploy.mjs", "export {}\n")
	commits, err := commitWorkOrderDeliveryV2(context.Background(), root, "quest-q06", "evidence-q06", []string{"cf-pages/.gitlab-ci.yml", "cf-vue-apps/deploy.mjs"})
	if err != nil || len(commits) != 2 {
		t.Fatalf("commits=%#v err=%v", commits, err)
	}
	for _, commit := range commits {
		dir := filepath.Join(root, commit.Repo)
		if commit.CommitID == heads[commit.Repo] || gitHeadForTest(t, dir) != commit.CommitID {
			t.Fatalf("%s: commit %s is not the new HEAD", commit.Repo, commit.CommitID)
		}
		files, _ := exec.Command("git", "-C", dir, "show", "--name-only", "--pretty=%B", "HEAD").CombinedOutput()
		if !strings.Contains(string(files), "Point Quest quest-q06") || strings.Contains(string(files), "cf-") && !strings.Contains(string(files), "Point Quest") {
			t.Fatalf("%s: unexpected commit: %s", commit.Repo, files)
		}
	}
	pages, _ := exec.Command("git", "-C", filepath.Join(root, "cf-pages"), "show", "--name-only", "--pretty=", "HEAD").CombinedOutput()
	if strings.TrimSpace(string(pages)) != ".gitlab-ci.yml" {
		t.Fatalf("cf-pages commit carries foreign files: %q", pages)
	}
}

func TestNestedDeliveryRefusesFileOutsideRepositories(t *testing.T) {
	root, heads := nestedReposForTest(t)
	writeDeliveryFile(t, root, "notes.md", "outside\n")
	writeDeliveryFile(t, root, "cf-pages/a.txt", "a\n")
	_, err := commitWorkOrderDeliveryV2(context.Background(), root, "q", "e", []string{"cf-pages/a.txt", "notes.md"})
	if err == nil || !strings.Contains(err.Error(), "notes.md") {
		t.Fatalf("file outside repositories was not refused: %v", err)
	}
	if gitHeadForTest(t, filepath.Join(root, "cf-pages")) != heads["cf-pages"] {
		t.Fatal("refused delivery still committed")
	}
}

// Сбой во втором репозитории снимает коммит, уже сделанный в первом.
func TestNestedDeliveryUndoesEarlierCommitsWhenLaterRepositoryFails(t *testing.T) {
	root, heads := nestedReposForTest(t)
	writeDeliveryFile(t, root, "cf-pages/a.txt", "a\n")
	writeDeliveryFile(t, root, "cf-vue-apps/b.txt", "b\n")
	writeDeliveryFile(t, root, "cf-vue-apps/user-change.txt", "not ours\n")
	_, err := commitWorkOrderDeliveryV2(context.Background(), root, "q", "e", []string{"cf-pages/a.txt", "cf-vue-apps/b.txt"})
	if err == nil || !strings.Contains(err.Error(), "cf-vue-apps") {
		t.Fatalf("drift in the second repository was not refused: %v", err)
	}
	if head := gitHeadForTest(t, filepath.Join(root, "cf-pages")); head != heads["cf-pages"] {
		t.Fatalf("first repository keeps a half-delivery commit: %s", head)
	}
	staged, _ := exec.Command("git", "-C", filepath.Join(root, "cf-pages"), "diff", "--cached", "--name-only").CombinedOutput()
	if strings.TrimSpace(string(staged)) != "" {
		t.Fatalf("undone delivery left files staged: %q", staged)
	}
}

func TestDirtyNestedRepositoryKeepsDeliveryWithoutCommit(t *testing.T) {
	root, _ := nestedReposForTest(t)
	writeDeliveryFile(t, root, "cf-vue-apps/wip.txt", "uncommitted\n")
	if _, ok := cleanNestedGitRepos(root); ok {
		t.Fatal("a dirty nested repository must not get squash delivery")
	}
}
