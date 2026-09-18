package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCorePerformanceProbeMeasuresAllDeclaredSurfaces(t *testing.T) {
	var cfg configuration
	cfg.SchemaVersion = 1
	cfg.Core.FixtureFiles = 100
	cfg.Core.IndexBuildMs = 10_000
	cfg.Core.IndexSearchP95Ms = 100
	cfg.Core.IndexHeapDeltaMB = 256
	cfg.Core.SQLiteEvents = 200
	cfg.Core.SQLiteMaxBytes = 32 << 20
	cfg.Core.SQLiteBytesPerEvent = 64 << 10
	// Exceed the normal UI feed cap so the test proves that the release probe
	// restores the complete durable history rather than a bounded first page.
	cfg.Core.FlowRuns = 501
	cfg.Core.ReopenAndFlowListMs = 5000
	cfg.Core.ParallelExecutionWrites = 8
	cfg.Core.ParallelExecutionWriteMs = 5000
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "slo.json")
	if err = os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := probe(context.Background(), path)
	if err != nil {
		t.Fatalf("probe=%#v err=%v", output, err)
	}
	if output.IndexFiles != cfg.Core.FixtureFiles || output.SQLiteEvents != cfg.Core.SQLiteEvents ||
		output.FlowRuns != cfg.Core.FlowRuns || output.ParallelExecutionWrites != cfg.Core.ParallelExecutionWrites || len(output.Failures) != 0 {
		t.Fatalf("incomplete performance evidence=%#v", output)
	}
}
