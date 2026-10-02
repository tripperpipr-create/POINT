// Package gitflow — детерминированная часть git-агента Point: осмотр
// репозиториев перед нарядом, решение о ветке, переключение при утверждении,
// коммит итога и отправка. Тексты (имя ветки по ТЗ, сообщение коммита)
// пишет модель в orchestrator; здесь только их запасные шаблоны.
//
// Ничего здесь не делает push сам по себе: отправку вызывает только явное
// действие человека.
package gitflow

import (
	"context"
	"fmt"
	"os"
	"strings"

	"local-agent-workbench/internal/osproc"
)

// Runner исполняет git в каталоге. Тесты подставляют свой.
type Runner interface {
	Run(ctx context.Context, dir string, args ...string) ([]byte, error)
}

// ExecRunner — настоящий git. Терминальных запросов пароля нет: ядро не
// интерактивно, а учётные данные даёт credential manager самого git.
type ExecRunner struct {
	// Env — добавки к окружению; авторство коммита берётся из git config
	// человека, сюда его не подставляют.
	Env []string
}

func (r ExecRunner) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := osproc.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"), r.Env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(out))
		if len(message) > 2048 {
			message = message[:2048]
		}
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		if message != "" {
			return out, fmt.Errorf("git %s: %w: %s", name, err, message)
		}
		return out, fmt.Errorf("git %s: %w", name, err)
	}
	return out, nil
}

func run(ctx context.Context, runner Runner, dir string, args ...string) (string, error) {
	out, err := runner.Run(ctx, dir, args...)
	return strings.TrimSpace(string(out)), err
}
