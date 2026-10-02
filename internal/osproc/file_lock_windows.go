//go:build windows

package osproc

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

func IsLockBusy(err error) bool { return errors.Is(err, windows.ERROR_LOCK_VIOLATION) }

// LockFile takes a nonblocking process-owned lease. The OS releases it on crash.
func LockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	var overlapped windows.Overlapped
	if err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() { _ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlapped); _ = f.Close() }, nil
}
