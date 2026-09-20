package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOrdinaryTestsRejectDeveloperOllamaAndAllowManagedFake(t *testing.T) {
	response, err := http.Get("http://127.0.0.1:11434/api/chat")
	if err == nil {
		response.Body.Close()
		t.Fatal("developer Ollama request escaped the package test guard")
	}
	if !strings.Contains(err.Error(), "unexpected network request") {
		t.Fatalf("unexpected guard error: %v", err)
	}
	fake := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	defer fake.Close()
	response, err = http.Get(fake.URL)
	if err != nil {
		t.Fatalf("managed fake server was rejected: %v", err)
	}
	response.Body.Close()
}
