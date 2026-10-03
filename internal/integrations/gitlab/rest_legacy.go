package gitlab

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"local-agent-workbench/internal/forge"
	"local-agent-workbench/internal/mcpclient"
)

// LegacyClient preserves old HTTP response models over the direct REST transport.
func (c *RESTClient) LegacyClient() *Client {
	tools := []string{}
	for _, groups := range featureTools {
		for _, alternatives := range groups {
			tools = append(tools, alternatives[0])
		}
	}
	return NewClient(restLegacyCaller{c}, strings.TrimSuffix(c.base, "/api/v4"), tools)
}

type restLegacyCaller struct{ client *RESTClient }

func (l restLegacyCaller) CallTool(ctx context.Context, tool string, arguments any) (mcpclient.CallResult, error) {
	c := l.client
	raw, _ := json.Marshal(arguments)
	a := map[string]any{}
	_ = json.Unmarshal(raw, &a)
	value := func(key string) string {
		if v, ok := a[key]; ok {
			return fmt.Sprint(v)
		}
		return ""
	}
	params := url.Values{}
	for k, v := range a {
		params.Set(k, fmt.Sprint(v))
	}
	project := value("project_id")
	iid := value("merge_request_iid")
	base := projectRoute(project)
	mr := base + "/merge_requests/" + iid
	path, method := base, "GET"
	body := map[string]any{}
	for k, v := range a {
		body[k] = v
	}
	switch tool {
	case "whoami":
		path = "/user"
	case "get_project":
		path = base
	case "list_merge_requests":
		path = "/merge_requests"
		if project != "" {
			path = base + "/merge_requests"
		}
	case "get_merge_request":
		path = mr
	case "get_merge_request_approval_state":
		path = mr + "/approval_state"
	case "mr_discussions":
		path = mr + "/discussions"
	case "list_merge_request_changed_files", "get_merge_request_file_diff", "get_merge_request_diffs":
		path = mr + "/diffs"
	case "get_file_contents":
		path = base + "/repository/files/" + url.PathEscape(value("file_path"))
	case "list_pipelines":
		path = base + "/pipelines"
	case "get_pipeline":
		path = base + "/pipelines/" + value("pipeline_id")
	case "list_merge_request_pipelines":
		path = mr + "/pipelines"
	case "list_pipeline_jobs":
		path = base + "/pipelines/" + value("pipeline_id") + "/jobs"
	case "get_pipeline_job_output":
		path = base + "/jobs/" + value("job_id") + "/trace"
	case "create_merge_request_note":
		path = mr + "/notes"
		method = "POST"
	case "create_merge_request_discussion_note":
		path = mr + "/discussions/" + url.PathEscape(value("discussion_id")) + "/notes"
		method = "POST"
	case "approve_merge_request":
		path = mr + "/approve"
		method = "POST"
	case "unapprove_merge_request":
		path = mr + "/unapprove"
		method = "POST"
	case "merge_merge_request":
		path = mr + "/merge"
		method = "PUT"
	case "retry_pipeline_job":
		path = base + "/jobs/" + value("job_id") + "/retry"
		method = "POST"
	case "list_projects":
		path = "/projects"
	case "list_commits":
		path = base + "/repository/commits"
	case "get_commit":
		path = base + "/repository/commits/" + url.PathEscape(value("sha"))
	case "get_commit_diff":
		path = base + "/repository/commits/" + url.PathEscape(value("sha")) + "/diff"
	case "list_branches":
		path = base + "/repository/branches"
	case "get_repository_tree":
		path = base + "/repository/tree"
	default:
		return mcpclient.CallResult{}, fmt.Errorf("unsupported legacy action %s", tool)
	}
	delete(body, "project_id")
	delete(body, "merge_request_iid")
	if method != "GET" {
		params = nil
	} else {
		body = nil
	}
	data, next, e := c.request(ctx, method, path, params, body)
	if e != nil {
		reason := ReasonToolError
		if issue, ok := e.(*forge.Error); ok {
			switch issue.Reason {
			case "auth":
				reason = ReasonAuth
			case "not_found":
				reason = ReasonNotFound
			}
		}
		return mcpclient.CallResult{}, &Error{Reason: reason, Tool: tool, Detail: e.Error()}
	}
	if tool == "mr_discussions" {
		data, _ = json.Marshal(map[string]any{"items": json.RawMessage(data), "pagination": map[string]any{"next_page": next}})
	}
	if tool == "get_file_contents" {
		var f struct {
			Content string `json:"content"`
			Size    int    `json:"size"`
		}
		if e = json.Unmarshal(data, &f); e != nil {
			return mcpclient.CallResult{}, e
		}
		text, e := base64.StdEncoding.DecodeString(f.Content)
		if e != nil {
			return mcpclient.CallResult{}, e
		}
		encoding := "utf8"
		if len(text) > maxFileContent {
			encoding = "too-large"
			text = nil
		}
		data, _ = json.Marshal(map[string]any{"file_path": value("file_path"), "content": string(text), "encoding": encoding, "size": f.Size})
	}
	if tool == "get_pipeline_job_output" {
		data, _ = json.Marshal(string(data))
		var text string
		_ = json.Unmarshal(data, &text)
		return mcpclient.CallResult{Content: []mcpclient.Content{{Type: "text", Text: text}}}, nil
	}

	return mcpclient.CallResult{Structured: data}, nil
}
