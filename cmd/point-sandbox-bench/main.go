// point-sandbox-bench measures isolated bind/volume commands with identical inputs and file auditing.
package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/filepolicy"
	"local-agent-workbench/internal/osproc"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/sandboxsync"
	"local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
)

type commands []string

func (c *commands) String() string     { return strings.Join(*c, "; ") }
func (c *commands) Set(v string) error { *c = append(*c, v); return nil }

type sample struct {
	Mode          string `json:"mode"`
	Command       string `json:"command"`
	SetupMS       int64  `json:"setupMs"`
	PreparationMS int64  `json:"preparationMs"`
	DurationMS    int64  `json:"durationMs"`
	ExitCode      int    `json:"exitCode"`
	Digest        string `json:"digest"`
	Error         string `json:"error,omitempty"`
}
type aggregate struct {
	Mode     string `json:"mode"`
	Command  string `json:"command"`
	Count    int    `json:"count"`
	MedianMS int64  `json:"medianMs"`
	P95MS    int64  `json:"p95Ms"`
}
type report struct {
	CreatedAt     time.Time   `json:"createdAt"`
	Project       string      `json:"project"`
	Revision      string      `json:"revision"`
	SourceSubdir  string      `json:"sourceSubdir,omitempty"`
	Image         string      `json:"image"`
	ImageDigest   string      `json:"imageDigest"`
	DockerVersion string      `json:"dockerVersion"`
	Rules         string      `json:"rules"`
	Audit         string      `json:"audit"`
	Samples       []sample    `json:"samples"`
	Aggregates    []aggregate `json:"aggregates"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	project := flag.String("project", ".", "portable source directory")
	revision := flag.String("revision", "", "freeze a Git revision with git archive before benchmarking")
	sourceSubdir := flag.String("source-subdir", "", "place the frozen Git tree under this portable relative directory (requires -revision)")
	image := flag.String("image", "point-agent-sandbox:1.2.2", "identical pinned Linux image for both modes")
	output := flag.String("output", "sandbox-bench.json", "JSON report")
	modes := flag.String("modes", "bind,volume", "modes to compare")
	prepare := flag.String("prepare", "", "unmeasured preparation before each non-true command, e.g. npm ci")
	hosts := flag.String("hosts", "", "comma-separated allowlisted TLS hosts; empty means deny-all")
	repetitions := flag.Int("runs", 5, "clean runs for each non-true command")
	warmRuns := flag.Int("warm-runs", 20, "warm audited true calls")
	var list commands
	flag.Var(&list, "command", "command to measure (repeatable; defaults to true)")
	flag.Parse()
	if len(list) == 0 {
		list = commands{"true"}
	}
	if *repetitions < 1 || *warmRuns < 1 {
		return fmt.Errorf("run counts must be positive")
	}
	if *sourceSubdir != "" {
		if *revision == "" {
			return fmt.Errorf("-source-subdir requires -revision")
		}
		if err := filepolicy.ValidatePath(*sourceSubdir); err != nil {
			return fmt.Errorf("source subdirectory: %w", err)
		}
	}
	abs, err := filepath.Abs(*project)
	if err != nil {
		return err
	}
	temporary, err := os.MkdirTemp("", "point-sandbox-bench-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	b := sandbox.NewContainerBackend(filepath.Join(temporary, "sandboxes"))
	b.Image = *image
	if err = b.Probe(context.Background()); err != nil {
		return err
	}
	scope := domain.NewID("bench")
	defer b.RemoveCacheVolumes(context.Background(), scope)
	r := report{CreatedAt: time.Now().UTC(), Project: abs, Image: *image, ImageDigest: b.ImageDigest, DockerVersion: b.DockerVersion, Rules: filepolicy.Current, Audit: "SHA-256 snapshots, patch diff and durable JSON journal; excludes LLM and app event bus"}
	r.SourceSubdir = *sourceSubdir
	if revision, e := osproc.Command("git", "-C", abs, "rev-parse", "HEAD").Output(); e == nil {
		r.Revision = strings.TrimSpace(string(revision))
	}
	if *revision != "" {
		resolved, e := osproc.Command("git", "-C", abs, "rev-parse", *revision+"^{commit}").Output()
		if e != nil {
			return e
		}
		r.Revision = strings.TrimSpace(string(resolved))
		archive, e := osproc.Command("git", "-C", abs, "archive", "--format=tar", r.Revision).Output()
		if e != nil {
			return e
		}
		frozen := filepath.Join(temporary, "input")
		if e = os.MkdirAll(frozen, 0755); e != nil {
			return e
		}
		sourceRoot := frozen
		if *sourceSubdir != "" {
			sourceRoot, e = sandboxsync.Resolve(frozen, *sourceSubdir)
			if e != nil {
				return e
			}
		}
		if e = os.MkdirAll(sourceRoot, 0755); e != nil {
			return e
		}
		tr := tar.NewReader(bytes.NewReader(archive))
		for {
			header, e := tr.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				return e
			}
			rel := strings.TrimSuffix(header.Name, "/")
			if rel == "" {
				continue
			}
			if filepolicy.ExcludedPath(filepolicy.Current, rel, header.Typeflag == tar.TypeDir) {
				continue
			}
			p, e := sandboxsync.Resolve(sourceRoot, rel)
			if e != nil {
				return e
			}
			if header.Typeflag == tar.TypeDir {
				if e = os.MkdirAll(p, 0755); e != nil {
					return e
				}
				continue
			}
			if header.Typeflag != tar.TypeReg {
				continue
			}
			if e = os.MkdirAll(filepath.Dir(p), 0755); e != nil {
				return e
			}
			f, e := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(header.Mode)&0777)
			if e != nil {
				return e
			}
			_, e = io.Copy(f, tr)
			closeErr := f.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
		}
		abs = frozen
	}
	policy := "DENY"
	var allowed []string
	if strings.TrimSpace(*hosts) != "" {
		policy = "ALLOWLIST"
		allowed = strings.Split(*hosts, ",")
	}
	for _, mode := range strings.Split(*modes, ",") {
		if mode != "bind" && mode != "volume" {
			return fmt.Errorf("invalid mode %q", mode)
		}
		for _, command := range list {
			count := *repetitions
			if command == "true" {
				count = *warmRuns
			}
			var record domain.SandboxRecord
			var fs *workspace.FS
			var previous *workspace.TextSnapshot
			var setup int64
			create := func() error {
				started := time.Now()
				var err error
				record, err = b.Create(context.Background(), sandbox.CreateRequest{WorkspaceID: "benchmark", QuestID: scope, WorkspacePath: abs, StorageMode: mode, FileRulesVersion: filepolicy.Current})
				if err != nil {
					return err
				}
				fs, err = workspace.Open(record.Path)
				if err != nil {
					return err
				}
				fs.FileRules = filepolicy.Current
				previous = nil
				setup = time.Since(started).Milliseconds()
				return nil
			}
			closeWorkspace := func() {
				if record.ID != "" {
					_ = b.Close(context.Background(), record, abs)
					record = domain.SandboxRecord{}
				}
			}
			auditRun := func(command string) (int64, int, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 620*time.Second)
				defer cancel()
				started := time.Now()
				var before, after workspace.TextSnapshot
				var err error
				if mode == "volume" {
					before, err = b.CaptureVolumeAudit(ctx, record.Path, previous, false)
				} else {
					before, err = fs.CaptureTextSnapshotFrom(ctx, previous)
				}
				if err != nil {
					return 0, -1, err
				}
				tool := tools.RunCommand{FS: fs, Executor: b, SandboxImage: sandbox.ExecutionImageForRecord(record), NetworkPolicy: policy, AllowedNetworkHosts: allowed, QuestID: scope, RunID: scope, Authoritative: true, DefaultTimeout: 600 * time.Second, MaxOutput: 128 * 1024}
				raw, _ := json.Marshal(map[string]any{"command": command, "timeoutSeconds": 600, "reason": "sandbox benchmark"})
				result := tool.Execute(ctx, raw)
				if mode == "volume" {
					after, err = b.CaptureVolumeAudit(context.Background(), record.Path, &before, true)
				} else {
					after, err = fs.CaptureTextSnapshotFrom(context.Background(), &before)
				}
				if err != nil {
					return 0, -1, err
				}
				changes := workspace.DiffTextSnapshots(before, after)
				journal, err := os.Create(filepath.Join(temporary, "audit.json"))
				if err != nil {
					return 0, -1, err
				}
				err = json.NewEncoder(journal).Encode(changes)
				if err == nil {
					err = journal.Sync()
				}
				closeErr := journal.Close()
				if err == nil {
					err = closeErr
				}
				if err != nil {
					return 0, -1, err
				}
				previous = &after
				var payload struct {
					ExitCode int    `json:"exitCode"`
					Stdout   string `json:"stdout"`
					Stderr   string `json:"stderr"`
				}
				_ = json.Unmarshal(result.Output, &payload)
				elapsed := time.Since(started).Milliseconds()
				if !result.OK || payload.ExitCode != 0 {
					message := payload.Stdout + "\n" + payload.Stderr
					if result.Error != nil {
						message = result.Error.Message + "\n" + message
					}
					return elapsed, payload.ExitCode, fmt.Errorf("%s", message)
				}
				return elapsed, payload.ExitCode, nil
			}
			// Warm the exact same quest download cache before collecting clean installation samples.
			if command == "npm ci" {
				if err = create(); err != nil {
					return err
				}
				_, _, warmErr := auditRun(command)
				closeWorkspace()
				if warmErr != nil {
					return warmErr
				}
			}
			if command == "true" {
				if err = create(); err != nil {
					return err
				}
				if _, _, err = auditRun("true"); err != nil {
					closeWorkspace()
					return err
				}
			}
			for n := 0; n < count; n++ {
				preparation := int64(0)
				if command != "true" {
					if err = create(); err != nil {
						return err
					}
					if *prepare != "" && command != "npm ci" {
						preparation, _, err = auditRun(*prepare)
						if err != nil {
							closeWorkspace()
							return err
						}
					} else {
						if _, _, err = auditRun("true"); err != nil {
							closeWorkspace()
							return err
						}
					}
				}
				elapsed, exit, runErr := auditRun(command)
				digest, _ := sandbox.TreeDigestWithRules(record.Path, filepolicy.Current)
				s := sample{Mode: mode, Command: command, SetupMS: setup, PreparationMS: preparation, DurationMS: elapsed, ExitCode: exit, Digest: digest}
				if runErr != nil {
					s.Error = runErr.Error()
				}
				r.Samples = append(r.Samples, s)
				fmt.Fprintf(os.Stderr, "%s %s %d/%d: %d ms, exit %d\n", mode, command, n+1, count, elapsed, exit)
				if command != "true" {
					closeWorkspace()
				}
				if runErr != nil {
					closeWorkspace()
					_ = writeReport(*output, r)
					return runErr
				}
			}
			closeWorkspace()
		}
	}
	for _, mode := range strings.Split(*modes, ",") {
		for _, command := range list {
			var values []int64
			for _, s := range r.Samples {
				if s.Mode == mode && s.Command == command && s.ExitCode == 0 && s.Error == "" {
					values = append(values, s.DurationMS)
				}
			}
			sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
			if len(values) > 0 {
				median := values[len(values)/2]
				if len(values)%2 == 0 {
					median = (values[len(values)/2-1] + median) / 2
				}
				r.Aggregates = append(r.Aggregates, aggregate{Mode: mode, Command: command, Count: len(values), MedianMS: median, P95MS: values[(95*len(values)+99)/100-1]})
			}
		}
	}
	return writeReport(*output, r)
}
func writeReport(path string, r report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}
