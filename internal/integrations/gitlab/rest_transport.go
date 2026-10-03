package gitlab

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"local-agent-workbench/internal/forge"
)

type RESTClient struct {
	base  string
	token string
	http  *http.Client
	views *Client
}

func NewRESTClient(connection forge.Connection, token string) (*RESTClient, error) {
	base, e := BaseURL(connection.URL)
	if e != nil {
		return nil, e
	}
	u, e := url.Parse(base)
	if e != nil || u.User != nil {
		return nil, errors.New("неверный адрес GitLab")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return nil, errors.New("для GitLab нужен HTTPS")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if connection.CAPath != "" {
		pem, e := os.ReadFile(connection.CAPath)
		if e != nil {
			return nil, e
		}
		pool, _ := x509.SystemCertPool()
		if pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("файл CA не содержит сертификата")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	h := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	return &RESTClient{base: base + "/api/v4", token: token, http: h, views: NewClient(nil, base, nil)}, nil
}
func (c *RESTClient) ID() string         { return "gitlab" }
func (c *RESTClient) ReviewTerm() string { return "MR" }
func (c *RESTClient) request(ctx context.Context, method, path string, params url.Values, body any) ([]byte, int, error) {
	var reader io.Reader
	if body != nil {
		raw, e := json.Marshal(body)
		if e != nil {
			return nil, 0, e
		}
		reader = bytes.NewReader(raw)
	}
	target := c.base + path
	if len(params) > 0 {
		target += "?" + params.Encode()
	}
	req, e := http.NewRequestWithContext(ctx, method, target, reader)
	if e != nil {
		return nil, 0, e
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, e := c.http.Do(req)
	if e != nil {
		return nil, 0, &forge.Error{Reason: "network", Problem: "GitLab не ответил; проверьте соединение", Uncertain: method != "GET"}
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024+1))
	if e != nil {
		return nil, 0, &forge.Error{Reason: "network", Problem: "ответ GitLab прерван", Uncertain: method != "GET"}
	}
	if len(raw) > 8*1024*1024 {
		return nil, 0, &forge.Error{Reason: "too_large", Problem: "ответ GitLab превышает 8 МБ", Uncertain: method != "GET"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		reason := "refused"
		switch response.StatusCode {
		case 401, 403:
			reason = "auth"
		case 404:
			reason = "not_found"
		case 409:
			reason = "stale"
		case 429:
			reason = "rate_limit"
		}
		text := clip(string(raw), 1500)
		if c.token != "" {
			text = strings.ReplaceAll(text, c.token, "[secret]")
		}
		return nil, 0, &forge.Error{Reason: reason, Problem: fmt.Sprintf("GitLab HTTP %d: %s", response.StatusCode, text), Uncertain: method != "GET" && response.StatusCode >= 500}
	}
	next, _ := strconv.Atoi(response.Header.Get("X-Next-Page"))
	return raw, next, nil
}
func (c *RESTClient) get(ctx context.Context, path string, params url.Values, target any) (int, error) {
	raw, next, e := c.request(ctx, "GET", path, params, nil)
	if e != nil {
		return 0, e
	}
	if e = json.Unmarshal(raw, target); e != nil {
		return 0, &forge.Error{Reason: "format", Problem: "GitLab вернул непонятный ответ"}
	}
	if params != nil {
		page, _ := strconv.Atoi(params.Get("page"))
		if page < 1 {
			page = 1
		}
		if next > 0 && next <= page {
			return 0, errors.New("GitLab вернул повторную страницу")
		}
	}
	return next, nil
}
func projectRoute(project string) string { return "/projects/" + url.PathEscape(project) }
func reviewRoute(q forge.Request) string {
	return projectRoute(q.Project) + "/merge_requests/" + strconv.Itoa(q.IID)
}
func pageParams(page int) url.Values {
	if page < 1 {
		page = 1
	}
	return url.Values{"page": {strconv.Itoa(page)}, "per_page": {"100"}}
}
func (c *RESTClient) review(ctx context.Context, q forge.Request) (MergeRequestDetail, error) {
	var raw rawMergeRequest
	_, e := c.get(ctx, reviewRoute(q), nil, &raw)
	v := c.views.mergeRequestDetail(raw)
	v.ProjectPath = q.Project
	return v, e
}
func (c *RESTClient) Execute(ctx context.Context, q forge.Request) (forge.Response, error) {
	switch q.Action {
	case "review", "changes", "discussions", "approvals", "update", "comment", "reply", "resolve", "approve", "unapprove", "merge":
		if q.IID <= 0 || q.Project == "" {
			return forge.Response{}, errors.New("нужны проект и MR")
		}
	}
	if q.Action == "create" && q.IID != 0 {
		return forge.Response{}, errors.New("создание MR не принимает iid")
	}
	if q.Page < 0 || q.Page > 1000000 {
		return forge.Response{}, errors.New("неверная страница")
	}
	if q.Project != "" && (strings.Contains(q.Project, "..") || strings.ContainsAny(q.Project, "\x00\r\n")) {
		return forge.Response{}, errors.New("неверный проект")
	}
	if q.Page < 0 || q.Page > 100000 {
		return forge.Response{}, errors.New("неверная страница")
	}
	if forge.IsWrite(q.Action) {
		return c.write(ctx, q)
	}
	return c.read(ctx, q)
}
