package sandbox_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"local-agent-workbench/internal/egress"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
)

func TestDockerOneCommandEgressGrantIntegration(t *testing.T) {
	if os.Getenv("POINT_SANDBOX_DOCKER_TEST") != "1" {
		t.Skip("set POINT_SANDBOX_DOCKER_TEST=1 with the sandbox image available")
	}
	fs, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	backend := sandbox.NewContainerBackend(filepath.Join(t.TempDir(), "sandboxes"))
	if image := strings.TrimSpace(os.Getenv("POINT_SANDBOX_IMAGE")); image != "" {
		backend.Image = image
	}
	if err := backend.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	grants := tools.NewNetworkGrantBook()
	tool := tools.RunCommand{FS: fs, Executor: backend, NetworkPolicy: "DENY", Grants: grants, RunID: "integration-once", QuestID: "integration-quest"}
	payload, _ := json.Marshal(map[string]any{
		"command": `python3 -c "import urllib.request; r=urllib.request.urlopen('https://registry.npmjs.org/-/ping', timeout=15); assert 200 <= r.status < 400"`,
		"reason":  "verify one-command TLS grant", "timeoutSeconds": 30,
	})
	before := tool.Execute(context.Background(), payload)
	if before.OK || before.Error == nil || before.Error.Code != "network_denied" || before.Error.Target != "registry.npmjs.org" {
		t.Fatalf("missing exact-host denial: %#v", before)
	}
	grants.GrantHostOnce("integration-once", before.Error.Target)
	allowed := tool.Execute(context.Background(), payload)
	if !allowed.OK {
		t.Fatalf("approved command failed: %#v", allowed)
	}
	var evidence struct {
		ExitCode      int                      `json:"exitCode"`
		PolicyDigest  string                   `json:"networkPolicyDigest"`
		GrantScope    string                   `json:"networkGrantScope"`
		GatewayStatus string                   `json:"networkGatewayStatus"`
		Decisions     []sandbox.EgressDecision `json:"networkGatewayDecisions"`
	}
	if err := json.Unmarshal(allowed.Output, &evidence); err != nil {
		t.Fatal(err)
	}
	policy, err := egress.Compile("DENY", []string{"registry.npmjs.org"}, egress.Quota{})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.ExitCode != 0 || evidence.GrantScope != "once" || evidence.PolicyDigest != policy.Digest || evidence.GatewayStatus != "recorded" {
		t.Fatalf("tool evidence does not match the approved gateway policy: %#v", evidence)
	}
	found := false
	for _, decision := range evidence.Decisions {
		if decision.FQDN == "registry.npmjs.org" && decision.Port == 443 && decision.Decision == "allowed" && decision.PolicyDigest == policy.Digest {
			found = true
		}
	}
	if !found {
		t.Fatalf("no matching gateway allow decision: %#v", evidence.Decisions)
	}
	after := tool.Execute(context.Background(), payload)
	if after.OK || after.Error == nil || after.Error.Code != "network_denied" {
		t.Fatalf("one-command grant remained available: %#v", after)
	}
}
