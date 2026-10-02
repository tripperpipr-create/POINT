package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/diagnostics"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/sandboxsync"
	"local-agent-workbench/internal/security"
	"local-agent-workbench/internal/tools"
	"local-agent-workbench/internal/workspace"
)

type dependencyFailure struct {
	Cause, Class, Detail string
	Exit                 int
	Arguments            json.RawMessage
}

func (f *dependencyFailure) Error() string { return "dependency preparation: " + f.Cause }

func dependencyFingerprint(root string, plan *domain.DependencyPlan) (string, error) {
	if plan == nil {
		return "", nil
	}
	if err := domain.ValidateDependencyPlan(plan); err != nil {
		return "", err
	}
	fs, err := workspace.Open(root)
	if err != nil {
		return "", err
	}
	raw, _ := json.Marshal(plan)
	h := sha256.New()
	_, _ = h.Write(raw)
	for _, p := range plan.Projects {
		for _, name := range p.ManifestPaths {
			target, err := fs.Resolve(name, false)
			if err != nil {
				return "", fmt.Errorf("manifest %s: %w", name, err)
			}
			data, err := os.ReadFile(target)
			if err != nil {
				return "", err
			}
			_, _ = h.Write([]byte("\x00" + name + "\x00"))
			_, _ = h.Write(data)
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
func (a *App) journalCriteria(input criteriaBatchInput, typ domain.EventType, data map[string]any) error {
	payload := make(map[string]any, len(data)+4)
	for key, value := range data {
		payload[key] = value
	}
	payload["phase"] = input.Phase
	payload["engine"] = input.Sandbox.Backend
	payload["engineVersion"] = input.Sandbox.BackendVersion
	payload["securityProfileVersion"] = sandbox.SecurityProfileVersion
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	event := domain.Event{ID: domain.NewID("event"), WorkspaceID: input.Sandbox.WorkspaceID,
		RunID: input.RunID, QuestID: input.QuestID, ExecutionID: input.Sandbox.ExecutionID,
		FlowRunID: input.FlowRunID, FlowNodeID: input.FlowNodeID, Type: typ, Actor: "system", Data: raw, CreatedAt: time.Now().UTC()}
	if err = a.store.Append(context.Background(), event); err != nil {
		return err
	}
	if a.eventSink != nil {
		a.eventSink(event)
	}
	return nil
}

func (a *App) prepareDependencies(ctx context.Context, input criteriaBatchInput, root string, tool tools.RunCommand) (failed *dependencyFailure) {
	defer func() {
		if failed != nil {
			_ = a.journalCriteria(input, domain.EventDependenciesFailed, map[string]any{"cause": failed.Cause, "causeClass": failed.Class, "storageMode": input.Sandbox.StorageMode})
		}
	}()
	if input.WorkOrder == nil || input.WorkOrder.Dependencies == nil {
		return nil
	}
	plan := input.WorkOrder.Dependencies
	failure := func(cause string) *dependencyFailure {
		return &dependencyFailure{Cause: security.Redact(cause), Class: diagnostics.FailureRuntime, Exit: -1}
	}
	fingerprint, err := dependencyFingerprint(root, plan)
	if err != nil {
		return failure(err.Error())
	}
	before, err := sandbox.TreeDigestWithRules(root, input.Sandbox.FileRulesVersion)
	if err != nil {
		return failure(err.Error())
	}
	beforeManifest, _ := sandboxsync.Scan(ctx, root, input.Sandbox.FileRulesVersion)
	fs, err := workspace.Open(root)
	if err != nil {
		return failure(err.Error())
	}
	for _, project := range plan.Projects {
		for _, command := range project.Commands {
			cwd := command.Cwd
			if cwd == "" {
				cwd = project.Cwd
			}
			raw, _ := json.Marshal(map[string]any{"command": command.Command, "cwd": cwd, "reason": "approved dependency preparation", "timeoutSeconds": command.TimeoutSeconds})
			payload := map[string]any{"manager": project.Manager, "cwd": cwd, "command": command.Command, "dependencyDigest": fingerprint, "storageMode": input.Sandbox.StorageMode, "authoritative": tool.Authoritative, "cacheState": "unmeasured"}
			if err := a.journalCriteria(input, domain.EventDependenciesStarted, payload); err != nil {
				return failure("journal: " + err.Error())
			}
			started := time.Now()
			runCtx, cancel := context.WithTimeout(ctx, time.Duration(command.TimeoutSeconds)*time.Second)
			outcome := tool.Execute(runCtx, raw)
			cancel()
			exit := toolResultExitCode(outcome)
			payload["durationMs"], payload["exitCode"] = time.Since(started).Milliseconds(), exit
			payload["passed"] = outcome.OK && exit == 0
			if err := a.journalCriteria(input, domain.EventDependenciesFinished, payload); err != nil {
				return failure("journal: " + err.Error())
			}
			after, digestErr := sandbox.TreeDigestWithRules(root, input.Sandbox.FileRulesVersion)
			if digestErr != nil {
				return failure("cannot audit installation: " + digestErr.Error())
			}
			if after != before {
				afterManifest, _ := sandboxsync.Scan(ctx, root, input.Sandbox.FileRulesVersion)
				previous := map[string]sandboxsync.Entry{}
				for _, e := range beforeManifest.Entries {
					previous[e.Path] = e
				}
				changed := []string{}
				for _, e := range afterManifest.Entries {
					if old, ok := previous[e.Path]; !ok || old != e {
						changed = append(changed, e.Path)
					}
					delete(previous, e.Path)
				}
				for name := range previous {
					changed = append(changed, name)
				}
				if len(changed) > 20 {
					changed = changed[:20]
				}
				return failure("installation changed portable sources or lockfiles: " + strings.Join(changed, ", "))
			}
			if !outcome.OK || exit != 0 {
				cause := deterministicAcceptFailureDetail(outcome)
				if cause == "" {
					cause = fmt.Sprintf("%s exited %d", project.Manager, exit)
				}
				class := diagnostics.FailureRuntime
				if diagnosed, ok := acceptCheckFailure(outcome); ok && (diagnosed.Class == diagnostics.FailureHuman || diagnosed.Class == diagnostics.FailureTransient) {
					class = diagnosed.Class
				}
				return &dependencyFailure{Cause: cause, Class: class, Detail: acceptCheckDetail(outcome.Output, input.Sandbox), Exit: exit, Arguments: raw}
			}
		}
		// Volume mirrors contain only portable files. Probe expected paths inside
		// the same isolated workspace instead of inspecting its host mirror.
		if len(project.ExpectedPaths) > 0 {
			checks := []string{}
			for _, name := range project.ExpectedPaths {
				if _, err := fs.Resolve(name, true); err != nil {
					return failure(err.Error())
				}
				checks = append(checks, "test -e '"+strings.ReplaceAll(name, "'", "'\"'\"'")+"'")
			}
			raw, _ := json.Marshal(map[string]any{"command": strings.Join(checks, " && "), "reason": "dependency readiness", "timeoutSeconds": 30})
			outcome := tool.Execute(ctx, raw)
			if !outcome.OK || toolResultExitCode(outcome) != 0 {
				return failure(project.Manager + " preparation did not provide expected paths")
			}
		}
	}
	return nil
}

func dependencyFailureBatch(criteria []domain.AcceptanceCriterion, failure *dependencyFailure) criteriaBatchResult {
	result := criteriaBatchResult{AllOK: false, PreparationFailed: true, PreparationClass: failure.Class, Summaries: []string{failure.Error()}}
	detail, _ := json.Marshal(map[string]any{"cause": failure.Cause, "causeClass": failure.Class, "exitCode": failure.Exit, "preparationFailed": true, "output": failure.Detail})
	for _, criterion := range criteria {
		status := "failed"
		if criterion.Kind == "manual" {
			status = "needs_review"
			result.NeedsReview = true
		}
		result.Criteria = append(result.Criteria, agent.CriterionEvidence{CriterionID: criterion.ID, Text: criterion.Text, Kind: criterion.Kind, Status: status,
			Check: &agent.CheckEvidence{Tool: "run_command", Arguments: failure.Arguments, ExitCode: &failure.Exit, Detail: string(detail), Status: "unavailable"}})
	}
	return result
}
