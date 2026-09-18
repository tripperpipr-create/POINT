package connections_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/connections"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/storage"
)

func TestConnectionPersistsSecretRefOnly(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "conn.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager := connections.Manager{Store: store}
	conn, err := manager.Upsert(context.Background(), connections.UpsertRequest{
		Provider: domain.ProviderOpenAI, DisplayName: "OpenAI", BaseURL: "https://api.openai.com/v1",
		SecretRef: "point.connection.test", Status: domain.ConnectionConnected,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(conn)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(raw)
	if strings.Contains(strings.ToLower(encoded), "sk-") || strings.Contains(encoded, "apiKey") {
		t.Fatalf("connection payload must not contain raw secrets: %s", encoded)
	}
	if conn.SecretRef == "" {
		t.Fatal("expected secretRef")
	}
}
