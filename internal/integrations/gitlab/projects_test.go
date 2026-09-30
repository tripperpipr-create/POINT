package gitlab

import (
	"context"
	"reflect"
	"testing"

	"local-agent-workbench/internal/mcpclient"
)

func TestProjects(t *testing.T) {
	client, caller := newFixtureClient(t, fixtureTools(t))
	projects, err := client.Projects(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	args := caller.last()
	if args["membership"] != true || args["order_by"] != "last_activity_at" || args["search"] != nil {
		t.Fatalf("list_projects args = %v", args)
	}
	if len(projects) != 4 {
		t.Fatalf("projects = %d", len(projects))
	}
	payments := projects[0]
	// Сервер отдаёт id строкой (z.coerce.string), GitLab — числом.
	if payments.ID != 42 || payments.Path != "billing/payments" || payments.Namespace != "billing" || payments.DefaultBranch != "main" ||
		payments.Stars != 12 || payments.AccessLevel != 30 || !reflect.DeepEqual(payments.Topics, []string{"go", "payments"}) {
		t.Fatalf("payments = %+v", payments)
	}
	if payments.HTTPURL != "https://gitlab.example.test/billing/payments.git" || payments.SSHURL != "git@gitlab.example.test:billing/payments.git" {
		t.Fatalf("clone urls = %q %q", payments.HTTPURL, payments.SSHURL)
	}
	if payments.LastActivityAt.IsZero() {
		t.Fatal("last activity not parsed")
	}
	// Адрес клона на чужой узел — подмена: окно его не покажет.
	if projects[2].SSHURL != "" || projects[2].Description != "" {
		t.Fatalf("foreign ssh url kept: %+v", projects[2])
	}
	if !projects[3].Archived {
		t.Fatalf("archived lost: %+v", projects[3])
	}

	if _, err = client.Projects(context.Background(), " billing/pay ", ProjectsOwned); err != nil {
		t.Fatal(err)
	}
	args = caller.last()
	if args["owned"] != true || args["membership"] != nil || args["search"] != "billing/pay" || args["search_namespaces"] != true {
		t.Fatalf("owned search args = %v", args)
	}
}

func TestProjectDetail(t *testing.T) {
	client, caller := newFixtureClient(t, fixtureTools(t))
	project, err := client.ProjectDetail(context.Background(), "billing/payments")
	if err != nil {
		t.Fatal(err)
	}
	if caller.last()["project_id"] != "billing/payments" || project.ID != 42 || project.WebURL != "https://gitlab.example.test/billing/payments" {
		t.Fatalf("project = %+v", project)
	}
}

func TestSSHURL(t *testing.T) {
	client, _ := newFixtureClient(t, nil)
	for raw, want := range map[string]string{
		"git@gitlab.example.test:billing/payments.git":       "git@gitlab.example.test:billing/payments.git",
		"git@ssh.gitlab.example.test:billing/payments.git":   "git@ssh.gitlab.example.test:billing/payments.git",
		"ssh://git@gitlab.example.test:2222/billing/pay.git": "ssh://git@gitlab.example.test:2222/billing/pay.git",
		"git@ssh.example.test:billing/payments.git":          "git@ssh.example.test:billing/payments.git",
		"git@evil.example.org:billing/payments.git":          "",
		"git@gitlab.example.test.evil.org:billing/p.git":     "",
		"git@gitlab.example.test:-oProxyCommand=calc":        "",
		"ssh://git@gitlab.example.test/billing/p.git?x=1":    "",
		"git@gitlab.example.test:billing/pay ments.git":      "",
		"https://gitlab.example.test/billing/payments.git":   "",
		"git@example.test:billing/payments.git":              "",
	} {
		if got := client.sshURL(raw); got != want {
			t.Errorf("sshURL(%q) = %q, want %q", raw, got, want)
		}
	}
	// Общий родитель — не общий суффикс верхнего уровня.
	if sameParentDomain("gitlab.com", "evil.com") {
		t.Fatal("top-level domain treated as shared parent")
	}
}

func TestCommits(t *testing.T) {
	client, caller := newFixtureClient(t, fixtureTools(t))
	commits, err := client.Commits(context.Background(), "billing/payments", "fix/webhook-retry", 0)
	if err != nil {
		t.Fatal(err)
	}
	args := caller.last()
	if args["ref_name"] != "fix/webhook-retry" || args["page"] != 1 || args["with_stats"] != true {
		t.Fatalf("list_commits args = %v", args)
	}
	if len(commits) != 3 {
		t.Fatalf("commits = %d", len(commits))
	}
	first := commits[0]
	if first.ShortID != "a1b2c3d4" || first.Stats == nil || first.Stats.Additions != 24 || first.Stats.Deletions != 3 ||
		first.Message == "" || first.AuthoredAt.IsZero() || len(first.ParentIDs) != 1 {
		t.Fatalf("first commit = %+v", first)
	}
	if merge := commits[2]; len(merge.ParentIDs) != 2 {
		t.Fatalf("merge commit parents = %v", merge.ParentIDs)
	}
	// Сообщение из одного заголовка не повторяется вторым полем.
	if commits[1].Message != "" {
		t.Fatalf("title-only message duplicated: %q", commits[1].Message)
	}
}

func TestCommitDetail(t *testing.T) {
	client, caller := newFixtureClient(t, fixtureTools(t))
	detail, err := client.Commit(context.Background(), "billing/payments", "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(caller.calls, []string{"get_commit", "get_commit_diff"}) || caller.arguments[0]["stats"] != true {
		t.Fatalf("calls = %v %v", caller.calls, caller.arguments)
	}
	if len(detail.Files) != 3 || detail.FilesTrimmed {
		t.Fatalf("files = %+v", detail.Files)
	}
	byPath := map[string]CommitFile{}
	for _, file := range detail.Files {
		byPath[file.NewPath] = file
	}
	if retry := byPath["internal/billing/retry.go"]; retry.Additions != 3 || retry.Deletions != 1 {
		t.Fatalf("retry.go counts = %+v", retry)
	}
	if test := byPath["internal/billing/retry_test.go"]; !test.New || test.Additions != 3 {
		t.Fatalf("new file = %+v", test)
	}
	if renamed := byPath["docs/webhooks.md"]; !renamed.Renamed || renamed.OldPath != "docs/hooks.md" {
		t.Fatalf("renamed = %+v", renamed)
	}
}

func TestBranches(t *testing.T) {
	client, _ := newFixtureClient(t, fixtureTools(t))
	branches, err := client.Branches(context.Background(), "billing/payments", "")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, branch := range branches {
		names = append(names, branch.Name)
	}
	// Ветка по умолчанию — первой, остальные — по свежести коммита.
	if !reflect.DeepEqual(names, []string{"main", "fix/webhook-retry", "feat/backoff"}) {
		t.Fatalf("branch order = %v", names)
	}
	if !branches[0].Protected || !branches[2].Merged || branches[1].Commit.ShortID != "a1b2c3d4" {
		t.Fatalf("branches = %+v", branches)
	}
}

func TestTree(t *testing.T) {
	client, caller := newFixtureClient(t, fixtureTools(t))
	tree, err := client.Tree(context.Background(), "billing/payments", "", "main")
	if err != nil {
		t.Fatal(err)
	}
	if caller.last()["ref"] != "main" || caller.last()["path"] != nil {
		t.Fatalf("tree args = %v", caller.last())
	}
	names := []string{}
	for _, entry := range tree.Entries {
		names = append(names, entry.Name)
	}
	if !reflect.DeepEqual(names, []string{"docs", "internal", "vendor-sdk", ".gitlab-ci.yml", "go.mod", "README.md"}) || tree.Trimmed {
		t.Fatalf("tree = %v trimmed=%v", names, tree.Trimmed)
	}

	// Сервер переходит на ответ {items, next_page_token}, когда страниц больше.
	caller.override["get_repository_tree"] = mcpclient.CallResult{Content: []mcpclient.Content{{Type: "text",
		Text: `{"items":[{"id":"b","name":"a.go","type":"blob","path":"src/a.go","mode":"100644"}],"next_page_token":"abc"}`}}}
	tree, err = client.Tree(context.Background(), "billing/payments", "src", "")
	if err != nil || len(tree.Entries) != 1 || !tree.Trimmed {
		t.Fatalf("keyset tree = %+v, %v", tree, err)
	}

	// Пустой репозиторий отвечает 404 на корень — это пустое дерево, не сбой.
	caller.override["get_repository_tree"] = errorResult("Repository or path not found")
	tree, err = client.Tree(context.Background(), "billing/empty", "", "")
	if err != nil || len(tree.Entries) != 0 {
		t.Fatalf("empty repo tree = %+v, %v", tree, err)
	}
}

func TestProjectFeaturesDegrade(t *testing.T) {
	client, _ := newFixtureClient(t, []string{"list_projects", "get_commit"})
	if _, err := client.Commit(context.Background(), "billing/payments", "a1b2c3d4"); ReasonOf(err) != ReasonToolMissing {
		t.Fatalf("commit without get_commit_diff: %v", err)
	}
	if _, err := client.Branches(context.Background(), "billing/payments", ""); ReasonOf(err) != ReasonToolMissing {
		t.Fatalf("branches without tool: %v", err)
	}
}
