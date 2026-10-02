package acceptance_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestMatrixModelProxyPreservesProviderPathHostAndByteAccounting(t *testing.T) {
	var actualPath, actualHost, authorization string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actualPath, actualHost, authorization = r.URL.Path, r.Host, r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"data":[{"id":"Qwen3.8-27B"}]}`)
	}))
	defer upstream.Close()
	route, _ := url.Parse(upstream.URL + "/v1")
	var counter atomic.Int64
	proxy, transport := matrixModelProxy(route, &counter)
	defer transport.CloseIdleConnections()
	local := httptest.NewServer(proxy)
	defer local.Close()
	request, _ := http.NewRequest(http.MethodGet, local.URL+"/v1/models", nil)
	request.Header.Set("Authorization", "Bearer synthetic-fixture")
	response, err := local.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err = io.ReadAll(response.Body); err != nil {
		t.Fatal(err)
	}
	if actualPath != "/v1/models" || actualHost != route.Host || authorization != "Bearer synthetic-fixture" || counter.Load() <= 0 {
		t.Fatalf("proxy changed request/accounting: path=%q host=%q counted=%d", actualPath, actualHost, counter.Load())
	}
}

func TestMatrixModelProxyRejectsRedirectBeforeClientCanForwardCredentials(t *testing.T) {
	var escaped atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escaped.Add(1)
	}))
	defer other.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer upstream.Close()
	route, _ := url.Parse(upstream.URL + "/v1")
	var counter atomic.Int64
	proxy, transport := matrixModelProxy(route, &counter)
	defer transport.CloseIdleConnections()
	local := httptest.NewServer(proxy)
	defer local.Close()
	request, _ := http.NewRequest(http.MethodGet, local.URL+"/v1/models", nil)
	request.Header.Set("Authorization", "Bearer synthetic-fixture")
	response, err := local.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadGateway || escaped.Load() != 0 {
		t.Fatalf("pinned model route escaped: status=%d requests=%d", response.StatusCode, escaped.Load())
	}
}
