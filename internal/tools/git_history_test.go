package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/workspace"
)

func gitHistoryRepo(t *testing.T) (string, func(args ...string)) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Point", "GIT_AUTHOR_EMAIL=point@local",
			"GIT_COMMITTER_NAME=Point", "GIT_COMMITTER_EMAIL=point@local",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, strings.TrimSpace(string(out)))
		}
	}
	run("init")
	return root, run
}

type gitLogPayload struct {
	Commits []struct {
		Hash      string `json:"hash"`
		ShortHash string `json:"shortHash"`
		Author    string `json:"author"`
		Date      string `json:"date"`
		Subject   string `json:"subject"`
	} `json:"commits"`
	Count int    `json:"count"`
	Path  string `json:"path"`
	Note  string `json:"note"`
}

func TestGitLogListsCommitsNewestFirst(t *testing.T) {
	root, runGitIn := gitHistoryRepo(t)
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", "one\n")
	runGitIn("add", "a.txt")
	runGitIn("commit", "-m", "first commit")
	write("b.txt", "two\n")
	runGitIn("add", "b.txt")
	// Сообщение с табуляцией: subject идёт в выдаче последним полем именно
	// потому, что внутри него может быть разделитель.
	runGitIn("commit", "-m", "second\tcommit")

	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	result := (GitLog{FS: fs}).Execute(context.Background(), json.RawMessage(`{}`))
	if !result.OK {
		t.Fatalf("git_log failed: %#v", result)
	}
	var payload gitLogPayload
	if err := json.Unmarshal(result.Output, &payload); err != nil {
		t.Fatalf("выдача не разбирается: %v (%s)", err, result.Output)
	}
	if payload.Count != 2 || len(payload.Commits) != 2 {
		t.Fatalf("ожидались два коммита: %s", result.Output)
	}
	if payload.Commits[0].Subject != "second\tcommit" {
		t.Fatalf("порядок или subject испорчены: %q", payload.Commits[0].Subject)
	}
	if payload.Commits[1].Subject != "first commit" {
		t.Fatalf("второй по счёту коммит не самый старый: %q", payload.Commits[1].Subject)
	}
	head := payload.Commits[0]
	if len(head.Hash) != 40 || head.ShortHash == "" || head.Author != "Point" || head.Date == "" {
		t.Fatalf("поля коммита неполны: %#v", head)
	}
	// Оговорка про давность обязана ехать вместе с историей: git на сервер сам
	// не ходит, и «последний коммит» верен на момент последнего fetch.
	if !strings.Contains(payload.Note, "fetch") {
		t.Fatalf("оговорка о давности потеряна: %q", payload.Note)
	}
}

func TestGitLogFiltersByPathAndMessage(t *testing.T) {
	root, runGitIn := gitHistoryRepo(t)
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("kept.txt", "one\n")
	runGitIn("add", "kept.txt")
	runGitIn("commit", "-m", "touch kept")
	write("other.txt", "two\n")
	runGitIn("add", "other.txt")
	runGitIn("commit", "-m", "TOUCH other")

	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	byPath := (GitLog{FS: fs}).Execute(context.Background(), json.RawMessage(`{"path":"kept.txt"}`))
	if !byPath.OK {
		t.Fatalf("git_log по пути failed: %#v", byPath)
	}
	var pathPayload gitLogPayload
	if err := json.Unmarshal(byPath.Output, &pathPayload); err != nil {
		t.Fatalf("выдача не разбирается: %v (%s)", err, byPath.Output)
	}
	if pathPayload.Count != 1 || pathPayload.Commits[0].Subject != "touch kept" || pathPayload.Path != "kept.txt" {
		t.Fatalf("отбор по пути не применился: %s", byPath.Output)
	}

	byMessage := (GitLog{FS: fs}).Execute(context.Background(), json.RawMessage(`{"contains":"touch OTHER"}`))
	if !byMessage.OK {
		t.Fatalf("git_log по сообщению failed: %#v", byMessage)
	}
	var messagePayload gitLogPayload
	if err := json.Unmarshal(byMessage.Output, &messagePayload); err != nil {
		t.Fatalf("выдача не разбирается: %v (%s)", err, byMessage.Output)
	}
	if messagePayload.Count != 1 || messagePayload.Commits[0].Subject != "TOUCH other" {
		t.Fatalf("отбор по сообщению регистрозависим или потерян: %s", byMessage.Output)
	}
}

func TestGitLogOnRepositoryWithoutCommits(t *testing.T) {
	root, _ := gitHistoryRepo(t)
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	// Пустая история — это ответ, а не поломка: отказ заставил бы модель гадать,
	// чего именно нет — репозитория, прав или коммитов.
	result := (GitLog{FS: fs}).Execute(context.Background(), json.RawMessage(`{}`))
	if !result.OK {
		t.Fatalf("пустая история отдана отказом: %#v", result)
	}
	var payload gitLogPayload
	if err := json.Unmarshal(result.Output, &payload); err != nil {
		t.Fatalf("выдача не разбирается: %v (%s)", err, result.Output)
	}
	if payload.Count != 0 || len(payload.Commits) != 0 || !strings.Contains(payload.Note, "no commits") {
		t.Fatalf("пустая история описана неверно: %s", result.Output)
	}
}

func TestGitTagsListsTagsWithTheirCommit(t *testing.T) {
	root, runGitIn := gitHistoryRepo(t)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitIn("add", "a.txt")
	runGitIn("commit", "-m", "init")
	runGitIn("tag", "v1.0.0")
	runGitIn("tag", "-a", "v2.0.0", "-m", "release two")
	runGitIn("tag", "nightly")

	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	result := (GitTags{FS: fs}).Execute(context.Background(), json.RawMessage(`{}`))
	if !result.OK {
		t.Fatalf("git_tags failed: %#v", result)
	}
	var payload struct {
		Tags []struct {
			Name      string `json:"name"`
			Revision  string `json:"revision"`
			Annotated bool   `json:"annotated"`
			Date      string `json:"date"`
			Subject   string `json:"subject"`
		} `json:"tags"`
		Count int    `json:"count"`
		Note  string `json:"note"`
	}
	if err := json.Unmarshal(result.Output, &payload); err != nil {
		t.Fatalf("выдача не разбирается: %v (%s)", err, result.Output)
	}
	if payload.Count != 3 {
		t.Fatalf("ожидались три метки: %s", result.Output)
	}
	head := strings.TrimSpace(gitTagsHeadRevision(t, root))
	byName := map[string]struct {
		revision  string
		annotated bool
		subject   string
		date      string
	}{}
	for _, tag := range payload.Tags {
		byName[tag.Name] = struct {
			revision  string
			annotated bool
			subject   string
			date      string
		}{tag.Revision, tag.Annotated, tag.Subject, tag.Date}
	}
	light, ok := byName["v1.0.0"]
	if !ok || light.annotated || light.revision != head || light.date == "" {
		t.Fatalf("лёгкая метка описана неверно: %#v (head=%s)", byName, head)
	}
	// У аннотированной метки objectname — объект метки, а не коммит; спрашивают
	// всегда про коммит, поэтому в revision обязан лежать он.
	annotated, ok := byName["v2.0.0"]
	if !ok || !annotated.annotated || annotated.revision != head || annotated.subject != "release two" {
		t.Fatalf("аннотированная метка описана неверно: %#v (head=%s)", byName, head)
	}
	if !strings.Contains(payload.Note, "fetch") {
		t.Fatalf("оговорка о давности потеряна: %q", payload.Note)
	}

	filtered := (GitTags{FS: fs}).Execute(context.Background(), json.RawMessage(`{"contains":"V2"}`))
	if !filtered.OK {
		t.Fatalf("git_tags с отбором failed: %#v", filtered)
	}
	text := string(filtered.Output)
	if !strings.Contains(text, "v2.0.0") {
		t.Fatalf("отбор регистронезависимый, метка потеряна: %s", text)
	}
	if strings.Contains(text, "nightly") || strings.Contains(text, "v1.0.0") {
		t.Fatalf("отбор не применился: %s", text)
	}
}

func gitTagsHeadRevision(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "rev-parse", "--short", "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return string(out)
}
