//go:build windows

package sandbox

func defaultContainerUser() string { return "10001:10001" }
