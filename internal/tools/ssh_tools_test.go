package tools_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/policy"
	"local-agent-workbench/internal/servers"
	"local-agent-workbench/internal/tools"
)

type stubProfiles struct {
	profile servers.Profile
}

func (s stubProfiles) GetServerProfile(context.Context, string) (servers.Profile, error) {
	return s.profile, nil
}

type stubRunner struct{}

func (stubRunner) Probe(context.Context, servers.Profile, string) (servers.ProbeResult, error) {
	return servers.ProbeResult{Message: "ok"}, nil
}
func (stubRunner) List(context.Context, servers.Profile, string, string) ([]string, string, error) {
	return []string{"drwxr-xr-x 2 root root 4096 ."}, "drwxr-xr-x 2 root root 4096 .\n", nil
}
func (stubRunner) Read(context.Context, servers.Profile, string, string) (string, bool, error) {
	return "preview", false, nil
}
func (stubRunner) Exec(context.Context, servers.Profile, string, string, time.Duration) (string, string, int, error) {
	return "pong\n", "", 0, nil
}

func TestSSHToolsRespectNetworkDeny(t *testing.T) {
	cfg := tools.SSHToolConfig{
		Profiles: stubProfiles{profile: servers.Profile{
			ID: "server-1", Host: "prod.example", User: "deploy", Port: 22, AuthMethod: servers.AuthAgent,
		}},
		Runner:        stubRunner{},
		NetworkPolicy: "DENY",
	}
	result := tools.SSHTestConnection{Config: cfg}.Execute(context.Background(), json.RawMessage(`{"profileId":"server-1","reason":"check"}`))
	if result.OK || result.Error == nil || result.Error.Code != "network_denied" {
		t.Fatalf("expected network_denied, got %#v", result)
	}
	cfg.AllowedNetworkHosts = []string{"prod.example"}
	result = tools.SSHTestConnection{Config: cfg}.Execute(context.Background(), json.RawMessage(`{"profileId":"server-1","reason":"check"}`))
	if !result.OK {
		t.Fatalf("expected ok after allowlist: %#v", result)
	}
}

func TestSSHExecRequiresApprovalPolicy(t *testing.T) {
	profile := domain.AgentProfile{AllowedTools: []string{"ssh_exec_remote"}, ToolPolicies: map[string]string{"ssh_exec_remote": "ASK"}}
	decision := policy.Engine{}.Evaluate(profile, "ssh_exec_remote")
	if decision.Denied || !decision.RequiresApproval {
		t.Fatalf("ssh_exec_remote must ask: %#v", decision)
	}
	denied := policy.Engine{}.Evaluate(domain.AgentProfile{}, "ssh_exec_remote")
	if !denied.Denied {
		t.Fatalf("default policy must DENY ssh tools: %#v", denied)
	}
}

func TestSSHPasswordAuthRejectedForAgent(t *testing.T) {
	cfg := tools.SSHToolConfig{
		Profiles: stubProfiles{profile: servers.Profile{
			ID: "server-1", Host: "box", User: "u", Port: 22, AuthMethod: servers.AuthPassword,
		}},
		Runner:              stubRunner{},
		NetworkPolicy:       "ALLOW",
		AllowedNetworkHosts: nil,
	}
	result := tools.SSHListRemote{Config: cfg}.Execute(context.Background(), json.RawMessage(`{"profileId":"server-1","reason":"ls"}`))
	if result.OK || result.Error == nil || result.Error.Code != "auth_unsupported" {
		t.Fatalf("expected auth_unsupported, got %#v", result)
	}
}

func TestSSHReadRemoteReturnsBoundedPreview(t *testing.T) {
	cfg := tools.SSHToolConfig{
		Profiles: stubProfiles{profile: servers.Profile{
			ID: "server-1", Host: "box", User: "u", Port: 22, AuthMethod: servers.AuthAgent,
		}},
		Runner: stubRunner{}, NetworkPolicy: "ALLOW",
	}
	result := tools.SSHReadRemote{Config: cfg}.Execute(context.Background(), json.RawMessage(`{"profileId":"server-1","path":"/srv/app/main.go","reason":"inspect"}`))
	if !result.OK {
		t.Fatalf("expected preview, got %#v", result)
	}
	var data map[string]any
	if err := json.Unmarshal(result.Output, &data); err != nil || data["content"] != "preview" || data["path"] != "/srv/app/main.go" {
		t.Fatalf("unexpected preview payload %s: %v", result.Output, err)
	}
	decision := policy.Engine{}.Evaluate(domain.AgentProfile{
		AllowedTools: []string{"ssh_read_remote"}, ToolPolicies: map[string]string{"ssh_read_remote": "ASK"},
	}, "ssh_read_remote")
	if decision.Denied || !decision.RequiresApproval {
		t.Fatalf("ssh_read_remote must ask before network access: %#v", decision)
	}
}
