package providers

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Поток Claude Code разбирается по событиям, а не по строкам вывода: текст
// приходит дельтами, расход — в итоге, а незнакомые виды событий обязаны
// проходить мимо, иначе обновление CLI ломает чат.
func TestClaudeCLIStreamReadsDeltasAndUsage(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"s1"}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Ветки: "}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"master и dev."}}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Ветки: master и dev."}]}}`,
		`{"type":"unknown_future_event","payload":{"whatever":1}}`,
		`{"type":"result","subtype":"success","result":"Ветки: master и dev.","usage":{"input_tokens":120,"output_tokens":18}}`,
	}, "\n")
	var text strings.Builder
	input, output := 0, 0
	err := claudeCLIReadStream(strings.NewReader(stream), func(event ModelEvent) error {
		switch event.Kind {
		case EventTextDelta:
			text.WriteString(event.Delta)
		case EventUsage:
			input, output = event.InputTokens, event.OutputTokens
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Один и тот же ответ приходит и дельтами, и целиком: повтор удвоил бы текст.
	if text.String() != "Ветки: master и dev." {
		t.Fatalf("текст ответа собран неверно: %q", text.String())
	}
	if input != 120 || output != 18 {
		t.Fatalf("расход не прочитан: in=%d out=%d", input, output)
	}
}

// Без потока частичных событий ответ всё равно доходит: старые версии CLI
// присылают только целое сообщение.
func TestClaudeCLIStreamFallsBackToWholeMessage(t *testing.T) {
	stream := `{"type":"assistant","message":{"content":[{"type":"text","text":"Готово."}]}}`
	var text strings.Builder
	if err := claudeCLIReadStream(strings.NewReader(stream), func(event ModelEvent) error {
		if event.Kind == EventTextDelta {
			text.WriteString(event.Delta)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if text.String() != "Готово." {
		t.Fatalf("целое сообщение потеряно: %q", text.String())
	}
}

// Отказ CLI — это отказ, а не пустой ответ: иначе человек получил бы молчание
// вместо причины.
func TestClaudeCLIStreamReportsFailedResult(t *testing.T) {
	stream := `{"type":"result","subtype":"error_during_execution","result":""}`
	err := claudeCLIReadStream(strings.NewReader(stream), func(ModelEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "error_during_execution") {
		t.Fatalf("отказ CLI не назван: %v", err)
	}
}

// Разговор разворачивается в один текст с подписанными ролями: CLI принимает
// вопрос целиком, и без подписей прошлые ответы читаются как часть нового.
func TestClaudeCLIPromptSeparatesSystemAndTurns(t *testing.T) {
	system, prompt := claudeCLIPrompt([]Message{
		{Role: "system", Content: "правила"},
		{Role: "user", Content: "Что в проекте?"},
		{Role: "assistant", Content: "Ветки: master."},
		{Role: "user", Content: "А тесты?"},
	})
	if system != "правила" {
		t.Fatalf("системное сообщение уехало не туда: %q", system)
	}
	for _, part := range []string{"Вопрос человека:\nЧто в проекте?", "Прошлый ответ помощника:\nВетки: master.", "Вопрос человека:\nА тесты?"} {
		if !strings.Contains(prompt, part) {
			t.Fatalf("в запросе нет части %q: %q", part, prompt)
		}
	}
	if strings.Index(prompt, "А тесты?") < strings.Index(prompt, "Что в проекте?") {
		t.Fatal("порядок реплик перевёрнут")
	}
}

// Пустой разговор до CLI не доходит: запускать процесс не за чем.
func TestClaudeCLIRefusesEmptyPrompt(t *testing.T) {
	err := NewClaudeCLI(Config{}).Stream(context.Background(), ModelRequest{Model: "sonnet"}, func(ModelEvent) error { return nil })
	if err == nil {
		t.Fatal("пустой запрос ушёл в CLI")
	}
}

// Установленный CLI не должен считаться отсутствующим только потому, что его
// каталога нет в PATH: установщик кладёт себя в ~/.local/bin и точку входа
// прописывает не всегда, а платить за это правкой системных переменных человеку
// незачем.
func TestClaudeCLICommandFindsNativeInstallOutsidePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv("PATH", filepath.Join(home, "empty"))
	binary := "claude"
	if runtime.GOOS == "windows" {
		binary = "claude.exe"
	}
	native := filepath.Join(home, ".local", "bin", binary)
	if err := os.MkdirAll(filepath.Dir(native), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(native, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := claudeCLICommand(); got != native {
		t.Fatalf("родная установка не найдена: %q вместо %q", got, native)
	}
}

// Когда CLI нет нигде, имя остаётся прежним: запуск провалится с понятной
// ошибкой «claude cli is not available», а не с выдуманным путём.
func TestClaudeCLICommandFallsBackToPlainName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv("PATH", filepath.Join(home, "empty"))
	if got := claudeCLICommand(); got != claudeCLIExecutable {
		t.Fatalf("без установки вернулось %q", got)
	}
}

// Ключ сессии не попадает в аргументы: их видит любой процесс на машине, и
// подсмотренный ключ — это чужие права. Конфигурация уходит файлом, доступным
// владельцу, и исчезает вместе с работой.
func TestSecretFileKeepsKeyOutOfCommandLine(t *testing.T) {
	path, cleanup, err := writeSecretFile("point-mcp-test-*.json", `{"mcpServers":{"point":{"headers":{"Authorization":"Bearer secret"}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "Bearer secret") {
		t.Fatalf("конфигурация записана неверно: %q", string(content))
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("временный файл с ключом пережил работу: %v", err)
	}
}

// Длинный разговор не должен упираться в предел командной строки: Windows
// обрывает её на 32 767 знаках, а сообщение человека Point принимает до 32 КБ.
func TestClaudeCLISendsLongPromptThroughStdin(t *testing.T) {
	// Подставной исполнитель: печатает свои аргументы и прочитанный ввод в том
	// же формате, что настоящий CLI, — так видно, что и куда уехало.
	script := writeFakeCLI(t)
	previous := claudeCLIExecutable
	claudeCLIExecutable = script
	t.Cleanup(func() { claudeCLIExecutable = previous })

	long := strings.Repeat("лог сборки, строка за строкой; ", 1200)
	var text strings.Builder
	err := NewClaudeCLI(Config{}).Stream(context.Background(), ModelRequest{
		Model:    "haiku",
		Messages: []Message{{Role: "system", Content: "правила"}, {Role: "user", Content: long}},
	}, func(event ModelEvent) error {
		if event.Kind == EventTextDelta {
			text.WriteString(event.Delta)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	answer := text.String()
	if !strings.Contains(answer, "STDIN_HAS_PROMPT") {
		t.Fatalf("разговор не ушёл в стандартный ввод: %q", answer)
	}
	if strings.Contains(answer, "ARGS_HAVE_PROMPT") {
		t.Fatal("разговор всё ещё уходит аргументом командной строки")
	}
}

// writeFakeCLI кладёт подставной исполнитель, отвечающий в формате настоящего:
// он сообщает, где оказался разговор — в аргументах или в стандартном вводе.
func writeFakeCLI(t *testing.T) string {
	t.Helper()
	source := filepath.Join(t.TempDir(), "fake_cli.go")
	program := `package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	stdin, _ := io.ReadAll(os.Stdin)
	marks := []string{}
	if len(strings.TrimSpace(string(stdin))) > 100 {
		marks = append(marks, "STDIN_HAS_PROMPT")
	}
	for _, argument := range os.Args[1:] {
		if len(argument) > 100 && strings.Contains(argument, "лог сборки") {
			marks = append(marks, "ARGS_HAVE_PROMPT")
		}
	}
	line := map[string]any{"type": "result", "subtype": "success", "result": strings.Join(marks, " ")}
	encoded, _ := json.Marshal(line)
	fmt.Println(string(encoded))
}
`
	if err := os.WriteFile(source, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(filepath.Dir(source), "fake_cli.exe")
	build := exec.Command("go", "build", "-o", binary, source)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("подставной CLI не собрался: %v\n%s", err, output)
	}
	return binary
}

// Расход у Claude Code — это не два числа. На замере из 20 132 входных токенов
// свежими были десять: считать только их значит показать человеку вчетверо
// меньше правды. Цена и пределы модели приходят там же.
func TestClaudeCLIStreamReadsCostCacheAndLimits(t *testing.T) {
	stream := `{"type":"result","subtype":"success","result":"Готово.","total_cost_usd":0.0146392,` +
		`"usage":{"input_tokens":10,"output_tokens":105,"cache_read_input_tokens":20122,"cache_creation_input_tokens":6046},` +
		`"modelUsage":{"claude-haiku-4-5":{"contextWindow":200000,"maxOutputTokens":32000}}}`
	var usage ModelEvent
	if err := claudeCLIReadStream(strings.NewReader(stream), func(event ModelEvent) error {
		if event.Kind == EventUsage {
			usage = event
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if usage.InputTokens != 10 || usage.OutputTokens != 105 {
		t.Fatalf("свежие токены прочитаны неверно: %#v", usage)
	}
	if usage.CacheReadTokens != 20122 || usage.CacheWriteTokens != 6046 {
		t.Fatalf("кэш потерян: read=%d write=%d", usage.CacheReadTokens, usage.CacheWriteTokens)
	}
	// 0,0146392 доллара — это 14 639 стотысячных: деньги считаются целыми, иначе
	// сумма по строкам разойдётся с итогом.
	if usage.CostMicroUSD != 14639 {
		t.Fatalf("стоимость посчитана неверно: %d", usage.CostMicroUSD)
	}
	if usage.ContextWindowTokens != 200000 || usage.ModelMaxOutputTokens != 32000 {
		t.Fatalf("пределы модели не прочитаны: %#v", usage)
	}
}

// Модели зовутся псевдонимами последней версии, а не перечнем версий.
//
// Перечисленные руками версии устаревают молча: список 4-й серии дожил до того,
// что CLI по тем же псевдонимам отдавал уже пятую. «opus», «sonnet» и «haiku»
// всегда указывают на последнюю доступную модель семейства — так их описывает
// сам CLI, и так они не расходятся с ним при обновлении.
func TestClaudeCLIModelsUseLatestAliases(t *testing.T) {
	script := writeFakeVersionCLI(t)
	previous := claudeCLIExecutable
	claudeCLIExecutable = script
	t.Cleanup(func() { claudeCLIExecutable = previous })

	models, err := discoverClaudeCLIModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 {
		t.Fatalf("моделей %d вместо трёх: %#v", len(models), models)
	}
	for index, want := range []string{"opus", "sonnet", "haiku"} {
		if models[index].ID != want {
			t.Fatalf("модель %d названа %q вместо %q", index, models[index].ID, want)
		}
		// Версия в имени — это привязка к поколению: она устареет вместе с кодом.
		if strings.Contains(models[index].ID, "-4-") || strings.Contains(models[index].ID, "-5") {
			t.Fatalf("в имени модели зашита версия: %q", models[index].ID)
		}
	}
	// Версия исполнителя — свойство CLI, а не модели: в названии ей не место.
	for _, model := range models {
		if strings.Contains(model.DisplayName, "2.9.9") {
			t.Fatalf("версия попала в название модели: %q", model.DisplayName)
		}
		if !strings.Contains(model.OwnedBy, "2.9.9") {
			t.Fatalf("версия не сообщена отдельным полем: %q", model.OwnedBy)
		}
	}
}

// Подставной CLI, отвечающий только на --version.
func writeFakeVersionCLI(t *testing.T) string {
	t.Helper()
	source := filepath.Join(t.TempDir(), "fake_version.go")
	program := `package main

import "fmt"

func main() { fmt.Println("2.9.9 (Claude Code)") }
`
	if err := os.WriteFile(source, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(filepath.Dir(source), "fake_version.exe")
	if output, err := exec.Command("go", "build", "-o", binary, source).CombinedOutput(); err != nil {
		t.Fatalf("подставной CLI не собрался: %v\n%s", err, output)
	}
	return binary
}
