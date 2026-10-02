package acceptance_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/connections"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/filepolicy"
	"local-agent-workbench/internal/osproc"
	"local-agent-workbench/internal/sandbox"
)

type measuredModelConn struct {
	net.Conn
	bytes *atomic.Int64
}

func (c measuredModelConn) Read(p []byte) (int, error) {
	n, e := c.Conn.Read(p)
	c.bytes.Add(int64(n))
	return n, e
}
func (c measuredModelConn) Write(p []byte) (int, error) {
	n, e := c.Conn.Write(p)
	c.bytes.Add(int64(n))
	return n, e
}

func matrixDigest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func matrixJSON(t *testing.T, root, name string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, name)
	temporary := file + ".tmp"
	if err = os.WriteFile(temporary, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(temporary, file); err != nil {
		t.Fatal(err)
	}
}
func matrixGate(t *testing.T, root, name string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("measurement coordinator did not release %s", name)
}

func matrixModelProxy(upstream *url.URL, counter *atomic.Int64) (*httputil.ReverseProxy, *http.Transport) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		c, e := dialer.DialContext(ctx, network, address)
		if e != nil {
			return nil, e
		}
		return measuredModelConn{c, counter}, nil
	}
	// Providers already prepend /v1. Do not join that prefix a second time.
	proxyTarget := *upstream
	proxyTarget.Path, proxyTarget.RawPath = "", ""
	proxy := httputil.NewSingleHostReverseProxy(&proxyTarget)
	director := proxy.Director
	proxy.Director = func(request *http.Request) { director(request); request.Host = upstream.Host }
	proxy.Transport = transport
	proxy.ModifyResponse = func(response *http.Response) error {
		if response.StatusCode >= 300 && response.StatusCode < 400 {
			return fmt.Errorf("pinned model endpoint returned a redirect; route remains unchanged")
		}
		return nil
	}
	proxy.ErrorLog = log.New(io.Discard, "", 0)
	proxy.FlushInterval = -1
	return proxy, transport
}

// One child performs one complete quest. The coordinator alternates engines,
// records whole-environment counters and preserves failed trials, without
// opening an existing user workspace or database.
func TestRuntimeMatrixLiveQuest(t *testing.T) {
	if os.Getenv("POINT_RUNTIME_MATRIX") != "1" {
		t.Skip("explicit full matrix opt-in required")
	}
	dir := os.Getenv("POINT_BENCH_TRIAL_DIR")
	if !filepath.IsAbs(dir) {
		t.Fatal("absolute isolated trial directory required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	matrixJSON(t, dir, "started.json", map[string]any{"pid": os.Getpid()})
	matrixGate(t, dir, "start.gate")
	stack := os.Getenv("POINT_BENCH_PROJECT")
	fixture, err := matrixFixture(stack)
	if err != nil {
		t.Fatal(err)
	}
	key := os.Getenv("POINT_LLMUX_API_KEY")
	route := os.Getenv("POINT_LLMUX_BASE_URL")
	if key == "" || route == "" {
		t.Fatal("pinned route and model credentials required in environment")
	}
	model := "Qwen3.8-27B"
	for name, value := range map[string]string{"POINT_AGENT_HUB_V2": "1", "POINT_DEFAULT_MODEL": model, "POINT_LIVE_WORKSPACE": "0", "POINT_FILE_ISOLATION": "sandbox", "REDIS_ADDR": ""} {
		t.Setenv(name, value)
	}
	project := filepath.Join(dir, "project")
	if err = os.Mkdir(project, 0755); err != nil {
		t.Fatal(err)
	}
	for name, body := range fixture.Files {
		if err = os.WriteFile(filepath.Join(project, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "--quiet"}, {"add", "-A"}, {"-c", "user.email=matrix@point.local", "-c", "user.name=Point Runtime Matrix", "commit", "--quiet", "--no-gpg-sign", "-m", "immutable fixture"}} {
		if out, e := osproc.Command("git", append([]string{"-C", project}, args...)...).CombinedOutput(); e != nil {
			t.Fatalf("fixture git: %v %s", e, out)
		}
	}
	upstream, err := url.Parse(route)
	if err != nil || upstream.Host == "" {
		t.Fatal("invalid pinned model route")
	}
	var modelBytes atomic.Int64
	proxy, transport := matrixModelProxy(upstream, &modelBytes)
	server := httptest.NewServer(proxy)
	defer server.Close()
	defer transport.CloseIdleConnections()
	data := filepath.Join(dir, "data")
	application, err := app.New(data)
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(context.Background())
	view, err := application.OpenWorkspace(project)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := application.SaveConnection(connections.UpsertRequest{ID: "matrix-model", Provider: domain.ProviderOpenAI, PresetID: "llmux", DisplayName: "Pinned matrix model", BaseURL: server.URL, SecretRef: "secret:matrix", DefaultModel: model})
	if err != nil {
		t.Fatal(err)
	}
	connection, err = application.ProbeConnection(connection.ID, key)
	if err != nil || connection.Status != domain.ConnectionConnected {
		t.Fatalf("pinned model connection probe failed: %v", err)
	}
	if err = application.SetDefaultConnection(connection.ID); err != nil {
		t.Fatal(err)
	}
	if err = wireLoopProfile(application, connection, model, server.URL); err != nil {
		t.Fatal(err)
	}
	if err = wireMasterToConnection(application, connection, model); err != nil {
		t.Fatal(err)
	}
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{Name: "Matrix implementation", RoleDescription: "Implement only the approved source file. Point performs independent checks.", ConnectionID: connection.ID, PrimaryModel: model, AllowedTools: []string{"project_map", "search_code", "list_files", "read_file", "search_text", "propose_patch", "run_command", "git_diff"}, MaxOutputTokens: 16384, ContextWindowTokens: 131072, ReasoningEffort: "low", MaxSteps: 25, MaxDurationSeconds: 900, ApprovalMode: domain.ApprovalSafe})
	if err != nil {
		t.Fatal(err)
	}
	exit := 0
	arguments, _ := json.Marshal(map[string]any{"command": fixture.Command, "timeoutSeconds": 600})
	criteria := []domain.AcceptanceCriterion{{ID: "tests", Kind: "verification", Text: "Pinned original tests pass", Tool: "run_command", Arguments: arguments, ExpectedExitCode: &exit, Deterministic: true}, {ID: "manual-contract", Kind: "manual", Text: "User reviews the product-code API contract; leave this criterion pending"}}
	goal := fmt.Sprintf("Implement parseCodes (Go: ParseCodes) in %s only. Split comma-separated product codes; trim surrounding whitespace; ignore empty entries; uppercase ASCII letters; reject codes containing anything outside [A-Z0-9-] (Go returns an error, PHP throws InvalidArgumentException, TypeScript throws Error); deduplicate and sort ascending. Empty input returns an empty list. Preserve every test, manifest and lockfile. Point runs the approved independent test suite. Do not accept the manual criterion.", fixture.Source)
	order := domain.NormalizeWorkOrder(domain.WorkOrder{State: "ready", Goal: goal, Scope: []string{fixture.Source}, OutOfScope: []string{"Tests, manifests, lockfiles, configuration, new files"}, Criteria: criteria, WorkspaceID: view.Workspace.ID, Workspace: domain.WorkspacePlan{Mode: "existing", Path: project, Isolation: "snapshot"}, Stack: domain.StackPresetRef{ID: "matrix-" + stack, Version: "1", Category: stack, Source: "benchmark"}, Sandbox: domain.RuntimeSpec{Kind: "managed", Image: os.Getenv("POINT_BENCH_IMAGE"), ImageDigest: os.Getenv("POINT_BENCH_EXECUTION_IMAGE_DIGEST")}, Dependencies: fixture.Dependencies, Roster: domain.AgentRosterPlan{Permanent: []domain.AgentDraft{{ID: agent.ID, Existing: true}}}, Routing: domain.ModelRoutingPolicy{Mode: "fixed", FixedConnectionID: connection.ID, FixedModel: model, FallbackMode: "wait"}, Budget: domain.BudgetEnvelope{Preset: "medium", Tokens: 200000, ActiveSeconds: 1800, MaxParallel: 1, MaxAttempts: 1}, Delivery: domain.DeliveryPolicy{ApplyMode: "automatic", CommitMode: "none", KeepPartialDays: 30}, Completion: domain.CompletionProfile{ID: "matrix", Version: "1", Checks: []domain.CompletionCheck{{Kind: domain.CompletionCheckAcceptance}, {Kind: "automated_tests", Command: fixture.Command}}}})
	for _, host := range fixture.Hosts {
		order.Network = append(order.Network, domain.NetworkGrant{Host: host, Purpose: "Pinned dependency installation"})
	}
	order, err = application.SaveWorkOrderV2(context.Background(), order)
	if err != nil {
		t.Fatal(err)
	}
	state, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if state.Sandbox.Unavailable != "" {
		t.Fatal(state.Sandbox.Unavailable)
	}
	pins := map[string]string{"sourceDigest": matrixDigest(fixture.Files), "criteriaDigest": matrixDigest(criteria), "commandsDigest": matrixDigest(map[string]string{"command": fixture.Command, "probeCommand": fixture.ProbeCommand, "probeSource": fixture.Probe, "goal": goal}), "dependencyPlanDigest": matrixDigest(fixture.Dependencies), "imageDigest": os.Getenv("POINT_BENCH_IMAGE_CONTENT_DIGEST"), "environmentDigest": matrixDigest(map[string]any{"model": model, "context": 131072, "output": 16384, "reasoning": "low", "maxSteps": 25, "fixtureVersion": matrixFixtureVersion, "route": route}), "filePolicyDigest": matrixDigest(filepolicy.Current), "networkPolicyDigest": matrixDigest(order.Network), "resourceLimitsDigest": matrixDigest(map[string]any{"memory": "2g", "cpu": "2", "pids": 256, "user": "10001:10001"}), "securityProfile": sandbox.SecurityProfileVersion}
	matrixJSON(t, dir, "prepared.json", map[string]any{"order": order, "pins": pins, "runtime": state.Sandbox, "route": route, "project": stack})
	matrixGate(t, dir, "approve.gate")
	approvedAt := time.Now().UTC()
	networkBefore := modelBytes.Load()
	approval, err := application.ApproveWorkOrderV2(context.Background(), order.ID, app.ApproveWorkOrderV2Request{Version: order.Version, Digest: domain.WorkOrderDigest(order), IdempotencyKey: "matrix", APIKey: key})
	if err != nil {
		t.Fatal(err)
	}
	matrixJSON(t, dir, "approved.json", map[string]any{"approvedAt": approvedAt, "questId": approval.QuestID})
	var quest domain.Quest
	var bundle domain.EvidenceBundle
	for time.Now().Before(approvedAt.Add(30 * time.Minute)) {
		autoApprovePendingTools(t, application, view.Workspace.ID, fixture.Hosts, nil)
		quest, err = application.WorkOrderQuestV2(context.Background(), approval.QuestID)
		if err != nil {
			t.Fatal(err)
		}
		bundle, err = application.EvidenceBundle(context.Background(), quest.ID)
		if err == nil && bundle.DeliveryReceipt != nil && bundle.DeliveryVerified && (domain.IsTerminalQuestStatus(quest.Status) || quest.Status == domain.QuestNeedsReview) {
			break
		}
		if quest.Status == domain.QuestBlocked || quest.Status == domain.QuestCancelled {
			break
		}
		time.Sleep(time.Second)
	}
	deliveredAt := time.Now().UTC()
	if bundle.DeliveryReceipt != nil {
		deliveredAt = bundle.DeliveryReceipt.DeliveredAt
	}
	matrixJSON(t, dir, "receipt.json", map[string]any{"approvedAt": approvedAt, "deliveredAt": deliveredAt, "modelNetworkBytes": modelBytes.Load() - networkBefore, "status": quest.Status, "receipt": bundle.DeliveryReceipt})
	matrixGate(t, dir, "inspect.gate")
	state, err = application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	quality := map[string]bool{"runtimeSecurity": os.Getenv("POINT_BENCH_SECURITY_ATTESTED") == "1", "mandatoryTests": false, "independentBehavior": false, "changeScope": true, "completeAudit": true, "correctVerdicts": quest.Status == domain.QuestNeedsReview, "deliveryVerified": bundle.DeliveryVerified && bundle.DeliveryReceipt != nil, "manualCriteriaPreserved": false}
	for _, criterion := range bundle.Criteria {
		if criterion.CriterionID == "tests" {
			quality["mandatoryTests"] = criterion.Satisfied && criterion.ExitCode != nil && *criterion.ExitCode == 0
		}
		if criterion.CriterionID == "manual-contract" {
			quality["manualCriteriaPreserved"] = !criterion.Satisfied && criterion.Review == ""
		}
	}
	for _, check := range bundle.VerificationChecks {
		if check.Kind == domain.CompletionCheckAcceptance {
			continue
		}
		if !check.Satisfied || check.ExitCode == nil || *check.ExitCode != 0 {
			quality["mandatoryTests"] = false
		}
	}
	for name, body := range fixture.Files {
		if name == fixture.Source {
			continue
		}
		raw, e := os.ReadFile(filepath.Join(project, name))
		if e != nil || string(raw) != body {
			quality["changeScope"] = false
		}
	}
	out, e := osproc.Command("git", "-C", project, "status", "--porcelain", "--untracked-files=all").CombinedOutput()
	if e != nil {
		quality["changeScope"] = false
	} else {
		for _, line := range strings.Split(strings.TrimRight(string(out), "\r\n"), "\n") {
			if len(line) > 3 && strings.TrimSpace(line[3:]) != fixture.Source {
				quality["changeScope"] = false
			}
		}
	}
	var details []app.RunDetails
	audits := 0
	for _, run := range state.Runs {
		detail, e := application.RunDetails(run.ID)
		if e != nil {
			quality["completeAudit"] = false
			continue
		}
		details = append(details, detail)
		audits += detail.Diagnostics.Workspace.Audits
		if detail.Diagnostics.Workspace.IncompleteAudits != 0 {
			quality["completeAudit"] = false
		}
	}
	quality["completeAudit"] = quality["completeAudit"] && audits > 0 && quality["deliveryVerified"]
	for _, call := range bundle.ModelCalls {
		if call.Model != model || call.FallbackReason != "" {
			quality["correctVerdicts"] = false
		}
	}
	quality["correctVerdicts"] = quality["correctVerdicts"] && len(bundle.ModelCalls) > 0
	probeProof, probeErr := matrixIndependentProbe(t, dir, project, fixture)
	quality["independentBehavior"] = probeErr == nil
	matrixJSON(t, dir, "evidence.json", map[string]any{"workOrder": approval.WorkOrder, "quest": quest, "evidence": bundle, "flowRuns": state.FlowRuns, "runs": details, "independentProbe": probeProof, "probeError": fmt.Sprint(probeErr), "quality": quality})
	receiptID := ""
	if bundle.DeliveryReceipt != nil {
		receiptID = bundle.DeliveryReceipt.ID
	}
	matrixJSON(t, dir, "sample.json", map[string]any{"id": filepath.Base(dir), "configuration": os.Getenv("POINT_BENCH_CONFIGURATION"), "project": stack, "regime": os.Getenv("POINT_BENCH_REGIME"), "kind": "full_quest", "engine": strings.TrimPrefix(state.Sandbox.Backend, "embedded-"), "engineVersion": state.Sandbox.Version, "runtimeDigest": os.Getenv("POINT_BENCH_RUNTIME_DIGEST"), "storageMode": os.Getenv("POINT_SANDBOX_WORKSPACE"), "verifyMode": os.Getenv("POINT_VERIFY_SERVICE"), "model": model, "route": route, "fallback": false, "approvedAt": approvedAt, "deliveredAt": deliveredAt, "humanWaitMs": 0, "installationMs": 0, "receiptId": receiptID, "evidence": "evidence.json", "pins": pins, "quality": quality})
	for name, passed := range quality {
		if !passed {
			t.Errorf("full quest quality failed: %s", name)
		}
	}
}

func matrixIndependentProbe(t *testing.T, dir, project string, f runtimeMatrixFixture) (map[string]any, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	clean := filepath.Join(dir, "independent-source")
	if err := sandbox.CopyPortable(ctx, project, clean, filepolicy.Current); err != nil {
		return nil, err
	}
	name := "independent.mjs"
	if f.Source == "codes.go" {
		name = "independent_test.go"
	}
	if f.Source == "codes.php" {
		name = "independent.php"
	}
	if err := os.WriteFile(filepath.Join(clean, name), []byte(f.Probe), 0644); err != nil {
		return nil, err
	}
	b := sandbox.NewContainerBackend(filepath.Join(dir, "independent-sandboxes"))
	if os.Getenv("POINT_SANDBOX_BACKEND") == "embedded" {
		var err error
		b, err = sandbox.NewEmbeddedBackend(b.Root)
		if err != nil {
			return nil, err
		}
	}
	record, err := b.Create(ctx, sandbox.CreateRequest{WorkspaceID: "matrix-private", WorkspacePath: clean, ExecutionID: "independent", StorageMode: "volume", FileRulesVersion: filepolicy.Current, Image: os.Getenv("POINT_BENCH_IMAGE")})
	if err != nil {
		return nil, err
	}
	defer b.Close(context.Background(), record, clean)
	proof := map[string]any{"sandbox": record, "commands": []any{}}
	var commands []any
	run := func(command, policy string) error {
		var stdout, stderr bytes.Buffer
		outcome, e := b.RunVolumeProcess(ctx, sandbox.ProcessRequest{WorkspaceRoot: record.Path, WorkingDirectory: record.Path, ShellCommand: command, NetworkPolicy: policy, AllowedNetworkHosts: f.Hosts, RunID: "independent", CacheScope: matrixDigest(filepath.Clean(dir)) + "-independent", Authoritative: true}, &stdout, &stderr)
		commands = append(commands, map[string]any{"command": command, "outcome": outcome, "error": fmt.Sprint(e), "stdout": stdout.String(), "stderr": stderr.String()})
		proof["commands"] = commands
		if e != nil {
			return e
		}
		if outcome.ExitCode != 0 || outcome.AuditIncomplete || outcome.TimedOut {
			return fmt.Errorf("independent command failed: %+v", outcome)
		}
		return nil
	}
	for _, p := range f.Dependencies.Projects {
		for _, c := range p.Commands {
			if err = run(c.Command, "ALLOWLIST"); err != nil {
				return proof, err
			}
		}
	}
	if err = run(f.ProbeCommand, "DENY"); err != nil {
		return proof, err
	}
	status, err := b.CheckWorkspace(ctx, record)
	proof["integrity"] = status
	if err == nil && status.Incomplete {
		err = fmt.Errorf("independent audit incomplete")
	}
	return proof, err
}
