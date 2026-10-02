//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"local-agent-workbench/internal/sandboxsync"
)

func protectControl() error { return unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0) }
func filesystemSpace(root string) (sandboxsync.Space, error) {
	var value unix.Statfs_t
	err := unix.Statfs(root, &value)
	return sandboxsync.Space{Available: value.Bavail * uint64(value.Bsize), Total: value.Blocks * uint64(value.Bsize)}, err
}
func reapNamespace() error {
	if os.Getpid() != 1 {
		return errors.New("namespace reaper must be PID 1")
	}
	if err := protectControl(); err != nil {
		return err
	}
	signal.Ignore(syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGABRT)
	for {
		var status syscall.WaitStatus
		_, err := syscall.Wait4(-1, &status, syscall.WNOHANG, nil)
		if err != nil && err != syscall.ECHILD && err != syscall.EINTR {
			return err
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func resetNamespace() error {
	cmdline, err := os.ReadFile("/proc/1/cmdline")
	if err != nil {
		return err
	}
	if !strings.Contains(string(cmdline), "point-sandboxd") || !strings.Contains(string(cmdline), "--init") {
		return errors.New("reset requires a point-sandboxd-owned PID namespace")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_ = syscall.Kill(-1, syscall.SIGSTOP)
		_ = syscall.Kill(-1, syscall.SIGKILL)
		entries, err := os.ReadDir("/proc")
		if err != nil {
			return err
		}
		remaining := false
		for _, e := range entries {
			pid, err := strconv.Atoi(e.Name())
			if err != nil || pid == 1 || pid == os.Getpid() {
				continue
			}
			stat, err := os.ReadFile("/proc/" + e.Name() + "/stat")
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			end := strings.LastIndexByte(string(stat), ')')
			if end < 0 || len(stat) <= end+2 {
				return errors.New("invalid process state")
			}
			if stat[end+2] != 'Z' {
				remaining = true
			}
		}
		if !remaining {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("residual process did not terminate; recreate container")
		}
		time.Sleep(5 * time.Millisecond)
	}
	entries, err := os.ReadDir("/tmp")
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err = os.RemoveAll("/tmp/" + e.Name()); err != nil {
			return err
		}
	}
	return nil
}
func initializeOwner(root, owner string) error {
	if os.Geteuid() != 0 {
		return errors.New("ownership setup requires trusted root helper")
	}
	parts := strings.Split(owner, ":")
	if len(parts) != 2 {
		return errors.New("owner must be uid:gid")
	}
	uid, err := strconv.Atoi(parts[0])
	if err != nil || uid <= 0 {
		return errors.New("invalid owner uid")
	}
	gid, err := strconv.Atoi(parts[1])
	if err != nil || gid <= 0 {
		return errors.New("invalid owner gid")
	}
	if err = os.Chmod(root, 0755); err != nil {
		return err
	}
	if err = os.Chown(root, uid, gid); err != nil {
		return fmt.Errorf("initialize workspace owner: %w", err)
	}
	return nil
}
