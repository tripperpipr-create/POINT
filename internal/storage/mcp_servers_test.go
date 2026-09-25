package storage

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestMCPServerRoundTripToolsAndDelete(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	server := domain.MCPServer{
		ID: "mcp_gl", DisplayName: "GitLab", Kind: domain.MCPServerGitLab, Transport: domain.MCPTransportStdio,
		Command: "npx", Args: []string{"-y", "@zereight/mcp-gitlab@2.1.66"},
		Env:         map[string]string{"GITLAB_API_URL": "https://gitlab.corp.example/api/v4"},
		SecretEnv:   map[string]string{"GITLAB_PERSONAL_ACCESS_TOKEN": "point.mcp.mcp_gl.GITLAB_PERSONAL_ACCESS_TOKEN"},
		Settings:    map[string]string{"url": "https://gitlab.corp.example"},
		TrustDigest: "sha256:abc", TrustedAt: &now, ResolvedCommand: "/usr/bin/npx",
		Status: domain.ConnectionConnected, CreatedAt: now, UpdatedAt: now,
	}
	if err = store.SaveMCPServer(ctx, server); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetMCPServer(ctx, "mcp_gl")
	if err != nil {
		t.Fatal(err)
	}
	if got.Args[1] != "@zereight/mcp-gitlab@2.1.66" || got.SecretEnv["GITLAB_PERSONAL_ACCESS_TOKEN"] == "" ||
		got.Env["GITLAB_API_URL"] == "" || got.TrustedAt == nil || !got.TrustedAt.Equal(now) || got.LastProbeAt != nil ||
		got.Kind != domain.MCPServerGitLab || got.Headers != nil {
		t.Fatalf("round trip lost fields: %+v", got)
	}
	tools := []domain.MCPTool{
		{ServerID: "mcp_gl", Name: "get_merge_request", InputSchema: json.RawMessage(`{"type":"object"}`),
			Annotations: json.RawMessage(`{"readOnlyHint":true}`), Digest: "sha256:1", ApprovedDigest: "sha256:1",
			Enabled: true, State: domain.MCPToolOK, Risk: domain.ToolRiskLow, UpdatedAt: now},
		{ServerID: "mcp_gl", Name: "merge_merge_request", Digest: "sha256:2", State: domain.MCPToolNew, Risk: domain.ToolRiskCritical, UpdatedAt: now},
	}
	if err = store.ReplaceMCPTools(ctx, "mcp_gl", tools); err != nil {
		t.Fatal(err)
	}
	listed, err := store.ListMCPTools(ctx, "mcp_gl")
	if err != nil || len(listed) != 2 || !listed[0].Enabled || listed[1].Enabled || string(listed[0].Annotations) != `{"readOnlyHint":true}` {
		t.Fatalf("tools = %+v, err = %v", listed, err)
	}
	if err = store.AppendIntegrationAction(ctx, domain.IntegrationAction{ID: "act_1", Actor: "owner", ServerID: "mcp_gl",
		Tool: "create_merge_request_note", Target: "group/app!12", Outcome: "ok", BodySHA256: "sha256:x", BodyLength: 42, At: now}); err != nil {
		t.Fatal(err)
	}
	actions, err := store.ListIntegrationActions(ctx, 10)
	if err != nil || len(actions) != 1 || actions[0].BodyLength != 42 {
		t.Fatalf("actions = %+v, err = %v", actions, err)
	}
	if err = store.DeleteMCPServer(ctx, "mcp_gl"); err != nil {
		t.Fatal(err)
	}
	if listed, _ = store.ListMCPTools(ctx, "mcp_gl"); len(listed) != 0 {
		t.Fatalf("tools outlived their server: %+v", listed)
	}
	if err = store.DeleteMCPServer(ctx, "mcp_gl"); err == nil {
		t.Fatal("second delete succeeded")
	}
}
