package tools

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestValidateDockerRef(t *testing.T) {
	if err := ValidateDockerRef("web_1"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDockerRef("/web_1"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDockerRef("../evil"); err == nil {
		t.Fatal("expected path traversal reject")
	}
	if err := ValidateDockerRef("a;rm -rf /"); err == nil {
		t.Fatal("expected shell metachar reject")
	}
	if err := ValidateDockerRef(""); err == nil {
		t.Fatal("expected empty reject")
	}
}

func TestDockerInspectMissingCLI(t *testing.T) {
	tool := DockerInspect{CLI: DockerCLI{LookPath: func(string) (string, error) {
		return "", exec.ErrNotFound
	}}}
	result := tool.Execute(context.Background(), json.RawMessage(`{"action":"status"}`))
	if !result.OK {
		t.Fatalf("status without CLI should still OK with available=false: %#v", result)
	}
	var payload map[string]any
	if err := json.Unmarshal(result.Output, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["available"] != false {
		t.Fatalf("payload=%#v", payload)
	}
}

func TestDockerInspectRejectsBadLogsArgs(t *testing.T) {
	tool := DockerInspect{}
	if invalid := tool.ValidateArguments(json.RawMessage(`{"action":"logs"}`)); invalid == nil {
		t.Fatal("logs without container must fail validation")
	}
	if invalid := tool.ValidateArguments(json.RawMessage(`{"action":"logs","container":"good-name"}`)); invalid != nil {
		t.Fatalf("valid logs args rejected: %#v", invalid)
	}
}

func TestDockerControlRejectsRemoveAndRequiresReason(t *testing.T) {
	tool := DockerControl{}
	if invalid := tool.ValidateArguments(json.RawMessage(`{"action":"rm","container":"x","reason":"no"}`)); invalid == nil {
		t.Fatal("rm must be rejected")
	}
	if invalid := tool.ValidateArguments(json.RawMessage(`{"action":"start","container":"web","reason":""}`)); invalid == nil {
		t.Fatal("empty reason must be rejected")
	}
	if invalid := tool.ValidateArguments(json.RawMessage(`{"action":"stop","container":"web","reason":"pause stack"}`)); invalid != nil {
		t.Fatalf("valid stop rejected: %#v", invalid)
	}
}

func TestDockerControlApprovalPreview(t *testing.T) {
	tool := DockerControl{}
	preview := tool.ApprovalArguments(json.RawMessage(`{"action":"start","container":"api","reason":"bring up"}`))
	var payload map[string]any
	if err := json.Unmarshal(preview, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["command"] != "docker start api" {
		t.Fatalf("preview=%#v", payload)
	}
}

func TestParseDockerJSONLines(t *testing.T) {
	items := parseDockerJSONLines("{\"ID\":\"abc\",\"Names\":\"web\"}\n{\"ID\":\"def\"}\n")
	if len(items) != 2 || items[0]["Names"] != "web" {
		t.Fatalf("items=%#v", items)
	}
}

func TestDockerInspectPsAndControlWithRunner(t *testing.T) {
	var seen []string
	cli := DockerCLI{Runner: func(_ context.Context, args ...string) (dockerRunResult, error) {
		seen = append(seen, strings.Join(args, " "))
		switch args[0] {
		case "ps":
			return dockerRunResult{Stdout: `{"ID":"c1","Names":"web","Status":"Up"}` + "\n"}, nil
		case "start", "stop":
			return dockerRunResult{Stdout: args[1] + "\n", ExitCode: 0}, nil
		default:
			return dockerRunResult{ExitCode: 1, Stderr: "unexpected"}, nil
		}
	}}
	inspect := DockerInspect{CLI: cli}
	result := inspect.Execute(context.Background(), json.RawMessage(`{"action":"ps","all":true}`))
	if !result.OK {
		t.Fatalf("ps failed: %#v", result)
	}
	var payload map[string]any
	if err := json.Unmarshal(result.Output, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["count"] != float64(1) {
		t.Fatalf("payload=%#v", payload)
	}
	control := DockerControl{CLI: cli}
	started := control.Execute(context.Background(), json.RawMessage(`{"action":"start","container":"web","reason":"tests"}`))
	if !started.OK {
		t.Fatalf("start failed: %#v", started)
	}
	if len(seen) < 2 || !strings.Contains(seen[0], "ps") || seen[1] != "start web" {
		t.Fatalf("seen=%#v", seen)
	}
}
