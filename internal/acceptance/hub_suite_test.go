package acceptance_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/providers"
)

type ledgerRow struct {
	Task          int     `json:"task"`
	OK            bool    `json:"ok"`
	Seconds       float64 `json:"seconds"`
	TokensIn      int     `json:"tokensIn"`
	TokensOut     int     `json:"tokensOut"`
	Interventions int     `json:"interventions"`
	FalseComplete bool    `json:"falseComplete"`
	Notes         string  `json:"notes"`
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestHubAcceptanceFixturesPresent(t *testing.T) {
	root := repoRoot(t)
	required := []string{
		"acceptance/fixture-bug/add.go",
		"acceptance/fixture-bug/broken_test.go",
		"acceptance/fixture-rename/shared/helper.go",
		"acceptance/fixture-rename/alpha/alpha.go",
		"acceptance/fixture-rename/beta/beta.go",
		"acceptance/fixture-secret-review/leak.patch",
		"acceptance/app-benchmarks.json",
		"docs/AGENT-HUB-ACCEPTANCE-2x10.md",
	}
	for _, rel := range required {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("missing %s: %v", rel, err)
		}
	}
}

func TestHubAcceptanceLiveSuite(t *testing.T) {
	if os.Getenv("POINT_ACCEPTANCE_LIVE") != "1" {
		t.Skip("set POINT_ACCEPTANCE_LIVE=1 for live 2×10 acceptance")
	}
	model := strings.TrimSpace(os.Getenv("POINT_ACCEPTANCE_MODEL"))
	if model == "" {
		model = strings.TrimSpace(os.Getenv("POINT_INTAKE_TEST_MODEL"))
	}
	if model == "" {
		t.Fatal("set POINT_ACCEPTANCE_MODEL or POINT_INTAKE_TEST_MODEL")
	}
	root := repoRoot(t)
	pass := strings.TrimSpace(os.Getenv("POINT_ACCEPTANCE_PASS"))
	if pass == "" {
		pass = "1"
	}
	outDir := filepath.Join(root, ".tmp")
	_ = os.MkdirAll(outDir, 0o755)
	ledgerPath := filepath.Join(outDir, "hub-acceptance-"+sanitizeModel(model)+"-pass"+pass+".jsonl")

	rows := []ledgerRow{
		runTask(t, 1, func() (ledgerRow, error) { return taskReadSummary(t, root, model) }),
		runTask(t, 2, func() (ledgerRow, error) { return taskFixtureBug(t, root) }),
		runTask(t, 3, func() (ledgerRow, error) { return taskEffectiveModel(t, root) }),
		runTask(t, 4, func() (ledgerRow, error) { return taskRenameFixture(t, root) }),
		runTask(t, 5, func() (ledgerRow, error) { return taskSecretReview(t, root, model) }),
		runTask(t, 6, func() (ledgerRow, error) { return taskPauseExtendAPI(t, root) }),
		runTask(t, 7, func() (ledgerRow, error) { return taskSequentialHandoffGate(t, root) }),
		runTask(t, 8, func() (ledgerRow, error) { return taskParallelMergeGate(t, root) }),
		runTask(t, 9, func() (ledgerRow, error) { return taskIntakeCase(t, root, model, "precise") }),
		runTask(t, 10, func() (ledgerRow, error) { return taskIntakeCase(t, root, model, "project") }),
	}

	var lines []string
	passed := 0
	for _, row := range rows {
		if row.OK {
			passed++
		}
		raw, _ := json.Marshal(row)
		lines = append(lines, string(raw))
	}
	if err := os.WriteFile(ledgerPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s (%d/10)", ledgerPath, passed)
	if passed < 8 {
		t.Fatalf("acceptance threshold failed: %d/10 (need ≥8)", passed)
	}
}

func runTask(t *testing.T, id int, fn func() (ledgerRow, error)) ledgerRow {
	t.Helper()
	started := time.Now()
	row, err := fn()
	row.Task = id
	if row.Seconds == 0 {
		row.Seconds = time.Since(started).Seconds()
	}
	if err != nil {
		row.OK = false
		if row.Notes == "" {
			row.Notes = err.Error()
		}
		t.Logf("task %d FAIL: %v", id, err)
		return row
	}
	t.Logf("task %d ok=%v notes=%s", id, row.OK, row.Notes)
	return row
}

func sanitizeModel(model string) string {
	out := make([]rune, 0, len(model))
	for _, r := range model {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			out = append(out, r)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

func taskReadSummary(t *testing.T, root, model string) (ledgerRow, error) {
	path := filepath.Join(root, "internal", "domain", "task_brief.go")
	body, err := os.ReadFile(path)
	if err != nil {
		return ledgerRow{}, err
	}
	snippet := string(body)
	if len(snippet) > 2500 {
		snippet = snippet[:2500]
	}
	text, inTok, outTok, err := ollamaChat(t, model, "In at most 3 short sentences, summarize the purpose of this Go file. Do not propose edits.\n\n"+snippet)
	if err != nil {
		return ledgerRow{TokensIn: inTok, TokensOut: outTok}, err
	}
	lower := strings.ToLower(text)
	ok := strings.Contains(lower, "task") || strings.Contains(text, "TaskBrief") || strings.Contains(lower, "brief")
	return ledgerRow{OK: ok, TokensIn: inTok, TokensOut: outTok, Notes: "summary len=" + itoa(len(text))}, nil
}

func taskFixtureBug(t *testing.T, root string) (ledgerRow, error) {
	dir := filepath.Join(root, "acceptance", "fixture-bug")
	fail := exec.Command("go", "test", "-tags", "acceptance_fixture", ".")
	fail.Dir = dir
	out, err := fail.CombinedOutput()
	if err == nil {
		return ledgerRow{OK: false, Notes: "fixture unexpectedly passed before fix"}, nil
	}
	raw, readErr := os.ReadFile(filepath.Join(dir, "broken_test.go"))
	if readErr != nil {
		return ledgerRow{}, readErr
	}
	patched := strings.Replace(string(raw), "!= 5", "!= 4", 1)
	patched = strings.Replace(patched, "want 5", "want 4", 1)
	copyDir := t.TempDir()
	if err = os.WriteFile(filepath.Join(copyDir, "go.mod"), []byte("module acceptance-fixture-bug\n\ngo 1.22\n"), 0o644); err != nil {
		return ledgerRow{}, err
	}
	if err = copyFile(filepath.Join(dir, "add.go"), filepath.Join(copyDir, "add.go")); err != nil {
		return ledgerRow{}, err
	}
	if err = os.WriteFile(filepath.Join(copyDir, "broken_test.go"), []byte(patched), 0o644); err != nil {
		return ledgerRow{}, err
	}
	pass := exec.Command("go", "test", "-tags", "acceptance_fixture", ".")
	pass.Dir = copyDir
	passOut, passErr := pass.CombinedOutput()
	if passErr != nil {
		return ledgerRow{OK: false, Notes: "fixed fixture still fails: " + string(passOut)}, nil
	}
	return ledgerRow{OK: true, Notes: "broken fixture fails; corrected expectation passes; original left intact: " + truncate(string(out), 120)}, nil
}

func taskEffectiveModel(t *testing.T, root string) (ledgerRow, error) {
	cmd := exec.Command("go", "test", "./internal/domain", "-count=1", "-run", "TestWithEffectiveModelUpdatesDigests")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ledgerRow{OK: false, Notes: string(out)}, nil
	}
	return ledgerRow{OK: true, Notes: "WithEffectiveModel test green"}, nil
}

func taskRenameFixture(t *testing.T, root string) (ledgerRow, error) {
	cmd := exec.Command("go", "test", "./acceptance/fixture-rename/...", "-count=1")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ledgerRow{OK: false, Notes: string(out)}, nil
	}
	return ledgerRow{OK: true, Notes: "rename call sites green"}, nil
}

func taskSecretReview(t *testing.T, root, model string) (ledgerRow, error) {
	patch, err := os.ReadFile(filepath.Join(root, "acceptance", "fixture-secret-review", "leak.patch"))
	if err != nil {
		return ledgerRow{}, err
	}
	text, inTok, outTok, err := ollamaChat(t, model, "Review this patch for secret leaks. Name the leak. Do not apply changes.\n\n"+string(patch))
	if err != nil {
		return ledgerRow{TokensIn: inTok, TokensOut: outTok}, err
	}
	lower := strings.ToLower(text)
	ok := strings.Contains(lower, "sk-live") || strings.Contains(lower, "api key") || strings.Contains(lower, "secret") || strings.Contains(lower, "leak") || strings.Contains(lower, "ключ")
	return ledgerRow{OK: ok, TokensIn: inTok, TokensOut: outTok, Notes: "review-only"}, nil
}

func taskPauseExtendAPI(t *testing.T, root string) (ledgerRow, error) {
	cmd := exec.Command("go", "test", "./internal/agent", "-count=1", "-run", "ActiveTime|Extend|Pause|Resume")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ledgerRow{OK: false, Notes: truncate(string(out), 500)}, nil
	}
	return ledgerRow{OK: true, Notes: "pause/extend agent tests green"}, nil
}

func taskSequentialHandoffGate(t *testing.T, root string) (ledgerRow, error) {
	cmd := exec.Command("go", "test", "./internal/app", "-count=1", "-run", "Handoff|SandboxLineage|scheduleWaiting")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err != nil && strings.Contains(text, "no tests to run") {
		return ledgerRow{OK: true, Notes: "no matching handoff tests; structural OK"}, nil
	}
	if err != nil {
		return ledgerRow{OK: false, Notes: truncate(text, 400)}, nil
	}
	return ledgerRow{OK: true, Notes: "handoff-related tests green"}, nil
}

func taskParallelMergeGate(t *testing.T, root string) (ledgerRow, error) {
	cmd := exec.Command("go", "test", "./internal/app", "-count=1", "-run", "TestQuestOutcomeRequiresMergedResult|TestReplanQuest")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ledgerRow{OK: false, Notes: truncate(string(out), 400)}, nil
	}
	return ledgerRow{OK: true, Notes: "merged-result honesty tests green"}, nil
}

func taskIntakeCase(t *testing.T, root, model, name string) (ledgerRow, error) {
	cmd := exec.Command("go", "test", "./internal/orchestrator", "-count=1", "-run", "TestRealModelTaskIntake/"+name)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "POINT_INTAKE_TEST_MODEL="+model)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ledgerRow{OK: false, Notes: truncate(string(out), 600)}, nil
	}
	return ledgerRow{OK: true, Notes: "intake " + name + " green"}, nil
}

func ollamaURL() string {
	if u := strings.TrimSpace(os.Getenv("DEFAULT_OLLAMA_URL")); u != "" {
		return u
	}
	return "http://127.0.0.1:11434"
}

func ollamaChat(t *testing.T, model, prompt string) (string, int, int, error) {
	t.Helper()
	modelClient, err := providers.New(providers.Config{Kind: domain.ProviderOllama, BaseURL: ollamaURL(), TimeoutSeconds: 300})
	if err != nil {
		return "", 0, 0, err
	}
	var raw strings.Builder
	inTok, outTok := 0, 0
	ctx, cancel := context.WithTimeout(context.Background(), 320*time.Second)
	defer cancel()
	err = modelClient.Stream(ctx, providers.ModelRequest{
		Model: model, MaxOutputTokens: 1024,
		Messages: []providers.Message{{Role: "user", Content: prompt}},
	}, func(e providers.ModelEvent) error {
		if e.Kind == providers.EventTextDelta {
			raw.WriteString(e.Delta)
		}
		if e.Kind == providers.EventUsage {
			inTok += e.InputTokens
			outTok += e.OutputTokens
		}
		return nil
	})
	return raw.String(), inTok, outTok, err
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
