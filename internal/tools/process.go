package tools

import (
	"context"
	"os/exec"

	"local-agent-workbench/internal/osproc"
)

// runProcess ждёт процесс и при отмене гасит его вместе с потомками. Дерево
// собирает osproc.StartGroup — одна реализация на всё ядро (раньше у этого
// файла были свои копии для Unix и Windows).
func runProcess(ctx context.Context, command *exec.Cmd) error {
	group, err := osproc.StartGroup(command, osproc.GroupOptions{})
	if err != nil {
		return err
	}
	defer group.Release()
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		group.Kill()
		<-done
		return ctx.Err()
	}
}
