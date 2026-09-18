//go:build !windows && !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package diagnostics

import "errors"

func AvailableDiskBytes(string) (uint64, error) {
	return 0, errors.New("free disk-space probe is unsupported on this platform")
}
