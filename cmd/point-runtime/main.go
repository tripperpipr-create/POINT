package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"time"

	"local-agent-workbench/internal/osproc"
	"local-agent-workbench/internal/sandbox"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "point-runtime: install|status|exec|idle -manifest <runtime.json>")
		os.Exit(2)
	}
	operation := os.Args[1]
	flags := flag.NewFlagSet(operation, flag.ExitOnError)
	manifest := flags.String("manifest", os.Getenv("POINT_EMBEDDED_RUNTIME"), "local pinned runtime pack manifest")
	digest := flags.String("digest", "", "pinned manifest digest")
	_ = flags.Parse(os.Args[2:])
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	m, actual, err := sandbox.ReadRuntimeManifest(*manifest)
	if err != nil {
		fail(err)
	}
	if *digest != "" && *digest != actual {
		fail(fmt.Errorf("pinned runtime manifest changed"))
	}
	switch operation {
	case "install":
		err = sandbox.ProvisionRuntime(ctx, *manifest)
		if err == nil {
			fmt.Println("Pinned runtime provisioned; run status to verify isolation")
		}
	case "status":
		_ = os.Setenv("POINT_EMBEDDED_RUNTIME", *manifest)
		base, _ := sandbox.RuntimeControlPath(actual)
		backend, createErr := sandbox.NewEmbeddedBackend(filepath.Join(base, "sandboxes"))
		if createErr != nil {
			fail(createErr)
		}
		_ = json.NewEncoder(os.Stdout).Encode(backend.Capabilities())
		if reason := backend.Unavailable(); reason != "" {
			err = fmt.Errorf("%s", reason)
		}
	case "exec":
		if len(flags.Args()) == 0 {
			fail(fmt.Errorf("engine operation required"))
		}
		startIdleMonitor(*manifest, actual)
		err = sandbox.RunRuntimeCommand(ctx, *manifest, actual, flags.Args(), os.Stdin, os.Stdout, os.Stderr)
	case "idle":
		base, pathErr := sandbox.RuntimeControlPath(actual)
		if pathErr != nil {
			fail(pathErr)
		}
		if mkErr := os.MkdirAll(base, 0700); mkErr != nil {
			fail(mkErr)
		}
		unlock, lockErr := osproc.LockFile(filepath.Join(base, "idle-monitor.lock"))
		if lockErr != nil {
			return
		}
		defer unlock()
		keeperDone := make(chan error, 1)
		startKeeper := func() error {
			keeper, keeperErr := sandbox.StartRuntimeKeeper(ctx, *manifest, actual)
			if keeperErr != nil {
				return keeperErr
			}
			go func() { keeperDone <- keeper.Wait() }()
			return nil
		}
		if err = startKeeper(); err != nil {
			fail(err)
		}
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case keeperErr := <-keeperDone:
				if keeperErr != nil {
					return
				}
				if err = startKeeper(); err != nil {
					return
				}
			case now := <-ticker.C:
				stopCtx, stopCancel := context.WithTimeout(ctx, 20*time.Second)
				stopped, stopErr := sandbox.StopIdleRuntime(stopCtx, *manifest, actual, now)
				stopCancel()
				if stopped || stopErr != nil {
					return
				}
			}
		}
	default:
		err = fmt.Errorf("unsupported operation %q for %s", operation, m.Engine)
	}
	if err != nil {
		fail(err)
	}
}

func startIdleMonitor(manifest, digest string) {
	base, err := sandbox.RuntimeControlPath(digest)
	if err != nil {
		return
	}
	unlock, err := osproc.LockFile(filepath.Join(base, "idle-monitor.lock"))
	if err != nil {
		return
	}
	unlock()
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := osproc.Command(exe, "idle", "-manifest", manifest, "-digest", digest)
	// No inherited pipes: the monitor must not hold an engine stream open.
	if cmd.Start() == nil {
		go func() { _ = cmd.Wait() }()
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "point-runtime:", err)
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() > 0 {
		os.Exit(exit.ExitCode())
	}
	os.Exit(1)
}
