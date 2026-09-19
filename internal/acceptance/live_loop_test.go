package acceptance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/connections"
	"local-agent-workbench/internal/domain"
)

// Лёгкий живой прогон: одна правка в настоящем проекте, от реплики человека до
// зелёных тестов. Он намеренно не похож на приёмку MVP (work_order_mvp_test.go)
// и не заменяет её: там наряд, шлюз доказательств и сценарий на часы, здесь —
// полоса «Агент» (app.StartFastAgent), десять минут и шесть фактов вместо
// шестнадцати условий шлюза.
//
// Смысл полигона — короткий круг обратной связи. Пока главный сценарий ездит
// часами, ни один дефект не успевает быть найденным дважды; на этой полосе
// круг стоит минуты, и вердикт можно получать десятки раз в день.
//
// Полоса «Агент» уже пишет в открытый проект напрямую (sandbox.LiveFileMutationEnabled
// по умолчанию истинна), propose_patch подтверждается сам, run_command спрашивает.
// Шлюза доказательств здесь нет вовсе: квест заводится сразу активным с одним
// ручным критерием. Поэтому лёгкая задача едет мимо шлюза, а не сквозь ослабленный.
const liveLoopTask = `Добавь в HTTP-сервер эндпоинт GET /healthz: он отвечает кодом 200 ` +
	`и телом {"status":"ok"} с заголовком Content-Type: application/json. ` +
	`Добавь на него тест в main_test.go. Команда go test ./... должна проходить.`

// liveLoopRow — строка журнала прогона. Она отвечает на шесть вопросов, и
// четвёртый из них (testsGreen) харнесс выясняет сам, запуская go test в копии
// проекта: отчёт агента о собственной работе доказательством не считается.
type liveLoopRow struct {
	Run             int      `json:"run"`
	Model           string   `json:"model"`
	RunID           string   `json:"runId"`
	QuestID         string   `json:"questId"`
	Status          string   `json:"status"`
	Seconds         float64  `json:"seconds"`
	Steps           int      `json:"steps"`
	ModelRequests   int      `json:"modelRequests"`
	Interventions   int      `json:"interventions"`
	ToolsUsed       []string `json:"toolsUsed"`
	ChangedFiles    []string `json:"changedFiles"`
	Terminal        bool     `json:"terminal"`
	EndpointPresent bool     `json:"endpointPresent"`
	TestPresent     bool     `json:"testPresent"`
	TestsGreen      bool     `json:"testsGreen"`
	TestOutput      string   `json:"testOutput,omitempty"`
	FirstFailure    string   `json:"firstFailure,omitempty"`
	Violations      []string `json:"violations,omitempty"`
}

func TestLiveLoopScenario(t *testing.T) {
	if os.Getenv("POINT_LIVE_LOOP") != "1" {
		t.Skip("set POINT_LIVE_LOOP=1 for the light live scenario")
	}
	// Умолчания те же, что и у живой приёмки PHP: от человека для запуска
	// нужен только ключ, всё остальное уже известно коду.
	model := firstNonEmpty(os.Getenv("POINT_LIVE_LOOP_MODEL"), os.Getenv("POINT_V2_MVP_MODEL"),
		os.Getenv("POINT_ACCEPTANCE_MODEL"), "Qwen3.6-35B-A3B")
	provider, preset := domain.ProviderOpenAI, "llmux"
	baseURL := firstNonEmpty(os.Getenv("POINT_LLMUX_BASE_URL"), os.Getenv("POINT_OPENAI_BASE_URL"),
		"https://llmux.ds3.centrofinans.ru/v1")
	apiKey := firstNonEmpty(os.Getenv("POINT_LLMUX_API_KEY"), os.Getenv("POINT_OPENAI_API_KEY"), os.Getenv("OPENAI_API_KEY"))
	if strings.EqualFold(strings.TrimSpace(os.Getenv("POINT_LIVE_LOOP_PROVIDER")), "ollama") {
		provider, preset = domain.ProviderOllama, "ollama"
		baseURL = firstNonEmpty(os.Getenv("POINT_OLLAMA_BASE_URL"), baseURL, "http://127.0.0.1:11434")
	}
	if model == "" || baseURL == "" {
		t.Fatal("live loop needs POINT_LIVE_LOOP_MODEL and a base URL")
	}
	if apiKey == "" && provider != domain.ProviderOllama {
		t.Fatal("live loop needs an API key for a remote provider")
	}
	run := 1
	if value := strings.TrimSpace(os.Getenv("POINT_LIVE_LOOP_RUN")); value != "" {
		fmt.Sscanf(value, "%d", &run)
	}

	t.Setenv("POINT_AGENT_HUB_V2", "1")
	t.Setenv("REDIS_ADDR", "")
	t.Setenv("POINT_DEFAULT_MODEL", model)
	// Живая запись — предмет этого полигона, и она не должна зависеть от того,
	// что осталось в окружении от прошлого прогона приёмки MVP (та ставит
	// POINT_LIVE_WORKSPACE=0 и POINT_FILE_ISOLATION=sandbox).
	t.Setenv("POINT_LIVE_WORKSPACE", "1")
	t.Setenv("POINT_FILE_ISOLATION", "")

	project := prepareLoopProject(t)
	dataDir := firstNonEmpty(os.Getenv("POINT_LIVE_LOOP_DATA_DIR"), t.TempDir())
	application, err := app.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	if _, err = application.OpenWorkspace(project); err != nil {
		t.Fatal(err)
	}

	secretRef := "secret:live-loop"
	if provider == domain.ProviderOllama {
		secretRef = ""
	}
	connection, err := application.SaveConnection(connections.UpsertRequest{
		ID: "conn-live-loop", Provider: provider, PresetID: preset,
		DisplayName: "Live loop", BaseURL: baseURL, SecretRef: secretRef, DefaultModel: model,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = application.SetDefaultConnection(connection.ID); err != nil {
		t.Fatal(err)
	}
	if err = wireLoopProfile(application, connection, model, baseURL); err != nil {
		t.Fatal(err)
	}
	// Полоса «Агент» запускается исполнителем проекта, а не legacy-профилем:
	// в продукте человек к этому моменту уже завёл агента — руками в Гильдии
	// или разговором с Мастером. Харнесс делает то же самое, а не обходит
	// контур: первый прогон полигона встал именно здесь.
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{
		Name: "Полигон", RoleDescription: "Правит код по короткому заданию",
		ConnectionID: connection.ID, PrimaryModel: model,
		AllowedTools:    []string{"project_map", "search_code", "list_files", "read_file", "search_text", "propose_patch", "run_command", "git_diff"},
		MaxOutputTokens: 16384, ContextWindowTokens: 131072, ReasoningEffort: "low",
		MaxSteps: 25, MaxDurationSeconds: 600, ApprovalMode: domain.ApprovalSafe,
	})
	if err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	started0, err := application.StartFastAgent(app.FastAgentRequest{
		ProfileID: agent.ID, Task: liveLoopTask, APIKey: apiKey,
	})
	if err != nil {
		// Отказ до первого шага — тоже результат полигона, и он обязан попасть
		// в журнал: «не запустилось» и «запустилось и не справилось» лечатся
		// в разных местах.
		writeLoopLedger(t, model, run, liveLoopRow{
			Run: run, Model: model, Seconds: time.Since(started).Seconds(),
			FirstFailure: "StartFastAgent: " + err.Error(),
			Violations:   []string{"прогон не стартовал: " + err.Error()},
		})
		t.Fatalf("start fast agent: %v", err)
	}

	final, interventions := awaitLoopRun(t, application, started0)
	row := liveLoopRow{
		Run: run, Model: model, RunID: final.ID, Status: string(final.Status),
		Seconds: time.Since(started).Seconds(), Steps: final.Step,
		ModelRequests: final.RequestCount, Interventions: interventions,
		ToolsUsed: final.ToolsUsed, ChangedFiles: final.ChangedFiles,
		Terminal:     final.Status == domain.RunCompleted,
		FirstFailure: strings.TrimSpace(final.Error),
	}
	row.EndpointPresent = loopSourceContains(t, project, "*.go", "/healthz")
	row.TestPresent = loopSourceContains(t, project, "*_test.go", "healthz")
	row.TestsGreen, row.TestOutput = loopTestsGreen(project)
	row.Violations = loopViolations(row)
	writeLoopLedger(t, model, run, row)

	if len(row.Violations) > 0 {
		t.Fatalf("live loop run %d failed: %s", run, strings.Join(row.Violations, "; "))
	}
}

// TestLiveLoopVerdictRejectsFalseCompletion держит сам вердикт. Приёмка,
// которую никто не проверял на подделках, пропускает ровно тот случай, ради
// которого её писали: прогон дошёл до completed, а работы нет.
func TestLiveLoopVerdictRejectsFalseCompletion(t *testing.T) {
	green := liveLoopRow{Terminal: true, EndpointPresent: true, TestPresent: true, TestsGreen: true}
	if violations := loopViolations(green); len(violations) != 0 {
		t.Fatalf("честный прогон объявлен нарушением: %v", violations)
	}
	fakes := map[string]liveLoopRow{
		"агент сказал «готово», а тесты красные":  {Terminal: true, EndpointPresent: true, TestPresent: true},
		"тесты зелёные, но эндпоинта нет":         {Terminal: true, TestPresent: true, TestsGreen: true},
		"эндпоинт есть, теста на него нет":        {Terminal: true, EndpointPresent: true, TestsGreen: true},
		"работа сделана, но прогон не завершился": {EndpointPresent: true, TestPresent: true, TestsGreen: true},
		"пустой прогон":                           {},
	}
	for name, row := range fakes {
		if violations := loopViolations(row); len(violations) == 0 {
			t.Fatalf("подделка принята как успех: %s", name)
		}
	}
}

// loopViolations — вердикт по шести фактам. Он намеренно не спрашивает у
// квеста, доволен ли он собой: агент, объявивший работу сделанной, и работа,
// которая действительно сделана, — разные утверждения, и полигон существует
// ради второго.
func loopViolations(row liveLoopRow) []string {
	var out []string
	if !row.Terminal {
		out = append(out, fmt.Sprintf("прогон не дошёл до completed (status=%s)", row.Status))
	}
	if !row.EndpointPresent {
		out = append(out, "эндпоинт /healthz не появился в исходниках")
	}
	if !row.TestPresent {
		out = append(out, "тест на /healthz не появился")
	}
	if !row.TestsGreen {
		out = append(out, "go test ./... в проекте не проходит")
	}
	return out
}

// awaitLoopRun ведёт прогон до терминального статуса, закрывая за человека
// очередь решений. Возвращает последний снимок и число вмешательств.
func awaitLoopRun(t *testing.T, application *app.App, run domain.Run) (domain.Run, int) {
	t.Helper()
	interventions := 0
	deadline := time.Now().Add(12 * time.Minute)
	last := run
	for time.Now().Before(deadline) {
		interventions += autoApprovePendingTools(t, application, run.WorkspaceID, nil, nil)
		details, err := application.RunDetails(run.ID)
		if err != nil {
			t.Logf("run details: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		last = details.Run
		switch last.Status {
		case domain.RunCompleted, domain.RunFailed, domain.RunCancelled, domain.RunInterrupted:
			// Решения могли встать в очередь уже после последнего круга —
			// добираем их, иначе отчёт соврёт о числе вмешательств.
			interventions += autoApprovePendingTools(t, application, run.WorkspaceID, nil, nil)
			return last, interventions
		}
		time.Sleep(2 * time.Second)
	}
	t.Logf("live loop hit the 12 minute deadline at status=%s step=%d", last.Status, last.Step)
	return last, interventions
}

// prepareLoopProject кладёт копию examples/go-health во временный каталог и
// заводит в ней git. Репозиторий обязателен: живая запись безопасна ровно
// потому, что откат есть, и git_diff без репозитория молчит.
func prepareLoopProject(t *testing.T) string {
	t.Helper()
	source := filepath.Join(repoRoot(t), "examples", "go-health")
	project := filepath.Join(t.TempDir(), "go-health")
	if err := copyLoopTree(source, project); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		full := append([]string{"-C", project}, args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "--quiet")
	git("add", "-A")
	git("-c", "user.email=loop@point.local", "-c", "user.name=Point Live Loop",
		"commit", "--quiet", "--no-gpg-sign", "-m", "baseline")
	return project
}

func copyLoopTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o644)
	})
}

// loopSourceContains ищет подстроку в файлах проекта по маске. Простая
// проверка намеренно: полигон отвечает на вопрос «появилось ли», а судит о
// работоспособности go test, а не разбор исходника.
func loopSourceContains(t *testing.T, project, pattern, needle string) bool {
	t.Helper()
	found := false
	err := filepath.WalkDir(project, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || found {
			return err
		}
		if entry.Name() == ".git" {
			return filepath.SkipDir
		}
		matched, matchErr := filepath.Match(pattern, entry.Name())
		if matchErr != nil || !matched {
			return matchErr
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), needle) {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Logf("scan %s for %q: %v", pattern, needle, err)
	}
	return found
}

// loopTestsGreen — внешняя правда о результате. Её выясняет харнесс, а не
// агент: прогон, сообщивший «тесты проходят», и проходящие тесты — разные
// утверждения, и вся приёмка держится на втором.
func loopTestsGreen(project string) (bool, string) {
	command := exec.Command("go", "test", "./...")
	command.Dir = project
	output, err := command.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if len(text) > 4000 {
		text = text[:4000] + "\n… вывод обрезан"
	}
	return err == nil, text
}

func writeLoopLedger(t *testing.T, model string, run int, row liveLoopRow) {
	t.Helper()
	dir := filepath.Join(repoRoot(t), ".tmp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("live-loop-%s-run%d.jsonl", sanitizeModel(model), run)
	if err = os.WriteFile(filepath.Join(dir, name), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", filepath.Join(dir, name))
}

// wireLoopProfile — исполнитель лёгкого прогона. Отличается от
// wireCodingProfileToConnection только пределами: 25 шагов и 10 минут вместо
// 100 и часа. Большие пределы на полигоне вредны — прогон, который может
// ходить кругами час, скрывает от вас, что он ходит кругами.
//
// Остальное повторяет приёмку MVP по тем же причинам, что записаны там:
// ApprovalSafe (ApprovalAlways останавливает headless), потолок вывода 16k
// (65k + размышление сжигают весь бюджет на мысли без единого вызова
// инструмента), reasoning_effort=low (medium на Qwen3.6 не оставлял места
// первому действию).
func wireLoopProfile(application *app.App, conn domain.Connection, model, baseURL string) error {
	profile := domain.DefaultProfile()
	profile.ID = "default"
	profile.Provider = conn.Provider
	profile.ProviderPreset = conn.PresetID
	profile.BaseURL = baseURL
	profile.Model = model
	profile.ConnectionID = conn.ID
	profile.ApprovalMode = domain.ApprovalSafe
	profile.MaxOutputTokens = 16384
	profile.ContextWindowTokens = 131072
	profile.ReasoningEffort = "low"
	profile.MaxSteps = 25
	profile.MaxDurationSeconds = 600
	profile.UpdatedAt = time.Now().UTC()
	_, err := application.SaveProfile(profile)
	return err
}
