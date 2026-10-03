package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"local-agent-workbench/internal/forge"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRESTReviewRevisionAndPosition(t *testing.T) {
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "secret" {
			t.Error("token missing")
		}
		if r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"iid":7,"sha":"head","diff_refs":{"base_sha":"base","head_sha":"head","start_sha":"start"}}`))
			return
		}
		writes++
		if !strings.HasSuffix(r.URL.Path, "/discussions") {
			t.Errorf("route %s", r.URL.Path)
		}
		var input map[string]any
		if e := json.NewDecoder(r.Body).Decode(&input); e != nil {
			t.Fatal(e)
		}
		p := input["position"].(map[string]any)
		if p["new_line"] != float64(3) || p["head_sha"] != "head" {
			t.Errorf("position %v", p)
		}
		_, _ = w.Write([]byte(`{"id":"thread"}`))
	}))
	defer server.Close()
	c, e := NewRESTClient(forge.Connection{URL: server.URL}, "secret")
	if e != nil {
		t.Fatal(e)
	}
	q := forge.Request{Action: "comment", Project: "group/repo", IID: 7, ExpectedSHA: "old", Body: "review"}
	_, e = c.Execute(context.Background(), q)
	var issue *forge.Error
	if !errors.As(e, &issue) || issue.Reason != "stale" || writes != 0 {
		t.Fatalf("stale: %v writes %d", e, writes)
	}
	q.ExpectedSHA = "head"
	q.Position = &forge.Position{OldPath: "a.go", NewPath: "a.go", NewLine: 3, BaseSHA: "base", HeadSHA: "head", StartSHA: "start"}
	if _, e = c.Execute(context.Background(), q); e != nil {
		t.Fatal(e)
	}
	if writes != 1 {
		t.Fatalf("writes %d", writes)
	}
	q.Position.HeadSHA = "stale"
	if _, e = c.Execute(context.Background(), q); e == nil || writes != 1 {
		t.Fatal("stale position published")
	}
}

func TestRESTCreateUsesExistingAndUnknownWriteNotRetried(t *testing.T) {
	writes := 0
	existing := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			if existing {
				_, _ = w.Write([]byte(`[{"iid":12,"sha":"head"}]`))
			} else {
				_, _ = w.Write([]byte(`[]`))
			}
			return
		}
		writes++
		w.WriteHeader(502)
		_, _ = w.Write([]byte("secret upstream failed"))
	}))
	defer server.Close()
	c, e := NewRESTClient(forge.Connection{URL: server.URL}, "secret")
	if e != nil {
		t.Fatal(e)
	}
	q := forge.Request{Action: "create", Project: "g/p", SourceBranch: "feature", TargetBranch: "main", Title: "Change"}
	v, e := c.Execute(context.Background(), q)
	if e != nil || v.Data.(MergeRequestDetail).IID != 12 || writes != 0 {
		t.Fatalf("%+v %v writes %d", v, e, writes)
	}
	existing = false
	_, e = c.Execute(context.Background(), q)
	var issue *forge.Error
	if !errors.As(e, &issue) || !issue.Uncertain || writes != 1 || strings.Contains(e.Error(), "secret upstream") {
		t.Fatalf("unknown %v writes %d", e, writes)
	}
}
func TestRESTDisabledReadOnlyAndPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/user"):
			_, _ = w.Write([]byte(`{"id":10}`))
		case strings.HasSuffix(r.URL.Path, "/self"):
			_, _ = w.Write([]byte(`{"scopes":["read_api"]}`))
		default:
			w.Header().Set("X-Next-Page", "2")
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()
	c, e := NewRESTClient(forge.Connection{URL: server.URL}, "secret")
	if e != nil {
		t.Fatal(e)
	}
	v, e := c.Execute(context.Background(), forge.Request{Action: "status"})
	if e != nil {
		t.Fatal(e)
	}
	if v.Capabilities["merge"].Available || !v.Capabilities["reviews"].Available {
		t.Fatal(v.Capabilities)
	}
	v, e = c.Execute(context.Background(), forge.Request{Action: "reviews", Scope: "review"})
	if e != nil || v.NextPage != 2 {
		t.Fatalf("%+v %v", v, e)
	}
}

func TestRESTReviewWriteCycle(t *testing.T) {
	type step struct{ action, method, suffix string }
	steps := []step{{"create", "POST", "/merge_requests"}, {"update", "PUT", "/merge_requests/7"},
		{"comment", "POST", "/discussions"}, {"reply", "POST", "/discussions/thread/notes"},
		{"resolve", "PUT", "/discussions/thread"}, {"approve", "POST", "/approve"}, {"unapprove", "POST", "/unapprove"},
		{"merge", "PUT", "/merge"}, {"retryJob", "POST", "/jobs/13/retry"}, {"retryPipeline", "POST", "/pipelines/19/retry"}}
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			if strings.HasSuffix(r.URL.Path, "/merge_requests") {
				_, _ = w.Write([]byte("[]"))
			} else {
				_, _ = w.Write([]byte(`{"iid":7,"sha":"head","diff_refs":{"base_sha":"base","head_sha":"head","start_sha":"start"}}`))
			}
			return
		}
		if writes >= len(steps) {
			t.Error("unexpected write")
			return
		}
		expected := steps[writes]
		writes++
		if r.Method != expected.method || !strings.HasSuffix(r.URL.Path, expected.suffix) {
			t.Errorf("%s: %s %s", expected.action, r.Method, r.URL.Path)
		}
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
		}
		if expected.action == "merge" || expected.action == "approve" {
			if body["sha"] != "head" {
				t.Error("missing revision")
			}
		}
		if expected.action == "resolve" && body["resolved"] != true {
			t.Error("thread not resolved")
		}
		if expected.action == "update" {
			reviewers, ok := body["reviewer_ids"].([]any)
			if !ok || len(reviewers) != 1 {
				t.Error("reviewers not sent")
			}
			if body["title"] != "Draft: title" {
				t.Error("draft title missing")
			}
		}
		_, _ = w.Write([]byte(`{"iid":7,"sha":"head"}`))
	}))
	defer server.Close()
	client, e := NewRESTClient(forge.Connection{URL: server.URL}, "secret")
	if e != nil {
		t.Fatal(e)
	}
	for _, step := range steps {
		q := forge.Request{Action: step.action, Project: "group/repo", IID: 7, ExpectedSHA: "head", Title: "title", Description: "body",
			SourceBranch: "feature", TargetBranch: "main", Draft: true, ReviewerIDs: []int{3}, Body: "note", DiscussionID: "thread", Resolved: true, Confirmed: true, JobID: 13, PipelineID: 19}
		if step.action == "create" || strings.HasPrefix(step.action, "retry") {
			q.IID = 0
		}
		if _, e := client.Execute(context.Background(), q); e != nil {
			t.Fatalf("%s: %v", step.action, e)
		}
	}
	if writes != len(steps) {
		t.Fatal(writes)
	}
}
