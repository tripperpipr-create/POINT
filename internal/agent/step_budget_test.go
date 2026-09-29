package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
	workbenchtools "local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
)

func TestStepBudgetCeilingComesOnlyFromBrief(t *testing.T) {
	if b := newStepBudget(30, nil); b.limit != 30 || b.ceiling != 30 {
		t.Fatalf("no brief: limit=%d ceiling=%d", b.limit, b.ceiling)
	}
	brief := &domain.TaskBrief{Budget: domain.TaskBudget{MaxSteps: 64}}
	if b := newStepBudget(30, brief); b.limit != 30 || b.ceiling != 64 {
		t.Fatalf("work order budget: limit=%d ceiling=%d", b.limit, b.ceiling)
	}
	brief.Budget.MaxSteps = 500
	if b := newStepBudget(30, brief); b.ceiling != maxStepCeiling {
		t.Fatalf("ceiling not capped: %d", b.ceiling)
	}
	brief.Budget.MaxSteps = 10
	if b := newStepBudget(30, brief); b.limit != 30 || b.ceiling != 30 {
		t.Fatalf("smaller brief budget must not cut the profile: limit=%d ceiling=%d", b.limit, b.ceiling)
	}
}

func TestStepBudgetExtendsOnlyWithRecentProgress(t *testing.T) {
	b := newStepBudget(30, &domain.TaskBrief{Budget: domain.TaskBudget{MaxSteps: 64}})
	if _, _, ok := b.autoExtend(30); ok {
		t.Fatal("extended without any progress")
	}
	b.markProgress(24)
	if _, _, ok := b.autoExtend(30); ok {
		t.Fatal("extended on stale progress")
	}
	b.markProgress(28)
	from, to, ok := b.autoExtend(30)
	if !ok || from != 30 || to != 40 {
		t.Fatalf("extension from=%d to=%d ok=%v", from, to, ok)
	}
	b.markProgress(60)
	for i := 0; i < 5; i++ {
		b.autoExtend(b.currentLimit())
	}
	if b.currentLimit() != 64 {
		t.Fatalf("limit passed the approved ceiling: %d", b.currentLimit())
	}
	if err := b.extendByHuman(); err != nil || b.currentLimit() != 74 {
		t.Fatalf("human extension: limit=%d err=%v", b.currentLimit(), err)
	}
	if err := b.extendByHuman(); err == nil {
		t.Fatal("second human extension allowed without a new approval")
	}
}

func TestProgressCountsOnlyAppliedPatchesAndPassingCommands(t *testing.T) {
	ok := func(output string) domain.ToolResult {
		return domain.ToolResult{OK: true, Output: json.RawMessage(output)}
	}
	if !progressfulToolResult("run_command", ok(`{"exitCode":0}`)) || !progressfulToolResult("propose_patch", ok(`{"status":"applied"}`)) {
		t.Fatal("real progress not counted")
	}
	if progressfulToolResult("run_command", ok(`{"exitCode":2}`)) || progressfulToolResult("read_file", ok(`{}`)) {
		t.Fatal("failed command or read counted as progress")
	}
}

// Квест 29.09: после `npm install` повтор проверки скрипта получил
// duplicate_tool_call — node_modules ревизию не сдвигают.
func TestCommandResultsAreForgottenAfterAnotherAction(t *testing.T) {
	check := providers.ToolCall{Name: "run_command", Arguments: json.RawMessage(`{"command":"node scripts/deploy.mjs","reason":"check"}`)}
	install := providers.ToolCall{Name: "run_command", Arguments: json.RawMessage(`{"command":"npm install","reason":"deps"}`)}
	read := providers.ToolCall{Name: "read_file", Arguments: json.RawMessage(`{"path":"a.js"}`)}
	completed := map[string]struct{}{}
	checkKey, installKey, readKey := toolExecutionKey(check, 3), toolExecutionKey(install, 3), toolExecutionKey(read, 3)
	completed[checkKey], completed[readKey] = struct{}{}, struct{}{}
	// Тот же вызов подряд по-прежнему повтор: своё действие ключ не снимает.
	releaseCommandResults(completed, checkKey)
	if _, done := completed[checkKey]; !done {
		t.Fatal("immediate identical command would run again")
	}
	releaseCommandResults(completed, installKey)
	if _, done := completed[checkKey]; done {
		t.Fatal("check stays rejected after npm install changed the environment")
	}
	if _, done := completed[readKey]; !done {
		t.Fatal("read results must follow the workspace revision, not commands")
	}
}

// Квест 29.09 записал deploy.mjs целиком на пятом ходу и на шестнадцатом
// получил inspection_required при перезаписи собственного файла.
func TestOwnFullWriteCountsAsInspection(t *testing.T) {
	root := t.TempDir()
	content := "export const x = 1\n"
	if err := os.WriteFile(filepath.Join(root, "deploy.mjs"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(content))
	applied, _ := json.Marshal(map[string]any{"status": "applied", "path": "deploy.mjs", "sha256": hex.EncodeToString(digest[:])})
	write := providers.ToolCall{Name: "propose_patch", Arguments: json.RawMessage(`{"path":"deploy.mjs","content":"export const x = 1\n"}`)}
	tracker := newObservationTracker(nil)
	tracker.Observe(write, domain.ToolResult{OK: true, Output: applied}, 1, 5, "global:write")
	rewrite := json.RawMessage(`{"path":"deploy.mjs","content":"export const x = 2\n"}`)
	if _, requirement := tracker.CheckPatch(fs, rewrite, 1, 6); requirement != nil {
		t.Fatalf("rewrite of own file demanded inspection: %#v", requirement)
	}
	// Точечная правка права на полную перезапись не даёт.
	edited := newObservationTracker(nil)
	edited.Observe(providers.ToolCall{Name: "propose_patch", Arguments: json.RawMessage(`{"path":"deploy.mjs","edits":[{"oldText":"1","newText":"2"}]}`)}, domain.ToolResult{OK: true, Output: applied}, 1, 5, "global:edit")
	if _, requirement := edited.CheckPatch(fs, rewrite, 1, 6); requirement == nil {
		t.Fatal("exact edit granted a blind full rewrite")
	}
}

func TestGitToolsHiddenWithoutWorkTree(t *testing.T) {
	fs, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if workbenchtools.GitWorkTreeAvailable(context.Background(), fs.Root()) {
		t.Skip("temporary directory is inside a Git work tree")
	}
	definitions := []domain.ToolDefinition{{Name: "read_file"}, {Name: "git_diff"}, {Name: "git_log"}, {Name: "run_command"}}
	filtered := withoutUnusableGitTools(context.Background(), definitions, workbenchtools.NewPatchManager(fs))
	names := []string{}
	for _, definition := range filtered {
		names = append(names, definition.Name)
	}
	if strings.Join(names, ",") != "read_file,run_command" {
		t.Fatalf("git tools offered without .git: %v", names)
	}
}

func (e *Engine) snapshotSteps(runID string) int {
	e.mu.RLock()
	active := e.active[runID]
	e.mu.RUnlock()
	if active == nil || active.steps == nil {
		return 0
	}
	return active.steps.currentLimit()
}

// stepScriptModel пишет по файлу за ход `writes` ходов, затем отвечает. Без
// инструментов в запросе (последний ход) отвечает сразу.
type stepScriptModel struct {
	mu          sync.Mutex
	writes      int
	turns       int
	sawNotice   bool
	toolessTurn bool
	listOnly    bool
}

func (m *stepScriptModel) Stream(_ context.Context, request providers.ModelRequest, emit func(providers.ModelEvent) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.turns++
	for _, message := range request.Messages {
		if strings.Contains(message.Content, "model turns left") {
			m.sawNotice = true
		}
	}
	if len(request.Tools) == 0 {
		m.toolessTurn = true
		return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Итог: файлы записаны, проверка прошла."})
	}
	if m.listOnly || m.turns == 1 {
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: fmt.Sprintf("list-%d", m.turns), Name: "list_files", Arguments: json.RawMessage(fmt.Sprintf(`{"maxDepth":%d}`, m.turns))}})
	}
	if m.turns <= m.writes+1 {
		args, _ := json.Marshal(map[string]any{"path": fmt.Sprintf("f%d.txt", m.turns), "content": "x\n", "reason": "write"})
		return emit(providers.ModelEvent{Kind: providers.EventToolCall, ToolCall: &providers.ToolCall{ID: fmt.Sprintf("write-%d", m.turns), Name: "propose_patch", Arguments: args}})
	}
	return emit(providers.ModelEvent{Kind: providers.EventTextDelta, Delta: "Готово."})
}

func startStepScriptRun(t *testing.T, model *stepScriptModel, profileSteps, briefSteps int) (*memoryRepo, *Engine, domain.Run) {
	t.Helper()
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModePrecise, Goal: "write files", ResultKind: "workspace_change", FastAgent: true,
		Criteria:    []domain.AcceptanceCriterion{{ID: "c1", Text: "files exist", Kind: "manual"}},
		Permissions: domain.TaskPermissions{WriteFiles: true},
		Budget:      domain.TaskBudget{Tokens: 100000, MaxParallel: 1, MaxAttempts: 1, MaxSteps: briefSteps},
	}))
	if err != nil {
		t.Fatal(err)
	}
	profile := domain.DefaultProfile()
	profile.ID = "p1"
	profile.AllowedTools = []string{"list_files", "propose_patch"}
	profile.MaxSteps = profileSteps
	profile.MaxDurationSeconds = 30
	run, err := engine.Start(StartInput{
		Workspace: domain.Workspace{ID: "ws", Path: t.TempDir()}, Task: "write files", TaskBrief: &brief,
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
	})
	if err != nil {
		t.Fatal(err)
	}
	return repo, engine, run
}

// Форма квеста 29.09: работа продвигается до самого лимита профиля. Прежде
// прогон проваливался на лимите; теперь лимит растёт до потолка брифа, и
// агент успевает ответить.
func TestProgressingRunOutgrowsProfileLimitUpToBriefCeiling(t *testing.T) {
	model := &stepScriptModel{writes: 7}
	repo, _, run := startStepScriptRun(t, model, 6, 12)
	finished := waitForTerminalRun(t, repo, run.ID)
	if finished.Status != domain.RunCompleted {
		t.Fatalf("progressing run did not complete: %#v", finished)
	}
	events, _ := repo.ListByRun(context.Background(), run.ID)
	extended := false
	for _, event := range events {
		if event.Type == domain.EventAgentGuardrail && strings.Contains(string(event.Data), `"code":"step_budget_extended"`) {
			extended = true
		}
	}
	if !extended {
		t.Fatal("no step_budget_extended event")
	}
}

// Застрявший агент продления не получает: за три хода до лимита он узнаёт
// остаток, а на лимите получает ход без инструментов и отвечает итогом.
func TestStalledRunGetsNoticeAndToolessFinalTurn(t *testing.T) {
	model := &stepScriptModel{listOnly: true}
	repo, _, run := startStepScriptRun(t, model, 5, 12)
	finished := waitForTerminalRun(t, repo, run.ID)
	model.mu.Lock()
	sawNotice, tooless, turns := model.sawNotice, model.toolessTurn, model.turns
	model.mu.Unlock()
	if finished.Status != domain.RunCompleted || !strings.Contains(finished.Result, "Итог") {
		t.Fatalf("final turn did not finish the run: %#v", finished)
	}
	if !sawNotice || !tooless || turns != 6 {
		t.Fatalf("notice=%v tooless=%v turns=%d", sawNotice, tooless, turns)
	}
}

// Ходы кончились, а машинный критерий не выполнен: прогон с брифом встаёт
// на паузу вместо провала, продление даёт одну порцию ходов, второе — нет.
func TestExhaustedBriefRunPausesAndExtendsOnce(t *testing.T) {
	repo := newMemoryRepo()
	engine := NewEngine(repo, nil)
	model := &stepScriptModel{listOnly: true}
	engine.SetModelFactory(func(providers.Config) (providers.Model, error) { return model, nil })
	brief, err := domain.ApproveTaskBrief(domain.NormalizeTaskBrief(domain.TaskBrief{
		Mode: domain.TaskModePrecise, Goal: "verify", ResultKind: "workspace_change",
		Criteria: []domain.AcceptanceCriterion{{ID: "check", Text: "Tests pass", Kind: "verification", Tool: "run_command",
			Arguments: json.RawMessage(`{"command":"go test ./..."}`)}},
		Permissions: domain.TaskPermissions{ExecuteCommands: true},
		Budget:      domain.TaskBudget{Tokens: 100000, MaxParallel: 1, MaxAttempts: 1, MaxSteps: 4},
	}))
	if err != nil {
		t.Fatal(err)
	}
	profile := domain.DefaultProfile()
	profile.ID = "p1"
	profile.AllowedTools = []string{"list_files", "run_command"}
	profile.MaxSteps = 4
	profile.MaxDurationSeconds = 30
	run, err := engine.Start(StartInput{
		Workspace: domain.Workspace{ID: "ws", Path: t.TempDir()}, Task: "verify", TaskBrief: &brief,
		Configuration: domain.NewRunConfigurationSnapshot("test", profile, nil, time.Now().UTC()),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.StopAll()
	deadline := time.Now().Add(10 * time.Second)
	var paused domain.Run
	for time.Now().Before(deadline) {
		paused = repo.run(run.ID)
		if paused.Status == domain.RunPaused || paused.Status == domain.RunFailed || paused.Status == domain.RunCompleted {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if paused.Status != domain.RunPaused || paused.Controller.PauseReason != domain.PauseReasonStepBudgetExhausted || paused.Controller.StepLimit != 4 {
		t.Fatalf("exhausted run did not pause for steps: %#v", paused)
	}
	checkpoint, err := repo.LatestRunCheckpoint(context.Background(), run.ID)
	if err != nil || checkpoint.PauseReason != domain.PauseReasonStepBudgetExhausted || checkpoint.StepGrant != 5 {
		t.Fatalf("checkpoint does not carry the step pause: %#v %v", checkpoint, err)
	}
	if err := engine.ExtendActiveTime(run.ID); err != nil {
		t.Fatalf("step extension refused: %v", err)
	}
	if limit := engine.snapshotSteps(run.ID); limit != 9 {
		t.Fatalf("limit after extension=%d", limit)
	}
	if err := engine.ExtendActiveTime(run.ID); err == nil {
		t.Fatal("second step extension allowed")
	}
}
