//go:build !windows

package osproc

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Корень запускает внука в фоне и ждёт. Kill обязан погасить обоих: убитый
// корень с живым внуком — ровно та утечка, ради которой группа заведена.
func TestKillStopsWholeTree(t *testing.T) {
	pidFile := t.TempDir() + "/grandchild.pid"
	command := Command("sh", "-c", "sleep 30 & echo $! > "+pidFile+"; wait")
	group, err := StartGroup(command, GroupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer group.Release()
	grandchild := waitPid(t, pidFile)
	group.Kill()
	_ = command.Wait()
	deadline := time.Now().Add(3 * time.Second)
	for alive(grandchild) {
		if time.Now().After(deadline) {
			t.Fatalf("внук %d пережил Kill группы", grandchild)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStartGroupKeepsExistingAttributesAndOptIn(t *testing.T) {
	command := Command("true")
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: false, Foreground: false}
	prepareGroup(command, GroupOptions{})
	if !command.SysProcAttr.Setpgid {
		t.Fatal("группа не выставлена")
	}
	if deathSignal(command.SysProcAttr) != 0 {
		t.Fatal("сигнал смерти родителя выставлен без DieWithParent")
	}
}

func TestKillIsSafeTwiceAndOnNil(t *testing.T) {
	var empty *Group
	empty.Kill()
	empty.Release()
	command := Command("true")
	group, err := StartGroup(command, GroupOptions{DieWithParent: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	group.Kill()
	group.Kill()
	group.Release()
	group.Release()
}

func waitPid(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw))); convErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("внук не записал свой pid")
	return 0
}

func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
