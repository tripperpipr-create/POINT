package gitlab

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"local-agent-workbench/internal/security"
	"net/url"
	"strconv"
	"strings"

	"local-agent-workbench/internal/forge"
)

func (c *RESTClient) read(ctx context.Context, q forge.Request) (forge.Response, error) {
	response := forge.Response{}
	switch q.Action {
	case "status":
		var user rawUser
		_, e := c.get(ctx, "/user", nil, &user)
		if e != nil {
			return response, e
		}
		response.Data = map[string]any{"user": user.view(), "term": "MR"}
		response.Capabilities = map[string]forge.Capability{}
		writable := true
		var token struct {
			Scopes []string `json:"scopes"`
		}
		if _, e = c.get(ctx, "/personal_access_tokens/self", nil, &token); e == nil {
			writable = false
			for _, scope := range token.Scopes {
				if scope == "api" {
					writable = true
				}
			}
		}
		if q.Project != "" {
			var project struct {
				Permissions struct {
					ProjectAccess *struct {
						AccessLevel int `json:"access_level"`
					} `json:"project_access"`
					GroupAccess *struct {
						AccessLevel int `json:"access_level"`
					} `json:"group_access"`
				} `json:"permissions"`
			}
			if _, e = c.get(ctx, projectRoute(q.Project), nil, &project); e != nil {
				return response, e
			}
			level := 0
			if project.Permissions.ProjectAccess != nil {
				level = project.Permissions.ProjectAccess.AccessLevel
			}
			if project.Permissions.GroupAccess != nil && project.Permissions.GroupAccess.AccessLevel > level {
				level = project.Permissions.GroupAccess.AccessLevel
			}
			if level > 0 && level < 30 {
				writable = false
			}
		}
		for _, name := range []string{"reviews", "diff", "discussions", "pipelines", "logs", "create", "update", "comment", "reply", "resolve", "approve", "unapprove", "merge", "retryJob", "retryPipeline"} {
			available := !forge.IsWrite(name) || writable
			reason := ""
			if !available {
				reason = "нужны права записи в проект и scope api"
			}
			response.Capabilities[name] = forge.Capability{Available: available, Reason: reason}
		}
	case "reviews":
		params := pageParams(q.Page)
		params.Set("state", "opened")
		route := "/merge_requests"
		if q.Project != "" {
			route = projectRoute(q.Project) + "/merge_requests"
			params.Set("scope", "all")
		}
		switch q.Scope {
		case "mine", "review":
			var user rawUser
			if _, e := c.get(ctx, "/user", nil, &user); e != nil {
				return response, e
			}
			params.Set("scope", "all")
			if q.Scope == "mine" {
				params.Set("author_id", strconv.Itoa(int(user.ID)))
			} else {
				params.Set("reviewer_id", strconv.Itoa(int(user.ID)))
			}
		}
		var raw []rawMergeRequest
		next, e := c.get(ctx, route, params, &raw)
		if e != nil {
			return response, e
		}
		out := []MergeRequestDetail{}
		for _, item := range raw {
			v := c.views.mergeRequestDetail(item)
			if q.Project != "" {
				v.ProjectPath = q.Project
			} else {
				v.ProjectPath = strconv.Itoa(v.ProjectID)
			}
			out = append(out, v)
		}
		response.Data = out
		response.NextPage = next
	case "review":
		v, e := c.review(ctx, q)
		if e != nil {
			return response, e
		}
		response.Data = v
	case "discussions":
		var raw []struct {
			ID         string    `json:"id"`
			Individual bool      `json:"individual_note"`
			Notes      []rawNote `json:"notes"`
		}
		next, e := c.get(ctx, reviewRoute(q)+"/discussions", pageParams(q.Page), &raw)
		if e != nil {
			return response, e
		}
		out := []Discussion{}
		for _, item := range raw {
			v := Discussion{ID: item.ID, Individual: item.Individual, Notes: []Note{}}
			for _, note := range item.Notes {
				n := noteView(note)
				v.Notes = append(v.Notes, n)
				if n.Resolvable {
					v.Resolvable = true
					v.Resolved = n.Resolved
				}
			}
			out = append(out, v)
		}
		response.Data = out
		response.NextPage = next
	case "changes":
		var raw []struct {
			OldPath   string `json:"old_path"`
			NewPath   string `json:"new_path"`
			New       bool   `json:"new_file"`
			Deleted   bool   `json:"deleted_file"`
			Renamed   bool   `json:"renamed_file"`
			Diff      string `json:"diff"`
			TooLarge  bool   `json:"too_large"`
			Collapsed bool   `json:"collapsed"`
		}
		next, e := c.get(ctx, reviewRoute(q)+"/diffs", pageParams(q.Page), &raw)
		if e != nil {
			return response, e
		}
		out := []map[string]any{}
		for _, f := range raw {
			diff, trimmed := clipFlag(f.Diff, maxDiff)
			out = append(out, map[string]any{"oldPath": f.OldPath, "newPath": f.NewPath, "new": f.New, "deleted": f.Deleted, "renamed": f.Renamed, "diff": diff, "trimmed": trimmed || f.TooLarge || f.Collapsed})
		}
		response.Data = out
		response.NextPage = next
	case "file":
		var raw struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
			Size     int    `json:"size"`
		}
		_, e := c.get(ctx, projectRoute(q.Project)+"/repository/files/"+url.PathEscape(q.Path), url.Values{"ref": {q.Ref}}, &raw)
		if e != nil {
			return response, e
		}
		if raw.Size > maxFileContent {
			response.Data = FileContent{Path: q.Path, Ref: q.Ref, TooBig: true}
			break
		}
		data, e := base64.StdEncoding.DecodeString(raw.Content)
		if e != nil {
			return response, e
		}
		binary := false
		for _, b := range data {
			if b == 0 {
				binary = true
				break
			}
		}
		response.Data = FileContent{Path: q.Path, Ref: q.Ref, Content: string(data), Binary: binary}
	case "pipelines":
		route := projectRoute(q.Project) + "/pipelines"
		params := pageParams(q.Page)
		if q.IID > 0 {
			route = reviewRoute(q) + "/pipelines"
		} else if q.Ref != "" {
			params.Set("ref", q.Ref)
		}
		var raw []rawPipeline
		next, e := c.get(ctx, route, params, &raw)
		if e != nil {
			return response, e
		}
		out := []Pipeline{}
		for _, item := range raw {
			out = append(out, c.views.pipeline(item))
		}
		response.Data = out
		response.NextPage = next
	case "jobs":
		var raw []rawJob
		next, e := c.get(ctx, projectRoute(q.Project)+"/pipelines/"+strconv.Itoa(q.PipelineID)+"/jobs", pageParams(q.Page), &raw)
		if e != nil {
			return response, e
		}
		out := []Job{}
		for _, item := range raw {
			out = append(out, c.views.job(item))
		}
		response.Data = out
		response.NextPage = next
	case "log", "logs":
		raw, _, e := c.request(ctx, "GET", projectRoute(q.Project)+"/jobs/"+strconv.Itoa(q.JobID)+"/trace", nil, nil)
		if e != nil {
			return response, e
		}
		text, trimmed := tail(security.Redact(cleanLog(strings.ReplaceAll(string(raw), c.token, "[secret]"))), maxJobLog)
		response.Data = JobLog{JobID: q.JobID, Text: text, Trimmed: trimmed}
	case "approvals":
		var raw json.RawMessage
		_, e := c.get(ctx, reviewRoute(q)+"/approval_state", nil, &raw)
		if e != nil {
			return response, e
		}
		var state struct {
			Rules []struct {
				Name     string    `json:"name"`
				Required int       `json:"approvals_required"`
				Approved bool      `json:"approved"`
				Users    []rawUser `json:"approved_by"`
			} `json:"rules"`
		}
		if e = json.Unmarshal(raw, &state); e != nil {
			return response, e
		}
		out := Approvals{Rules: []ApprovalRule{}}
		for _, rule := range state.Rules {
			out.Rules = append(out.Rules, ApprovalRule{Name: rule.Name, Required: rule.Required, Approved: rule.Approved, ApprovedBy: users(rule.Users)})
		}
		response.Data = out
	case "users":
		var raw []rawUser
		next, e := c.get(ctx, projectRoute(q.Project)+"/members/all", pageParams(q.Page), &raw)
		if e != nil {
			return response, e
		}
		response.Data = users(raw)
		response.NextPage = next
	default:
		return response, errors.New("неизвестное действие провайдера")
	}
	return response, nil
}
