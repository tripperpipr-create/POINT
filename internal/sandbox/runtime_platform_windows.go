//go:build windows

package sandbox

import (
	"errors"
	"golang.org/x/sys/windows"
	"runtime"
)

func runtimePlatformCheck() error {
	if runtime.GOARCH != "amd64" || windows.RtlGetVersion().BuildNumber < 22000 {
		return errors.New("embedded runtime requires Windows 11 x64")
	}
	return nil
}
