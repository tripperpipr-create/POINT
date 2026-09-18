package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllamaDisablesThinkingOnlyWhenRequested(t *testing.T) {
	for _, disable := range []bool{false, true} {
		var request map[string]json.RawMessage
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&request)
			w.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"},"done":true}`))
		}))
		model := NewOllama(Config{BaseURL: server.URL, TimeoutSeconds: 5})
		err := model.Stream(context.Background(), ModelRequest{Model: "local-model", DisableThinking: disable, JSONSchema: json.RawMessage(`{"type":"object"}`)}, func(ModelEvent) error { return nil })
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
		if string(request["format"]) != `{"type":"object"}` {
			t.Fatal("native schema was not transmitted")
		}
		value, present := request["think"]
		if disable && (!present || string(value) != "false") {
			t.Fatalf("explicit disable lost: %s", value)
		}
		if !disable && present {
			t.Fatal("provider default overridden")
		}
	}
}

func TestOllamaContextWindow(t *testing.T) {
	for _, window := range []int{0, 16384, 32768} {
		var request struct {
			Options map[string]json.RawMessage `json:"options"`
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&request)
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"},"done":true}`))
		}))
		err := NewOllama(Config{BaseURL: server.URL, TimeoutSeconds: 5}).Stream(context.Background(), ModelRequest{Model: "local", ContextWindowTokens: window}, func(ModelEvent) error { return nil })
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
		raw, present := request.Options["num_ctx"]
		if window == 0 {
			if present {
				t.Fatal("provider default overridden")
			}
			continue
		}
		var got int
		if json.Unmarshal(raw, &got) != nil || got != window {
			t.Fatalf("lost context setting: %s", raw)
		}
	}
}
