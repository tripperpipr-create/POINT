//go:build windows

package sandbox

import (
	"sync/atomic"

	"golang.org/x/sys/windows"
)

type mirrorWatcher interface {
	Dirty() bool
	Close()
}
type windowsMirrorWatcher struct {
	handle windows.Handle
	dirty  atomic.Bool
	failed atomic.Bool
}

func newMirrorWatcher(root string) mirrorWatcher {
	path, err := windows.UTF16PtrFromString(root)
	w := &windowsMirrorWatcher{handle: windows.InvalidHandle}
	w.dirty.Store(true)
	if err != nil {
		w.failed.Store(true)
		return w
	}
	w.handle, err = windows.CreateFile(path, windows.FILE_LIST_DIRECTORY, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		w.failed.Store(true)
		return w
	}
	go func() {
		buf := make([]byte, 64*1024)
		for {
			var count uint32
			err := windows.ReadDirectoryChanges(w.handle, &buf[0], uint32(len(buf)), true, windows.FILE_NOTIFY_CHANGE_FILE_NAME|windows.FILE_NOTIFY_CHANGE_DIR_NAME|windows.FILE_NOTIFY_CHANGE_SIZE|windows.FILE_NOTIFY_CHANGE_LAST_WRITE|windows.FILE_NOTIFY_CHANGE_ATTRIBUTES, &count, nil, 0)
			if err != nil {
				w.failed.Store(true)
				return
			}
			w.dirty.Store(true)
		}
	}()
	return w
}
func (w *windowsMirrorWatcher) Dirty() bool { return w.failed.Load() || w.dirty.Swap(false) }
func (w *windowsMirrorWatcher) Close() {
	if w.handle != windows.InvalidHandle {
		_ = windows.CancelIoEx(w.handle, nil)
		_ = windows.CloseHandle(w.handle)
	}
}
