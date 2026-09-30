package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// Продвижение кандидата переписывает запись улучшения. Раньше повторное
// сохранение теряло снимки Blueprint, и откат ставил Blueprint пустой
// список навыков — вместе с навыками, которые были у него до обучения.
func TestPromotedSkillRollbackKeepsBlueprintSkillsThatPredatedIt(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	tools := []string{"project_map", "list_files", "search_code", "read_file", "search_text"}
	handmade, err := application.SaveSkill(domain.SkillDefinition{Name: "Соглашения проекта", Instructions: "Следуй принятому стилю."})
	if err != nil {
		t.Fatal(err)
	}
	blueprint, err := application.SaveBlueprint(domain.AgentBlueprint{
		Name: "Backend", RoleDescription: "Permanent backend specialist", Provider: domain.ProviderOllama, BaseURL: unavailableLearningProvider(t),
		PrimaryModel: "qwen2.5-coder:7b", AllowedTools: tools, SkillIDs: []string{handmade.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstPath, secondPath := t.TempDir(), t.TempDir()
	firstWorkspace, err := application.OpenWorkspace(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	firstAgent, err := application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(firstWorkspace.Workspace.ID, blueprint))
	if err != nil {
		t.Fatal(err)
	}
	first, err := application.reviewAgentRun(ctx, saveLearningRun(t, application, firstWorkspace.Workspace.ID, firstAgent, "run-delta-first", time.Now().UTC()), firstAgent.ID, "")
	if err != nil || first.PromotionStatus != "candidate" || first.AfterSkill == nil {
		t.Fatalf("first project candidate=%#v err=%v", first, err)
	}
	secondWorkspace, err := application.OpenWorkspace(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	secondAgent, err := application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(secondWorkspace.Workspace.ID, blueprint))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.reviewAgentRun(ctx, saveLearningRun(t, application, secondWorkspace.Workspace.ID, secondAgent, "run-delta-second", time.Now().UTC().Add(time.Minute)), secondAgent.ID, ""); err != nil {
		t.Fatal(err)
	}
	attribution := domain.SkillDefinitionAttribution(*first.AfterSkill)
	for index, workspaceID := range []string{firstWorkspace.Workspace.ID, secondWorkspace.Workspace.ID, firstWorkspace.Workspace.ID} {
		outcome := canaryOutcome(attribution, domain.RunCompleted, "healthy", time.Now().UTC().Add(time.Duration(index)*time.Minute))
		outcome.ID = fmt.Sprintf("delta-canary-outcome-%d", index)
		outcome.RunID = fmt.Sprintf("delta-canary-run-%d", index)
		outcome.WorkspaceID = workspaceID
		if err = application.store.SaveSkillOutcome(ctx, outcome); err != nil {
			t.Fatal(err)
		}
	}
	if err = application.evaluateAppliedSkillCanary(ctx, first.SkillID); err != nil {
		t.Fatal(err)
	}
	if _, err = application.OpenWorkspace(firstPath); err != nil {
		t.Fatal(err)
	}
	if _, err = application.PromoteAgentImprovement(first.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := application.store.GetAgentImprovement(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(stored.BeforeBlueprintSkillIDs, handmade.ID) || !slices.Contains(stored.AfterBlueprintSkillIDs, first.SkillID) {
		t.Fatalf("promotion snapshots were not persisted: before=%#v after=%#v", stored.BeforeBlueprintSkillIDs, stored.AfterBlueprintSkillIDs)
	}
	if _, err = application.RollbackAgentImprovement(first.ID); err != nil {
		t.Fatal(err)
	}
	storedBlueprint, err := application.store.GetBlueprint(ctx, blueprint.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(storedBlueprint.SkillIDs, handmade.ID) || slices.Contains(storedBlueprint.SkillIDs, first.SkillID) {
		t.Fatalf("rollback must remove only the promoted skill: %#v", storedBlueprint.SkillIDs)
	}
	for _, agentID := range []string{firstAgent.ID, secondAgent.ID} {
		agent, getErr := application.store.GetProjectAgent(ctx, agentID)
		if getErr != nil || !slices.Contains(agent.SkillIDs, handmade.ID) || slices.Contains(agent.SkillIDs, first.SkillID) {
			t.Fatalf("agent %s after rollback: %#v err=%v", agentID, agent.SkillIDs, getErr)
		}
	}
}

// Откат старого урока снимает только его правило. Навык и урок, которые
// появились у агента позже, остаются на месте.
func TestRollbackOfOlderLessonKeepsLaterAgentChanges(t *testing.T) {
	application := newTestApp(t)
	ctx := context.Background()
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agent, err := application.SaveProjectAgent(domain.ProjectAgent{
		WorkspaceID: view.Workspace.ID, Name: "QA", Provider: domain.ProviderOllama, BaseURL: "http://127.0.0.1:11434", PrimaryModel: "qwen2.5-coder:7b",
	})
	if err != nil {
		t.Fatal(err)
	}
	teach := func(content string) domain.AgentImprovement {
		t.Helper()
		request := ManualLearningRequest{ProjectAgentID: agent.ID, Kind: "instruction", Scope: "project", Content: content}
		preview, previewErr := application.PreviewManualLearning(request)
		if previewErr != nil {
			t.Fatal(previewErr)
		}
		request.ConfirmationToken = preview.ConfirmationToken
		applied, applyErr := application.ApplyManualLearning(request)
		if applyErr != nil {
			t.Fatal(applyErr)
		}
		return applied
	}
	older := teach("Record the verifier output before reporting completion.")
	later, err := application.SaveSkill(domain.SkillDefinition{Name: "Поздний навык", Instructions: "Добавлен после первого урока."})
	if err != nil {
		t.Fatal(err)
	}
	current, err := application.store.GetProjectAgent(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.SkillIDs = append(current.SkillIDs, later.ID)
	if _, err = application.SaveProjectAgent(current); err != nil {
		t.Fatal(err)
	}
	newer := teach("Name the failing criterion in the final report.")
	if _, err = application.RollbackAgentImprovement(older.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := application.store.GetProjectAgent(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(stored.Rules, older.Instruction) {
		t.Fatalf("rolled-back lesson survived: %#v", stored.Rules)
	}
	if !slices.Contains(stored.Rules, newer.Instruction) || !slices.Contains(stored.SkillIDs, later.ID) {
		t.Fatalf("rollback erased later changes: rules=%#v skills=%#v", stored.Rules, stored.SkillIDs)
	}
}

func TestRevertListDeltaRestoresRemovedValueInPlace(t *testing.T) {
	delta := diffLists([]string{"a", "old", "c"}, []string{"a", "c", "new"})
	got := revertListDelta([]string{"a", "c", "new", "later"}, delta)
	if want := []string{"a", "old", "c", "later"}; !slices.Equal(got, want) {
		t.Fatalf("revert=%#v want %#v", got, want)
	}
}

// Рецензент выучил одно правило, без навыка. Продвинутое в Blueprint, оно
// обязано дойти до уже созданных агентов: промпт читает правила агента.
func TestPromotedInstructionWithoutSkillReachesExistingAgents(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	instruction := "Before reporting completion, record an explicit successful verifier result appropriate to the change."
	reviewJSON, err := json.Marshal(map[string]any{
		"decision": "skip", "memoryDecision": "skip",
		"instructionDecision": "learn", "instructionKey": "verify-before-finish", "instruction": instruction,
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writePlannerSSE(t, w, string(reviewJSON), 120, 60)
	}))
	defer provider.Close()
	application := newTestApp(t)
	ctx := context.Background()
	blueprint, err := application.SaveBlueprint(domain.AgentBlueprint{
		Name: "QA", RoleDescription: "Permanent quality specialist", Provider: domain.ProviderOpenAI, ProviderPreset: "openai",
		BaseURL: provider.URL, PrimaryModel: "review-model", AllowedTools: []string{"project_map", "list_files", "search_code", "read_file", "search_text"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var agents []domain.ProjectAgent
	var last domain.AgentImprovement
	for index := 0; index < 2; index++ {
		view, openErr := application.OpenWorkspace(t.TempDir())
		if openErr != nil {
			t.Fatal(openErr)
		}
		agent, saveErr := application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(view.Workspace.ID, blueprint))
		if saveErr != nil {
			t.Fatal(saveErr)
		}
		agents = append(agents, agent)
		run := saveLearningRun(t, application, view.Workspace.ID, agent, fmt.Sprintf("run-instruction-only-%d", index), time.Now().UTC().Add(time.Duration(index)*time.Minute))
		if last, err = application.reviewAgentRun(ctx, run, agent.ID, "transient-key"); err != nil {
			t.Fatal(err)
		}
	}
	if last.InstructionStatus != "promoted" {
		t.Fatalf("instruction was not promoted on the second project: %#v", last)
	}
	for _, agent := range agents {
		stored, getErr := application.store.GetProjectAgent(ctx, agent.ID)
		if getErr != nil || !slices.Contains(stored.Rules, instruction) {
			t.Fatalf("existing agent %s did not receive the promoted instruction: %#v err=%v", agent.ID, stored.Rules, getErr)
		}
	}
}

// Кандидат, по которому провалился прогон, получает ревизию с текстом
// рецензента. Раньше провал лишь расширял канарейку, а исправление терялось.
func TestFailedRunRevisesCandidateSkillInsteadOfExtendingCanary(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	recovery := "When a tool fails, inspect its output before retrying and record an explicit verifier result."
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		review := map[string]any{"decision": "create", "name": "Evidence-first change", "description": "Reusable verified workflow.",
			"instructions": "Inspect the relevant context, make the bounded change, and record an explicit verifier result.",
			"memoryDecision": "skip", "instructionDecision": "skip"}
		if calls.Add(1) > 2 {
			review["decision"], review["name"], review["instructions"] = "update", "Recover from tool failures", recovery
		}
		encoded, _ := json.Marshal(review)
		writePlannerSSE(t, w, string(encoded), 120, 60)
	}))
	defer provider.Close()
	application := newTestApp(t)
	ctx := context.Background()
	blueprint, err := application.SaveBlueprint(domain.AgentBlueprint{
		Name: "Backend", RoleDescription: "Permanent backend specialist", Provider: domain.ProviderOpenAI, ProviderPreset: "openai",
		BaseURL: provider.URL, PrimaryModel: "review-model", AllowedTools: []string{"project_map", "list_files", "search_code", "read_file", "search_text"},
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := application.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agent, err := application.SaveProjectAgent(domain.ProjectAgentFromBlueprint(view.Workspace.ID, blueprint))
	if err != nil {
		t.Fatal(err)
	}
	first, err := application.reviewAgentRun(ctx, saveLearningRun(t, application, view.Workspace.ID, agent, "run-candidate-source", time.Now().UTC()), agent.ID, "key")
	if err != nil || first.PromotionStatus != "candidate" || first.AfterSkill == nil {
		t.Fatalf("candidate=%#v err=%v", first, err)
	}
	failed := saveLearningRun(t, application, view.Workspace.ID, agent, "run-candidate-failed", time.Now().UTC().Add(time.Minute))
	failed.Status = domain.RunFailed
	if err = application.store.SaveRun(ctx, failed); err != nil {
		t.Fatal(err)
	}
	second, err := application.reviewAgentRun(ctx, failed, agent.ID, "key")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID || second.AfterSkill == nil || second.AfterSkill.Instructions != recovery {
		t.Fatalf("failure fix was dropped: id=%s first=%s status=%s failure=%q", second.ID, first.ID, second.Status, second.Failure)
	}
	if skillFamilyField(second.AfterSkill.Configuration, "supersedesSkillId") != first.SkillID {
		t.Fatalf("revision does not supersede the candidate: %#v", second.AfterSkill.Configuration)
	}
	stored, _ := application.store.GetProjectAgent(ctx, agent.ID)
	if !slices.Contains(stored.SkillIDs, second.SkillID) || slices.Contains(stored.SkillIDs, first.SkillID) {
		t.Fatalf("agent skills after revision: %#v", stored.SkillIDs)
	}
}
