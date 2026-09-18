package providers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"local-agent-workbench/internal/osproc"
	"local-agent-workbench/internal/observability"
	"local-agent-workbench/internal/security"
)

// Claude Code отвечает локальным процессом. Ключа у такого подключения нет:
// вход человек уже сделал в самом CLI, и Point не видит и не хранит его учётные
// данные — в этом и смысл подключения «через CLI, а не через API».
//
// Инструменты здесь чужие: Claude Code исполняет свои сам и наружу их не
// отдаёт. Поэтому читающие инструменты Point ему не объявляются, а сам он
// ограничен чтением проекта — файлы, поиск и справка git. Запись, правка и
// произвольные команды запрещены явным списком: помощник Point советует, а не
// меняет.
type ClaudeCLI struct {
	config Config
}

func NewClaudeCLI(config Config) *ClaudeCLI { return &ClaudeCLI{config: config} }

// Граница проходит по существу дела, а не по скорости. Читать и искать Claude
// Code волен своими руками: это быстро, ничего не меняет и ничем не рискует.
// Всё остальное — правка файлов, изменения в репозитории, базы, сеть,
// инфраструктура — идёт только через инструменты Point: там записаны права
// сущности, там же подтверждения и учёт.
//
// Git перечислен по подкомандам: Bash без ограничения — это любая команда.
var claudeCLINativeTools = []string{
	"Read", "Grep", "Glob",
	"Bash(git log:*)", "Bash(git show:*)", "Bash(git diff:*)",
	"Bash(git status:*)", "Bash(git branch:*)", "Bash(git tag:*)",
}

// Что запрещено всегда, даже если появится в настройках CLI по умолчанию.
var claudeCLIDeniedTools = []string{"Write", "Edit", "NotebookEdit", "WebFetch", "WebSearch", "Task"}

// claudeCLIExecutable — имя запускаемого файла. Отдельная переменная нужна
// тестам: подменить её дешевле, чем PATH всего процесса.
var claudeCLIExecutable = "claude"

// Куда установщик Claude Code кладёт себя, не спрашивая PATH. Точку входа он
// прописывает не всегда, и человек остаётся с установленным CLI, которого
// «нет»: сообщение об этом честное, но чинится оно правкой системных
// переменных — платой, которой можно не брать. Ядро смотрит сюда само.
func claudeCLICommand() string {
	if path, err := exec.LookPath(claudeCLIExecutable); err == nil {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return claudeCLIExecutable
	}
	for _, candidate := range []string{
		filepath.Join(home, ".local", "bin", "claude.exe"),
		filepath.Join(home, ".local", "bin", "claude"),
		filepath.Join(home, ".claude", "local", "claude.exe"),
		filepath.Join(home, ".claude", "local", "claude"),
	} {
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate
		}
	}
	return claudeCLIExecutable
}

func (c *ClaudeCLI) Stream(ctx context.Context, request ModelRequest, onEvent func(ModelEvent) error) error {
	log := observability.From(ctx)
	system, prompt := claudeCLIPrompt(request.Messages)
	if strings.TrimSpace(prompt) == "" {
		return errors.New("claude cli request has no user message")
	}
	// Разговор уходит в стандартный ввод, а не аргументом. Командная строка
	// Windows обрывается на 32 767 знаках, а одно сообщение человека Point
	// принимает до 32 КБ: вставленный лог сам по себе съедал бы весь лимит, и
	// запуск падал бы до модели с невнятной ошибкой.
	args := []string{"--print", "--output-format", "stream-json", "--verbose", "--include-partial-messages"}
	if model := strings.TrimSpace(request.Model); model != "" {
		args = append(args, "--model", model)
	}
	// Системное сообщение обычно идёт своим флагом: так исполнитель отличает
	// правила от вопроса. Но и оно живёт в командной строке, поэтому длинное
	// уезжает в стандартный ввод вместе с разговором — правила лучше прочитать
	// в начале текста, чем не запуститься вовсе.
	stdin := prompt
	if trimmed := strings.TrimSpace(system); trimmed != "" {
		if len([]rune(trimmed)) <= claudeCLISystemArgumentLimit {
			args = append(args, "--append-system-prompt", trimmed)
		} else {
			stdin = trimmed + "\n\n" + prompt
		}
	}
	args = append(args, "--allowed-tools", strings.Join(c.allowedTools(), ","))
	args = append(args, "--disallowed-tools", strings.Join(claudeCLIDeniedTools, ","))
	// Конфигурация MCP несёт ключ сессии, а аргументы командной строки на этой
	// машине читает любой процесс. Поэтому она уходит файлом, доступным только
	// владельцу, и удаляется сразу после работы.
	if config := strings.TrimSpace(c.config.MCPConfigJSON); config != "" {
		path, cleanup, configErr := writeSecretFile("point-mcp-*.json", config)
		if configErr != nil {
			return fmt.Errorf("prepare mcp config: %w", configErr)
		}
		defer cleanup()
		args = append(args, "--mcp-config", path)
	}
	command := osproc.CommandContext(ctx, claudeCLICommand(), args...)
	command.Dir = c.config.WorkingDir
	command.Stdin = strings.NewReader(stdin)
	stdout, pipeErr := command.StdoutPipe()
	if pipeErr != nil {
		return fmt.Errorf("claude cli stdout: %w", pipeErr)
	}
	var stderr strings.Builder
	command.Stderr = &stderr
	log.Info("provider claude cli request",
		"model", request.Model,
		"message_count", len(request.Messages),
		"prompt_bytes", len(prompt),
		"system_bytes", len(system),
		"stdin_bytes", len(stdin),
		"has_working_dir", c.config.WorkingDir != "",
	)
	if startErr := command.Start(); startErr != nil {
		return fmt.Errorf("claude cli is not available: %w", startErr)
	}
	streamErr := claudeCLIReadStream(stdout, onEvent)
	waitErr := command.Wait()
	if streamErr != nil {
		return streamErr
	}
	if waitErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = waitErr.Error()
		}
		log.Warn("provider claude cli failed", "error", security.Redact(detail))
		return fmt.Errorf("claude cli failed: %s", observability.Snippet(security.Redact(detail), 400))
	}
	return nil
}

// claudeCLIPrompt разворачивает разговор в один текст: CLI принимает вопрос
// целиком, а не список сообщений. Роли подписаны явно — иначе прошлые ответы
// читаются как часть нового вопроса.
func claudeCLIPrompt(messages []Message) (system string, prompt string) {
	systems := make([]string, 0, 2)
	turns := make([]string, 0, len(messages))
	for _, message := range messages {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		switch message.Role {
		case "system":
			systems = append(systems, content)
		case "assistant":
			turns = append(turns, "Прошлый ответ помощника:\n"+content)
		case "user":
			turns = append(turns, "Вопрос человека:\n"+content)
		}
	}
	return strings.Join(systems, "\n\n"), strings.Join(turns, "\n\n")
}

// Событие потока Claude Code. Разбираем только то, что нужно чату: текст ответа
// и расход токенов. Незнакомые виды пропускаются молча — формат живёт своей
// жизнью, и падать из-за нового поля значит ломаться на обновлении CLI.
type claudeCLIEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Result  string `json:"result"`
	Message struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage claudeCLIUsage `json:"usage"`
	} `json:"message"`
	Event struct {
		Type  string `json:"type"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`
	Usage claudeCLIUsage `json:"usage"`
	// Стоимость обращения и пределы модели Claude Code сообщает сам. Без них
	// расход виден вчетверо меньше настоящего: на замере из 20 132 входных
	// токенов свежими были десять, остальное пришло из кэша.
	TotalCostUSD float64                   `json:"total_cost_usd"`
	ModelUsage   map[string]claudeCLIModel `json:"modelUsage"`
}

type claudeCLIModel struct {
	ContextWindow   int `json:"contextWindow"`
	MaxOutputTokens int `json:"maxOutputTokens"`
}

type claudeCLIUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

func claudeCLIReadStream(stdout io.Reader, onEvent func(ModelEvent) error) error {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	streamed := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var event claudeCLIEvent
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		switch event.Type {
		case "stream_event":
			// Частичный текст. Он же придёт целиком в assistant-сообщении,
			// поэтому дальше по потоку повторять его нельзя.
			if event.Event.Delta.Text != "" {
				streamed = true
				if err := onEvent(ModelEvent{Kind: EventTextDelta, Delta: event.Event.Delta.Text}); err != nil {
					return err
				}
			}
		case "assistant":
			if streamed {
				continue
			}
			for _, block := range event.Message.Content {
				if block.Type == "text" && block.Text != "" {
					if err := onEvent(ModelEvent{Kind: EventTextDelta, Delta: block.Text}); err != nil {
						return err
					}
				}
			}
		case "result":
			usage := event.Usage
			if usage.InputTokens == 0 && usage.OutputTokens == 0 {
				usage = event.Message.Usage
			}
			// Пределы берутся у той модели, которая отвечала: их сообщает сам
			// исполнитель, и подставлять сюда чужие числа из каталога значит
			// показать человеку не тот предел.
			window, maxOutput := 0, 0
			for _, model := range event.ModelUsage {
				if model.ContextWindow > window {
					window = model.ContextWindow
				}
				if model.MaxOutputTokens > maxOutput {
					maxOutput = model.MaxOutputTokens
				}
			}
			if usage.InputTokens > 0 || usage.OutputTokens > 0 || event.TotalCostUSD > 0 {
				if err := onEvent(ModelEvent{
					Kind: EventUsage, InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
					CacheReadTokens: usage.CacheReadInputTokens, CacheWriteTokens: usage.CacheCreationInputTokens,
					CostMicroUSD:        int(event.TotalCostUSD*1_000_000 + 0.5),
					ContextWindowTokens: window, ModelMaxOutputTokens: maxOutput,
				}); err != nil {
					return err
				}
			}
			if !streamed && event.Result != "" {
				if err := onEvent(ModelEvent{Kind: EventTextDelta, Delta: event.Result}); err != nil {
					return err
				}
			}
			if event.Subtype != "" && event.Subtype != "success" {
				return fmt.Errorf("claude cli returned %s", event.Subtype)
			}
		}
	}
	return scanner.Err()
}

// Модели называются псевдонимами, а не версиями. Так их называет сам CLI:
// «opus», «sonnet» и «haiku» указывают на последнюю доступную модель семейства,
// и список не устаревает вместе с нашим кодом. Прошлая попытка перечислить
// версии руками выдала claude-opus-4-1 и claude-sonnet-4-5 — на поколение
// позади того, что CLI отдаёт по тем же псевдонимам сегодня.
//
// Спросить у него точные имена можно только запросом к модели, то есть за
// деньги человека, а локального списка у CLI нет. Пределы Point узнаёт иначе —
// из первого же ответа, где исполнитель сообщает окно контекста и потолок.
//
// Проверять здесь нечего, кроме одного: на месте ли сам CLI и отвечает ли он.
// Версия уходит отдельным полем, а не в название модели: она свойство
// исполнителя, и в списке моделей ей не место.
func discoverClaudeCLIModels(ctx context.Context) ([]ModelInfo, error) {
	command := osproc.CommandContext(ctx, claudeCLICommand(), "--version")
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("claude cli is not available: %w", err)
	}
	version := strings.TrimSpace(string(output))
	observability.From(ctx).Info("provider claude cli discovered", "version", observability.Snippet(version, 80))
	owner := "Claude Code " + version
	return []ModelInfo{
		{ID: "opus", DisplayName: "Opus — последняя версия", OwnedBy: owner},
		{ID: "sonnet", DisplayName: "Sonnet — последняя версия", OwnedBy: owner},
		{ID: "haiku", DisplayName: "Haiku — последняя версия", OwnedBy: owner},
	}, nil
}

// allowedTools собирает разрешённое для одного вызова: нативное чтение плюс
// поимённо те инструменты Point, что выданы сущности. Шаблоном `mcp__point__*`
// это не делается: шаблон переживёт появление нового инструмента в реестре и
// выдаст его молча, а права должны меняться только вместе с решением о них.
func (c *ClaudeCLI) allowedTools() []string {
	allowed := append([]string(nil), claudeCLINativeTools...)
	for _, tool := range c.config.MCPTools {
		if name := strings.TrimSpace(tool); name != "" {
			allowed = append(allowed, mcpToolPattern(name))
		}
	}
	return allowed
}

// mcpToolPattern повторяет имя, под которым инструмент Point виден исполнителю.
// Пакет mcp сюда не импортируется: провайдеры не должны знать про устройство
// сервера, им нужна одна строка.
func mcpToolPattern(name string) string { return "mcp__point__" + name }

// Сколько знаков системного сообщения ещё можно отдать аргументом. Предел
// командной строки Windows — 32 767 знаков на всё вместе с флагами и путями;
// половина оставлена с запасом, остальное уходит в стандартный ввод.
const claudeCLISystemArgumentLimit = 16000

// writeSecretFile кладёт содержимое во временный файл, доступный только
// владельцу, и возвращает путь вместе с уборкой. Так передаются вещи, которым
// не место в аргументах командной строки: их видно всякому, кто перечислит
// процессы.
func writeSecretFile(pattern, content string) (string, func(), error) {
	file, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", nil, err
	}
	name := file.Name()
	cleanup := func() { _ = os.Remove(name) }
	if err = file.Chmod(0o600); err != nil && !errors.Is(err, os.ErrInvalid) {
		// Windows прав POSIX не знает: файл и так лежит в личной папке
		// пользователя, и отказ здесь не повод не работать.
		_ = err
	}
	if _, err = io.WriteString(file, content); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, err
	}
	if err = file.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return name, cleanup, nil
}
