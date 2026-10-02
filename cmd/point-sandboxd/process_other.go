//go:build !linux

package main

import (
	"errors"
	"local-agent-workbench/internal/sandboxsync"
)

func protectControl() error { return nil }
func filesystemSpace(string) (sandboxsync.Space, error) {
	return sandboxsync.Space{}, errors.New("sandboxd capacity query requires Linux")
}
func reapNamespace() error  { return errors.New("sandboxd requires Linux") }
func resetNamespace() error { return errors.New("sandboxd reset requires Linux") }
func initializeOwner(string, string) error {
	return errors.New("sandboxd ownership setup requires Linux")
}
