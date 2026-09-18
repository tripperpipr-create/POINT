//go:build !windows

package sandbox

import (
	"os"
	"strconv"
)

func defaultContainerUser() string {
	uid, gid := os.Geteuid(), os.Getegid()
	if uid == 0 || gid == 0 {
		return "10001:10001"
	}
	return strconv.Itoa(uid) + ":" + strconv.Itoa(gid)
}
