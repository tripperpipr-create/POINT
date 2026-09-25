package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// httpTransport — удалённый сервер по Streamable HTTP (спецификация
// 2025-03-26 и новее): каждое сообщение — POST, ответ — JSON или поток SSE.
// Устаревший транспорт «HTTP+SSE» 2024 года не поддерживается: импорт
// mcp.json отказывает в `type: sse` с объяснением.
type httpTransport struct {
	endpoint string
	headers  map[string]string
	client   *http.Client
	log      *ring
	handle   func(message) *message

	mu       sync.Mutex
	session  string
	protocol string

	closed    chan struct{}
	closeOnce sync.Once
}

func newHTTPTransport(cfg Config, log *ring, handle func(message) *message) (*httpTransport, error) {
	endpoint, err := url.Parse(strings.TrimSpace(cfg.URL))
	if err != nil || endpoint.Host == "" {
		return nil, newError(KindProtocol, err, "server URL is not an absolute URL")
	}
	if endpoint.User != nil {
		return nil, newError(KindProtocol, nil, "credentials inside the server URL are refused; use a secret header")
	}
	switch endpoint.Scheme {
	case "https":
	case "http":
		if !loopbackHost(endpoint.Hostname()) {
			return nil, newError(KindProtocol, nil, "plain http is allowed only on this computer (localhost); use https")
		}
	default:
		return nil, newError(KindProtocol, nil, "unsupported URL scheme %q", endpoint.Scheme)
	}
	dial := cfg.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
	}
	transport := &http.Transport{
		// Прокси окружения не действует: соединение идёт ровно туда, что
		// проверил и закрепил DialContext.
		Proxy:                 nil,
		DialContext:           dial,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 0,
		MaxIdleConns:          4,
		IdleConnTimeout:       90 * time.Second,
	}
	return &httpTransport{
		endpoint: endpoint.String(),
		headers:  cfg.Headers,
		client: &http.Client{
			Transport: transport,
			// Редирект увёл бы запрос с токеном на чужой узел в обход
			// проверки адреса.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		log:    log,
		handle: handle,
		closed: make(chan struct{}),
	}, nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (t *httpTransport) negotiated(version string) {
	t.mu.Lock()
	t.protocol = version
	t.mu.Unlock()
}

func (t *httpTransport) done() <-chan struct{} { return t.closed }

func (t *httpTransport) newRequest(ctx context.Context, method string, body []byte) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.endpoint, reader)
	if err != nil {
		return nil, newError(KindProtocol, err, "build request")
	}
	for name, value := range t.headers {
		req.Header.Set(name, value)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	t.mu.Lock()
	if t.session != "" {
		req.Header.Set("Mcp-Session-Id", t.session)
	}
	if t.protocol != "" {
		req.Header.Set("MCP-Protocol-Version", t.protocol)
	}
	t.mu.Unlock()
	return req, nil
}

func (t *httpTransport) post(ctx context.Context, msg message) (*http.Response, error) {
	select {
	case <-t.closed:
		return nil, newError(KindNetwork, nil, "connection closed")
	default:
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, newError(KindProtocol, err, "encode message")
	}
	req, err := t.newRequest(ctx, http.MethodPost, body)
	if err != nil {
		return nil, err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		var typed *Error
		if errors.As(err, &typed) {
			return nil, typed
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, newError(KindNetwork, err, "%s", err.Error())
	}
	if msg.Method == "initialize" {
		if session := resp.Header.Get("Mcp-Session-Id"); session != "" {
			t.mu.Lock()
			t.session = clip(session, 512)
			t.mu.Unlock()
		}
	}
	return resp, nil
}

func (t *httpTransport) statusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	detail := strings.TrimSpace(string(body))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return &Error{Kind: KindAuth, Status: resp.StatusCode, Detail: fmt.Sprintf("server refused access (%d)", resp.StatusCode)}
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return &Error{Kind: KindProtocol, Status: resp.StatusCode, Detail: "server answered with a redirect; redirects are refused"}
	default:
		return &Error{Kind: KindNetwork, Status: resp.StatusCode, Detail: fmt.Sprintf("server answered %d %s", resp.StatusCode, clip(detail, 300))}
	}
}

func (t *httpTransport) call(ctx context.Context, msg message) (message, error) {
	resp, err := t.post(ctx, msg)
	if err != nil {
		return message{}, err
	}
	defer resp.Body.Close()
	t.mu.Lock()
	hadSession := t.session != ""
	t.mu.Unlock()
	if resp.StatusCode == http.StatusNotFound && hadSession && msg.Method != "initialize" {
		t.mu.Lock()
		t.session = ""
		t.mu.Unlock()
		return message{}, errSessionExpired
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return message{}, t.statusError(resp)
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	want := idKey(msg.ID)
	switch mediaType {
	case "text/event-stream":
		var answer *message
		err := readSSE(resp.Body, maxMessageBytes, func(data []byte) (bool, error) {
			found, err := t.absorb(ctx, data, want)
			if found != nil {
				answer = found
				return true, nil
			}
			return false, err
		})
		if err != nil {
			return message{}, err
		}
		if answer == nil {
			return message{}, newError(KindProtocol, nil, "event stream ended without an answer to %s", msg.Method)
		}
		return *answer, nil
	case "application/json":
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxMessageBytes+1))
		if err != nil {
			return message{}, newError(KindNetwork, err, "read answer")
		}
		if len(data) > maxMessageBytes {
			return message{}, newError(KindTooLarge, nil, "answer is larger than %d bytes", maxMessageBytes)
		}
		found, err := t.absorb(ctx, data, want)
		if err != nil {
			return message{}, err
		}
		if found == nil {
			return message{}, newError(KindProtocol, nil, "answer does not match request %s", msg.Method)
		}
		return *found, nil
	default:
		return message{}, newError(KindProtocol, nil, "unexpected content type %q", clip(mediaType, 100))
	}
}

// absorb разбирает одно сообщение или пачку: ответ с нашим id возвращается,
// запросы сервера получают ответ отдельным POST, уведомления — обработчику.
func (t *httpTransport) absorb(ctx context.Context, data []byte, want string) (*message, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil
	}
	var batch []message
	if data[0] == '[' {
		if err := json.Unmarshal(data, &batch); err != nil {
			return nil, newError(KindProtocol, err, "unreadable batch")
		}
	} else {
		var single message
		if err := json.Unmarshal(data, &single); err != nil {
			return nil, newError(KindProtocol, err, "unreadable message")
		}
		batch = []message{single}
	}
	var found *message
	for index := range batch {
		msg := batch[index]
		if msg.isResponse() {
			if idKey(msg.ID) == want {
				found = &msg
			}
			continue
		}
		if reply := t.handle(msg); reply != nil {
			resp, err := t.post(ctx, *reply)
			if err == nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
				resp.Body.Close()
			}
		}
	}
	return found, nil
}

func (t *httpTransport) notify(ctx context.Context, msg message) error {
	resp, err := t.post(ctx, msg)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return t.statusError(resp)
	}
	return nil
}

// close закрывает сессию на сервере: DELETE с её номером. Сервер вправе
// ответить 405 — сессия тогда умрёт по его собственному сроку.
func (t *httpTransport) close() error {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		session := t.session
		t.mu.Unlock()
		if session != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if req, err := t.newRequest(ctx, http.MethodDelete, nil); err == nil {
				if resp, err := t.client.Do(req); err == nil {
					resp.Body.Close()
				}
			}
			cancel()
		}
		close(t.closed)
		t.client.CloseIdleConnections()
	})
	return nil
}
