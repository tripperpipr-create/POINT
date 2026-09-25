package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeHTTPServer — удалённый сервер по Streamable HTTP. tools/call «stream»
// отвечает потоком SSE и посреди него спрашивает клиента ping; ответ клиента
// приходит отдельным POST.
type fakeHTTPServer struct {
	mu           sync.Mutex
	initializes  int
	expireOnce   atomic.Bool
	pingAnswered chan struct{}
	deleted      atomic.Bool
	sawHeader    atomic.Value
}

func (f *fakeHTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		f.deleted.Store(true)
		w.WriteHeader(http.StatusOK)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var msg map[string]json.RawMessage
	_ = json.Unmarshal(body, &msg)
	var method string
	_ = json.Unmarshal(msg["method"], &method)
	if auth := r.Header.Get("Authorization"); auth != "" {
		f.sawHeader.Store(auth)
	}
	if method == "" {
		// Ответ клиента на наш ping.
		select {
		case f.pingAnswered <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if method != "initialize" && r.Header.Get("Mcp-Session-Id") != "session-1" {
		http.Error(w, "no session", http.StatusBadRequest)
		return
	}
	if method != "initialize" && r.Header.Get("MCP-Protocol-Version") != "2025-06-18" {
		http.Error(w, "no protocol header", http.StatusBadRequest)
		return
	}
	id := msg["id"]
	reply := func(result any) {
		w.Header().Set("Content-Type", "application/json")
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
		_, _ = w.Write(raw)
	}
	switch method {
	case "initialize":
		f.mu.Lock()
		f.initializes++
		f.mu.Unlock()
		w.Header().Set("Mcp-Session-Id", "session-1")
		reply(map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "remote", "version": "2"},
		})
	case "notifications/initialized", "notifications/cancelled":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		if f.expireOnce.CompareAndSwap(true, false) {
			http.Error(w, "session expired", http.StatusNotFound)
			return
		}
		reply(map[string]any{"tools": []map[string]any{{"name": "get_merge_request", "inputSchema": map[string]any{"type": "object"}}}})
	case "tools/call":
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, ": keep-alive\n\n")
		fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":99,\"method\":\"ping\"}\n\n")
		flusher.Flush()
		select {
		case <-f.pingAnswered:
		case <-time.After(3 * time.Second):
			return
		}
		raw, _ := json.MarshalIndent(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
			"content": []map[string]any{{"type": "text", "text": "streamed"}},
		}}, "", "  ")
		// Данные события — несколько строк data:, склеиваемых переводом строки,
		// как вправе делать сервер (там, где перевод строки допустим в JSON).
		fmt.Fprint(w, "id: 7\n")
		for _, line := range strings.Split(string(raw), "\n") {
			fmt.Fprintf(w, "data: %s\n", line)
		}
		fmt.Fprint(w, "\n")
		flusher.Flush()
	}
}

func httpConfig(url string) Config {
	return Config{Name: "remote", Transport: TransportHTTP, URL: url, Headers: map[string]string{"Authorization": "Bearer SUPERSECRETVALUE"},
		Secrets: []string{"SUPERSECRETVALUE"}, StartTimeout: 5 * time.Second, CallTimeout: 5 * time.Second}
}

func TestHTTPStreamSessionAndServerPing(t *testing.T) {
	fake := &fakeHTTPServer{pingAnswered: make(chan struct{}, 1)}
	server := httptest.NewServer(fake)
	defer server.Close()
	client, err := Dial(context.Background(), httpConfig(server.URL+"/mcp"))
	if err != nil {
		t.Fatal(err)
	}
	if client.Info().Name != "remote" {
		t.Fatalf("info = %+v", client.Info())
	}
	if fake.sawHeader.Load() != "Bearer SUPERSECRETVALUE" {
		t.Fatal("secret header not sent")
	}
	result, err := client.CallTool(context.Background(), "get_merge_request", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text() != "streamed" {
		t.Fatalf("stream result = %q", result.Text())
	}
	// Сервер забыл сессию: клиент приветствуется заново и повторяет запрос.
	fake.expireOnce.Store(true)
	tools, err := client.ListTools(context.Background())
	if err != nil || len(tools) != 1 {
		t.Fatalf("after expiry: %v %+v", err, tools)
	}
	fake.mu.Lock()
	initializes := fake.initializes
	fake.mu.Unlock()
	if initializes != 2 {
		t.Fatalf("initialize count = %d, want 2", initializes)
	}
	_ = client.Close()
	if !fake.deleted.Load() {
		t.Fatal("session was not closed with DELETE")
	}
}

func TestHTTPAuthRedirectAndScheme(t *testing.T) {
	unauthorized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "token SUPERSECRETVALUE rejected", http.StatusUnauthorized)
	}))
	defer unauthorized.Close()
	_, err := Dial(context.Background(), httpConfig(unauthorized.URL))
	if KindOf(err) != KindAuth {
		t.Fatalf("401: kind = %q, err = %v", KindOf(err), err)
	}
	if strings.Contains(err.Error(), "SUPERSECRETVALUE") {
		t.Fatalf("secret in error: %v", err)
	}

	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://elsewhere.example/mcp", http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	_, err = Dial(context.Background(), httpConfig(redirect.URL))
	if KindOf(err) != KindProtocol {
		t.Fatalf("redirect: kind = %q, err = %v", KindOf(err), err)
	}

	_, err = Dial(context.Background(), httpConfig("http://gitlab.example.test/mcp"))
	if KindOf(err) != KindProtocol || !strings.Contains(err.Error(), "https") {
		t.Fatalf("plain http to a remote host: %v", err)
	}
	_, err = Dial(context.Background(), httpConfig("https://user:pass@gitlab.example.test/mcp"))
	if KindOf(err) != KindProtocol {
		t.Fatalf("credentials in URL: %v", err)
	}
}

type stubResolver map[string][]string

func (s stubResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	values, ok := s[host]
	if !ok {
		return nil, fmt.Errorf("no such host %s", host)
	}
	out := make([]net.IPAddr, 0, len(values))
	for _, value := range values {
		out = append(out, net.IPAddr{IP: net.ParseIP(value)})
	}
	return out, nil
}

func TestPinAddress(t *testing.T) {
	resolver := stubResolver{
		"mcp.example.com":        {"8.8.4.4", "8.8.8.8"},
		"gitlab.corp.example":    {"10.20.0.5"},
		"gitlab.vpn.example":     {"100.101.1.2"},
		"rebind.example.com":     {"8.8.8.8", "10.0.0.1"},
		"metadata.example.com":   {"169.254.169.254"},
		"localhost.example.test": {"127.0.0.1"},
	}
	ctx := context.Background()
	cases := []struct {
		host, granted string
		want          string
		kind          Kind
	}{
		{"mcp.example.com", "", "8.8.4.4", ""},
		{"gitlab.corp.example", "", "", KindPrivateHostDenied},
		{"gitlab.corp.example", "gitlab.corp.example", "10.20.0.5", ""},
		{"GITLAB.corp.example.", "gitlab.corp.example", "10.20.0.5", ""},
		{"gitlab.corp.example", "other.corp.example", "", KindPrivateHostDenied},
		{"gitlab.vpn.example", "gitlab.vpn.example", "100.101.1.2", ""},
		{"rebind.example.com", "rebind.example.com", "", KindNetwork},
		{"metadata.example.com", "metadata.example.com", "", KindNetwork},
		{"localhost.example.test", "localhost.example.test", "127.0.0.1", ""},
		{"10.20.0.5", "10.20.0.5", "10.20.0.5", ""},
		{"10.20.0.5", "", "", KindPrivateHostDenied},
		{"8.8.8.8", "", "", KindNetwork},
		{"169.254.169.254", "169.254.169.254", "", KindNetwork},
		{"missing.example.com", "", "", KindNetwork},
	}
	for _, tc := range cases {
		got, err := PinAddress(ctx, resolver, tc.host, tc.granted)
		if tc.kind != "" {
			if KindOf(err) != tc.kind {
				t.Errorf("%s granted=%q: kind %q, err %v", tc.host, tc.granted, KindOf(err), err)
			}
			continue
		}
		if err != nil || got != netip.MustParseAddr(tc.want) {
			t.Errorf("%s granted=%q: got %v err %v, want %s", tc.host, tc.granted, got, err, tc.want)
		}
	}
}

// Соединение идёт на закреплённый адрес; отказ адреса — типизированный сбой,
// а не «connection refused».
func TestPinnedDialerRefusesPrivateHostThroughHTTP(t *testing.T) {
	dial := PinnedDialer(stubResolver{"gitlab.corp.example": {"10.20.0.5"}}, "")
	cfg := httpConfig("https://gitlab.corp.example/mcp")
	cfg.DialContext = dial
	_, err := Dial(context.Background(), cfg)
	if KindOf(err) != KindPrivateHostDenied {
		t.Fatalf("kind = %q, err = %v", KindOf(err), err)
	}
}
