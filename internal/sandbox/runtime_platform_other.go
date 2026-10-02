//go:build !windows

package sandbox

import "errors"

func runtimePlatformCheck() error { return errors.New("embedded runtime v1 requires Windows 11 x64") }
