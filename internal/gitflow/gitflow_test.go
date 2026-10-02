package gitflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := ExecRunner{}.Run(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func identity(t *testing.T, dir string) {
	git(t, dir, "config", "user.name", "Анна Тестова")
	git(t, dir, "config", "user.email", "anna@example.test")
	git(t, dir, "config", "commit.gpgsign", "false")
}

// stale собирает инцидент 02.10: локальная ветка влита в main на сервере и
// там удалена, а локальный main отстаёт от origin/main.
func stale(t *testing.T) (root, work string) {
	root = t.TempDir()
	origin := filepath.Join(root, "origin.git")
	git(t, root, "init", "--bare", "-b", "main", origin)
	git(t, origin, "config", "receive.advertisePushOptions", "true")
	seed := filepath.Join(root, "seed")
	git(t, root, "clone", "-q", origin, seed)
	identity(t, seed)
	write(t, filepath.Join(seed, "app.txt"), "v1\n")
	git(t, seed, "add", "-A")
	git(t, seed, "commit", "-q", "-m", "fix: init")
	git(t, seed, "push", "-q", "origin", "main")

	work = filepath.Join(root, "work")
	git(t, root, "clone", "-q", origin, work)
	identity(t, work)
	git(t, work, "switch", "-q", "-c", "merge/reports-v21")
	write(t, filepath.Join(work, "report.txt"), "report\n")
	git(t, work, "add", "-A")
	git(t, work, "commit", "-q", "-m", "feat: reports")
	git(t, work, "push", "-q", "-u", "origin", "merge/reports-v21")

	git(t, seed, "fetch", "-q", "origin")
	git(t, seed, "merge", "-q", "--no-ff", "-m", "Merge branch 'merge/reports-v21' into 'main'", "origin/merge/reports-v21")
	write(t, filepath.Join(seed, "later.txt"), "later\n")
	git(t, seed, "add", "-A")
	git(t, seed, "commit", "-q", "-m", "fix: later work")
	git(t, seed, "push", "-q", "origin", "main")
	git(t, seed, "push", "-q", "origin", "--delete", "merge/reports-v21")
	return root, work
}

func TestInspectSeesMergedDeletedBranch(t *testing.T) {
	_, work := stale(t)
	reports := Inspect(context.Background(), ExecRunner{}, work, InspectOptions{Fetch: true})
	if len(reports) != 1 {
		t.Fatalf("reports = %+v", reports)
	}
	report := reports[0]
	if report.Path != "." || report.Current != "merge/reports-v21" || !report.UpstreamGone || !report.MergedIntoDefault {
		t.Fatalf("stale branch not recognised: %+v", report)
	}
	if report.DefaultRef != "origin/main" || report.BehindDefault != 2 || report.Protected {
		t.Fatalf("default branch facts wrong: %+v", report)
	}
	warnings := strings.Join(Warnings(report), " | ")
	for _, want := range []string{"удалена на сервере", "уже влита в main", "отстаёт от origin/main на 2 коммита"} {
		if !strings.Contains(warnings, want) {
			t.Fatalf("warning %q missing: %s", want, warnings)
		}
	}
	plan := BuildPlan(reports, "feat/flag-get-flag")
	if plan.Choice != domain.GitChoiceRequired || plan.Recommended != RecommendNewDefault || plan.Mode != "" {
		t.Fatalf("unprotected stale branch must ask with new-default advice: %+v", plan)
	}
	if err := domain.GitPlanApprovalError(plan); err == nil {
		t.Fatal("plan with open choice must not be approvable")
	}
	if err := ApplyChoice(plan, RecommendNewDefault, "feat/flag-get-flag"); err != nil {
		t.Fatal(err)
	}
	if plan.Choice != domain.GitChoiceChosen || plan.Mode != domain.GitModeNew || plan.BaseKind != "default" {
		t.Fatalf("choice not applied: %+v", plan)
	}
	base, commit := domain.GitRepoBase(*plan, plan.Repositories[0])
	if base != "origin/main" || commit != git(t, work, "rev-parse", "origin/main") {
		t.Fatalf("base = %s %s", base, commit)
	}
}

func TestProtectedMainProposesNewBranchFromFreshOrigin(t *testing.T) {
	_, work := stale(t)
	git(t, work, "switch", "-q", "main")
	reports := Inspect(context.Background(), ExecRunner{}, work, InspectOptions{Fetch: true})
	report := reports[0]
	if !report.Protected || report.Behind != 3 || report.CurrentBase != "origin/main" {
		t.Fatalf("local main behind origin must branch from origin/main: %+v", report)
	}
	plan := BuildPlan(reports, "feat/x")
	if plan.Choice != domain.GitChoiceProposed || plan.Mode != domain.GitModeNew || plan.BaseKind != "current" {
		t.Fatalf("protected branch must get a proposed new branch: %+v", plan)
	}
	if err := domain.GitPlanApprovalError(plan); err != nil {
		t.Fatal(err)
	}
	_, commit := domain.GitRepoBase(*plan, plan.Repositories[0])
	created, err := Checkout(context.Background(), ExecRunner{}, work, plan.Branch, commit)
	if err != nil || !created {
		t.Fatalf("checkout: %v %v", created, err)
	}
	if upstream, _ := run(context.Background(), ExecRunner{}, work, "rev-parse", "--abbrev-ref", "feat/x@{upstream}"); upstream != "" && !strings.Contains(upstream, "feat/x") {
		t.Fatalf("new branch must not track %s", upstream)
	}
	if again, err := Checkout(context.Background(), ExecRunner{}, work, plan.Branch, commit); err != nil || again {
		t.Fatalf("checkout must be idempotent: %v %v", again, err)
	}
	UndoCheckout(context.Background(), ExecRunner{}, work, "main", "feat/x", commit)
	if current := git(t, work, "branch", "--show-current"); current != "main" {
		t.Fatalf("undo left %s", current)
	}
	if _, err := run(context.Background(), ExecRunner{}, work, "rev-parse", "--verify", "--quiet", "refs/heads/feat/x"); err == nil {
		t.Fatal("undo must delete the untouched branch")
	}
}

func TestDirtyTreeKeepsBranchAndExplains(t *testing.T) {
	_, work := stale(t)
	write(t, filepath.Join(work, "mine.txt"), "draft\n")
	plan := BuildPlan(Inspect(context.Background(), ExecRunner{}, work, InspectOptions{}), "feat/x")
	if plan.Mode != domain.GitModeNone || plan.Choice != domain.GitChoiceProposed || !strings.Contains(strings.Join(plan.Notes, " "), "незакоммиченные изменения (1)") {
		t.Fatalf("dirty plan = %+v", plan)
	}
}

func TestCommitTakesOnlyQuestFilesAsUser(t *testing.T) {
	_, work := stale(t)
	write(t, filepath.Join(work, "quest.txt"), "quest\n")
	write(t, filepath.Join(work, "app.txt"), "v2\n")
	write(t, filepath.Join(work, "mine.txt"), "not part of the quest\n")
	message := ComposeMessage("feat: endpoint", "Что сделано.", map[string]string{"Point-Quest": "quest_1"})
	commit, err := Commit(context.Background(), ExecRunner{}, work, []string{"quest.txt", "app.txt"}, message)
	if err != nil {
		t.Fatal(err)
	}
	files := git(t, work, "show", "--name-only", "--format=", commit)
	if files != "app.txt\nquest.txt" {
		t.Fatalf("commit files = %q", files)
	}
	if author := git(t, work, "log", "-1", "--format=%an <%ae>"); author != "Анна Тестова <anna@example.test>" {
		t.Fatalf("author = %s", author)
	}
	if body := git(t, work, "log", "-1", "--format=%B"); !strings.HasSuffix(body, "Point-Quest: quest_1") || !strings.HasPrefix(body, "feat: endpoint\n\nЧто сделано.") {
		t.Fatalf("message = %q", body)
	}
	if status := git(t, work, "status", "--porcelain"); status != "?? mine.txt" {
		t.Fatalf("foreign file must stay untouched: %q", status)
	}
	if _, err := Commit(context.Background(), ExecRunner{}, work, []string{"quest.txt"}, "again"); err != ErrNothingToCommit {
		t.Fatalf("second commit err = %v", err)
	}
}

func TestPushSetsUpstreamAndSendsMergeRequestOptions(t *testing.T) {
	_, work := stale(t)
	git(t, work, "switch", "-q", "--no-track", "-c", "feat/push", "origin/main")
	write(t, filepath.Join(work, "x.txt"), "x\n")
	if _, err := Commit(context.Background(), ExecRunner{}, work, []string{"x.txt"}, "feat: x"); err != nil {
		t.Fatal(err)
	}
	result, err := Push(context.Background(), ExecRunner{}, work, "origin", "feat/push", &MergeRequest{Target: "main", Title: "feat: x", Description: "line one\nline two"})
	if err != nil {
		t.Fatalf("push: %v %s", err, result.Output)
	}
	if upstream := git(t, work, "rev-parse", "--abbrev-ref", "feat/push@{upstream}"); upstream != "origin/feat/push" {
		t.Fatalf("upstream = %s", upstream)
	}
	again, err := Push(context.Background(), ExecRunner{}, work, "origin", "feat/push", nil)
	if err != nil || !again.UpToDate {
		t.Fatalf("second push must be up to date: %+v %v", again, err)
	}
}

func TestNestedRepositoriesAreFound(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"lk-backend", "lk-admin"} {
		dir := filepath.Join(root, name)
		git(t, root, "init", "-q", "-b", "main", dir)
	}
	reports := Repositories(context.Background(), ExecRunner{}, root)
	if len(reports) != 2 || reports[0].Path != "lk-admin" || reports[1].Path != "lk-backend" {
		t.Fatalf("nested = %+v", reports)
	}
}

func TestNamingFallbacks(t *testing.T) {
	if got := Slug("Новый эндпоинт GET /flag/get-flag?project=...", 40); got != "novyy-endpoint-get-flag-get-flag-project" {
		t.Fatalf("slug = %q", got)
	}
	if got := FallbackBranchName("Исправить падение отчёта", map[string]int{"bugfix": 3, "fix": 1}); got != "bugfix/ispravit-padenie-otcheta" {
		t.Fatalf("branch = %q", got)
	}
	if got := FallbackBranchName("Новый эндпоинт", nil); got != "feat/novyy-endpoint" {
		t.Fatalf("branch = %q", got)
	}
	if got := SanitizeBranchName("`feat/Flag get flag..x`"); got != "feat/Flag-get-flag.x" {
		t.Fatalf("sanitize = %q", got)
	}
	style := DetectCommitStyle([]string{"fix: onboarding", "feat(api): x", "app v2.1", "fix path"})
	if !style.Conventional || style.Cyrillic {
		t.Fatalf("style = %+v", style)
	}
	if got := FallbackCommitSubject("Новый эндпоинт GET /flag/get-flag", style); got != "feat: новый эндпоинт GET /flag/get-flag" {
		t.Fatalf("subject = %q", got)
	}
	if got := ComposeMessage("s", "", map[string]string{"Point-Quest": "q", "Point-Evidence": "e"}); got != "s\n\nPoint-Quest: q\nPoint-Evidence: e" {
		t.Fatalf("message = %q", got)
	}
}

func TestRemoteURLs(t *testing.T) {
	for remote, want := range map[string]string{
		"https://user:token@gitlab.centrofinans.ru/sites/lk-backend.git": "https://gitlab.centrofinans.ru/sites/lk-backend",
		"git@gitlab.example.test:billing/payments.git":                    "https://gitlab.example.test/billing/payments",
	} {
		if got, ok := WebURL(remote); !ok || got != want {
			t.Fatalf("WebURL(%s) = %s", remote, got)
		}
	}
	if !LooksLikeGitLab("https://gitlab.centrofinans.ru/sites/lk-backend.git", "") || LooksLikeGitLab("https://github.com/a/b.git", "") {
		t.Fatal("gitlab detection wrong")
	}
	if !LooksLikeGitLab("https://code.company.test/a/b.git", "https://code.company.test") {
		t.Fatal("connected gitlab host must count")
	}
	url := NewMergeRequestURL("git@gitlab.example.test:billing/payments.git", "feat/x", "main")
	if !strings.HasPrefix(url, "https://gitlab.example.test/billing/payments/-/merge_requests/new?") || !strings.Contains(url, "feat%2Fx") {
		t.Fatalf("new MR url = %s", url)
	}
	created, open := ParseMergeRequestURLs("remote: View merge request for feat/x:\nremote:   https://gitlab.example.test/a/b/-/merge_requests/12\n")
	if created != "https://gitlab.example.test/a/b/-/merge_requests/12" || open != "" {
		t.Fatalf("parse = %s %s", created, open)
	}
}

type fakeRunner map[string]string

func (f fakeRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	if out, ok := f[strings.Join(args, " ")]; ok {
		return []byte(out), nil
	}
	return nil, os.ErrNotExist
}

func TestCommitRefusesWithoutIdentity(t *testing.T) {
	_, err := Commit(context.Background(), fakeRunner{}, t.TempDir(), []string{"a.txt"}, "m")
	if err == nil || !strings.Contains(err.Error(), "Git не знает, кто вы") {
		t.Fatalf("err = %v", err)
	}
}
