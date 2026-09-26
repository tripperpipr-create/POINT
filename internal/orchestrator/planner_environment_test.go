package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

type capturingPlannerModel struct {
	raw      string
	requests *[]providers.ModelRequest
}

func (m capturingPlannerModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	*m.requests = append(*m.requests, request)
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: m.raw})
}

func singleStagePlan(instruction string) string {
	plan := map[string]any{
		"agentIds": []string{"backend"}, "rationale": "One writer.", "requiresApproval": false,
		"stages": []map[string]any{{"name": "Implement", "agentId": "backend", "instruction": instruction, "phase": 1}},
	}
	raw, _ := json.Marshal(plan)
	return string(raw)
}

func environmentPlannerRequest() PlanRequest {
	request := testPlannerRequest()
	request.Config.TeamPreference = 20
	request.Proposal.Brief = &domain.TaskBrief{Goal: "Go-сервис с /health и PostgreSQL"}
	request.Environment = &ExecutionEnvironment{
		SandboxBackend: "docker", DockerInSandbox: false,
		NetworkHosts: []string{"proxy.golang.org", "sum.golang.org"}, HostVerifiedCriteria: []string{"compose-up", "health-ok"},
	}
	return request
}

func TestPlannerSeesExecutionEnvironmentAndRetryFeedback(t *testing.T) {
	var requests []providers.ModelRequest
	request := environmentPlannerRequest()
	request.RetryFeedback = "стадия 1 поручает Docker"
	planner := Planner{NewModel: func(providers.Config) (providers.Model, error) {
		return capturingPlannerModel{raw: singleStagePlan("Implement /health with pgx/v5 and run go test ./..."), requests: &requests}, nil
	}}
	if _, err := planner.Plan(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	payload := requests[0].Messages[1].Content
	for _, want := range []string{`"executionEnvironment"`, `"dockerInSandbox":false`, "proxy.golang.org", `"hostVerifiedCriteria":["compose-up","health-ok"]`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("planner payload lacks %s: %s", want, payload)
		}
	}
	last := requests[0].Messages[len(requests[0].Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "стадия 1 поручает Docker") {
		t.Fatalf("retry feedback did not reach the model: %#v", last)
	}
}

func TestPlannerRejectsDockerWorkInSandboxWithoutDocker(t *testing.T) {
	request := environmentPlannerRequest()
	for instruction, rejected := range map[string]bool{
		"Run docker compose build and docker compose up -d, then curl /health": true,
		"Start the stack with docker-compose up and verify":                     true,
		"Create Dockerfile, docker-compose.yml and .dockerignore; run go build": false,
	} {
		err := validateModelPlan(mustDecodePlan(t, singleStagePlan(instruction)), request, request.Agents)
		if rejected != (err != nil) {
			t.Fatalf("%q: rejected=%v err=%v", instruction, rejected, err)
		}
		if rejected && !strings.Contains(err.Error(), "compose-up, health-ok") {
			t.Fatalf("rejection must name the host-verified criteria: %v", err)
		}
	}
	request.Environment.DockerInSandbox = true
	if err := validateModelPlan(mustDecodePlan(t, singleStagePlan("Run docker compose up -d")), request, request.Agents); err != nil {
		t.Fatalf("Docker work must stay allowed where the sandbox has Docker: %v", err)
	}
}

// The first live Go quest planned "Use only the Go standard library because
// external module downloads are not permitted" and shipped a hand-written
// PostgreSQL wire protocol. Only the approved brief may ask for that.
func TestPlannerRejectsUnapprovedStdlibOnlyDowngrade(t *testing.T) {
	request := environmentPlannerRequest()
	degraded := "Create a minimal Go service. Use only the Go standard library because external module downloads are not permitted."
	if err := validateModelPlan(mustDecodePlan(t, singleStagePlan(degraded)), request, request.Agents); err == nil || !strings.Contains(err.Error(), "стандартной библиотекой") {
		t.Fatalf("stdlib-only downgrade was accepted: %v", err)
	}
	request.Proposal.Brief.Decisions = []domain.BriefDecision{{Topic: "Зависимости", Decision: "Только стандартная библиотека Go, без внешних зависимостей", Source: "user"}}
	if err := validateModelPlan(mustDecodePlan(t, singleStagePlan(degraded)), request, request.Agents); err != nil {
		t.Fatalf("a user-approved stdlib-only decision must be respected: %v", err)
	}
}

func mustDecodePlan(t *testing.T, raw string) ModelPlan {
	t.Helper()
	plan, err := decodeModelPlan(raw)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
