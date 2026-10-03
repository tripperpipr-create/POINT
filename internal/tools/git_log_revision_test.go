package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/workspace"
)

func TestGitLogSingleRevisionsAndFileFilter(t *testing.T) {
	root, git := gitHistoryRepo(t)
	for _, file := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte(file), 0600); err != nil {
			t.Fatal(err)
		}
		git("add", file)
		git("commit", "-m", file)
	}
	git("branch", "topic", "HEAD~1")
	git("update-ref", "refs/remotes/origin/topic", "HEAD~1")
	git("tag", "-a", "v1", "HEAD~1", "-m", "version")
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	sha, err := gitLogRevision(context.Background(), root, "topic")
	if err != nil {
		t.Fatal(err)
	}
	for _, rev := range []string{"topic", "origin/topic", "v1", sha, "HEAD~1"} {
		t.Run(rev, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"revision": rev, "path": "a.txt"})
			result := (GitLog{FS: fs}).Execute(context.Background(), raw)
			var payload gitLogPayload
			if !result.OK || json.Unmarshal(result.Output, &payload) != nil || payload.Count != 1 || payload.Commits[0].Hash != sha {
				t.Fatalf("%s: %+v %s", rev, result, result.Output)
			}
		})
	}
	result := (GitLog{FS: fs}).Execute(context.Background(), json.RawMessage(`{}`))
	var payload gitLogPayload
	_ = json.Unmarshal(result.Output, &payload)
	if !result.OK || payload.Count != 2 || payload.Commits[0].Subject != "b.txt" {
		t.Fatal(result)
	}
	for _, rev := range []string{"unknown", "--all", "--output=owned", "HEAD..topic", "HEAD...topic", "HEAD\n--all", ":/a" + string(rune(0))} {
		raw, _ := json.Marshal(map[string]string{"revision": rev})
		result = (GitLog{FS: fs}).Execute(context.Background(), raw)
		if result.OK || result.Error == nil || result.Error.Code != "invalid_revision" {
			t.Fatalf("%q: %+v", rev, result)
		}
	}
	result = (GitLog{FS: fs}).Execute(context.Background(), json.RawMessage(`{"path":"origin/topic"}`))
	if result.OK || result.Error == nil || result.Error.Code != "invalid_path" || !strings.Contains(EncodeResult(result), "revision") {
		t.Fatal(result)
	}
	// Existing paths that also name a branch remain file filters.
	git("branch", "a.txt")
	result = (GitLog{FS: fs}).Execute(context.Background(), json.RawMessage(`{"path":"a.txt"}`))
	_ = json.Unmarshal(result.Output, &payload)
	if !result.OK || payload.Count != 1 {
		t.Fatal(result)
	}
}

func TestGitLogRevisionSelectsNestedRepository(t *testing.T) {
	root := nestedGitWorkspace(t)
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	result := (GitLog{FS: fs}).Execute(context.Background(), json.RawMessage(`{"repo":"cf-vue-apps","revision":"HEAD","path":"cf-vue-apps/README.md"}`))
	var payload gitLogPayload
	_ = json.Unmarshal(result.Output, &payload)
	if !result.OK || payload.Count != 1 || payload.Commits[0].Subject != "init cf-vue-apps" || payload.Path != "README.md" {
		t.Fatal(result)
	}
}
