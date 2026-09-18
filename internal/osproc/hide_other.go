//go:build !windows

package osproc

import "os/exec"

// На Unix консольного окна не бывает: процесс наследует терминал родителя и
// своего не создаёт. Прятать нечего, и функция намеренно пуста — так вызов
// osproc.Command остаётся одинаковым на всех платформах.
func hideWindow(*exec.Cmd) {}
