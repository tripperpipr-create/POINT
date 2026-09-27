package main

// Нагрузка soak: файлы рабочей области, накопленная история прогонов и
// выполнений Flow, сверка истории после прогона и замер кучи.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/storage"
)

func writeFixture(root string, files int) (int64, error) {
	var total int64
	for index := 0; index < files; index++ {
		directory := filepath.Join(root, fmt.Sprintf("module-%04d", index/100))
		if index%100 == 0 {
			if err := os.MkdirAll(directory, 0o700); err != nil {
				return 0, err
			}
		}
		content := fmt.Sprintf("package module%04d\n\nfunc SoakMarker%05d() int { return %d }\n", index/100, index, index)
		if err := os.WriteFile(filepath.Join(directory, fmt.Sprintf("file-%05d.go", index)), []byte(content), 0o600); err != nil {
			return 0, err
		}
		total += int64(len(content))
	}
	return total, nil
}

func seedRuns(ctx context.Context, store *storage.SQLite, workspaceID string, profile soakProfile, now time.Time) (int64, error) {
	jobs := make(chan int)
	errCh := make(chan error, profile.ConcurrentFullRuns)
	var wait sync.WaitGroup
	var active, maximum atomic.Int64
	eventsPerRun := profile.Events / profile.Runs
	for worker := 0; worker < profile.ConcurrentFullRuns; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range jobs {
				current := active.Add(1)
				for {
					observed := maximum.Load()
					if current <= observed || maximum.CompareAndSwap(observed, current) {
						break
					}
				}
				finished := now.Add(time.Duration(index) * time.Millisecond)
				runID := fmt.Sprintf("soak-run-%05d", index)
				err := store.SaveRun(ctx, domain.Run{
					ID: runID, AgentID: "soak-agent", ProfileID: "soak-profile", WorkspaceID: workspaceID,
					Task: "bounded soak task", Provider: "scripted", Model: "fixture", Status: domain.RunCompleted,
					Step: eventsPerRun, RequestCount: 1, StartedAt: now, FinishedAt: &finished, DurationMs: int64(index % 1000),
				})
				for eventIndex := 0; err == nil && eventIndex < eventsPerRun; eventIndex++ {
					eventType := domain.EventModelResponded
					if eventIndex == eventsPerRun-1 {
						eventType = domain.EventRunCompleted
					}
					payload := json.RawMessage(fmt.Sprintf(`{"sequence":%d,"bounded":true}`, eventIndex))
					err = store.Append(ctx, domain.Event{
						ID: fmt.Sprintf("soak-event-%05d-%03d", index, eventIndex), RunID: runID, AgentID: "soak-agent",
						Type: eventType, Step: eventIndex, Actor: "soak", Data: payload,
						CreatedAt: now.Add(time.Duration(index*eventsPerRun+eventIndex) * time.Microsecond),
					})
				}
				active.Add(-1)
				if err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	for index := 0; index < profile.Runs; index++ {
		jobs <- index
	}
	close(jobs)
	wait.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return maximum.Load(), err
		}
	}
	return maximum.Load(), nil
}

func seedFlowExecutions(ctx context.Context, store *storage.SQLite, workspaceID string, count int, now time.Time) error {
	for index := 0; index < count; index++ {
		finished := now.Add(time.Duration(index) * time.Millisecond)
		flowID := fmt.Sprintf("soak-flow-%05d", index)
		runID := fmt.Sprintf("soak-run-%05d", index)
		if err := store.SaveFlowRun(ctx, domain.FlowRun{
			ID: flowID, FlowID: "soak-flow-definition", WorkspaceID: workspaceID, Status: domain.RunCompleted,
			NodeStates: map[string]domain.FlowNodeState{"agent": {Status: "completed", Attempts: 1, Output: map[string]any{"verified": true}}},
			StartedAt:  now, FinishedAt: &finished,
		}); err != nil {
			return err
		}
		if err := store.SaveExecution(ctx, domain.ExecutionInstance{
			ID: fmt.Sprintf("soak-execution-%05d", index), WorkspaceID: workspaceID, ProjectAgentID: "soak-agent",
			FlowRunID: flowID, FlowNodeID: "agent", RunID: runID, Task: "bounded flow execution",
			Status: domain.RunCompleted, StartedAt: now, FinishedAt: &finished,
		}); err != nil {
			return err
		}
	}
	return nil
}

func verifyHistory(ctx context.Context, store *storage.SQLite, profile soakProfile) (lost, orphan int, err error) {
	eventsPerRun := profile.Events / profile.Runs
	for index := 0; index < profile.Runs; index++ {
		runID := fmt.Sprintf("soak-run-%05d", index)
		if _, err = store.GetRun(ctx, runID); err != nil {
			return lost, orphan, err
		}
		events, listErr := store.ListByRun(ctx, runID)
		if listErr != nil {
			return lost, orphan, listErr
		}
		terminal := 0
		for _, event := range events {
			if event.Type == domain.EventRunCompleted || event.Type == domain.EventRunFailed || event.Type == domain.EventRunCancelled {
				terminal++
			}
		}
		if len(events) != eventsPerRun || terminal != 1 {
			lost++
		}
	}
	for index := 0; index < profile.FlowExecutions; index++ {
		flowID := fmt.Sprintf("soak-flow-%05d", index)
		if _, flowErr := store.GetFlowRun(ctx, flowID); flowErr != nil {
			orphan++
		}
		if _, runErr := store.GetRun(ctx, fmt.Sprintf("soak-run-%05d", index)); runErr != nil {
			orphan++
		}
	}
	return lost, orphan, nil
}

func heapMB() float64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return float64(stats.HeapAlloc) / 1024 / 1024
}
