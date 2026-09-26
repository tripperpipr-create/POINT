// Разбор прогона: годится ли он в опыт и что из него следует.
package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
)

// reviewAgentRun turns one verified complex trajectory into a project-scoped,
// versioned skill. Calls are serialized because two runs may finish together
// and target the same learned procedure.
func (a *App) reviewAgentRun(ctx context.Context, run domain.Run, projectAgentID, apiKey string) (domain.AgentImprovement, error) {
	a.learningReviewMu.Lock()
	defer a.learningReviewMu.Unlock()
	if existing, err := a.store.FindAgentImprovementByRun(ctx, run.ID); err == nil {
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return domain.AgentImprovement{}, err
	}
	if run.WorkspaceID == "" || strings.TrimSpace(projectAgentID) == "" {
		return domain.AgentImprovement{}, errors.New("project-agent runs require workspace and agent identity")
	}
	switch run.Status {
	case domain.RunCompleted, domain.RunFailed, domain.RunInterrupted:
	default:
		return domain.AgentImprovement{}, errors.New("only terminal project-agent runs can be reviewed")
	}
	agent, err := a.store.GetProjectAgent(ctx, projectAgentID)
	if err != nil {
		return domain.AgentImprovement{}, err
	}
	// Direct review callers (manual retry and compatibility paths) may not have
	// passed through queueAgentImprovement. The evaluation lifecycle still has
	// to make a temporary specialist non-runnable before a proposal is exposed.
	if agent.Temporary && strings.TrimSpace(agent.ParentAgentID) != "" && agent.Status == domain.ProjectAgentActive {
		if err = a.store.SetProjectAgentStatus(ctx, agent.ID, domain.ProjectAgentActive, domain.ProjectAgentEvaluationPending); err != nil {
			return domain.AgentImprovement{}, err
		}
		agent.Status = domain.ProjectAgentEvaluationPending
	}
	if agent.WorkspaceID != run.WorkspaceID {
		return domain.AgentImprovement{}, errors.New("run and project agent belong to different workspaces")
	}
	report, err := a.runDiagnostics(ctx, run)
	if err != nil {
		return domain.AgentImprovement{}, err
	}
	trajectory, err := a.learningTrajectory(ctx, run, report)
	if err != nil {
		return domain.AgentImprovement{}, err
	}
	trigger := learningReviewTrigger(report, trajectory)
	if trigger == "" {
		return domain.AgentImprovement{}, fmt.Errorf("%w: health=%s tools=%d verification=%t", errAgentLearningIneligible, report.Health, trajectory.ToolCalls, report.Verification.Recorded)
	}
	trajectory.Tools = allowedLearningTools(trajectory.Tools, agent.AllowedTools)
	if len(trajectory.Tools) == 0 {
		return domain.AgentImprovement{}, fmt.Errorf("%w: verified trajectory has no currently allowed reusable tools", errAgentLearningIneligible)
	}

	var blueprint *domain.AgentBlueprint
	ownerID := projectAgentID
	ownerKind := "project_agent"
	if strings.TrimSpace(agent.BlueprintID) != "" {
		stored, blueprintErr := a.store.GetBlueprint(ctx, agent.BlueprintID)
		if blueprintErr != nil && !errors.Is(blueprintErr, sql.ErrNoRows) {
			return domain.AgentImprovement{}, blueprintErr
		}
		if blueprintErr == nil {
			blueprint = &stored
			ownerID = stored.ID
			ownerKind = "blueprint"
		}
	}
	signature := workflowDigest(ownerID, agent.RoleDescription, trajectory.Tools, report.Verification.Required || trajectory.VerificationRequired)
	skills, err := a.store.ListSkills(ctx)
	if err != nil {
		return domain.AgentImprovement{}, err
	}
	var previous *domain.SkillDefinition
	for index := range skills {
		if managedSkillMatches(skills[index], ownerID, signature) {
			copy := skills[index]
			previous = &copy
			break
		}
	}
	// Skills created by the first implementation were owned by a project-agent.
	// Let that same agent migrate its history to blueprint ownership lazily.
	if previous == nil && blueprint != nil {
		legacySignature := workflowDigest(projectAgentID, agent.RoleDescription, trajectory.Tools, report.Verification.Required || trajectory.VerificationRequired)
		for index := range skills {
			if managedSkillMatches(skills[index], projectAgentID, legacySignature) {
				copy := skills[index]
				previous = &copy
				break
			}
		}
	}
	// Also match legacy tool-order signatures so old skills remain patchable.
	if previous == nil {
		legacyToolSignature := learningSignature(ownerID, trajectory.Tools)
		for index := range skills {
			if managedSkillMatches(skills[index], ownerID, legacyToolSignature) {
				copy := skills[index]
				previous = &copy
				signature = legacyToolSignature
				break
			}
		}
	}
	// A failure used to mint a new Skill whenever the tool set differed a
	// little: the developer agent ended with 18 near-identical «record
	// verification evidence» Skills eating half its context. One failure
	// category keeps one recovery Skill per agent, and the reviewer revises it
	// with the previous text in view.
	failureCategory := learningFailureCategory(report)
	if previous == nil && trigger == learningTriggerFailure {
		previous = equippedRecoverySkill(skills, agent, failureCategory)
	}
	skillLocked := previous != nil && trigger != learningTriggerFeedback && trigger != learningTriggerFailure && runLoadedExactSkillRevision(run, *previous)

	review, mode, modelFailure := a.generateLearningReview(ctx, run, agent, trajectory, previous, trigger, apiKey)
	if agent.Temporary && strings.TrimSpace(agent.ParentAgentID) != "" && strings.TrimSpace(modelFailure) != "" {
		// A deterministic learning fallback is useful for permanent agents, but
		// it must never authorize a reusable Blueprint. Keep the temporary agent
		// pending so the exact evidence can be reviewed again when the model is
		// available.
		return domain.AgentImprovement{}, fmt.Errorf("%w: %s", errSubagentEvaluatorUnavailable, security.Redact(modelFailure))
	}
	if skillLocked {
		// Exact loaded skill revision: do not mint a new Skill ID, but still
		// allow Memory / Instruction learning from the reviewer.
		review.Decision = "skip"
	}
	now := time.Now().UTC()
	item := domain.AgentImprovement{
		ID: domain.NewID("improvement"), WorkspaceID: run.WorkspaceID, ProjectAgentID: projectAgentID,
		BlueprintID: agent.BlueprintID,
		SourceRunID: run.ID, Status: "applying", Trigger: trigger, Evidence: trajectory.Evidence,
		BeforeSkillIDs: append([]string(nil), agent.SkillIDs...), ReviewMode: mode, Model: run.Model,
		Failure: truncateRunes(security.Redact(modelFailure), 1000), CreatedAt: now, UpdatedAt: now,
	}
	if review.Decision == "skip" && review.MemoryDecision != "learn" && review.InstructionDecision != "learn" {
		item.Status = "skipped"
		if agent.Temporary && strings.TrimSpace(agent.ParentAgentID) != "" {
			item.Kind = "subagent_specialization"
			item.Evidence = append(item.Evidence, "Master found no reusable benefit from the temporary subagent")
		}
		if err = a.store.SaveAgentImprovement(ctx, item); err != nil {
			return domain.AgentImprovement{}, err
		}
		if skillLocked {
			_ = a.markRunLearningSignals(ctx, run.ID, "consumed")
		} else {
			_ = a.markRunLearningSignals(ctx, run.ID, "reviewed")
		}
		return item, nil
	}
	evaluation := evaluateLearningCandidate(review, previous, agent, trajectory, report, trigger, now)
	item.Evidence = append(item.Evidence, learningEvaluationEvidence(evaluation)...)
	if !evaluation.Passed {
		item.Status = "skipped"
		item.Failure = learningEvaluationFailure(evaluation)
		if err = a.store.SaveAgentImprovement(ctx, item); err != nil {
			return domain.AgentImprovement{}, err
		}
		_ = a.markRunLearningSignals(ctx, run.ID, "reviewed")
		return item, nil
	}
	if !skillLocked && previous != nil && fmt.Sprint(previous.Configuration["promotionStatus"]) == "candidate" {
		return a.extendExistingSkillCanaryLocked(ctx, *previous, agent, run, review, blueprint, evaluation)
	}

	revision, sourceRuns, sourceWorkspaces, sourceAgents := nextLearningEvidence(previous, run.ID, run.WorkspaceID, agent.ID)
	var learned domain.SkillDefinition
	promotionStatus := ""
	if review.Decision != "skip" {
		learned, promotionStatus = buildLearnedSkill(review, previous, agent, blueprint, ownerID, ownerKind, signature, trajectory.Tools, sourceRuns, sourceWorkspaces, sourceAgents, revision, now, trigger == learningTriggerFailure)
		learned.Configuration["evalGate"] = evaluation
		learned.Configuration["workflowDigest"] = signature
		if trigger == learningTriggerFailure {
			learned.Configuration["failureCategory"] = failureCategory
		}
		item.SkillID = learned.ID
		item.PromotionStatus = promotionStatus
		item.AfterSkill = skillPointer(learned)
		if trigger == learningTriggerFailure {
			item.Kind = "skill_recovery"
		} else if previous == nil {
			item.Kind = "skill_created"
		} else {
			item.Kind = "skill_updated"
			item.BeforeSkill = skillPointer(*previous)
		}
	} else if review.MemoryDecision == "learn" {
		item.Kind = "memory_learned"
	} else {
		item.Kind = "instruction_learned"
	}
	if agent.Temporary && strings.TrimSpace(agent.ParentAgentID) != "" {
		item.Kind = "subagent_specialization"
		item.Evidence = append(item.Evidence,
			"temporary subagent usefulness reviewed for parent agent "+agent.ParentAgentID,
			"Blueprint promotion requires an explicit user decision")
		if review.Decision != "skip" {
			promotionStatus = "candidate"
			item.PromotionStatus = "candidate"
			learned.Configuration = cloneAnyMap(learned.Configuration)
			learned.Configuration["promotionStatus"] = "candidate"
			learned.Configuration["promotionReason"] = "temporary_subagent_usefulness_review"
			learned.Configuration["parentAgentId"] = agent.ParentAgentID
			item.AfterSkill = skillPointer(learned)
		}
	}
	if err = a.planLearningInstruction(ctx, &item, review, blueprint, run); err != nil {
		return domain.AgentImprovement{}, err
	}
	var bindings []domain.ProjectAgent
	var beforeBindings, beforeRules map[string][]string
	if review.Decision != "skip" {
		bindings, beforeBindings, beforeRules, err = a.learningBindings(ctx, agent, learned, promotionStatus, item.Instruction, item.InstructionStatus)
		if err != nil {
			return domain.AgentImprovement{}, err
		}
		item.BeforeAgentSkillIDs = beforeBindings
		item.AfterAgentSkillIDs = snapshotAgentSkillIDs(bindings)
		item.BeforeAgentRules = beforeRules
		item.AfterAgentRules = snapshotAgentRules(bindings)
		item.BeforeSkillIDs = append([]string(nil), beforeBindings[agent.ID]...)
		item.AfterSkillIDs = append([]string(nil), item.AfterAgentSkillIDs[agent.ID]...)
		if blueprint != nil && promotionStatus == "promoted" {
			item.BeforeBlueprintSkillIDs = append([]string(nil), blueprint.SkillIDs...)
			if !slices.Contains(blueprint.SkillIDs, learned.ID) {
				blueprint.SkillIDs = append(blueprint.SkillIDs, learned.ID)
			}
			item.AfterBlueprintSkillIDs = append([]string(nil), blueprint.SkillIDs...)
		}
		item.Evidence = append(item.Evidence, learningPromotionEvidence(promotionStatus, fmt.Sprint(learned.Configuration["promotionReason"]), len(sourceWorkspaces)))
		shadow := a.evaluateLearningShadowBenchmark(ctx, agent, previous, &learned, true)
		item.ShadowEvaluation = &shadow
		item.Evidence = append(item.Evidence, "shadow: "+shadow.Status+" (historical benchmark compare)")
		for _, reason := range shadow.Reasons {
			item.Evidence = append(item.Evidence, "shadow/"+reason)
		}
	}
	memoryToSave, err := a.planLearningMemory(ctx, &item, review, blueprint, agent, run, now)
	if err != nil {
		return domain.AgentImprovement{}, err
	}
	if err = a.store.SaveAgentImprovement(ctx, item); err != nil {
		return domain.AgentImprovement{}, err
	}
	if review.Decision != "skip" {
		if err = a.validateSkillRequiredTools(learned.RequiredTools); err != nil {
			return a.failAgentImprovement(ctx, item, err)
		}
		if err = a.store.SaveSkill(ctx, learned); err != nil {
			return a.failAgentImprovement(ctx, item, err)
		}
		configuredWorkspaces := map[string]bool{}
		for _, boundAgent := range bindings {
			if slices.Contains(boundAgent.SkillIDs, learned.ID) && !configuredWorkspaces[boundAgent.WorkspaceID] {
				if err = a.ensureLearnedProjectSkill(ctx, boundAgent.WorkspaceID, learned.ID, ownerID, ownerKind, promotionStatus, now); err != nil {
					return a.failAgentImprovement(ctx, item, err)
				}
				configuredWorkspaces[boundAgent.WorkspaceID] = true
			}
			boundAgent.UpdatedAt = now
			if err = a.store.SaveProjectAgent(ctx, boundAgent); err != nil {
				return a.failAgentImprovement(ctx, item, err)
			}
		}
	}
	if memoryToSave != nil {
		if err = a.store.SaveMemory(ctx, *memoryToSave); err != nil {
			return a.failAgentImprovement(ctx, item, err)
		}
	}
	if blueprint != nil && (promotionStatus == "promoted" || item.InstructionStatus == "promoted") {
		blueprint.UpdatedAt = now
		if err = a.store.SaveBlueprint(ctx, *blueprint); err != nil {
			return a.failAgentImprovement(ctx, item, err)
		}
	}
	// New skill apply is always unproven; proven only via canary improved or later real shadow pass.
	item.Status = learningProofStatus(item.ShadowEvaluation, nil)
	item.Effect = "insufficient_sample"
	item.UpdatedAt = time.Now().UTC()
	if err = a.store.SaveAgentImprovement(ctx, item); err != nil {
		return a.failAgentImprovement(ctx, item, err)
	}
	principleKind := "success"
	if trigger == learningTriggerFailure {
		principleKind = "failure"
	}
	_ = a.indexLearningPrinciple(ctx, item, run, a.learningQuestID(ctx, run, trajectory), principleKind)
	_ = a.markRunLearningSignals(ctx, run.ID, "consumed")
	return item, nil
}

func (a *App) extendExistingSkillCanaryLocked(ctx context.Context, skill domain.SkillDefinition, agent domain.ProjectAgent, run domain.Run, review learningReview, blueprint *domain.AgentBlueprint, evaluation learningEvaluation) (domain.AgentImprovement, error) {
	item, err := a.appliedImprovementForSkillRevision(ctx, skill.ID, skillLearningRevisionNumber(skill.Configuration["revision"]))
	if err != nil {
		return domain.AgentImprovement{}, err
	}
	if item.PromotionStatus != "candidate" {
		return item, nil
	}
	now := time.Now().UTC()
	item.Evidence = append(item.Evidence, learningEvaluationEvidence(evaluation)...)
	evidence := fmt.Sprintf("canary назначен проекту %s по verified Run %s", agent.WorkspaceID, run.ID)
	if !slices.Contains(item.Evidence, evidence) {
		item.Evidence = append(item.Evidence, evidence)
	}
	if err = a.planLearningInstruction(ctx, &item, review, blueprint, run); err != nil {
		return domain.AgentImprovement{}, err
	}
	bindings, beforeBindings, beforeRules, err := a.learningBindings(ctx, agent, skill, "candidate", item.Instruction, item.InstructionStatus)
	if err != nil {
		return domain.AgentImprovement{}, err
	}
	item.BeforeAgentSkillIDs = mergeStringSliceSnapshots(item.BeforeAgentSkillIDs, beforeBindings, false)
	item.BeforeAgentRules = mergeStringSliceSnapshots(item.BeforeAgentRules, beforeRules, false)
	item.AfterAgentSkillIDs = mergeStringSliceSnapshots(item.AfterAgentSkillIDs, snapshotAgentSkillIDs(bindings), true)
	item.AfterAgentRules = mergeStringSliceSnapshots(item.AfterAgentRules, snapshotAgentRules(bindings), true)
	memoryToSave, err := a.planLearningMemory(ctx, &item, review, blueprint, agent, run, now)
	if err != nil {
		return domain.AgentImprovement{}, err
	}
	item.Status = "applying"
	item.UpdatedAt = now
	if err = a.store.SaveAgentImprovement(ctx, item); err != nil {
		return domain.AgentImprovement{}, err
	}
	if memoryToSave != nil {
		if err = a.store.SaveMemory(ctx, *memoryToSave); err != nil {
			return a.failAgentImprovement(ctx, item, err)
		}
	}
	configuredWorkspaces := map[string]bool{}
	for _, boundAgent := range bindings {
		if slices.Contains(boundAgent.SkillIDs, skill.ID) && !configuredWorkspaces[boundAgent.WorkspaceID] {
			if err = a.ensureLearnedProjectSkill(ctx, boundAgent.WorkspaceID, skill.ID, fmt.Sprint(skill.Configuration["ownerId"]), fmt.Sprint(skill.Configuration["ownerKind"]), "candidate", now); err != nil {
				return a.failAgentImprovement(ctx, item, err)
			}
			configuredWorkspaces[boundAgent.WorkspaceID] = true
		}
		boundAgent.UpdatedAt = now
		if err = a.store.SaveProjectAgent(ctx, boundAgent); err != nil {
			return a.failAgentImprovement(ctx, item, err)
		}
	}
	if blueprint != nil && item.InstructionStatus == "promoted" {
		blueprint.UpdatedAt = now
		if err = a.store.SaveBlueprint(ctx, *blueprint); err != nil {
			return a.failAgentImprovement(ctx, item, err)
		}
	}
	item.Status = "applied_unproven"
	item.Effect = "insufficient_sample"
	item.UpdatedAt = time.Now().UTC()
	if err = a.store.SaveAgentImprovement(ctx, item); err != nil {
		return a.failAgentImprovement(ctx, item, err)
	}
	_ = a.markRunLearningSignals(ctx, run.ID, "consumed")
	return item, nil
}

func mergeStringSliceSnapshots(current, updates map[string][]string, overwrite bool) map[string][]string {
	if current == nil {
		current = map[string][]string{}
	}
	for key, values := range updates {
		if _, exists := current[key]; exists && !overwrite {
			continue
		}
		current[key] = append([]string(nil), values...)
	}
	return current
}

func (a *App) appliedImprovementForSkillRevision(ctx context.Context, skillID string, revision int) (domain.AgentImprovement, error) {
	items, err := a.store.ListAgentImprovementsForSkill(ctx, skillID, 1000)
	if err != nil {
		return domain.AgentImprovement{}, err
	}
	for _, item := range items {
		if improvementIsApplied(item.Status) && item.AfterSkill != nil && skillLearningRevisionNumber(item.AfterSkill.Configuration["revision"]) == revision {
			return item, nil
		}
	}
	return domain.AgentImprovement{}, sql.ErrNoRows
}

func runLoadedExactSkillRevision(run domain.Run, skill domain.SkillDefinition) bool {
	expected := domain.SkillDefinitionAttribution(skill)
	for _, attribution := range run.ConfigurationSnapshot.SkillAttributions {
		if attribution.SkillID == expected.SkillID && attribution.Revision == expected.Revision && attribution.Digest == expected.Digest {
			return true
		}
	}
	return false
}

func skillLearningRevisionNumber(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		return 0
	}
}
