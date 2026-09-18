//go:build windows

package diagnostics

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

func AvailableDiskBytes(path string) (uint64, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	root := filepath.VolumeName(abs)
	if root == "" {
		root = abs
	} else {
		root += `\`
	}
	rootUTF16, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return 0, err
	}
	var available uint64
	if err = windows.GetDiskFreeSpaceEx(rootUTF16, &available, nil, nil); err != nil {
		return 0, err
	}
	return available, nil
}
