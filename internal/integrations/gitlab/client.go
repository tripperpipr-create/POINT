package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"local-agent-workbench/internal/mcpclient"
	"local-agent-workbench/internal/security"
)

// Caller — вызов инструмента MCP-сервера плагина. В ядре это
// App.CallMCPTool, в тестах — фикстуры.
type Caller interface {
	CallTool(ctx context.Context, tool string, arguments any) (mcpclient.CallResult, error)
}

// Reason — почему экран не получил данных. Отличается от рода сбоя MCP:
// сервер ответил, но не тем, что нужно экрану.
type Reason string

const (
	ReasonToolMissing Reason = "tool_missing"
	ReasonAuth        Reason = "auth"
	ReasonNotFound    Reason = "not_found"
	ReasonToolError   Reason = "tool_error"
	ReasonFormat      Reason = "format"
)

// Error — отказ экрана с причиной.
type Error struct {
	Reason Reason
	Tool   string
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("gitlab %s (%s): %s", e.Reason, e.Tool, e.Detail)
}

// ReasonOf — причина отказа экрана или пусто.
func ReasonOf(err error) Reason {
	var target *Error
	if errors.As(err, &target) {
		return target.Reason
	}
	return ""
}

// Client — экраны GitLab поверх инструментов сервера.
type Client struct {
	call   Caller
	origin string
	caps   map[Feature]Capability
}

// NewClient: gitlabURL — адрес GitLab из настроек плагина; tools — имена из
// последнего снимка сервера.
func NewClient(call Caller, gitlabURL string, tools []string) *Client {
	client := &Client{call: call, caps: Capabilities(tools)}
	if base, err := BaseURL(gitlabURL); err == nil {
		if parsed, parseErr := url.Parse(base); parseErr == nil {
			client.origin = parsed.Scheme + "://" + parsed.Host
		}
	}
	return client
}

// Capabilities — что умеет подключённый сервер.
func (c *Client) Capabilities() map[Feature]Capability { return c.caps }

// sameOrigin оставляет ссылку, только если она ведёт на этот же GitLab:
// «Открыть в GitLab» не должно уводить на адрес, подставленный в ответ.
func (c *Client) sameOrigin(raw string) string {
	if raw == "" || c.origin == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme+"://"+strings.ToLower(parsed.Host) != c.origin {
		return ""
	}
	return raw
}

func (c *Client) tool(feature Feature, index int) (string, error) {
	capability := c.caps[feature]
	if !capability.Available {
		missing := ""
		if len(capability.Missing) > 0 {
			missing = capability.Missing[0]
		}
		return "", &Error{Reason: ReasonToolMissing, Tool: missing, Detail: "server does not provide this tool"}
	}
	if index >= len(capability.Tools) {
		index = 0
	}
	return capability.Tools[index], nil
}

// invoke вызывает инструмент и разбирает JSON ответа в target (nil — ответ
// не нужен). Ошибка инструмента классифицируется по тексту GitLab.
func (c *Client) invoke(ctx context.Context, tool string, arguments any, target any) (string, error) {
	result, err := c.call.CallTool(ctx, tool, arguments)
	if err != nil {
		return "", err
	}
	text := result.Text()
	if result.IsError {
		detail := clip(security.Redact(strings.TrimSpace(text)), 500)
		lower := strings.ToLower(text)
		switch {
		case strings.Contains(lower, "401") || strings.Contains(lower, "unauthorized") ||
			strings.Contains(lower, "403") || strings.Contains(lower, "forbidden"):
			return "", &Error{Reason: ReasonAuth, Tool: tool, Detail: detail}
		case strings.Contains(lower, "404") || strings.Contains(lower, "not found"):
			return "", &Error{Reason: ReasonNotFound, Tool: tool, Detail: detail}
		default:
			return "", &Error{Reason: ReasonToolError, Tool: tool, Detail: detail}
		}
	}
	if target != nil {
		if err := decodeText(text, target); err != nil {
			return "", &Error{Reason: ReasonFormat, Tool: tool, Detail: "answer did not parse: " + err.Error()}
		}
	}
	return text, nil
}

// Scope — какие MR показать.
type Scope string

const (
	ScopeMine    Scope = "mine"
	ScopeReview  Scope = "review"
	ScopeProject Scope = "project"
)

// ProjectInfo — проект GitLab по пути.
type ProjectInfo struct {
	ID            int    `json:"id"`
	Path          string `json:"path"`
	Name          string `json:"name"`
	DefaultBranch string `json:"defaultBranch"`
	WebURL        string `json:"webUrl,omitempty"`
}

func (c *Client) WhoAmI(ctx context.Context) (User, error) {
	tool, err := c.tool(FeatureWhoAmI, 0)
	if err != nil {
		return User{}, err
	}
	var raw rawUser
	if _, err = c.invoke(ctx, tool, map[string]any{}, &raw); err != nil {
		return User{}, err
	}
	return raw.view(), nil
}

func (c *Client) Project(ctx context.Context, path string) (ProjectInfo, error) {
	tool, err := c.tool(FeatureProject, 0)
	if err != nil {
		return ProjectInfo{}, err
	}
	var raw struct {
		ID                int    `json:"id"`
		PathWithNamespace string `json:"path_with_namespace"`
		Name              string `json:"name"`
		DefaultBranch     string `json:"default_branch"`
		WebURL            string `json:"web_url"`
	}
	if _, err = c.invoke(ctx, tool, map[string]any{"project_id": path}, &raw); err != nil {
		return ProjectInfo{}, err
	}
	return ProjectInfo{ID: raw.ID, Path: clip(raw.PathWithNamespace, 255), Name: clip(raw.Name, 255),
		DefaultBranch: clip(raw.DefaultBranch, 255), WebURL: c.sameOrigin(raw.WebURL)}, nil
}

// MergeRequests — открытые MR. project пустой — по всем проектам владельца.
func (c *Client) MergeRequests(ctx context.Context, project string, scope Scope, username string) ([]MergeRequest, error) {
	tool, err := c.tool(FeatureMergeRequests, 0)
	if err != nil {
		return nil, err
	}
	args := map[string]any{"state": "opened", "order_by": "updated_at", "sort": "desc", "per_page": 50}
	if project != "" {
		args["project_id"] = project
	}
	switch scope {
	case ScopeMine:
		if username != "" {
			args["author_username"] = username
		} else {
			args["scope"] = "created_by_me"
		}
	case ScopeReview:
		if username == "" {
			return nil, &Error{Reason: ReasonToolError, Tool: tool, Detail: "reviewer name is unknown"}
		}
		args["reviewer_username"] = username
		if project == "" {
			args["scope"] = "all"
		}
	case ScopeProject:
		if project == "" {
			args["scope"] = "all"
		}
	}
	var raw []rawMergeRequest
	if _, err = c.invoke(ctx, tool, args, &raw); err != nil {
		return nil, err
	}
	out := make([]MergeRequest, 0, len(raw))
	for _, item := range raw {
		view := c.mergeRequest(item)
		if view.ProjectPath == "" {
			view.ProjectPath = project
		}
		out = append(out, view)
	}
	return out, nil
}

func (c *Client) MergeRequest(ctx context.Context, project string, iid int) (MergeRequestDetail, error) {
	tool, err := c.tool(FeatureMergeRequest, 0)
	if err != nil {
		return MergeRequestDetail{}, err
	}
	var raw rawMergeRequest
	if _, err = c.invoke(ctx, tool, map[string]any{"project_id": project, "merge_request_iid": iid}, &raw); err != nil {
		return MergeRequestDetail{}, err
	}
	detail := c.mergeRequestDetail(raw)
	if detail.ProjectPath == "" {
		detail.ProjectPath = project
	}
	return detail, nil
}

func (c *Client) Approvals(ctx context.Context, project string, iid int) (Approvals, error) {
	tool, err := c.tool(FeatureApprovals, 0)
	if err != nil {
		return Approvals{}, err
	}
	var raw struct {
		Rules []struct {
			Name              string    `json:"name"`
			ApprovalsRequired int       `json:"approvals_required"`
			Approved          bool      `json:"approved"`
			ApprovedBy        []rawUser `json:"approved_by"`
		} `json:"rules"`
	}
	if _, err = c.invoke(ctx, tool, map[string]any{"project_id": project, "merge_request_iid": iid}, &raw); err != nil {
		return Approvals{}, err
	}
	approvals := Approvals{Rules: []ApprovalRule{}}
	seen := map[int]bool{}
	for _, rule := range raw.Rules {
		approvals.Rules = append(approvals.Rules, ApprovalRule{Name: clip(rule.Name, 200), Required: rule.ApprovalsRequired,
			Approved: rule.Approved, ApprovedBy: users(rule.ApprovedBy)})
		for _, user := range rule.ApprovedBy {
			if !seen[user.ID] {
				seen[user.ID] = true
				approvals.ApprovedBy = append(approvals.ApprovedBy, user.view())
			}
		}
	}
	return approvals, nil
}

// Discussions — все нити MR, не больше пяти страниц по сто.
func (c *Client) Discussions(ctx context.Context, project string, iid int) ([]Discussion, error) {
	tool, err := c.tool(FeatureDiscussions, 0)
	if err != nil {
		return nil, err
	}
	var out []Discussion
	for page := 1; page <= 5; page++ {
		var raw struct {
			Items []struct {
				ID         string    `json:"id"`
				Individual bool      `json:"individual_note"`
				Notes      []rawNote `json:"notes"`
			} `json:"items"`
			Pagination struct {
				NextPage *int `json:"next_page"`
			} `json:"pagination"`
		}
		if _, err = c.invoke(ctx, tool, map[string]any{"project_id": project, "merge_request_iid": iid, "page": page, "per_page": 100}, &raw); err != nil {
			return nil, err
		}
		for _, item := range raw.Items {
			discussion := Discussion{ID: clip(item.ID, 100), Individual: item.Individual}
			for _, note := range item.Notes {
				view := noteView(note)
				discussion.Notes = append(discussion.Notes, view)
				if view.Resolvable {
					discussion.Resolvable = true
					discussion.Resolved = view.Resolved
				}
			}
			out = append(out, discussion)
		}
		if raw.Pagination.NextPage == nil || *raw.Pagination.NextPage <= page {
			break
		}
	}
	return out, nil
}

func (c *Client) ChangedFiles(ctx context.Context, project string, iid int) ([]ChangedFile, error) {
	tool, err := c.tool(FeatureChanges, 0)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		OldPath string `json:"old_path"`
		NewPath string `json:"new_path"`
		New     bool   `json:"new_file"`
		Deleted bool   `json:"deleted_file"`
		Renamed bool   `json:"renamed_file"`
	}
	if _, err = c.invoke(ctx, tool, map[string]any{"project_id": project, "merge_request_iid": iid}, &raw); err != nil {
		return nil, err
	}
	out := make([]ChangedFile, 0, len(raw))
	for _, item := range raw {
		out = append(out, ChangedFile{OldPath: clip(item.OldPath, 1000), NewPath: clip(item.NewPath, 1000), New: item.New, Deleted: item.Deleted, Renamed: item.Renamed})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NewPath < out[j].NewPath })
	return out, nil
}

type rawDiff struct {
	OldPath string `json:"old_path"`
	NewPath string `json:"new_path"`
	Diff    string `json:"diff"`
	Error   string `json:"error"`
}

// FileDiff — diff одного файла MR текстом (запасной вид, если содержимое
// файла недоступно).
func (c *Client) FileDiff(ctx context.Context, project string, iid int, path string) (FileDiff, error) {
	tool, err := c.tool(FeatureDiff, 0)
	if err != nil {
		return FileDiff{}, err
	}
	var raw []rawDiff
	args := map[string]any{"project_id": project, "merge_request_iid": iid}
	if tool == "get_merge_request_file_diff" {
		args["file_paths"] = []string{path}
	}
	if _, err = c.invoke(ctx, tool, args, &raw); err != nil {
		return FileDiff{}, err
	}
	for _, item := range raw {
		if item.Error == "" && (item.NewPath == path || item.OldPath == path) {
			diff, trimmed := clipFlag(item.Diff, maxDiff)
			return FileDiff{OldPath: item.OldPath, NewPath: item.NewPath, Diff: diff, Trimmed: trimmed}, nil
		}
	}
	return FileDiff{}, &Error{Reason: ReasonNotFound, Tool: tool, Detail: "file is not in the merge request"}
}

// FileContent — содержимое файла на ревизии, для нативного diff IDE.
// Отсутствие файла — не ошибка: у добавленного нет базы, у удалённого — головы.
func (c *Client) FileContent(ctx context.Context, project, path, ref string) (FileContent, error) {
	tool, err := c.tool(FeatureFile, 0)
	if err != nil {
		return FileContent{}, err
	}
	var raw struct {
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
		Size     int    `json:"size"`
	}
	content := FileContent{Path: path, Ref: ref}
	if _, err = c.invoke(ctx, tool, map[string]any{"project_id": project, "file_path": path, "ref": ref}, &raw); err != nil {
		if ReasonOf(err) == ReasonNotFound {
			content.Missing = true
			return content, nil
		}
		return FileContent{}, err
	}
	switch {
	case raw.Size > maxFileContent || len(raw.Content) > maxFileContent:
		content.TooBig = true
	case raw.Encoding != "" && raw.Encoding != "utf8" && raw.Encoding != "text":
		content.Binary = true
	default:
		content.Content = raw.Content
	}
	return content, nil
}

func (c *Client) Pipelines(ctx context.Context, project, ref string) ([]Pipeline, error) {
	tool, err := c.tool(FeaturePipelines, 0)
	if err != nil {
		return nil, err
	}
	args := map[string]any{"project_id": project, "order_by": "id", "sort": "desc", "per_page": 20}
	if ref != "" {
		args["ref"] = ref
	}
	var raw []rawPipeline
	if _, err = c.invoke(ctx, tool, args, &raw); err != nil {
		return nil, err
	}
	return c.pipelines(raw), nil
}

func (c *Client) MergeRequestPipelines(ctx context.Context, project string, iid int) ([]Pipeline, error) {
	tool, err := c.tool(FeatureMRPipelines, 0)
	if err != nil {
		return nil, err
	}
	var raw []rawPipeline
	if _, err = c.invoke(ctx, tool, map[string]any{"project_id": project, "merge_request_iid": iid, "per_page": 20}, &raw); err != nil {
		return nil, err
	}
	return c.pipelines(raw), nil
}

func (c *Client) pipelines(raw []rawPipeline) []Pipeline {
	out := make([]Pipeline, 0, len(raw))
	for _, item := range raw {
		out = append(out, c.pipeline(item))
	}
	return out
}

func (c *Client) Jobs(ctx context.Context, project string, pipelineID int) ([]Job, error) {
	tool, err := c.tool(FeatureJobs, 0)
	if err != nil {
		return nil, err
	}
	var raw []rawJob
	if _, err = c.invoke(ctx, tool, map[string]any{"project_id": project, "pipeline_id": pipelineID, "per_page": 100}, &raw); err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(raw))
	for _, item := range raw {
		out = append(out, c.job(item))
	}
	return out, nil
}

// JobLog — хвост лога джоба без управляющих последовательностей и без
// служебных строк сервера («Untrusted CI job trace», «Log truncated»).
func (c *Client) JobLog(ctx context.Context, project string, jobID int) (JobLog, error) {
	tool, err := c.tool(FeatureJobLog, 0)
	if err != nil {
		return JobLog{}, err
	}
	text, err := c.invoke(ctx, tool, map[string]any{"project_id": project, "job_id": jobID, "limit": 1000}, nil)
	if err != nil {
		return JobLog{}, err
	}
	trimmed, header := false, false
	for {
		line, rest, _ := strings.Cut(text, "\n")
		if strings.HasPrefix(line, "[Untrusted CI job trace") {
			text, header = rest, true
			continue
		}
		if strings.HasPrefix(line, "[Log truncated:") {
			text, trimmed, header = rest, true, true
			continue
		}
		break
	}
	if header {
		text = strings.TrimPrefix(text, "\n")
	}
	text = security.Redact(cleanLog(text))
	text, cut := tail(text, maxJobLog)
	return JobLog{JobID: jobID, Text: text, Trimmed: trimmed || cut}, nil
}

// Действия владельца. Возвращают свежий вид того, что изменили.

func (c *Client) Comment(ctx context.Context, project string, iid int, discussionID, body string) error {
	feature, tool := FeatureComment, "create_merge_request_note"
	args := map[string]any{"project_id": project, "merge_request_iid": iid, "body": body}
	if discussionID != "" {
		feature = FeatureReply
		args["discussion_id"] = discussionID
	}
	var err error
	if tool, err = c.tool(feature, 0); err != nil {
		return err
	}
	_, err = c.invoke(ctx, tool, args, nil)
	return err
}

func (c *Client) SetApproval(ctx context.Context, project string, iid int, approve bool, sha string) error {
	index := 1
	args := map[string]any{"project_id": project, "merge_request_iid": iid}
	if approve {
		index = 0
		if sha != "" {
			args["sha"] = sha
		}
	}
	tool, err := c.tool(FeatureApprove, index)
	if err != nil {
		return err
	}
	_, err = c.invoke(ctx, tool, args, nil)
	return err
}

// Merge сливает MR, только если его голова — та, что владелец видел (sha):
// GitLab отказывает, если за это время в ветку пришли новые коммиты.
func (c *Client) Merge(ctx context.Context, project string, iid int, sha string, removeSource bool) (MergeRequestDetail, error) {
	if strings.TrimSpace(sha) == "" {
		return MergeRequestDetail{}, &Error{Reason: ReasonToolError, Tool: "merge_merge_request", Detail: "expected head sha is required"}
	}
	tool, err := c.tool(FeatureMerge, 0)
	if err != nil {
		return MergeRequestDetail{}, err
	}
	var raw rawMergeRequest
	if _, err = c.invoke(ctx, tool, map[string]any{"project_id": project, "merge_request_iid": iid, "sha": sha,
		"should_remove_source_branch": removeSource}, &raw); err != nil {
		return MergeRequestDetail{}, err
	}
	return c.mergeRequestDetail(raw), nil
}

func (c *Client) RetryJob(ctx context.Context, project string, jobID int) (Job, error) {
	tool, err := c.tool(FeatureRetry, 0)
	if err != nil {
		return Job{}, err
	}
	var raw rawJob
	if _, err = c.invoke(ctx, tool, map[string]any{"project_id": project, "job_id": jobID}, &raw); err != nil {
		return Job{}, err
	}
	return c.job(raw), nil
}
