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
		switch withoutComposeProject(command) {
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
	stack := "docker compose -p point-verify "
	if strings.Join(runner.commands, " | ") != stack+"down -v --remove-orphans | "+stack+"stop postgres | "+stack+"start postgres | "+stack+"down -v --remove-orphans" {
		t.Fatalf("outage must stop and then restore the service on a clean stack: %v", runner.commands)
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
	case withoutComposeProject(command) == "docker compose up -d":
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
	// A port held by another stack is the environment's problem: another
	// attempt by the same executor would only repeat it.
	if repairable := repairableHostFailuresV2(checks); len(repairable) != 0 {
		t.Fatalf("an environment failure was offered for repair: %#v", repairable)
	}
}

// withoutComposeProject strips the quest's own project from a scoped Compose
// command, so fakes can answer the approved command they know.
func withoutComposeProject(command string) string {
	if rest, ok := strings.CutPrefix(command, "docker compose -p "); ok {
		if _, tail, found := strings.Cut(rest, " "); found {
			return "docker compose " + tail
		}
	}
	return command
}

type staleVolumeRunner struct{ commands []string }

func (runner *staleVolumeRunner) Run(_ context.Context, _, command string) (int, string, error) {
	runner.commands = append(runner.commands, command)
	switch withoutComposeProject(command) {
	case "docker compose up -d":
		return 1, "Container app-1  Error\ndependency failed to start: container postgres-1 exited (1)", nil
	case "docker compose logs --no-color --tail=40":
		return 0, "postgres-1  | 2026-09-26 06:36:44.488 UTC [142] FATAL:  role \"postgres\" does not exist\n" +
			"postgres-1  | 2026-09-26 06:36:45.488 UTC [143] FATAL:  role \"postgres\" does not exist\n" +
			"app-1  | 2026/09/26 06:36:40 listening on :8080\n", nil
	}
	if strings.Contains(command, " ps -a ") {
		return 0, "postgres: exited Exited (1)\napp: running Up 5 seconds", nil
	}
	return 0, "", nil
}

// Live run 26.09: the default Compose project is the folder name, so the
// stack of an earlier quest was recreated in place and postgres kept its
// foreign volume. The quest's own project starts clean, is torn down after,
// and a failure carries what the containers said.
func TestHostChecksRunOnACleanQuestStackAndKeepContainerLogs(t *testing.T) {
	order, bundle := hostCheckOrderV2(t, map[string]any{"stack-up": map[string]any{"command": "docker compose up -d"}})
	bundle.QuestID = "quest_ae4f2b5a598e036e141070a9"
	runner := &staleVolumeRunner{}
	checks := (&App{completionCheckRunner: runner}).runDeferredComposeCriteriaV2(context.Background(), order, t.TempDir(), &bundle)
	if len(checks) != 1 || checks[0].Satisfied || checks[0].ExitCode == nil {
		t.Fatalf("failed stack must fail its criterion: %#v", checks)
	}
	stack := "docker compose -p point-ae4f2b5a598e036e "
	if len(runner.commands) < 3 || runner.commands[0] != stack+"down -v --remove-orphans" ||
		runner.commands[len(runner.commands)-1] != stack+"down -v --remove-orphans" {
		t.Fatalf("the quest stack must start clean and be torn down: %v", runner.commands)
	}
	for _, command := range runner.commands {
		if !strings.HasPrefix(command, stack) {
			t.Fatalf("a Compose command escaped the quest project: %q", command)
		}
	}
	if !strings.Contains(bundle.HostDiagnostics, `role "postgres" does not exist`) || !strings.Contains(bundle.HostDiagnostics, "postgres: exited") {
		t.Fatalf("container logs were not kept: %q", bundle.HostDiagnostics)
	}
	highlights := hostDiagnosticHighlightsV2(bundle.HostDiagnostics, 4)
	if len(highlights) != 1 || !strings.Contains(highlights[0], `role "postgres" does not exist`) {
		t.Fatalf("the card must show the one line that explains the failure once: %v", highlights)
	}
	if repairable := repairableHostFailuresV2(checks); len(repairable) != 1 {
		t.Fatalf("an observed failure must be offered for repair: %#v", repairable)
	}
}
