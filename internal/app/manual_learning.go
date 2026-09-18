package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/security"
)

type ManualLearningRequest struct {
	ProjectAgentID    string `json:"projectAgentId"`
	Kind              string `json:"kind"`  // memory | instruction
	Scope             string `json:"scope"` // project | profile
	Content           string `json:"content"`
	ConfirmationToken string `json:"confirmationToken,omitempty"`
}

type ManualLearningPreview struct {
	ProjectAgentID    string   `json:"projectAgentId"`
	AgentName         string   `json:"agentName"`
	BlueprintID       string   `json:"blueprintId,omitempty"`
	Kind              string   `json:"kind"`
	Scope             string   `json:"scope"`
	Content           string   `json:"content"`
	Summary           string   `json:"summary"`
	Changes           []string `json:"changes"`
	Warnings          []string `json:"warnings"`
	NoChange          bool     `json:"noChange"`
	ConfirmationToken string   `json:"confirmationToken"`
}

// PreviewManualLearning is side-effect free. ApplyManualLearning recomputes
// this token from current snapshots, so a stale confirmation cannot overwrite
// a newer agent or Blueprint edit.
func (a *App) PreviewManualLearning(request ManualLearningRequest) (ManualLearningPreview, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return ManualLearningPreview{}, err
	}
	request.ProjectAgentID = strings.TrimSpace(request.ProjectAgentID)
	request.Kind = strings.ToLower(strings.TrimSpace(request.Kind))
	request.Scope = strings.ToLower(strings.TrimSpace(request.Scope))
	request.Content = strings.TrimSpace(request.Content)
	if request.ProjectAgentID == "" || request.Content == "" {
		return ManualLearningPreview{}, errors.New("projectAgentId and content are required")
	}
	if request.Kind != "memory" && request.Kind != "instruction" {
		return ManualLearningPreview{}, errors.New("manual learning kind must be memory or instruction")
	}
	if request.Scope != "project" && request.Scope != "profile" {
		return ManualLearningPreview{}, errors.New("manual learning scope must be project or profile")
	}
	limit := 2000
	if request.Kind == "instruction" || request.Scope == "profile" {
		limit = 600
	}
	if len([]rune(request.Content)) > limit {
		return ManualLearningPreview{}, fmt.Errorf("manual lesson exceeds %d characters", limit)
	}
	if security.Redact(request.Content) != request.Content || strings.Contains(request.Content, "[REDACTED]") {
		return ManualLearningPreview{}, errors.New("manual lesson appears to contain a secret; remove it before teaching")
	}
	ctx := context.Background()
	agent, err := a.store.GetProjectAgent(ctx, request.ProjectAgentID)
	if err != nil {
		return ManualLearningPreview{}, err
	}
	if agent.WorkspaceID != ws.ID {
		return ManualLearningPreview{}, errForeignWorld
	}
	var blueprint domain.AgentBlueprint
	if request.Scope == "profile" {
		if strings.TrimSpace(agent.BlueprintID) == "" {
			return ManualLearningPreview{}, errors.New("universal learning requires an agent with a Blueprint")
		}
		blueprint, err = a.store.GetBlueprint(ctx, agent.BlueprintID)
		if err != nil {
			return ManualLearningPreview{}, err
		}
		if !safePortableMemory(request.Content) {
			return ManualLearningPreview{}, errors.New("universal lesson must be portable and must not contain repository paths or secrets")
		}
	}
	preview := ManualLearningPreview{
		ProjectAgentID: agent.ID, AgentName: agent.Name, BlueprintID: agent.BlueprintID,
		Kind: request.Kind, Scope: request.Scope, Content: request.Content,
		Warnings: []string{"Изменение будет применено только после отдельного подтверждения и останется в журнале с точным откатом."},
	}
	if request.Kind == "memory" {
		ownerID, kind, workspaceID := agent.ID, domain.MemoryAgent, ws.ID
		if request.Scope == "profile" {
			ownerID, kind, workspaceID = blueprint.ID, domain.MemoryProfile, ""
			preview.Summary = "Добавить переносимую память в основной профиль " + blueprint.Name
			preview.Changes = []string{"Новая закреплённая Memory будет доступна этому специалисту в других проектах."}
		} else {
			preview.Summary = "Добавить локальную память агенту " + agent.Name
			preview.Changes = []string{"Новая закреплённая Memory останется в текущем проекте."}
		}
		memories, listErr := a.store.ListMemories(ctx, ws.ID)
		if listErr != nil {
			return ManualLearningPreview{}, listErr
		}
		for _, memory := range memories {
			if memory.WorkspaceID == workspaceID && memory.OwnerID == ownerID && memory.Kind == kind && strings.EqualFold(strings.TrimSpace(memory.Content), request.Content) {
				preview.NoChange = true
				preview.Changes = []string{"Такая Memory уже существует; повторная запись не нужна."}
				break
			}
		}
	} else if request.Scope == "profile" {
		preview.Summary = "Добавить постоянное правило в основной профиль " + blueprint.Name
		preview.Changes = []string{"Rule будет добавлено в Blueprint и в существующие экземпляры этого специалиста."}
		preview.NoChange = containsFold(blueprint.Rules, request.Content)
	} else {
		preview.Summary = "Добавить локальное правило агенту " + agent.Name
		preview.Changes = []string{"Rule будет действовать только у этого проектного агента."}
		preview.NoChange = containsFold(agent.Rules, request.Content)
	}
	if preview.NoChange && request.Kind == "instruction" {
		preview.Changes = []string{"Такое Rule уже существует; повторная запись не нужна."}
	}
	preview.ConfirmationToken = manualLearningToken(preview, agent.UpdatedAt, blueprint.UpdatedAt)
	return preview, nil
}

func (a *App) ApplyManualLearning(request ManualLearningRequest) (domain.AgentImprovement, error) {
	a.learningReviewMu.Lock()
	defer a.learningReviewMu.Unlock()
	preview, err := a.PreviewManualLearning(ManualLearningRequest{
		ProjectAgentID: request.ProjectAgentID, Kind: request.Kind, Scope: request.Scope, Content: request.Content,
	})
	if err != nil {
		return domain.AgentImprovement{}, err
	}
	if strings.TrimSpace(request.ConfirmationToken) == "" || request.ConfirmationToken != preview.ConfirmationToken {
		return domain.AgentImprovement{}, errors.New("manual learning confirmation is missing or stale; preview the lesson again")
	}
	if preview.NoChange {
		return domain.AgentImprovement{}, errors.New("manual lesson is already present")
	}
	ctx := context.Background()
	ws, _ := a.requireWorkspace()
	agent, err := a.store.GetProjectAgent(ctx, preview.ProjectAgentID)
	if err != nil {
		return domain.AgentImprovement{}, err
	}
	now := time.Now().UTC()
	item := domain.AgentImprovement{
		ID: "improvement-manual-" + preview.ConfirmationToken[:20], WorkspaceID: ws.ID,
		ProjectAgentID: agent.ID, BlueprintID: agent.BlueprintID,
		SourceRunID: "manual:" + preview.ConfirmationToken, Kind: preview.Kind + "_learned", Status: "applying",
		Trigger: "manual_teach", Evidence: []string{"явно подтверждено пользователем", "preview fingerprint: " + preview.ConfirmationToken[:12]},
		ReviewMode: "manual", CreatedAt: now, UpdatedAt: now,
		BeforeSkillIDs: append([]string(nil), agent.SkillIDs...), AfterSkillIDs: append([]string(nil), agent.SkillIDs...),
	}
	if existing, findErr := a.store.FindAgentImprovementByRun(ctx, item.SourceRunID); findErr == nil {
		return existing, nil
	} else if !errors.Is(findErr, sql.ErrNoRows) {
		return domain.AgentImprovement{}, findErr
	}
	if preview.Kind == "memory" {
		memory := domain.MemoryRecord{
			ID: "memory-manual-" + preview.ConfirmationToken[:20], WorkspaceID: ws.ID,
			Kind: domain.MemoryAgent, OwnerID: agent.ID, Content: preview.Content,
			Source: "manual-teach:" + item.ID, Confidence: 1, Pinned: true, CreatedAt: now, UpdatedAt: now,
		}
		item.MemoryStatus = "confirmed"
		if preview.Scope == "profile" {
			memory.WorkspaceID, memory.Kind, memory.OwnerID = "", domain.MemoryProfile, preview.BlueprintID
			item.MemoryStatus = "promoted"
		}
		item.MemoryID = memory.ID
		item.MemoryKey = "manual-lesson"
		item.MemorySignature = manualLearningSignature(memory.OwnerID, preview.Content)
		item.AfterMemory = &memory
		if err = a.store.SaveAgentImprovement(ctx, item); err != nil {
			return domain.AgentImprovement{}, err
		}
		if err = a.store.SaveMemory(ctx, memory); err != nil {
			return a.failAgentImprovement(ctx, item, err)
		}
	} else {
		item.Instruction = preview.Content
		item.InstructionKey = "manual-lesson"
		item.InstructionSignature = manualLearningSignature(map[bool]string{true: preview.BlueprintID, false: agent.ID}[preview.Scope == "profile"], preview.Content)
		item.InstructionStatus = "confirmed"
		if preview.Scope == "profile" {
			blueprint, getErr := a.store.GetBlueprint(ctx, preview.BlueprintID)
			if getErr != nil {
				return domain.AgentImprovement{}, getErr
			}
			item.InstructionStatus = "promoted"
			item.BeforeBlueprintRules = append([]string(nil), blueprint.Rules...)
			blueprint.Rules = appendUniqueFold(blueprint.Rules, preview.Content)
			item.AfterBlueprintRules = append([]string(nil), blueprint.Rules...)
			allAgents, listErr := a.store.ListAllProjectAgents(ctx)
			if listErr != nil {
				return domain.AgentImprovement{}, listErr
			}
			item.BeforeAgentRules, item.AfterAgentRules = map[string][]string{}, map[string][]string{}
			for _, bound := range allAgents {
				if bound.BlueprintID != blueprint.ID {
					continue
				}
				item.BeforeAgentRules[bound.ID] = append([]string(nil), bound.Rules...)
				item.AfterAgentRules[bound.ID] = appendUniqueFold(bound.Rules, preview.Content)
			}
			if err = a.store.SaveAgentImprovement(ctx, item); err != nil {
				return domain.AgentImprovement{}, err
			}
			blueprint.UpdatedAt = now
			if err = a.store.SaveBlueprint(ctx, blueprint); err != nil {
				return a.failAgentImprovement(ctx, item, err)
			}
			for _, bound := range allAgents {
				rules, ok := item.AfterAgentRules[bound.ID]
				if !ok {
					continue
				}
				bound.Rules, bound.UpdatedAt = append([]string(nil), rules...), now
				if err = a.store.SaveProjectAgent(ctx, bound); err != nil {
					return a.failAgentImprovement(ctx, item, err)
				}
			}
		} else {
			item.BeforeAgentRules = map[string][]string{agent.ID: append([]string(nil), agent.Rules...)}
			agent.Rules = appendUniqueFold(agent.Rules, preview.Content)
			item.AfterAgentRules = map[string][]string{agent.ID: append([]string(nil), agent.Rules...)}
			if err = a.store.SaveAgentImprovement(ctx, item); err != nil {
				return domain.AgentImprovement{}, err
			}
			agent.UpdatedAt = now
			if err = a.store.SaveProjectAgent(ctx, agent); err != nil {
				return a.failAgentImprovement(ctx, item, err)
			}
		}
	}
	item.Status = "applied"
	item.UpdatedAt = time.Now().UTC()
	if err = a.store.SaveAgentImprovement(ctx, item); err != nil {
		return domain.AgentImprovement{}, err
	}
	return item, nil
}

type ExperienceSearchItem struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"`
	Title          string    `json:"title"`
	Summary        string    `json:"summary"`
	Evidence       []string  `json:"evidence,omitempty"`
	RunID          string    `json:"runId,omitempty"`
	SkillID        string    `json:"skillId,omitempty"`
	ProjectAgentID string    `json:"projectAgentId,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	score          int
}

func (a *App) SearchExperience(query string, limit int) ([]ExperienceSearchItem, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if len([]rune(query)) < 2 {
		return nil, errors.New("experience query must contain at least 2 characters")
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	ctx := context.Background()
	memories, err := a.store.ListMemories(ctx, ws.ID)
	if err != nil {
		return nil, err
	}
	signals, err := a.store.ListLearningSignals(ctx, ws.ID, 500)
	if err != nil {
		return nil, err
	}
	outcomes, err := a.store.ListSkillOutcomes(ctx, ws.ID, 500)
	if err != nil {
		return nil, err
	}
	improvements, err := a.store.ListAgentImprovements(ctx, ws.ID, 500)
	if err != nil {
		return nil, err
	}
	items := make([]ExperienceSearchItem, 0)
	add := func(item ExperienceSearchItem, haystack string) {
		lower := strings.ToLower(haystack)
		if !strings.Contains(lower, query) {
			return
		}
		item.score = strings.Count(lower, query)
		if strings.Contains(strings.ToLower(item.Title), query) {
			item.score += 3
		}
		items = append(items, item)
	}
	for _, memory := range memories {
		add(ExperienceSearchItem{ID: memory.ID, Kind: "memory", Title: "Memory", Summary: memory.Content, CreatedAt: memory.UpdatedAt}, memory.Content+" "+memory.Source)
	}
	for _, signal := range signals {
		add(ExperienceSearchItem{ID: signal.ID, Kind: "signal", Title: signal.Summary, Summary: strings.Join(signal.Evidence, " · "), Evidence: signal.Evidence, RunID: signal.RunID, ProjectAgentID: signal.ProjectAgentID, CreatedAt: signal.UpdatedAt}, signal.Summary+" "+strings.Join(signal.Evidence, " "))
	}
	for _, outcome := range outcomes {
		title := fmt.Sprintf("%s · revision %d", outcome.SkillName, outcome.SkillRevision)
		summary := fmt.Sprintf("health=%s · tools=%d · failures=%d · verification=%t", outcome.Health, outcome.ToolCalls, outcome.ToolFailures, outcome.VerificationRecorded)
		add(ExperienceSearchItem{ID: outcome.ID, Kind: "skill_outcome", Title: title, Summary: summary, RunID: outcome.RunID, SkillID: outcome.SkillID, ProjectAgentID: outcome.ProjectAgentID, CreatedAt: outcome.CreatedAt}, title+" "+summary+" "+outcome.SkillDigest+" "+outcome.PromotionStatus)
	}
	for _, improvement := range improvements {
		title := improvement.Trigger
		if improvement.AfterSkill != nil {
			title = improvement.AfterSkill.Name
		}
		add(ExperienceSearchItem{ID: improvement.ID, Kind: "improvement", Title: title, Summary: strings.Join(improvement.Evidence, " · "), Evidence: improvement.Evidence, RunID: improvement.SourceRunID, SkillID: improvement.SkillID, ProjectAgentID: improvement.ProjectAgentID, CreatedAt: improvement.UpdatedAt}, title+" "+improvement.Instruction+" "+strings.Join(improvement.Evidence, " "))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].score != items[j].score {
			return items[i].score > items[j].score
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	for index := range items {
		items[index].score = 0
	}
	return items, nil
}

func manualLearningToken(preview ManualLearningPreview, agentUpdated, blueprintUpdated time.Time) string {
	value := strings.Join([]string{preview.ProjectAgentID, preview.BlueprintID, preview.Kind, preview.Scope, preview.Content, agentUpdated.UTC().Format(time.RFC3339Nano), blueprintUpdated.UTC().Format(time.RFC3339Nano)}, "\x00")
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func manualLearningSignature(ownerID, content string) string {
	digest := sha256.Sum256([]byte(ownerID + "\x00" + strings.ToLower(strings.TrimSpace(content))))
	return "manual-" + hex.EncodeToString(digest[:16])
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

func appendUniqueFold(values []string, value string) []string {
	if containsFold(values, value) {
		return append([]string(nil), values...)
	}
	return append(append([]string(nil), values...), value)
}
