// point-sandboxd is a static, image-independent workspace transport and process supervisor.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"local-agent-workbench/internal/filepolicy"
	"local-agent-workbench/internal/osproc"
	"local-agent-workbench/internal/sandboxsync"
)

func main() {
	if err := execute(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(125)
	}
}

func execute(args []string, input io.Reader, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("expected run, reset, delta, apply, clone or digest")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	root := flags.String("root", "/workspace", "workspace root")
	rules := flags.String("rules", filepolicy.Current, "file rules")
	initMode := flags.Bool("init", false, "serve as the namespace reaper")
	gatewayMode := flags.Bool("gateway", false, "serve the controlled-egress supervisor")
	from := flags.String("from", "/source", "clone source")
	full := flags.Bool("full", false, "clone dependencies and build outputs too")
	owner := flags.String("owner", "", "initialize root ownership (trusted setup only)")
	self := flags.Bool("self", false, "hash the running helper")
	space := flags.Bool("space", false, "query workspace filesystem capacity")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if !filepolicy.ValidVersion(*rules) {
		return fmt.Errorf("unknown file rules %q", *rules)
	}
	if args[0] == "run" && *initMode {
		return reapNamespace()
	}
	if args[0] == "run" && *gatewayMode {
		return serveGateway(input, output)
	}
	if err := protectControl(); err != nil {
		return err
	}
	switch args[0] {
	case "digest":
		if *space {
			value, err := filesystemSpace(*root)
			if err != nil {
				return err
			}
			return json.NewEncoder(output).Encode(value)
		}
		if *self {
			path, err := os.Executable()
			if err != nil {
				return err
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			h := sha256.New()
			if _, err = io.Copy(h, f); err != nil {
				return err
			}
			_, err = fmt.Fprintln(output, "sha256:"+hex.EncodeToString(h.Sum(nil)))
			return err
		}
		m, err := sandboxsync.Scan(context.Background(), *root, *rules)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(m)
	case "reset":
		return resetNamespace()
	case "clone":
		return cloneTree(*from, *root, *rules, *full)
	case "apply":
		if *owner != "" {
			return initializeOwner(*root, *owner)
		}
		var request sandboxsync.Request
		if err := json.NewDecoder(input).Decode(&request); err != nil {
			return err
		}
		if err := validateRequest(request); err != nil {
			return err
		}
		return sandboxsync.Apply(*root, request.Changes)
	case "delta", "run":
		var request sandboxsync.Request
		if err := json.NewDecoder(input).Decode(&request); err != nil {
			return err
		}
		if err := validateRequest(request); err != nil {
			return err
		}
		return runRequest(args[0], *root, request, output)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func validateRequest(r sandboxsync.Request) error {
	if r.Version != sandboxsync.ProtocolVersion || r.Operation == "" || !filepolicy.ValidVersion(r.Rules) || r.Rules != filepolicy.Current {
		return errors.New("invalid sandbox protocol, operation or rules")
	}
	if r.Before.Rules != "" && r.Before.Rules != r.Rules {
		return errors.New("manifest rules differ")
	}
	if r.Before.Digest != "" {
		if err := sandboxsync.ValidateManifest(r.Before); err != nil {
			return err
		}
	} else if len(r.Before.Entries) != 0 {
		return errors.New("manifest entries require a digest")
	}
	if r.TimeoutMS < 0 || r.TimeoutMS > 24*60*60*1000 {
		return errors.New("invalid command timeout")
	}
	return nil
}

type frameWriter struct {
	mu        sync.Mutex
	encoder   *json.Encoder
	operation string
	err       error
}

func (w *frameWriter) send(f sandboxsync.Frame) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	f.Version = sandboxsync.ProtocolVersion
	f.Operation = w.operation
	if w.err == nil {
		w.err = w.encoder.Encode(f)
	}
	return w.err
}

type streamWriter struct {
	channel string
	frames  *frameWriter
}

func (w streamWriter) Write(data []byte) (int, error) {
	for start := 0; start < len(data); {
		end := min(start+32*1024, len(data))
		if err := w.frames.send(sandboxsync.Frame{Kind: w.channel, Data: data[start:end]}); err != nil {
			return start, err
		}
		start = end
	}
	return len(data), nil
}

func runRequest(kind, root string, r sandboxsync.Request, out io.Writer) error {
	w := &frameWriter{encoder: json.NewEncoder(out), operation: r.Operation}
	if err := resetNamespace(); err != nil {
		return err
	}
	if err := sandboxsync.Apply(root, r.Changes); err != nil {
		return err
	}
	before := r.Before
	// The host supplies the baseline after applying its own already-audited edits.
	exit, timedOut := 0, false
	if kind == "run" {
		if r.Program == "" && r.Shell == "" {
			return errors.New("empty command")
		}
		cwd := root
		var err error
		if r.CWD != "" && r.CWD != "." {
			cwd, err = sandboxsync.Resolve(root, r.CWD)
			if err != nil {
				return err
			}
		}
		ctx := context.Background()
		cancel := func() {}
		if r.TimeoutMS > 0 {
			ctx, cancel = context.WithTimeout(ctx, time.Duration(r.TimeoutMS)*time.Millisecond)
		}
		var command *exec.Cmd
		if r.Shell != "" {
			command = osproc.CommandContext(ctx, "/bin/sh", "-lc", r.Shell)
		} else {
			command = osproc.CommandContext(ctx, r.Program, r.Arguments...)
		}
		command.Dir = cwd
		command.Env = append([]string(nil), r.Environment...)
		hasPath := false
		for _, value := range command.Env {
			if strings.HasPrefix(value, "PATH=") {
				hasPath = true
			}
		}
		if !hasPath {
			command.Env = append(command.Env, "PATH="+os.Getenv("PATH"))
		}
		command.Stdout = streamWriter{"stdout", w}
		command.Stderr = streamWriter{"stderr", w}
		command.WaitDelay = 200 * time.Millisecond
		if err = w.send(sandboxsync.Frame{Kind: "started"}); err != nil {
			cancel()
			return err
		}
		err = command.Run()
		timedOut = ctx.Err() == context.DeadlineExceeded
		cancel()
		if err != nil {
			exit = -1
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				exit = ee.ExitCode()
			} else if errors.Is(err, exec.ErrWaitDelay) && command.ProcessState != nil {
				// Detached descendants can keep output pipes open after their
				// parent exits. Reset kills them; retain the parent's real status.
				exit = command.ProcessState.ExitCode()
			}
			if command.ProcessState == nil {
				_ = w.send(sandboxsync.Frame{Kind: "stderr", Data: []byte(err.Error())})
			}
		}
	}
	if err := resetNamespace(); err != nil {
		return err
	}
	after, err := sandboxsync.Scan(context.Background(), root, r.Rules)
	if err != nil {
		return err
	}
	for _, change := range sandboxsync.Changes(before, after) {
		c, err := sandboxsync.ReadChange(root, change)
		if err != nil {
			return err
		}
		if err = w.send(sandboxsync.Frame{Kind: "change", Change: &c}); err != nil {
			return err
		}
	}
	return w.send(sandboxsync.Frame{Kind: "result", Manifest: &after, ExitCode: exit, TimedOut: timedOut})
}

func cloneTree(src, dst, rules string, full bool) error {
	if !full {
		m, err := sandboxsync.Scan(context.Background(), src, rules)
		if err != nil {
			return err
		}
		for _, e := range m.Entries {
			c, err := sandboxsync.ReadChange(src, sandboxsync.Change{Entry: e})
			if err != nil {
				return err
			}
			if err = sandboxsync.Apply(dst, []sandboxsync.Change{c}); err != nil {
				return err
			}
		}
		return nil
	}
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm()|0700)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("cannot clone special file %s", rel)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
}
