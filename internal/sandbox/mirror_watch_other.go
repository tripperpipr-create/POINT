//go:build !windows

package sandbox

type mirrorWatcher interface {
	Dirty() bool
	Close()
}

// Polling fallback favors correctness when a native recursive observer is unavailable.
type pollingMirrorWatcher struct{}

func newMirrorWatcher(string) mirrorWatcher { return pollingMirrorWatcher{} }
func (pollingMirrorWatcher) Dirty() bool    { return true }
func (pollingMirrorWatcher) Close()         {}
