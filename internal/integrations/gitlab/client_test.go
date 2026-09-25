package gitlab

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/mcpclient"
)

const fixtures = "testdata/zereight-2.1.66"

// fixtureCaller отвечает файлами снятых ответов и запоминает аргументы.
type fixtureCaller struct {
	t         *testing.T
	calls     []string
	arguments []map[string]any
	override  map[string]mcpclient.CallResult
}

func (f *fixtureCaller) CallTool(_ context.Context, tool string, arguments any) (mcpclient.CallResult, error) {
	f.calls = append(f.calls, tool)
	args, _ := arguments.(map[string]any)
	f.arguments = append(f.arguments, args)
	if result, ok := f.override[tool]; ok {
		return result, nil
	}
	for _, name := range []string{tool + ".json", tool + ".txt"} {
		raw, err := os.ReadFile(filepath.Join(fixtures, "responses", name))
		if err == nil {
			return mcpclient.CallResult{Content: []mcpclient.Content{{Type: "text", Text: string(raw)}}}, nil
		}
	}
	f.t.Fatalf("no fixture for %s", tool)
	return mcpclient.CallResult{}, nil
}

func (f *fixtureCaller) last() map[string]any { return f.arguments[len(f.arguments)-1] }

func fixtureTools(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtures, "tools-list.json"))
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(list.Tools))
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func newFixtureClient(t *testing.T, tools []string) (*Client, *fixtureCaller) {
	caller := &fixtureCaller{t: t, override: map[string]mcpclient.CallResult{}}
	return NewClient(caller, "https://GitLab.example.test/", tools), caller
}

func errorResult(text string) mcpclient.CallResult {
	return mcpclient.CallResult{IsError: true, Content: []mcpclient.Content{{Type: "text", Text: text}}}
}

func TestToolsMatchSpike(t *testing.T) {
	captured := fixtureTools(t)
	want := append([]string(nil), Tools...)
	sort.Strings(captured)
	sort.Strings(want)
	if !reflect.DeepEqual(captured, want) {
		t.Fatalf("tools-list.json = %v, recipe tools = %v", captured, want)
	}
	raw, err := os.ReadFile(filepath.Join(fixtures, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Package string `json:"package"`
		Version string `json:"version"`
		Recipe  struct {
			Env map[string]string `json:"env"`
		} `json:"recipe"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Package != ServerPackage || manifest.Version != ServerVersion {
		t.Fatalf("manifest pins %s@%s, recipe %s@%s", manifest.Package, manifest.Version, ServerPackage, ServerVersion)
	}
	launch, err := Recipe(Settings{URL: "https://gitlab.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range manifest.Recipe.Env {
		if name == "GITLAB_TOOLS" {
			got := strings.Split(launch.Env[name], ",")
			wanted := strings.Split(value, ",")
			sort.Strings(got)
			sort.Strings(wanted)
			if !reflect.DeepEqual(got, wanted) {
				t.Fatalf("GITLAB_TOOLS = %v, manifest %v", got, wanted)
			}
			continue
		}
		if launch.Env[name] != value {
			t.Fatalf("%s = %q, manifest %q", name, launch.Env[name], value)
		}
	}
}

func TestCapabilities(t *testing.T) {
	caps := Capabilities(fixtureTools(t))
	for feature, capability := range caps {
		if !capability.Available {
			t.Fatalf("%s unavailable with the full tool list: %+v", feature, capability)
		}
	}
	if got := caps[FeatureDiff].Tools; !reflect.DeepEqual(got, []string{"get_merge_request_file_diff"}) {
		t.Fatalf("diff tools = %v", got)
	}

	// Без одного инструмента выключается одна возможность, а не весь плагин.
	caps = Capabilities([]string{"whoami", "list_merge_requests", "approve_merge_request", "get_merge_request_diffs"})
	if !caps[FeatureMergeRequests].Available || !caps[FeatureWhoAmI].Available {
		t.Fatalf("available features lost: %+v", caps)
	}
	if caps[FeatureApprove].Available || !reflect.DeepEqual(caps[FeatureApprove].Missing, []string{"unapprove_merge_request"}) {
		t.Fatalf("approve = %+v", caps[FeatureApprove])
	}
	if got := caps[FeatureDiff]; !got.Available || got.Tools[0] != "get_merge_request_diffs" {
		t.Fatalf("diff fallback = %+v", got)
	}

	client, caller := newFixtureClient(t, []string{"whoami"})
	if _, err := client.Pipelines(context.Background(), "billing/payments", ""); ReasonOf(err) != ReasonToolMissing {
		t.Fatalf("missing tool error = %v", err)
	}
	if len(caller.calls) != 0 {
		t.Fatalf("server called without the tool: %v", caller.calls)
	}
}

func TestMergeRequestLists(t *testing.T) {
	ctx := context.Background()
	client, caller := newFixtureClient(t, fixtureTools(t))
	cases := []struct {
		project, username string
		scope             Scope
		want              map[string]any
		absent            []string
	}{
		{"billing/payments", "anna", ScopeMine, map[string]any{"project_id": "billing/payments", "author_username": "anna"}, []string{"scope"}},
		{"", "", ScopeMine, map[string]any{"scope": "created_by_me"}, []string{"project_id", "author_username"}},
		{"", "anna", ScopeReview, map[string]any{"reviewer_username": "anna", "scope": "all"}, []string{"project_id"}},
		{"billing/payments", "anna", ScopeReview, map[string]any{"reviewer_username": "anna"}, []string{"scope"}},
		{"", "", ScopeProject, map[string]any{"scope": "all"}, nil},
		{"billing/payments", "", ScopeProject, map[string]any{"project_id": "billing/payments"}, []string{"scope"}},
	}
	for _, tc := range cases {
		list, err := client.MergeRequests(ctx, tc.project, tc.scope, tc.username)
		if err != nil {
			t.Fatalf("%+v: %v", tc, err)
		}
		args := caller.last()
		if args["state"] != "opened" {
			t.Fatalf("%+v: state = %v", tc, args["state"])
		}
		for key, value := range tc.want {
			if args[key] != value {
				t.Fatalf("%+v: %s = %v, want %v", tc, key, args[key], value)
			}
		}
		for _, key := range tc.absent {
			if _, ok := args[key]; ok {
				t.Fatalf("%+v: unexpected %s = %v", tc, key, args[key])
			}
		}
		if len(list) != 2 {
			t.Fatalf("list = %+v", list)
		}
	}

	if _, err := client.MergeRequests(ctx, "", ScopeReview, ""); err == nil {
		t.Fatal("review list without a username must fail")
	}

	list, _ := client.MergeRequests(ctx, "billing/payments", ScopeProject, "")
	first, second := list[0], list[1]
	if first.IID != 12 || first.ProjectPath != "billing/payments" || first.MergeStatus != "mergeable" || first.Draft ||
		first.WebURL != "https://gitlab.example.test/billing/payments/-/merge_requests/12" || first.Author.Username != "anna" ||
		len(first.Reviewers) != 1 || first.Assignees != nil {
		t.Fatalf("first MR = %+v", first)
	}
	// Чужой адрес в ответе не становится ссылкой «Открыть в GitLab».
	if second.WebURL != "" || !second.Draft || !second.HasConflicts || !second.BlockingThread || second.MergeStatus != "draft_status" {
		t.Fatalf("second MR = %+v", second)
	}
}

func TestMergeRequestDetail(t *testing.T) {
	ctx := context.Background()
	client, caller := newFixtureClient(t, fixtureTools(t))
	detail, err := client.MergeRequest(ctx, "billing/payments", 12)
	if err != nil {
		t.Fatal(err)
	}
	if caller.last()["merge_request_iid"] != 12 {
		t.Fatalf("args = %v", caller.last())
	}
	// Описание остаётся исходным markdown: разбор и экранирование — в webview.
	if !strings.Contains(detail.Description, "<script>") || detail.DescTrimmed {
		t.Fatalf("description = %q", detail.Description)
	}
	if detail.DiffRefs.HeadSHA != detail.SHA || detail.DiffRefs.BaseSHA == "" || !detail.RemoveSource || detail.MergedBy != nil ||
		detail.ChangesCount != "3" || detail.MergeError != "" {
		t.Fatalf("detail = %+v", detail)
	}

	approvals, err := client.Approvals(ctx, "billing/payments", 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(approvals.Rules) != 2 || !approvals.Rules[0].Approved || approvals.Rules[1].Approved ||
		len(approvals.ApprovedBy) != 1 || approvals.ApprovedBy[0].Username != "boris" {
		t.Fatalf("approvals = %+v", approvals)
	}

	discussions, err := client.Discussions(ctx, "billing/payments", 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(discussions) != 2 || len(caller.calls) != 3 {
		t.Fatalf("discussions = %+v, calls = %v", discussions, caller.calls)
	}
	system, thread := discussions[0], discussions[1]
	if !system.Individual || !system.Notes[0].System || system.Resolvable {
		t.Fatalf("system note = %+v", system)
	}
	position := thread.Notes[0].Position
	if !thread.Resolvable || thread.Resolved || position == nil || position.NewLine != 27 || position.OldLine != 0 ||
		position.NewPath != "internal/billing/retry.go" || thread.Notes[1].Position != nil {
		t.Fatalf("thread = %+v", thread)
	}

	files, err := client.ChangedFiles(ctx, "billing/payments", 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || files[0].NewPath != "docs/webhooks.md" || !files[0].Renamed || files[0].OldPath != "docs/hooks.md" ||
		!files[1].New {
		t.Fatalf("files = %+v", files)
	}
}

func TestDiffAndFiles(t *testing.T) {
	ctx := context.Background()
	client, caller := newFixtureClient(t, fixtureTools(t))
	diff, err := client.FileDiff(ctx, "billing/payments", 12, "internal/billing/retry.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(diff.Diff, "@@ -20,7") || diff.Trimmed {
		t.Fatalf("diff = %+v", diff)
	}
	if paths := caller.last()["file_paths"]; !reflect.DeepEqual(paths, []string{"internal/billing/retry.go"}) {
		t.Fatalf("file_paths = %v", paths)
	}
	if _, err := client.FileDiff(ctx, "billing/payments", 12, "nope.go"); ReasonOf(err) != ReasonNotFound {
		t.Fatalf("missing file diff = %v", err)
	}

	content, err := client.FileContent(ctx, "billing/payments", "internal/billing/retry.go", detailHead)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(content.Content, "package billing") || content.Missing || content.Binary || content.TooBig {
		t.Fatalf("content = %+v", content)
	}
	if args := caller.last(); args["ref"] != detailHead || args["file_path"] != "internal/billing/retry.go" {
		t.Fatalf("args = %v", args)
	}

	caller.override["get_file_contents"] = errorResult("GitLab API error: 404 Not Found - {\"message\":\"404 File Not Found\"}")
	content, err = client.FileContent(ctx, "billing/payments", "internal/billing/idempotency.go", "base")
	if err != nil || !content.Missing || content.Content != "" {
		t.Fatalf("missing base file = %+v, %v", content, err)
	}
	caller.override["get_file_contents"] = mcpclient.CallResult{Content: []mcpclient.Content{{Type: "text",
		Text: `{"file_path":"logo.png","encoding":"base64","content":"iVBORw0KGgo=","size":8}`}}}
	if content, _ = client.FileContent(ctx, "billing/payments", "logo.png", "main"); !content.Binary || content.Content != "" {
		t.Fatalf("binary file = %+v", content)
	}
	caller.override["get_file_contents"] = mcpclient.CallResult{Content: []mcpclient.Content{{Type: "text",
		Text: `{"file_path":"dump.sql","encoding":"utf8","content":"","size":5000000}`}}}
	if content, _ = client.FileContent(ctx, "billing/payments", "dump.sql", "main"); !content.TooBig {
		t.Fatalf("big file = %+v", content)
	}

	// Запасной инструмент diff отдаёт все файлы MR; нужный выбирается по пути.
	fallback, caller := newFixtureClient(t, []string{"get_merge_request_diffs"})
	caller.override["get_merge_request_diffs"] = caller.readFixture("get_merge_request_file_diff")
	diff, err = fallback.FileDiff(ctx, "billing/payments", 12, "internal/billing/retry.go")
	if err != nil || diff.NewPath != "internal/billing/retry.go" {
		t.Fatalf("fallback diff = %+v, %v", diff, err)
	}
	if _, ok := caller.last()["file_paths"]; ok {
		t.Fatalf("fallback sent file_paths: %v", caller.last())
	}
}

const detailHead = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"

func (f *fixtureCaller) readFixture(tool string) mcpclient.CallResult {
	raw, err := os.ReadFile(filepath.Join(fixtures, "responses", tool+".json"))
	if err != nil {
		f.t.Fatal(err)
	}
	return mcpclient.CallResult{Content: []mcpclient.Content{{Type: "text", Text: string(raw)}}}
}

func TestPipelinesAndJobs(t *testing.T) {
	ctx := context.Background()
	client, caller := newFixtureClient(t, fixtureTools(t))
	pipelines, err := client.Pipelines(ctx, "billing/payments", "fix/webhook-retry")
	if err != nil {
		t.Fatal(err)
	}
	if caller.last()["ref"] != "fix/webhook-retry" {
		t.Fatalf("args = %v", caller.last())
	}
	if len(pipelines) != 2 || pipelines[0].Status != "failed" || pipelines[0].Duration != 512.4 || pipelines[0].User == nil ||
		pipelines[1].User != nil || pipelines[0].WebURL == "" {
		t.Fatalf("pipelines = %+v", pipelines)
	}
	if _, err = client.Pipelines(ctx, "billing/payments", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := caller.last()["ref"]; ok {
		t.Fatalf("empty ref sent: %v", caller.last())
	}
	if mr, err := client.MergeRequestPipelines(ctx, "billing/payments", 12); err != nil || len(mr) != 2 {
		t.Fatalf("mr pipelines = %+v, %v", mr, err)
	}

	jobs, err := client.Jobs(ctx, "billing/payments", 3301)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[1].Status != "failed" || jobs[1].FailureReason != "script_failure" || jobs[1].StartedAt.IsZero() {
		t.Fatalf("jobs = %+v", jobs)
	}

	log, err := client.JobLog(ctx, "billing/payments", 77002)
	if err != nil {
		t.Fatal(err)
	}
	if !log.Trimmed || strings.HasPrefix(log.Text, "\n") || strings.Contains(log.Text, "Untrusted CI job trace") ||
		strings.Contains(log.Text, "\x1b") || strings.Contains(log.Text, "glcbt-") || !strings.Contains(log.Text, "[REDACTED]") ||
		!strings.Contains(log.Text, "--- FAIL: TestRetryIdempotent") || !strings.HasPrefix(log.Text, "section_start:") {
		t.Fatalf("log = %+v", log)
	}

	caller.override["get_pipeline_job_output"] = mcpclient.CallResult{Content: []mcpclient.Content{{Type: "text",
		Text: "[Untrusted CI job trace: data]\n\nok\n"}}}
	if log, _ = client.JobLog(ctx, "billing/payments", 1); log.Trimmed || log.Text != "ok\n" {
		t.Fatalf("short log = %+v", log)
	}
	long := strings.Repeat("строка лога\n", maxJobLog/10)
	caller.override["get_pipeline_job_output"] = mcpclient.CallResult{Content: []mcpclient.Content{{Type: "text", Text: long + "FAIL\n"}}}
	if log, _ = client.JobLog(ctx, "billing/payments", 1); !log.Trimmed || len(log.Text) > maxJobLog ||
		!strings.HasSuffix(log.Text, "FAIL\n") || !strings.HasPrefix(log.Text, "строка") {
		t.Fatalf("long log: trimmed=%v len=%d head=%q", log.Trimmed, len(log.Text), log.Text[:20])
	}
}

func TestActions(t *testing.T) {
	ctx := context.Background()
	client, caller := newFixtureClient(t, fixtureTools(t))
	caller.override["create_merge_request_note"] = mcpclient.CallResult{Content: []mcpclient.Content{{Type: "text", Text: `{"id":600}`}}}
	caller.override["create_merge_request_discussion_note"] = caller.override["create_merge_request_note"]
	caller.override["approve_merge_request"] = mcpclient.CallResult{Content: []mcpclient.Content{{Type: "text", Text: `{}`}}}
	caller.override["unapprove_merge_request"] = caller.override["approve_merge_request"]

	if err := client.Comment(ctx, "billing/payments", 12, "", "Готово"); err != nil || caller.calls[len(caller.calls)-1] != "create_merge_request_note" {
		t.Fatalf("comment: %v, %v", err, caller.calls)
	}
	if err := client.Comment(ctx, "billing/payments", 12, "d3b4", "Ответ"); err != nil ||
		caller.calls[len(caller.calls)-1] != "create_merge_request_discussion_note" || caller.last()["discussion_id"] != "d3b4" {
		t.Fatalf("reply: %v, %v %v", err, caller.calls, caller.last())
	}
	if err := client.SetApproval(ctx, "billing/payments", 12, true, detailHead); err != nil || caller.last()["sha"] != detailHead ||
		caller.calls[len(caller.calls)-1] != "approve_merge_request" {
		t.Fatalf("approve: %v, %v", err, caller.last())
	}
	if err := client.SetApproval(ctx, "billing/payments", 12, false, detailHead); err != nil || caller.calls[len(caller.calls)-1] != "unapprove_merge_request" {
		t.Fatalf("unapprove: %v, %v", err, caller.calls)
	}
	if _, ok := caller.last()["sha"]; ok {
		t.Fatalf("unapprove sent sha: %v", caller.last())
	}

	calls := len(caller.calls)
	if _, err := client.Merge(ctx, "billing/payments", 12, " ", true); err == nil || len(caller.calls) != calls {
		t.Fatalf("merge without sha: %v, %v", err, caller.calls)
	}
	merged, err := client.Merge(ctx, "billing/payments", 12, detailHead, true)
	if err != nil || merged.State != "merged" || merged.MergedBy == nil || merged.MergedBy.Username != "anna" {
		t.Fatalf("merge = %+v, %v", merged, err)
	}
	if args := caller.last(); args["sha"] != detailHead || args["should_remove_source_branch"] != true {
		t.Fatalf("merge args = %v", args)
	}

	job, err := client.RetryJob(ctx, "billing/payments", 77002)
	if err != nil || job.ID != 77010 || job.Status != "pending" {
		t.Fatalf("retry = %+v, %v", job, err)
	}
}

func TestToolErrors(t *testing.T) {
	ctx := context.Background()
	client, caller := newFixtureClient(t, fixtureTools(t))
	cases := []struct {
		text string
		want Reason
	}{
		{"GitLab API error: 401 Unauthorized {\"message\":\"401 Unauthorized\"} token glpat-abcdefghijklmnopqrstuvwxyz", ReasonAuth},
		{"GitLab API error: 403 Forbidden", ReasonAuth},
		{"GitLab API error: 404 Project Not Found", ReasonNotFound},
		{"GitLab API error: 405 Method Not Allowed: Merge request is not mergeable", ReasonToolError},
	}
	for _, tc := range cases {
		caller.override["whoami"] = errorResult(tc.text)
		_, err := client.WhoAmI(ctx)
		if ReasonOf(err) != tc.want {
			t.Fatalf("%q: reason %q, want %q", tc.text, ReasonOf(err), tc.want)
		}
		if strings.Contains(err.Error(), "glpat-") {
			t.Fatalf("token leaked: %v", err)
		}
	}
	caller.override["whoami"] = mcpclient.CallResult{Content: []mcpclient.Content{{Type: "text", Text: "not json"}}}
	if _, err := client.WhoAmI(ctx); ReasonOf(err) != ReasonFormat {
		t.Fatalf("format error = %v", err)
	}
	delete(caller.override, "whoami")
	user, err := client.WhoAmI(ctx)
	if err != nil || user.Username != "anna" {
		t.Fatalf("whoami = %+v, %v", user, err)
	}
	project, err := client.Project(ctx, "billing/payments")
	if err != nil || project.ID != 42 || project.DefaultBranch != "main" || project.WebURL == "" {
		t.Fatalf("project = %+v, %v", project, err)
	}
}

func TestRemote(t *testing.T) {
	cases := []struct {
		raw  string
		want Remote
		ok   bool
	}{
		{"git@gitlab.company.local:billing/payments.git", Remote{"gitlab.company.local", "billing/payments"}, true},
		{"gitlab.company.local:group/sub/app", Remote{"gitlab.company.local", "group/sub/app"}, true},
		{"ssh://git@GitLab.Company.Local:2222/billing/payments.git", Remote{"gitlab.company.local", "billing/payments"}, true},
		{"https://oauth2:glpat-secret@gitlab.company.local/billing/payments.git/", Remote{"gitlab.company.local", "billing/payments"}, true},
		{"https://gitlab.company.local/gitlab/billing/payments", Remote{"gitlab.company.local", "gitlab/billing/payments"}, true},
		{"", Remote{}, false},
		{`C:\repos\payments`, Remote{}, false},
		{"https://gitlab.company.local/payments", Remote{}, false},
		{"/home/anna/payments", Remote{}, false},
	}
	for _, tc := range cases {
		got, err := ParseRemote(tc.raw)
		if (err == nil) != tc.ok || got != tc.want {
			t.Fatalf("ParseRemote(%q) = %+v, %v", tc.raw, got, err)
		}
	}

	projects := []struct {
		remote, gitlab, want string
		ok                   bool
	}{
		{"git@gitlab.company.local:billing/payments.git", "https://gitlab.company.local", "billing/payments", true},
		{"git@gitlab.company.local:billing/payments.git", "https://gitlab.company.local/api/v4", "billing/payments", true},
		{"https://gitlab.company.local/gitlab/billing/payments.git", "https://gitlab.company.local/gitlab", "billing/payments", true},
		{"git@gitlab.company.local:billing/payments.git", "https://gitlab.company.local/gitlab", "billing/payments", true},
		{"git@ssh.gitlab.company.local:billing/payments.git", "https://gitlab.company.local", "", false},
		{"git@github.com:billing/payments.git", "https://gitlab.company.local", "", false},
	}
	for _, tc := range projects {
		remote, err := ParseRemote(tc.remote)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := ProjectFor(remote, tc.gitlab)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("ProjectFor(%q, %q) = %q, %v", tc.remote, tc.gitlab, got, ok)
		}
	}
}

func TestRecipe(t *testing.T) {
	for raw, want := range map[string]string{
		"gitlab.company.local":                 "https://gitlab.company.local/api/v4",
		"https://GitLab.Company.Local/":        "https://gitlab.company.local/api/v4",
		"https://gitlab.company.local/api/v4/": "https://gitlab.company.local/api/v4",
		"https://host.local:8443/gitlab":       "https://host.local:8443/gitlab/api/v4",
		"http://127.0.0.1:8080/gitlab/api/v4":  "http://127.0.0.1:8080/gitlab/api/v4",
	} {
		got, err := APIURL(raw)
		if err != nil || got != want {
			t.Fatalf("APIURL(%q) = %q, %v", raw, got, err)
		}
	}
	for _, raw := range []string{"", "ftp://gitlab.local", "https://user:pw@gitlab.local", "https://"} {
		if _, err := APIURL(raw); err == nil {
			t.Fatalf("APIURL(%q) accepted", raw)
		}
	}

	launch, err := Recipe(Settings{URL: "gitlab.company.local", CAPath: " /etc/ssl/company-ca.pem "})
	if err != nil {
		t.Fatal(err)
	}
	if launch.Command != "npx" || !reflect.DeepEqual(launch.Args, []string{"-y", "@zereight/mcp-gitlab@2.1.66"}) ||
		!reflect.DeepEqual(launch.SecretEnv, []string{TokenVariable}) {
		t.Fatalf("launch = %+v", launch)
	}
	if launch.Env["GITLAB_API_URL"] != "https://gitlab.company.local/api/v4" || launch.Env["NODE_EXTRA_CA_CERTS"] != "/etc/ssl/company-ca.pem" ||
		launch.Env["GITLAB_CA_CERT_PATH"] != "/etc/ssl/company-ca.pem" || launch.Env[TokenVariable] != "" {
		t.Fatalf("env = %v", launch.Env)
	}
	if plain, _ := Recipe(Settings{URL: "gitlab.company.local"}); plain.Env["NODE_EXTRA_CA_CERTS"] != "" {
		t.Fatalf("CA without a path: %v", plain.Env)
	}

	for tool, want := range map[string]domain.ToolRisk{
		"merge_merge_request":       domain.ToolRiskCritical,
		"create_merge_request_note": domain.ToolRiskHigh,
		"retry_pipeline_job":        domain.ToolRiskHigh,
		"list_merge_requests":       domain.ToolRiskLow,
	} {
		if got, ok := PresetRisk(tool); !ok || got != want {
			t.Fatalf("PresetRisk(%s) = %s, %v", tool, got, ok)
		}
	}
	if _, ok := PresetRisk("delete_merge_request"); ok {
		t.Fatal("unknown tool got a preset risk")
	}
}
