package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/storage"
	"local-agent-workbench/internal/workspace"
)

type configuration struct {
	SchemaVersion int `json:"schemaVersion"`
	Core          struct {
		FixtureFiles             int     `json:"fixtureFiles"`
		IndexBuildMs             int64   `json:"indexBuildMs"`
		IndexSearchP95Ms         float64 `json:"indexSearchP95Ms"`
		IndexHeapDeltaMB         float64 `json:"indexHeapDeltaMB"`
		SQLiteEvents             int     `json:"sqliteEvents"`
		SQLiteMaxBytes           int64   `json:"sqliteMaxBytes"`
		SQLiteBytesPerEvent      float64 `json:"sqliteBytesPerEvent"`
		FlowRuns                 int     `json:"flowRuns"`
		ReopenAndFlowListMs      int64   `json:"reopenAndFlowListMs"`
		ParallelExecutionWrites  int     `json:"parallelExecutionWrites"`
		ParallelExecutionWriteMs int64   `json:"parallelExecutionWriteMs"`
	} `json:"core"`
}

type result struct {
	SchemaVersion            int      `json:"schemaVersion"`
	FixtureFiles             int      `json:"fixtureFiles"`
	FixtureBytes             int64    `json:"fixtureBytes"`
	IndexFiles               int      `json:"indexFiles"`
	IndexChunks              int      `json:"indexChunks"`
	IndexPartial             bool     `json:"indexPartial"`
	IndexBuildMs             int64    `json:"indexBuildMs"`
	IndexSearchP95Ms         float64  `json:"indexSearchP95Ms"`
	IndexHeapDeltaMB         float64  `json:"indexHeapDeltaMB"`
	SQLiteEvents             int      `json:"sqliteEvents"`
	SQLiteBytes              int64    `json:"sqliteBytes"`
	SQLiteBytesPerEvent      float64  `json:"sqliteBytesPerEvent"`
	FlowRuns                 int      `json:"flowRuns"`
	ReopenAndFlowListMs      int64    `json:"reopenAndFlowListMs"`
	ParallelExecutionWrites  int      `json:"parallelExecutionWrites"`
	ParallelExecutionWriteMs int64    `json:"parallelExecutionWriteMs"`
	Failures                 []string `json:"failures"`
}

func main() {
	sloPath := flag.String("slo", "distribution/performance-slo.json", "performance SLO JSON")
	flag.Parse()
	output, err := probe(context.Background(), *sloPath)
	if output.Failures == nil {
		output.Failures = []string{}
	}
	encoded, _ := json.MarshalIndent(output, "", "  ")
	_, _ = os.Stdout.Write(append(encoded, '\n'))
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func probe(ctx context.Context, sloPath string) (result, error) {
	var cfg configuration
	encoded, err := os.ReadFile(sloPath)
	if err != nil {
		return result{}, err
	}
	if err = json.Unmarshal(encoded, &cfg); err != nil {
		return result{}, err
	}
	if cfg.SchemaVersion != 1 || cfg.Core.FixtureFiles <= 0 || cfg.Core.SQLiteEvents <= 0 || cfg.Core.FlowRuns <= 0 || cfg.Core.ParallelExecutionWrites <= 0 {
		return result{}, errors.New("invalid core performance SLO configuration")
	}
	root, err := os.MkdirTemp("", "point-performance-probe-")
	if err != nil {
		return result{}, err
	}
	defer os.RemoveAll(root)
	workspaceRoot := filepath.Join(root, "workspace")
	dataRoot := filepath.Join(root, "data")
	if err = os.MkdirAll(workspaceRoot, 0o700); err != nil {
		return result{}, err
	}
	fixtureBytes, err := writeFixture(workspaceRoot, cfg.Core.FixtureFiles)
	if err != nil {
		return result{}, err
	}
	output := result{SchemaVersion: 1, FixtureFiles: cfg.Core.FixtureFiles, FixtureBytes: fixtureBytes, SQLiteEvents: cfg.Core.SQLiteEvents, FlowRuns: cfg.Core.FlowRuns, ParallelExecutionWrites: cfg.Core.ParallelExecutionWrites, Failures: []string{}}

	filesystem, err := workspace.Open(workspaceRoot)
	if err != nil {
		return output, err
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	status, err := filesystem.BuildIndex(ctx)
	output.IndexBuildMs = time.Since(started).Milliseconds()
	if err != nil {
		return output, err
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	output.IndexFiles, output.IndexChunks, output.IndexPartial = status.Files, status.Chunks, status.Partial
	if after.HeapAlloc > before.HeapAlloc {
		output.IndexHeapDeltaMB = float64(after.HeapAlloc-before.HeapAlloc) / 1024 / 1024
	}
	latencies := make([]float64, 200)
	for index := range latencies {
		queryStarted := time.Now()
		result := filesystem.LookupIndex(fmt.Sprintf("PerformanceMarker%05d", index%cfg.Core.FixtureFiles), 8)
		latencies[index] = float64(time.Since(queryStarted).Microseconds()) / 1000
		if len(result.Hits) == 0 {
			return output, fmt.Errorf("index lookup missed fixture marker %d", index)
		}
	}
	sort.Float64s(latencies)
	output.IndexSearchP95Ms = latencies[int(float64(len(latencies)-1)*0.95)]

	if err = os.MkdirAll(dataRoot, 0o700); err != nil {
		return output, err
	}
	databasePath := filepath.Join(dataRoot, "workbench.db")
	store, err := storage.Open(databasePath)
	if err != nil {
		return output, err
	}
	now := time.Now().UTC()
	for index := 0; index < cfg.Core.SQLiteEvents; index++ {
		if err = store.Append(ctx, domain.Event{
			ID: fmt.Sprintf("perf-event-%06d", index), RunID: fmt.Sprintf("run-%04d", index/100), AgentID: "performance-agent",
			Type: domain.EventModelResponded, Data: json.RawMessage(`{"bytes":128,"status":"recorded"}`), CreatedAt: now.Add(time.Duration(index) * time.Nanosecond),
		}); err != nil {
			_ = store.Close()
			return output, err
		}
	}
	for index := 0; index < cfg.Core.FlowRuns; index++ {
		states := make(map[string]domain.FlowNodeState, 12)
		for node := 0; node < 12; node++ {
			states[fmt.Sprintf("node-%02d", node)] = domain.FlowNodeState{Status: "completed", Attempts: 1, Output: map[string]any{"verified": true, "sequence": node}}
		}
		finished := now.Add(time.Duration(index) * time.Millisecond)
		if err = store.SaveFlowRun(ctx, domain.FlowRun{
			ID: fmt.Sprintf("perf-flow-run-%04d", index), FlowID: "perf-flow", WorkspaceID: "perf-workspace",
			Status: domain.RunCompleted, NodeStates: states, Snapshot: map[string]any{"revision": 1}, StartedAt: now, FinishedAt: &finished,
		}); err != nil {
			_ = store.Close()
			return output, err
		}
	}
	parallelStarted := time.Now()
	parallelErrors := make(chan error, cfg.Core.ParallelExecutionWrites)
	var wait sync.WaitGroup
	for index := 0; index < cfg.Core.ParallelExecutionWrites; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			parallelErrors <- store.SaveExecution(ctx, domain.ExecutionInstance{
				ID: fmt.Sprintf("perf-execution-%03d", index), WorkspaceID: "perf-workspace", ProjectAgentID: "perf-agent",
				Task: "bounded parallel persistence", Status: domain.RunCompleted, StartedAt: now,
			})
		}(index)
	}
	wait.Wait()
	close(parallelErrors)
	for parallelErr := range parallelErrors {
		if parallelErr != nil {
			_ = store.Close()
			return output, parallelErr
		}
	}
	output.ParallelExecutionWriteMs = time.Since(parallelStarted).Milliseconds()
	if err = store.Close(); err != nil {
		return output, err
	}
	databaseInfo, err := os.Stat(databasePath)
	if err != nil {
		return output, err
	}
	output.SQLiteBytes = databaseInfo.Size()
	output.SQLiteBytesPerEvent = float64(databaseInfo.Size()) / float64(cfg.Core.SQLiteEvents)
	reopenStarted := time.Now()
	store, err = storage.Open(databasePath)
	if err != nil {
		return output, err
	}
	// The regular workspace feed is deliberately capped at 500 rows for UI
	// safety. The performance contract must restore the complete persisted
	// history, so query the probe's single immutable Flow instead of silently
	// measuring only the first page.
	flowRuns, err := store.ListFlowRunsByFlowID(ctx, "perf-flow")
	if err != nil {
		_ = store.Close()
		return output, err
	}
	output.ReopenAndFlowListMs = time.Since(reopenStarted).Milliseconds()
	if err = store.Close(); err != nil {
		return output, err
	}
	if len(flowRuns) != cfg.Core.FlowRuns {
		return output, fmt.Errorf("restored flow history=%d, want %d", len(flowRuns), cfg.Core.FlowRuns)
	}
	for _, flowRun := range flowRuns {
		if flowRun.WorkspaceID != "perf-workspace" || flowRun.Status != domain.RunCompleted || len(flowRun.NodeStates) != 12 {
			return output, fmt.Errorf("restored flow history contains incomplete run %q", flowRun.ID)
		}
	}

	checkMax(&output.Failures, "indexBuildMs", float64(output.IndexBuildMs), float64(cfg.Core.IndexBuildMs))
	checkMax(&output.Failures, "indexSearchP95Ms", output.IndexSearchP95Ms, cfg.Core.IndexSearchP95Ms)
	checkMax(&output.Failures, "indexHeapDeltaMB", output.IndexHeapDeltaMB, cfg.Core.IndexHeapDeltaMB)
	checkMax(&output.Failures, "sqliteBytes", float64(output.SQLiteBytes), float64(cfg.Core.SQLiteMaxBytes))
	checkMax(&output.Failures, "sqliteBytesPerEvent", output.SQLiteBytesPerEvent, cfg.Core.SQLiteBytesPerEvent)
	checkMax(&output.Failures, "reopenAndFlowListMs", float64(output.ReopenAndFlowListMs), float64(cfg.Core.ReopenAndFlowListMs))
	checkMax(&output.Failures, "parallelExecutionWriteMs", float64(output.ParallelExecutionWriteMs), float64(cfg.Core.ParallelExecutionWriteMs))
	if output.IndexFiles != cfg.Core.FixtureFiles || output.IndexPartial {
		output.Failures = append(output.Failures, fmt.Sprintf("index coverage files=%d/%d partial=%t", output.IndexFiles, cfg.Core.FixtureFiles, output.IndexPartial))
	}
	if len(output.Failures) > 0 {
		return output, errors.New(strings.Join(output.Failures, "; "))
	}
	return output, nil
}

func writeFixture(root string, files int) (int64, error) {
	var total int64
	for index := 0; index < files; index++ {
		directory := filepath.Join(root, fmt.Sprintf("module-%03d", index/100))
		if index%100 == 0 {
			if err := os.MkdirAll(directory, 0o700); err != nil {
				return 0, err
			}
		}
		content := fmt.Sprintf("package module%03d\n\n// PerformanceMarker%05d is deterministic index evidence.\nfunc PerformanceMarker%05d() string { return %q }\n", index/100, index, index, strings.Repeat("x", 768))
		if err := os.WriteFile(filepath.Join(directory, fmt.Sprintf("file-%05d.go", index)), []byte(content), 0o600); err != nil {
			return 0, err
		}
		total += int64(len(content))
	}
	return total, nil
}

func checkMax(failures *[]string, name string, actual, limit float64) {
	if actual > limit {
		*failures = append(*failures, fmt.Sprintf("%s %.2f exceeds %.2f", name, actual, limit))
	}
}
