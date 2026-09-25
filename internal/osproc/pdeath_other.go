//go:build !linux && !windows

package osproc

import "syscall"

// Сигнала смерти родителя здесь нет; остаётся закрытый stdin (см. GroupOptions).
func setParentDeathSignal(*syscall.SysProcAttr) {}

func deathSignal(*syscall.SysProcAttr) syscall.Signal { return 0 }
