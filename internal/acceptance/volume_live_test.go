package acceptance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/connections"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/osproc"
)

// Explicitly opt in: these quests call the configured live model and retain
// isolated projects/databases for independently inspecting delivery and timing.
func TestVolumeLiveQuests(t *testing.T) {
	if os.Getenv("POINT_VOLUME_LIVE_QUESTS") != "1" {
		t.Skip("set POINT_VOLUME_LIVE_QUESTS=1 with the live-loop model credentials")
	}
	model := firstNonEmpty(os.Getenv("POINT_LIVE_LOOP_MODEL"), os.Getenv("POINT_V2_MVP_MODEL"), os.Getenv("POINT_ACCEPTANCE_MODEL"), "Qwen3.6-35B-A3B")
	base := firstNonEmpty(os.Getenv("POINT_LLMUX_BASE_URL"), os.Getenv("POINT_OPENAI_BASE_URL"), "https://llmux.ds3.centrofinans.ru/v1")
	key := firstNonEmpty(os.Getenv("POINT_LLMUX_API_KEY"), os.Getenv("POINT_OPENAI_API_KEY"), os.Getenv("OPENAI_API_KEY"))
	if key == "" {
		t.Fatal("live model credentials are required")
	}
	for name, value := range map[string]string{"POINT_AGENT_HUB_V2": "1", "REDIS_ADDR": "", "POINT_DEFAULT_MODEL": model, "POINT_SANDBOX_BACKEND": "docker", "POINT_SANDBOX_WORKSPACE": "volume", "POINT_LIVE_WORKSPACE": "0", "POINT_FILE_ISOLATION": "sandbox"} {
		t.Setenv(name, value)
	}
	baseDir := filepath.Join(repoRoot(t), "build", fmt.Sprintf("volume-live-%s", time.Now().UTC().Format("20060102-150405")))
	for _, stack := range []string{"go", "node"} {
		t.Run(stack, func(t *testing.T) { runVolumeLiveQuest(t, baseDir, stack, model, base, key) })
	}
}

func runVolumeLiveQuest(t *testing.T, baseDir, stack, model, base, key string) {
	ctx := context.Background()
	project := filepath.Join(baseDir, stack, "project")
	data := filepath.Join(baseDir, stack, "data")
	if err := os.MkdirAll(project, 0755); err != nil {
		t.Fatal(err)
	}
	command, task := "go test ./...", liveLoopTask
	if stack == "go" {
		if err := copyLoopTree(filepath.Join(repoRoot(t), "examples", "go-health"), project); err != nil {
			t.Fatal(err)
		}
	} else {
		command = "npm test"
		task = "Реализуй export function add(a, b) в arithmetic.js: возвращай сумму двух чисел. Добавь тесты положительных, отрицательных и дробных чисел в arithmetic.test.js. Не меняй package.json и команду npm test."
		files := map[string]string{"package.json": `{"name":"point-volume-live-node","version":"1.0.0","private":true,"type":"module","scripts":{"test":"node --test"}}`, "arithmetic.js": "export function add(a, b) { return 0 }\n", "arithmetic.test.js": "import test from 'node:test'; import assert from 'node:assert/strict'; import {add} from './arithmetic.js'; test('export', () => assert.equal(typeof add, 'function'));\n"}
		for path, body := range files {
			if err := os.WriteFile(filepath.Join(project, path), []byte(body), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, args := range [][]string{{"init", "--quiet"}, {"add", "-A"}, {"-c", "user.email=volume@point.local", "-c", "user.name=Point Volume Acceptance", "commit", "--quiet", "--no-gpg-sign", "-m", "baseline"}} {
		if output, err := osproc.Command("git", append([]string{"-C", project}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("initialize fixture: %v %s", err, output)
		}
	}
	application, err := app.New(data)
	if err != nil {
		t.Fatal(err)
	}
	defer application.Shutdown(ctx)
	view, err := application.OpenWorkspace(project)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := application.SaveConnection(connections.UpsertRequest{ID: "volume-live-connection", Provider: domain.ProviderOpenAI, PresetID: "llmux", DisplayName: "Volume live acceptance", BaseURL: base, SecretRef: "secret:volume-live", DefaultModel: model})
	if err != nil {
		t.Fatal(err)
	}
	if err = application.SetDefaultConnection(connection.ID); err != nil {
		t.Fatal(err)
	}
	if err = wireLoopProfile(application, connection, model, base); err != nil {
		t.Fatal(err)
	}
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{Name: "Volume acceptance", RoleDescription: "Implement and verify the approved small source change", ConnectionID: connection.ID, PrimaryModel: model, AllowedTools: []string{"project_map", "search_code", "list_files", "read_file", "search_text", "propose_patch", "run_command", "git_diff"}, MaxOutputTokens: 16384, ContextWindowTokens: 131072, ReasoningEffort: "low", MaxSteps: 25, MaxDurationSeconds: 600, ApprovalMode: domain.ApprovalSafe})
	if err != nil {
		t.Fatal(err)
	}
	exit := 0
	arguments, _ := json.Marshal(map[string]any{"command": command, "timeoutSeconds": 600})
	order := domain.NormalizeWorkOrder(domain.WorkOrder{State: "ready", Goal: task, Scope: []string{"Source implementation and tests"}, Criteria: []domain.AcceptanceCriterion{{ID: "done", Kind: "verification", Text: "Original test command passes", Tool: "run_command", Arguments: arguments, ExpectedExitCode: &exit}}, WorkspaceID: view.Workspace.ID, Workspace: domain.WorkspacePlan{Mode: "existing", Path: project, Isolation: "snapshot"}, Stack: domain.StackPresetRef{ID: "volume-" + stack, Version: "1", Category: stack, Source: "benchmark"}, Roster: domain.AgentRosterPlan{Permanent: []domain.AgentDraft{{ID: agent.ID, Existing: true}}}, Routing: domain.ModelRoutingPolicy{Mode: "fixed", FixedConnectionID: connection.ID, FixedModel: model, FallbackMode: "wait"}, Budget: domain.BudgetEnvelope{Preset: "medium", Tokens: 200000, ActiveSeconds: 1200, MaxParallel: 1, MaxAttempts: 1}, Delivery: domain.DeliveryPolicy{ApplyMode: "automatic", CommitMode: "none", KeepPartialDays: 30}, Completion: domain.CompletionProfile{ID: "volume-live-" + stack, Version: "1", Checks: []domain.CompletionCheck{{Kind: domain.CompletionCheckAcceptance}, {Kind: "automated_tests", Command: command}}}})
	order, err = application.SaveWorkOrderV2(ctx, order)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	approval, err := application.ApproveWorkOrderV2(ctx, order.ID, app.ApproveWorkOrderV2Request{Version: order.Version, Digest: domain.WorkOrderDigest(order), IdempotencyKey: "volume-live-" + stack, APIKey: key})
	if err != nil {
		t.Fatal(err)
	}
	var quest domain.Quest
	var bundle domain.EvidenceBundle
	for time.Now().Before(started.Add(25 * time.Minute)) {
		autoApprovePendingTools(t, application, view.Workspace.ID, nil, nil)
		quest, err = application.WorkOrderQuestV2(ctx, approval.QuestID)
		if err != nil {
			t.Fatal(err)
		}
		if domain.IsTerminalQuestStatus(quest.Status) || quest.Status == domain.QuestNeedsReview || quest.Status == domain.QuestBlocked {
			break
		}
		time.Sleep(2 * time.Second)
	}
	bundle, err = application.EvidenceBundle(ctx, quest.ID)
	problems := []string{}
	if err != nil {
		problems = append(problems, "evidence: "+err.Error())
	} else {
		problems = append(problems, mvpViolations(approval.WorkOrder, quest.Status, bundle)...)
	}
	if stack == "node" {
		probe := osproc.Command("node", "--input-type=module", "-e", `import {add} from './arithmetic.js'; if(add(1,2)!==3 || add(-2,1)!==-1 || add(0.25,0.5)!==0.75) process.exit(1)`)
		probe.Dir = project
		if output, err := probe.CombinedOutput(); err != nil {
			problems = append(problems, fmt.Sprintf("independent delivered-source probe: %v %s", err, output))
		}
	} else if !loopSourceContains(t, project, "*.go", "/healthz") || !loopSourceContains(t, project, "*_test.go", "healthz") {
		problems = append(problems, "delivered healthz endpoint or its test is absent")
	}
	state, stateErr := application.Bootstrap()
	if stateErr != nil {
		problems = append(problems, stateErr.Error())
	}
	if len(state.FlowRuns) == 0 {
		problems = append(problems, "no Flow was executed")
	}
	ledger := map[string]any{"stack": stack, "model": model, "questId": quest.ID, "status": quest.Status, "seconds": time.Since(started).Seconds(), "command": command, "project": project, "database": filepath.Join(data, "hub-v2.db"), "flowRuns": state.FlowRuns, "evidence": bundle, "violations": problems}
	raw, _ := json.MarshalIndent(ledger, "", "  ")
	file := filepath.Join(baseDir, stack, "result.json")
	if err = os.WriteFile(file, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("live quest evidence: %s", file)
	if len(problems) > 0 {
		t.Fatal(strings.Join(problems, "; "))
	}
}
