package main

// Отказы профиля soak: отмена прогона, таймаут провайдера, отказ записи у
// предела диска, временная потеря записи и восстановление прерванных
// прогонов после рестарта ядра. Каждое упражнение возвращает наблюдение, а
// не верит на слово.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"local-agent-workbench/internal/app"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/storage"
)

func exerciseCancelledRun(application *app.App) (int, int, error) {
	requested := make(chan struct{}, 1)
	release := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		select {
		case requested <- struct{}{}:
		default:
		}
		select {
		case <-request.Context().Done():
		case <-release:
		}
	}))
	defer func() {
		close(release)
		provider.Close()
	}()
	profile := domain.DefaultProfile()
	profile.ID, profile.Name, profile.BaseURL, profile.Model = "soak-cancel-run", "Soak cancellation", provider.URL, "scripted"
	profile.AllowedTools = []string{"read_file"}
	profile.MaxDurationSeconds = 10
	if _, err := application.SaveProfile(profile); err != nil {
		return 0, 0, err
	}
	run, err := application.StartRun(app.StartRunRequest{ProfileID: profile.ID, Task: "Wait until this run is cancelled"})
	if err != nil {
		return 0, 0, err
	}
	select {
	case <-requested:
	case <-time.After(5 * time.Second):
		return 0, 0, errors.New("cancellation provider was not reached")
	}
	if err = application.CancelRun(run.ID); err != nil {
		return 0, 0, err
	}
	details, err := waitRunStatus(application, run.ID, domain.RunCancelled, 5*time.Second)
	if err != nil {
		return 0, 0, err
	}
	return 1, countRunEvent(details, domain.EventRunCancelled), nil
}

func exerciseProviderTimeout(application *app.App) (int, int, error) {
	requested := make(chan struct{}, 1)
	release := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		select {
		case requested <- struct{}{}:
		default:
		}
		select {
		case <-request.Context().Done():
		case <-release:
		}
	}))
	defer func() {
		close(release)
		provider.Close()
	}()
	profile := domain.DefaultProfile()
	profile.ID, profile.Name, profile.BaseURL, profile.Model = "soak-provider-timeout", "Soak provider timeout", provider.URL, "scripted"
	profile.AllowedTools = []string{"read_file"}
	profile.MaxDurationSeconds = 1
	if _, err := application.SaveProfile(profile); err != nil {
		return 0, 0, err
	}
	run, err := application.StartRun(app.StartRunRequest{ProfileID: profile.ID, Task: "Wait for the bounded provider timeout"})
	if err != nil {
		return 0, 0, err
	}
	select {
	case <-requested:
	case <-time.After(5 * time.Second):
		return 0, 0, errors.New("timeout provider was not reached")
	}
	details, err := waitRunStatus(application, run.ID, domain.RunFailed, 6*time.Second)
	if err != nil {
		return 0, 0, err
	}
	lowerError := strings.ToLower(details.Run.Error)
	if !strings.Contains(lowerError, "deadline") && !strings.Contains(lowerError, "timeout") && !strings.Contains(lowerError, "timed out") {
		return 0, 0, fmt.Errorf("provider timeout was not attributed in run error: %q", details.Run.Error)
	}
	return 1, countRunEvent(details, domain.EventRunFailed), nil
}

func waitRunStatus(application *app.App, runID string, wanted domain.RunStatus, timeout time.Duration) (app.RunDetails, error) {
	deadline := time.Now().Add(timeout)
	for {
		details, err := application.RunDetails(runID)
		if err != nil {
			return app.RunDetails{}, err
		}
		if details.Run.Status == wanted {
			return details, nil
		}
		if details.Run.Status == domain.RunCompleted || details.Run.Status == domain.RunFailed || details.Run.Status == domain.RunCancelled || details.Run.Status == domain.RunInterrupted {
			return details, fmt.Errorf("run %s ended as %s, want %s", runID, details.Run.Status, wanted)
		}
		if time.Now().After(deadline) {
			return details, fmt.Errorf("run %s stayed %s, want %s", runID, details.Run.Status, wanted)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func countRunEvent(details app.RunDetails, kind domain.EventType) int {
	count := 0
	for _, event := range details.Events {
		if event.Type == kind {
			count++
		}
	}
	return count
}

func exerciseNearDiskLimitRefusal(root string) (bool, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return false, err
	}
	path := filepath.Join(root, "bounded-history.bin")
	baseline := []byte(strings.Repeat("a", 4096))
	if err := os.WriteFile(path, baseline, 0o600); err != nil {
		return false, err
	}
	payload := []byte(strings.Repeat("b", 4096))
	refused := appendWithinDiskBudget(path, payload, int64(len(baseline)+len(payload)-1)) != nil
	afterRefusal, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if !refused || string(afterRefusal) != string(baseline) {
		return false, errors.New("near-disk-limit guard changed the file before refusing growth")
	}
	if err = appendWithinDiskBudget(path, payload, int64(len(baseline)+len(payload))); err != nil {
		return false, fmt.Errorf("bounded write did not recover after quota increased: %w", err)
	}
	return true, nil
}

func appendWithinDiskBudget(path string, payload []byte, maximumBytes int64) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size()+int64(len(payload)) > maximumBytes {
		return fmt.Errorf("disk safety limit would be exceeded: %d + %d > %d", info.Size(), len(payload), maximumBytes)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err = file.Write(payload); err != nil {
		return err
	}
	return file.Sync()
}

func exerciseTemporaryDiskWriteLoss(ctx context.Context, root string) (denied, recovered bool, err error) {
	if err = os.MkdirAll(root, 0o700); err != nil {
		return false, false, err
	}
	databasePath := filepath.Join(root, "workbench.db")
	store, err := storage.Open(databasePath)
	if err != nil {
		return false, false, err
	}
	if err = store.SaveSetting(ctx, "disk-fault", "before"); err != nil {
		_ = store.Close()
		return false, false, err
	}
	if err = store.Close(); err != nil {
		return false, false, err
	}

	readOnly, err := sql.Open("sqlite", readOnlySQLiteDSN(databasePath))
	if err != nil {
		return false, false, err
	}
	_, writeErr := readOnly.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('disk-fault','during') ON CONFLICT(key) DO UPDATE SET value=excluded.value`)
	closeErr := readOnly.Close()
	if writeErr == nil {
		return false, false, errors.New("read-only database accepted a write during temporary disk-loss injection")
	}
	if closeErr != nil {
		return false, false, closeErr
	}
	denied = true

	store, err = storage.Open(databasePath)
	if err != nil {
		return denied, false, err
	}
	defer store.Close()
	if err = store.SaveSetting(ctx, "disk-fault", "recovered"); err != nil {
		return denied, false, err
	}
	value, err := store.Setting(ctx, "disk-fault")
	if err != nil {
		return denied, false, err
	}
	health, err := store.Health(ctx)
	if err != nil {
		return denied, false, err
	}
	recovered = value == "recovered" && health.Integrity == "ok" && health.ForeignKeyViolations == 0
	if !recovered {
		return denied, false, fmt.Errorf("database did not recover after temporary read-only access: value=%q integrity=%s foreign_keys=%d", value, health.Integrity, health.ForeignKeyViolations)
	}
	return denied, recovered, nil
}

func readOnlySQLiteDSN(path string) string {
	slashPath := filepath.ToSlash(filepath.Clean(path))
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	uri := url.URL{Scheme: "file", Path: slashPath}
	query := uri.Query()
	query.Set("mode", "ro")
	query.Set("immutable", "1")
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "foreign_keys(1)")
	uri.RawQuery = query.Encode()
	return uri.String()
}

func seedInterruptedWorkload(ctx context.Context, store *storage.SQLite, workspaceID string, now time.Time) error {
	run := domain.Run{
		ID: "soak-interrupted-run", AgentID: "soak-agent", ProfileID: "soak-profile", WorkspaceID: workspaceID,
		Task: "recover this unfinished run", Provider: "scripted", Model: "fixture", Status: domain.RunRunning,
		StartedAt: now, ToolsUsed: []string{}, ChangedFiles: []string{},
	}
	if err := store.SaveRun(ctx, run); err != nil {
		return err
	}
	flow := domain.FlowRun{
		ID: "soak-interrupted-flow", FlowID: "soak-flow-definition", WorkspaceID: workspaceID,
		Status: domain.RunRunning, NodeStates: map[string]domain.FlowNodeState{"agent": {Status: string(domain.RunRunning)}}, StartedAt: now,
	}
	if err := store.SaveFlowRun(ctx, flow); err != nil {
		return err
	}
	return store.SaveExecution(ctx, domain.ExecutionInstance{
		ID: "soak-interrupted-execution", WorkspaceID: workspaceID, ProjectAgentID: "soak-agent",
		FlowRunID: flow.ID, FlowNodeID: "agent", RunID: run.ID, Task: run.Task, Status: domain.RunRunning, StartedAt: now,
	})
}

func verifyInterruptedRecovery(ctx context.Context, store *storage.SQLite, workspaceID string) (recoveryEvidence, error) {
	evidence := recoveryEvidence{}
	run, err := store.GetRun(ctx, "soak-interrupted-run")
	if err != nil {
		return evidence, err
	}
	if run.Status == domain.RunInterrupted && run.FinishedAt != nil {
		evidence.Runs = 1
	}
	flow, err := store.GetFlowRun(ctx, "soak-interrupted-flow")
	if err != nil {
		return evidence, err
	}
	if flow.Status == domain.RunInterrupted && flow.FinishedAt != nil {
		evidence.FlowRuns = 1
	}
	executions, err := store.ListExecutions(ctx, workspaceID, 500)
	if err != nil {
		return evidence, err
	}
	for _, execution := range executions {
		if execution.ID == "soak-interrupted-execution" && execution.Status == domain.RunInterrupted && execution.FinishedAt != nil {
			evidence.Executions = 1
			break
		}
	}
	if evidence.Runs != 1 || evidence.FlowRuns != 1 || evidence.Executions != 1 {
		return evidence, fmt.Errorf("interrupted recovery incomplete: %+v", evidence)
	}
	return evidence, nil
}
