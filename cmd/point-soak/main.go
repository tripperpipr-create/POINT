package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/storage"
	"local-agent-workbench/internal/workspace"
)

type soakFile struct {
	SchemaVersion  int                    `json:"schemaVersion"`
	Profiles       map[string]soakProfile `json:"profiles"`
	RequiredFaults []string               `json:"requiredFaults"`
}

type soakProfile struct {
	ReleaseQualifying     bool    `json:"releaseQualifying"`
	DurationSeconds       int     `json:"durationSeconds"`
	FixtureFiles          int     `json:"fixtureFiles"`
	Events                int     `json:"events"`
	Runs                  int     `json:"runs"`
	FlowExecutions        int     `json:"flowExecutions"`
	ConcurrentFullRuns    int     `json:"concurrentFullRuns"`
	RestartEverySeconds   int     `json:"restartEverySeconds"`
	MemoryGrowthPercent   float64 `json:"memoryGrowthPercent"`
	HistoryReopenMs       int64   `json:"historyReopenMs"`
	DatabaseMaxBytes      int64   `json:"databaseMaxBytes"`
	DatabaseBytesPerEvent float64 `json:"databaseBytesPerEvent"`
	DesktopProbeEverySec  int     `json:"desktopProbeEverySeconds"`
	HubCyclesPerProbe     int     `json:"hubCyclesPerProbe"`
}

type fullRunEvidence struct {
	Requested      int      `json:"requested"`
	Completed      int      `json:"completed"`
	MaxConcurrent  int64    `json:"maxConcurrent"`
	DurationMs     int64    `json:"durationMs"`
	TerminalEvents int      `json:"terminalEvents"`
	Cancelled      int      `json:"cancelled"`
	CancelEvents   int      `json:"cancelTerminalEvents"`
	TimedOut       int      `json:"providerTimedOut"`
	TimeoutEvents  int      `json:"providerTimeoutTerminalEvents"`
	Failures       []string `json:"failures"`
}

type faultEvidence struct {
	CoreRestarts             int  `json:"coreRestarts"`
	DiskLowWriteRefused      bool `json:"diskLowWriteRefused"`
	TemporaryDiskWriteDenied bool `json:"temporaryDiskWriteDenied"`
	DiskWriteRecovered       bool `json:"diskWriteRecovered"`
	RunCancellation          bool `json:"runCancellation"`
	ProviderTimeout          bool `json:"providerTimeout"`
	InterruptedRunRecovery   bool `json:"interruptedRunRecovery"`
	CodeOSSRestarts          int  `json:"codeOSSRestarts"`
	DockerRestarts           int  `json:"dockerRestarts"`
}

type recoveryEvidence struct {
	Runs       int `json:"runs"`
	FlowRuns   int `json:"flowRuns"`
	Executions int `json:"executions"`
}

type soakResult struct {
	SchemaVersion          int              `json:"schemaVersion"`
	Profile                string           `json:"profile"`
	ReleaseProfile         bool             `json:"releaseProfile"`
	StartedAt              time.Time        `json:"startedAt"`
	FinishedAt             time.Time        `json:"finishedAt"`
	DurationSeconds        float64          `json:"durationSeconds"`
	FixtureFiles           int              `json:"fixtureFiles"`
	FixtureBytes           int64            `json:"fixtureBytes"`
	IndexFiles             int              `json:"indexFiles"`
	IndexChunks            int              `json:"indexChunks"`
	IndexPartial           bool             `json:"indexPartial"`
	IndexBuildMs           int64            `json:"indexBuildMs"`
	Runs                   int              `json:"runs"`
	Events                 int              `json:"events"`
	TerminalEvents         int              `json:"terminalEvents"`
	FlowExecutions         int              `json:"flowExecutions"`
	ConcurrentWriters      int64            `json:"concurrentWriters"`
	FullRuns               fullRunEvidence  `json:"fullRuns"`
	Faults                 faultEvidence    `json:"faults"`
	InterruptedRecovery    recoveryEvidence `json:"interruptedRecovery"`
	SQLiteIntegrity        string           `json:"sqliteIntegrity"`
	ForeignKeyViolations   int              `json:"foreignKeyViolations"`
	OrphanExecutions       int              `json:"orphanExecutions"`
	LostTerminalEvents     int              `json:"lostTerminalEvents"`
	DatabaseBytes          int64            `json:"databaseBytes"`
	DatabaseBytesPerEvent  float64          `json:"databaseBytesPerEvent"`
	HistoryReopenMs        int64            `json:"historyReopenMs"`
	BaselineHeapMB         float64          `json:"baselineHeapMB"`
	MaximumHeapMB          float64          `json:"maximumHeapMB"`
	MemoryGrowthPercent    float64          `json:"memoryGrowthPercent"`
	CoreCriteriaPassed     bool             `json:"coreCriteriaPassed"`
	ExternalFaultsVerified bool             `json:"externalFaultsVerified"`
	ReleaseQualified       bool             `json:"releaseQualified"`
	Failures               []string         `json:"failures"`
}

func main() {
	configPath := flag.String("config", "distribution/soak-profile.json", "soak profile JSON")
	profileName := flag.String("profile", "quick", "quick, eight-hour, or twenty-four-hour")
	outputPath := flag.String("output", "", "optional report path")
	flag.Parse()

	result, err := runSoak(context.Background(), *configPath, *profileName)
	if result.Failures == nil {
		result.Failures = []string{}
	}
	encoded, _ := json.MarshalIndent(result, "", "  ")
	encoded = append(encoded, '\n')
	if *outputPath != "" {
		if writeErr := writeReport(*outputPath, encoded); writeErr != nil && err == nil {
			err = writeErr
		}
	}
	_, _ = os.Stdout.Write(encoded)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func writeReport(path string, encoded []byte) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return err
	}
	// Reports are explicitly repeatable at one configured path. A direct write
	// avoids os.Rename's platform-dependent refusal to replace an existing file
	// on Windows; the JSON is generated completely before this function runs.
	return os.WriteFile(absolute, encoded, 0o600)
}

func readProfile(path, name string) (soakProfile, []string, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return soakProfile{}, nil, err
	}
	var cfg soakFile
	if err = json.Unmarshal(encoded, &cfg); err != nil {
		return soakProfile{}, nil, err
	}
	profile, ok := cfg.Profiles[name]
	if cfg.SchemaVersion != 1 || !ok {
		return soakProfile{}, nil, errors.New("unknown or invalid soak profile")
	}
	if profile.DurationSeconds <= 0 || profile.FixtureFiles <= 0 || profile.Events <= 0 || profile.Runs <= 0 ||
		profile.FlowExecutions <= 0 || profile.ConcurrentFullRuns < 4 || profile.RestartEverySeconds <= 0 ||
		profile.Events%profile.Runs != 0 || profile.DatabaseMaxBytes <= 0 || profile.DatabaseBytesPerEvent <= 0 ||
		profile.DesktopProbeEverySec <= 0 || profile.HubCyclesPerProbe <= 0 {
		return soakProfile{}, nil, errors.New("invalid soak profile limits")
	}
	if profile.ReleaseQualifying && (profile.DurationSeconds < 8*60*60 || profile.FixtureFiles < 50_000 || profile.Events < 100_000 ||
		profile.Runs < 5_000 || profile.FlowExecutions < 2_000 || profile.MemoryGrowthPercent > 10 ||
		profile.HistoryReopenMs > 5_000 || profile.DatabaseMaxBytes > 128*1024*1024 || profile.DatabaseBytesPerEvent > 512 ||
		profile.DesktopProbeEverySec > 60*60 || profile.HubCyclesPerProbe < 2) {
		return soakProfile{}, nil, errors.New("release soak profile is weaker than the production objective")
	}
	return profile, cfg.RequiredFaults, nil
}

func runSoak(ctx context.Context, configPath, profileName string) (soakResult, error) {
	profile, _, err := readProfile(configPath, profileName)
	if err != nil {
		return soakResult{}, err
	}
	root, err := os.MkdirTemp("", "point-soak-")
	if err != nil {
		return soakResult{}, err
	}
	defer os.RemoveAll(root)
	started := time.Now().UTC()
	result := soakResult{SchemaVersion: 1, Profile: profileName, ReleaseProfile: profile.ReleaseQualifying, StartedAt: started, Failures: []string{}}

	workspaceRoot := filepath.Join(root, "workspace")
	if err = os.MkdirAll(workspaceRoot, 0o700); err != nil {
		return result, err
	}
	if err = os.WriteFile(filepath.Join(workspaceRoot, "README.md"), []byte("# Point soak fixture\n"), 0o600); err != nil {
		return result, err
	}
	result.FullRuns, err = runConcurrentFullRuns(ctx, filepath.Join(root, "full-runs"), workspaceRoot, profile.ConcurrentFullRuns)
	if err != nil {
		result.Failures = append(result.Failures, "full concurrent runs: "+err.Error())
	}
	result.Faults.RunCancellation = result.FullRuns.Cancelled == 1 && result.FullRuns.CancelEvents == 1
	result.Faults.ProviderTimeout = result.FullRuns.TimedOut == 1 && result.FullRuns.TimeoutEvents == 1
	if err = os.Remove(filepath.Join(workspaceRoot, "README.md")); err != nil {
		return result, err
	}

	result.FixtureBytes, err = writeFixture(workspaceRoot, profile.FixtureFiles)
	if err != nil {
		return result, err
	}
	result.FixtureFiles = profile.FixtureFiles
	filesystem, err := workspace.Open(workspaceRoot)
	if err != nil {
		return result, err
	}
	indexStarted := time.Now()
	indexStatus, err := filesystem.BuildIndex(ctx)
	result.IndexBuildMs = time.Since(indexStarted).Milliseconds()
	if err != nil {
		return result, err
	}
	result.IndexFiles, result.IndexChunks, result.IndexPartial = indexStatus.Files, indexStatus.Chunks, indexStatus.Partial

	databasePath := filepath.Join(root, "data", "workbench.db")
	if err = os.MkdirAll(filepath.Dir(databasePath), 0o700); err != nil {
		return result, err
	}
	store, err := storage.Open(databasePath)
	if err != nil {
		return result, err
	}
	workspaceID := "soak-workspace"
	now := time.Now().UTC()
	if err = store.SaveWorkspace(ctx, domain.Workspace{ID: workspaceID, Path: workspaceRoot, Name: "Point soak", OpenedAt: now}); err != nil {
		_ = store.Close()
		return result, err
	}
	maxWriters, err := seedRuns(ctx, store, workspaceID, profile, now)
	if err != nil {
		_ = store.Close()
		return result, err
	}
	result.ConcurrentWriters = maxWriters
	result.Runs, result.Events, result.TerminalEvents = profile.Runs, profile.Events, profile.Runs
	if err = seedFlowExecutions(ctx, store, workspaceID, profile.FlowExecutions, now); err != nil {
		_ = store.Close()
		return result, err
	}
	result.FlowExecutions = profile.FlowExecutions

	result.Faults.DiskLowWriteRefused, err = exerciseNearDiskLimitRefusal(filepath.Join(root, "disk-limit"))
	if err != nil {
		return result, err
	}
	result.Faults.TemporaryDiskWriteDenied, result.Faults.DiskWriteRecovered, err = exerciseTemporaryDiskWriteLoss(ctx, filepath.Join(root, "disk-loss"))
	if err != nil {
		return result, err
	}
	if err = seedInterruptedWorkload(ctx, store, workspaceID, now); err != nil {
		return result, err
	}
	if err = store.Close(); err != nil {
		return result, err
	}
	store, err = storage.Open(databasePath)
	if err != nil {
		return result, err
	}
	result.Faults.CoreRestarts++
	result.InterruptedRecovery, err = verifyInterruptedRecovery(ctx, store, workspaceID)
	if err != nil {
		return result, err
	}
	result.Faults.InterruptedRunRecovery = result.InterruptedRecovery.Runs == 1 && result.InterruptedRecovery.FlowRuns == 1 && result.InterruptedRecovery.Executions == 1
	runtime.GC()
	result.BaselineHeapMB = heapMB()
	result.MaximumHeapMB = result.BaselineHeapMB
	workloadStarted := time.Now()
	deadline := workloadStarted.Add(time.Duration(profile.DurationSeconds) * time.Second)
	nextRestart := workloadStarted.Add(time.Duration(profile.RestartEverySeconds) * time.Second)
	iteration := 0
	for time.Now().Before(deadline) {
		iteration++
		runIndex := iteration % profile.Runs
		if _, err = store.GetRun(ctx, fmt.Sprintf("soak-run-%05d", runIndex)); err != nil {
			break
		}
		if iteration%7 == 0 {
			flowIndex := iteration % profile.FlowExecutions
			if _, err = store.GetFlowRun(ctx, fmt.Sprintf("soak-flow-%05d", flowIndex)); err != nil {
				break
			}
		}
		if time.Now().After(nextRestart) {
			if closeErr := store.Close(); closeErr != nil {
				err = closeErr
				break
			}
			store, err = storage.Open(databasePath)
			if err != nil {
				break
			}
			if _, err = store.Health(ctx); err != nil {
				break
			}
			result.Faults.CoreRestarts++
			nextRestart = time.Now().Add(time.Duration(profile.RestartEverySeconds) * time.Second)
		}
		if iteration%128 == 0 {
			runtime.GC()
			result.MaximumHeapMB = max(result.MaximumHeapMB, heapMB())
			time.Sleep(5 * time.Millisecond)
		}
	}
	result.DurationSeconds = time.Since(workloadStarted).Seconds()
	if err != nil {
		result.Failures = append(result.Failures, "steady workload: "+err.Error())
	}
	if result.Faults.CoreRestarts == 0 {
		if closeErr := store.Close(); closeErr == nil {
			store, err = storage.Open(databasePath)
			if err == nil {
				result.Faults.CoreRestarts = 1
			}
		} else {
			err = closeErr
		}
	}
	if err != nil {
		result.Failures = append(result.Failures, "core restart: "+err.Error())
	}

	if store != nil {
		health, healthErr := store.Health(ctx)
		if healthErr != nil {
			result.Failures = append(result.Failures, "database health: "+healthErr.Error())
		} else {
			result.SQLiteIntegrity = health.Integrity
			result.ForeignKeyViolations = health.ForeignKeyViolations
		}
		lost, orphan, verifyErr := verifyHistory(ctx, store, profile)
		result.LostTerminalEvents, result.OrphanExecutions = lost, orphan
		if verifyErr != nil {
			result.Failures = append(result.Failures, "history verification: "+verifyErr.Error())
		}
		_ = store.Close()
	}

	reopenStarted := time.Now()
	store, err = storage.Open(databasePath)
	if err == nil {
		_, err = store.GetRun(ctx, "soak-run-00000")
	}
	if err == nil {
		_, err = store.GetRun(ctx, fmt.Sprintf("soak-run-%05d", profile.Runs-1))
	}
	if err == nil {
		_, err = store.GetFlowRun(ctx, fmt.Sprintf("soak-flow-%05d", profile.FlowExecutions-1))
	}
	if err == nil {
		_, err = store.ListByRun(ctx, "soak-run-00000")
	}
	result.HistoryReopenMs = time.Since(reopenStarted).Milliseconds()
	if err != nil {
		result.Failures = append(result.Failures, "history reopen: "+err.Error())
	}
	if store != nil {
		_ = store.Close()
	}
	info, statErr := os.Stat(databasePath)
	if statErr != nil {
		return result, statErr
	}
	result.DatabaseBytes = info.Size()
	result.DatabaseBytesPerEvent = float64(info.Size()) / float64(profile.Events)
	runtime.GC()
	result.MaximumHeapMB = max(result.MaximumHeapMB, heapMB())
	if result.BaselineHeapMB > 0 {
		result.MemoryGrowthPercent = (result.MaximumHeapMB - result.BaselineHeapMB) / result.BaselineHeapMB * 100
	}

	checkSoakCriteria(&result, profile)
	result.CoreCriteriaPassed = len(result.Failures) == 0
	// The Go harness proves Core/storage/index and a deterministic disk-low
	// refusal. The outer PowerShell controller must add real Code-OSS and
	// disposable Docker restart evidence before a release can qualify.
	result.ExternalFaultsVerified = result.Faults.CodeOSSRestarts > 0 && result.Faults.DockerRestarts > 0
	result.ReleaseQualified = result.ReleaseProfile && result.CoreCriteriaPassed && result.ExternalFaultsVerified
	result.FinishedAt = time.Now().UTC()
	if len(result.Failures) > 0 {
		return result, errors.New(strings.Join(result.Failures, "; "))
	}
	return result, nil
}

func runConcurrentFullRuns(ctx context.Context, dataRoot, workspaceRoot string, count int) (fullRunEvidence, error) {
	evidence := fullRunEvidence{Requested: count, Failures: []string{}}
	var inFlight, maximum atomic.Int64
	release := make(chan struct{})
	ready := make(chan struct{}, count)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		active := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			observed := maximum.Load()
			if active <= observed || maximum.CompareAndSwap(observed, active) {
				break
			}
		}
		ready <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "content": "bounded soak run complete"}, "done": true})
	}))
	defer provider.Close()
	_ = os.Setenv("REDIS_ADDR", "")
	application, err := app.New(dataRoot, app.WithSandboxBackend(&sandbox.Manager{Root: filepath.Join(dataRoot, "sandboxes")}))
	if err != nil {
		return evidence, err
	}
	defer application.Shutdown(context.Background())
	if _, err = application.OpenWorkspace(workspaceRoot); err != nil {
		return evidence, err
	}
	profile := domain.DefaultProfile()
	profile.ID, profile.Name, profile.BaseURL, profile.Model = "soak-full-run", "Soak full run", provider.URL, "scripted"
	profile.AllowedTools = []string{"read_file", "search_text"}
	if _, err = application.SaveProfile(profile); err != nil {
		return evidence, err
	}
	started := time.Now()
	runIDs := make(chan string, count)
	errCh := make(chan error, count)
	for index := 0; index < count; index++ {
		go func(index int) {
			run, runErr := application.StartRun(app.StartRunRequest{ProfileID: profile.ID, Task: fmt.Sprintf("Inspect bounded soak fixture %d", index)})
			if runErr != nil {
				errCh <- runErr
				return
			}
			runIDs <- run.ID
		}(index)
	}
	for index := 0; index < count; index++ {
		select {
		case <-ready:
		case runErr := <-errCh:
			close(release)
			return evidence, runErr
		case <-time.After(20 * time.Second):
			close(release)
			return evidence, errors.New("full runs did not reach provider concurrently")
		}
	}
	close(release)
	ids := make([]string, 0, count)
	for len(ids) < count {
		select {
		case id := <-runIDs:
			ids = append(ids, id)
		case runErr := <-errCh:
			return evidence, runErr
		case <-time.After(20 * time.Second):
			return evidence, errors.New("full run launch timed out")
		}
	}
	deadline := time.Now().Add(30 * time.Second)
	for _, id := range ids {
		for {
			details, detailErr := application.RunDetails(id)
			if detailErr != nil {
				return evidence, detailErr
			}
			if details.Run.Status == domain.RunCompleted {
				evidence.Completed++
				for _, event := range details.Events {
					if event.Type == domain.EventRunCompleted {
						evidence.TerminalEvents++
					}
				}
				break
			}
			if time.Now().After(deadline) {
				return evidence, fmt.Errorf("run %s stayed %s", id, details.Run.Status)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	evidence.MaxConcurrent = maximum.Load()
	evidence.DurationMs = time.Since(started).Milliseconds()
	if evidence.MaxConcurrent < int64(count) || evidence.Completed != count || evidence.TerminalEvents != count {
		return evidence, fmt.Errorf("full run evidence incomplete: %+v", evidence)
	}
	evidence.Cancelled, evidence.CancelEvents, err = exerciseCancelledRun(application)
	if err != nil {
		return evidence, err
	}
	evidence.TimedOut, evidence.TimeoutEvents, err = exerciseProviderTimeout(application)
	if err != nil {
		return evidence, err
	}
	return evidence, nil
}

func checkSoakCriteria(result *soakResult, profile soakProfile) {
	check := func(ok bool, message string) {
		if !ok {
			result.Failures = append(result.Failures, message)
		}
	}
	check(result.FullRuns.Completed == profile.ConcurrentFullRuns && result.FullRuns.MaxConcurrent >= int64(profile.ConcurrentFullRuns), "four full concurrent runs were not proven")
	check(result.FullRuns.Cancelled == 1 && result.FullRuns.CancelEvents == 1 && result.Faults.RunCancellation, "run cancellation and its single terminal event were not proven")
	check(result.FullRuns.TimedOut == 1 && result.FullRuns.TimeoutEvents == 1 && result.Faults.ProviderTimeout, "provider timeout and its single failed terminal event were not proven")
	check(result.FixtureFiles == profile.FixtureFiles, "fixture file count mismatch")
	check(result.IndexFiles == profile.FixtureFiles && !result.IndexPartial, "50k index coverage is partial or incomplete")
	check(result.Runs == profile.Runs && result.Events == profile.Events && result.FlowExecutions == profile.FlowExecutions, "workload cardinality mismatch")
	check(result.ConcurrentWriters >= int64(profile.ConcurrentFullRuns), "concurrent persistence workload was not proven")
	check(result.SQLiteIntegrity == "ok" && result.ForeignKeyViolations == 0, "SQLite integrity or foreign keys failed")
	check(result.OrphanExecutions == 0, "orphan executions detected")
	check(result.LostTerminalEvents == 0, "lost or duplicate terminal events detected")
	check(result.HistoryReopenMs <= profile.HistoryReopenMs, fmt.Sprintf("history reopen %d ms exceeds %d ms", result.HistoryReopenMs, profile.HistoryReopenMs))
	check(result.DatabaseBytes <= profile.DatabaseMaxBytes, fmt.Sprintf("database %d bytes exceeds %d", result.DatabaseBytes, profile.DatabaseMaxBytes))
	check(result.DatabaseBytesPerEvent <= profile.DatabaseBytesPerEvent, fmt.Sprintf("database density %.2f exceeds %.2f bytes/event", result.DatabaseBytesPerEvent, profile.DatabaseBytesPerEvent))
	check(result.MemoryGrowthPercent <= profile.MemoryGrowthPercent, fmt.Sprintf("heap growth %.2f%% exceeds %.2f%%", result.MemoryGrowthPercent, profile.MemoryGrowthPercent))
	check(result.Faults.CoreRestarts > 0, "core restart fault was not exercised")
	check(result.Faults.DiskLowWriteRefused, "disk-low write refusal was not exercised")
	check(result.Faults.TemporaryDiskWriteDenied && result.Faults.DiskWriteRecovered, "temporary disk write loss and recovery were not exercised")
	check(result.Faults.InterruptedRunRecovery && result.InterruptedRecovery.Runs == 1 && result.InterruptedRecovery.FlowRuns == 1 && result.InterruptedRecovery.Executions == 1, "unfinished Run/Flow/execution recovery as interrupted was not proven")
	check(result.DurationSeconds >= float64(profile.DurationSeconds)*0.99, "soak duration was shorter than the selected profile")
	sort.Strings(result.Failures)
}
