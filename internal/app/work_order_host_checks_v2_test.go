package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func hostCheckOrderV2(t *testing.T, criteria map[string]any) (domain.WorkOrder, domain.EvidenceBundle) {
	t.Helper()
	order := domain.WorkOrder{}
	bundle := domain.EvidenceBundle{}
	for id, args := range criteria {
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		order.Criteria = append(order.Criteria, domain.AcceptanceCriterion{ID: id, Kind: "verification", Tool: "run_command", Text: id, Arguments: raw})
		bundle.Criteria = append(bundle.Criteria, domain.CriterionEvidence{CriterionID: id})
		bundle.VerificationChecks = append(bundle.VerificationChecks, domain.VerificationCheck{ID: id, Kind: "acceptance"})
	}
	return order, bundle
}

// The first live Go quest passed "status=ok" while the service reported
// db:"down" against a healthy database. A named field must be observed.
func TestHostHealthCheckObservesNamedJSONFields(t *testing.T) {
	var dbUp atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		db := "down"
		if dbUp.Load() {
			db = "up"
		}
		_, _ = w.Write([]byte(`{"status":"ok","db":"` + db + `"}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	code, summary, err := managedHTTPExpectV2(ctx, server.URL+"/health", 200, map[string]any{"db": "up"})
	if err != nil || code == 0 || !strings.Contains(summary, "db=down, ждали up") || !strings.Contains(summary, `"db":"down"`) {
		t.Fatalf("db:down must fail with the observed body: code=%d err=%v summary=%q", code, err, summary)
	}

	dbUp.Store(true)
	order, bundle := hostCheckOrderV2(t, map[string]any{
		"health-db-up": map[string]any{"command": "curl -sf " + server.URL + "/health", "expectJson": map[string]any{"status": "ok", "db": "up"}},
	})
	runner := &scriptedCompletionRunner{results: map[string]int{}}
	checks := (&App{completionCheckRunner: runner}).runDeferredComposeCriteriaV2(context.Background(), order, t.TempDir(), &bundle)
	if len(checks) != 1 || !checks[0].Satisfied || len(runner.commands) != 0 {
		t.Fatalf("health with db:up was not verified by a probe: checks=%#v commands=%v", checks, runner.commands)
	}
}

// "БД упала" used to be a manual criterion because only the 503 + /live
// scenario of an older acceptance run was supported. Any status and fields
// can be proven now, and the service is always brought back.
func TestHostOutageCriterionProvesDegradedAnswerAndRestores(t *testing.T) {
	var stopped atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		db := "up"
		if stopped.Load() {
			db = "down"
		}
		_, _ = w.Write([]byte(`{"status":"ok","db":"` + db + `"}`))
	}))
	defer server.Close()
	order, bundle := hostCheckOrderV2(t, map[string]any{
		"db-down": map[string]any{
			"command": "docker compose stop postgres", "url": server.URL + "/health",
			"expectStatus": 200, "expectJson": map[string]any{"db": "down"}, "recoverJson": map[string]any{"db": "up"},
		},
	})
	runner := &scriptedCompletionRunner{results: map[string]int{}}
	runner.onRun = func(command string) {
		switch command {
		case "docker compose stop postgres":
			stopped.Store(true)
		case "docker compose start postgres":
			stopped.Store(false)
		}
	}
	checks := (&App{completionCheckRunner: runner}).runDeferredComposeCriteriaV2(context.Background(), order, t.TempDir(), &bundle)
	if len(checks) != 1 || !checks[0].Satisfied || checks[0].ExitCode == nil {
		t.Fatalf("outage criterion was not proven: %#v", checks)
	}
	if strings.Join(runner.commands, " | ") != "docker compose stop postgres | docker compose start postgres" {
		t.Fatalf("outage must stop and then restore the service: %v", runner.commands)
	}
	if stopped.Load() {
		t.Fatal("the service was left stopped after verification")
	}
}

func TestHostOutageRejectsShellAndForeignURLs(t *testing.T) {
	order := domain.WorkOrder{Delivery: domain.DeliveryPolicy{ApplicationURL: "http://localhost:8080"}}
	for _, args := range []map[string]any{
		{"command": "docker compose stop postgres && rm -rf /", "url": "http://localhost:8080/health"},
		{"command": "docker compose stop postgres", "url": "http://example.com/health"},
		{"command": "docker compose stop postgres", "url": "http://localhost:9090/health"},
		{"command": "curl -sf http://localhost:8080/health; rm -rf /"},
	} {
		raw, _ := json.Marshal(args)
		if check := managedHostCheckFromArgsV2(order, raw); check.Kind != "" {
			t.Fatalf("unsafe host check accepted: %v -> %#v", args, check)
		}
	}
}

type portConflictRunner struct{ commands []string }

func (runner *portConflictRunner) Run(_ context.Context, _, command string) (int, string, error) {
	runner.commands = append(runner.commands, command)
	switch {
	case command == "docker compose up -d":
		return 1, "Container systemio-app-1  Starting\nError response from daemon: driver failed programming external connectivity on endpoint systemio-app-1: Bind for 0.0.0.0:8080 failed: port is already allocated", nil
	case strings.HasPrefix(command, "docker ps --filter publish=8080"):
		return 0, "other-app-1 com.docker.compose.project=other,com.docker.compose.project.working_dir=C:\\work\\other\n", nil
	}
	return 0, "", nil
}

// A busy port used to end as "устраните указанную ошибку". The card has to
// name the holder and what to do.
func TestStackUpPortConflictNamesTheHolder(t *testing.T) {
	order, bundle := hostCheckOrderV2(t, map[string]any{"compose-up": map[string]any{"command": "docker compose up -d"}})
	runner := &portConflictRunner{}
	checks := (&App{completionCheckRunner: runner}).runDeferredComposeCriteriaV2(context.Background(), order, t.TempDir(), &bundle)
	if len(checks) != 1 || checks[0].Satisfied {
		t.Fatalf("port conflict must fail the criterion: %#v", checks)
	}
	action := hostCheckActionV2(checks[0].Summary)
	for _, want := range []string{"порт 8080", "other-app-1", "Compose-проект other", `C:\work\other`, "docker stop other-app-1"} {
		if !strings.Contains(action, want) {
			t.Fatalf("action %q lacks %q (summary %q)", action, want, checks[0].Summary)
		}
	}
	failed := failedHostCriteriaV2(checks, order)
	if len(failed) != 1 || !strings.Contains(failed[0], "port is already allocated") || strings.Contains(failed[0], "Нужно действие") {
		t.Fatalf("limitation must carry the reason, not the action: %v", failed)
	}
}
