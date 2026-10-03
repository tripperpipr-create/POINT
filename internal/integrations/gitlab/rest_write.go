package gitlab

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"local-agent-workbench/internal/forge"
)

func (c *RESTClient) write(ctx context.Context, q forge.Request) (forge.Response, error) {
	response := forge.Response{}
	if q.Project == "" {
		return response, errors.New("нужен проект действия")
	}
	var current MergeRequestDetail
	if q.IID > 0 {
		var e error
		current, e = c.review(ctx, q)
		if e != nil {
			return response, e
		}
		if q.ExpectedSHA == "" || q.ExpectedSHA != current.SHA {
			return response, &forge.Error{Reason: "stale", Problem: "MR изменился: обновите ревизию перед действием"}
		}
	}
	method, path := "POST", reviewRoute(q)
	body := map[string]any{}
	switch q.Action {
	case "create", "update":
		if strings.TrimSpace(q.Title) == "" || q.TargetBranch == "" {
			return response, errors.New("нужны заголовок и целевая ветка")
		}
		title := strings.TrimPrefix(q.Title, "Draft: ")
		if q.Draft {
			title = "Draft: " + title
		}
		body = map[string]any{"title": title, "description": q.Description, "target_branch": q.TargetBranch, "reviewer_ids": q.ReviewerIDs}
		if q.Action == "create" {
			if q.SourceBranch == "" || q.SourceBranch == q.TargetBranch {
				return response, errors.New("выберите исходную ветку, отличную от целевой")
			}
			var existing []rawMergeRequest
			_, e := c.get(ctx, projectRoute(q.Project)+"/merge_requests", url.Values{"state": {"opened"}, "source_branch": {q.SourceBranch}, "target_branch": {q.TargetBranch}, "scope": {"all"}}, &existing)
			if e != nil {
				return response, e
			}
			if len(existing) > 0 {
				v := c.views.mergeRequestDetail(existing[0])
				v.ProjectPath = q.Project
				response.Data = v
				return response, nil
			}
			body["source_branch"] = q.SourceBranch
			path = projectRoute(q.Project) + "/merge_requests"
		} else {
			method = "PUT"
		}
	case "comment":
		if strings.TrimSpace(q.Body) == "" || len(q.Body) > maxNoteBody {
			return response, errors.New("комментарий пуст или слишком велик")
		}
		path += "/discussions"
		body["body"] = q.Body
		if q.Position != nil {
			p := q.Position
			if p.HeadSHA != current.DiffRefs.HeadSHA || p.BaseSHA != current.DiffRefs.BaseSHA || p.StartSHA != current.DiffRefs.StartSHA {
				return response, &forge.Error{Reason: "stale", Problem: "diff MR изменился"}
			}
			if p.OldPath == "" || p.NewPath == "" || (p.OldLine <= 0 && p.NewLine <= 0) {
				return response, errors.New("неверная позиция комментария")
			}
			position := map[string]any{"position_type": "text", "base_sha": p.BaseSHA, "head_sha": p.HeadSHA, "start_sha": p.StartSHA, "old_path": p.OldPath, "new_path": p.NewPath}
			if p.OldLine > 0 {
				position["old_line"] = p.OldLine
			}
			if p.NewLine > 0 {
				position["new_line"] = p.NewLine
			}
			body["position"] = position
		}
	case "reply", "resolve":
		if q.DiscussionID == "" {
			return response, errors.New("нужна нить обсуждения")
		}
		path += "/discussions/" + url.PathEscape(q.DiscussionID)
		if q.Action == "reply" {
			if strings.TrimSpace(q.Body) == "" || len(q.Body) > maxNoteBody {
				return response, errors.New("ответ пуст или слишком велик")
			}
			path += "/notes"
			body["body"] = q.Body
		} else {
			method = "PUT"
			body["resolved"] = q.Resolved
		}
	case "approve":
		path += "/approve"
		body["sha"] = q.ExpectedSHA
	case "unapprove":
		path += "/unapprove"
	case "merge":
		if !q.Confirmed {
			return response, errors.New("подтвердите слияние")
		}
		method = "PUT"
		path += "/merge"
		body = map[string]any{"sha": q.ExpectedSHA, "squash": q.Squash, "should_remove_source_branch": q.RemoveSourceBranch}
	case "retryJob":
		if q.JobID <= 0 {
			return response, errors.New("нужна джоба")
		}
		path = projectRoute(q.Project) + "/jobs/" + strconv.Itoa(q.JobID) + "/retry"
	case "retryPipeline":
		if q.PipelineID <= 0 {
			return response, errors.New("нужен пайплайн")
		}
		path = projectRoute(q.Project) + "/pipelines/" + strconv.Itoa(q.PipelineID) + "/retry"
	default:
		return response, errors.New("неизвестное действие записи")
	}
	raw, _, e := c.request(ctx, method, path, nil, body)
	if e != nil {
		return response, e
	}
	if q.Action == "create" || q.Action == "update" || q.Action == "merge" {
		var item rawMergeRequest
		if e = decodeText(string(raw), &item); e != nil {
			return response, &forge.Error{Reason: "format", Problem: "операция выполнена, обновите MR для проверки результата", Uncertain: true}
		}
		v := c.views.mergeRequestDetail(item)
		v.ProjectPath = q.Project
		response.Data = v
	} else {
		response.Data = map[string]any{"done": true}
	}
	return response, nil
}
