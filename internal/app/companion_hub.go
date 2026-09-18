package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"local-agent-workbench/internal/companion"
	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/policy"
	"local-agent-workbench/internal/storage"
)

func (a *App) CompanionChat(ctx context.Context, req companion.ChatRequest) (companion.ChatResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ws, err := a.requireWorkspace()
	if err != nil {
		return companion.ChatResponse{}, err
	}
	if req.WorkspaceID != "" && req.WorkspaceID != ws.ID {
		return companion.ChatResponse{}, errors.New("companion workspace does not match the open project")
	}
	req.WorkspaceID = ws.ID
	projectFS, _ := a.fs()
	skills := a.resolveCompanionSkills(ctx, ws.ID)
	svc := companion.Service{
		Store: a.store, ProjectContext: projectFS, ConfigResolver: a.resolveCompanionConnection,
		ModelFactory:           a.budgetedModelFactory(modelBudgetScope{WorkspaceID: ws.ID, ProjectAgentID: "companion", Outcome: "companion_model"}),
		UsageManagedExternally: true,
		Skills:                 skills,
		WorkspaceRoot:          ws.Path,
		ToolBridge:             companionToolBridge{app: a},
	}
	// Инструменты появляются только вместе с рабочей папкой. Присваивать поле
	// безусловно нельзя: интерфейс с нулевым указателем внутри сам по себе не
	// нулевой, и компаньон принял бы его за рабочий набор.
	if projectFS != nil {
		svc.Tools = newCompanionReadTools(projectFS, skills)
	}
	return svc.Chat(ctx, req)
}

type CompanionLive struct {
	Companion               *domain.CompanionConfig        `json:"companion,omitempty"`
	CompanionInterventions  []domain.CompanionIntervention `json:"companionInterventions,omitempty"`
	CompanionDismissedCount int                            `json:"companionDismissedCount"`
}

func (a *App) CompanionLive(focusPath string) (CompanionLive, error) {
	workspace, err := a.requireWorkspace()
	if err != nil {
		return CompanionLive{}, err
	}
	ctx := context.Background()
	hub := HubBootstrap{}
	if hub.ProjectAgents, err = a.store.ListProjectAgents(ctx, workspace.ID); err != nil {
		return CompanionLive{}, err
	}
	if hub.Executions, err = a.store.ListExecutions(ctx, workspace.ID, 50); err != nil {
		return CompanionLive{}, err
	}
	if hub.ChangeSets, err = a.store.ListChangeSets(ctx, workspace.ID); err != nil {
		return CompanionLive{}, err
	}
	if hub.UsageRecords, err = a.store.ListUsageRecords(ctx, workspace.ID, 200); err != nil {
		return CompanionLive{}, err
	}
	if hub.Connections, err = a.store.ListConnections(ctx); err != nil {
		return CompanionLive{}, err
	}
	if hub.IDEObservations, err = a.store.ListIDEObservations(ctx, workspace.ID, 100); err != nil {
		return CompanionLive{}, err
	}
	svc := companion.Service{Store: a.store}
	cfg, err := svc.EnsureConfig(ctx, workspace.ID)
	if err != nil {
		return CompanionLive{}, err
	}
	hub.Companion = &cfg
	budget, err := a.loadHubBudgetSettings(ctx, workspace.ID)
	if err != nil {
		return CompanionLive{}, err
	}
	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	budgetInterventions := companion.BuildBudgetInterventions(companion.BudgetSnapshot{
		DailyLimitCents: budget.DailyCents, DailyUsedCents: a.usageCostCents(hub.UsageRecords, dayStart),
		MonthlyLimitCents: budget.MonthlyCents, MonthlyUsedCents: a.usageCostCents(hub.UsageRecords, monthStart), HardStop: budget.HardStop,
	})
	workspaceRuns, err := a.store.ListRunsForWorkspace(ctx, workspace.ID, 80)
	if err != nil {
		return CompanionLive{}, err
	}
	linkedRuns := make(map[string]domain.RunStatus, len(hub.Executions))
	for _, execution := range hub.Executions {
		if execution.RunID != "" {
			linkedRuns[execution.RunID] = execution.Status
		}
	}
	diagnosticRuns := make([]domain.Run, 0, min(12, len(workspaceRuns)))
	selectedRuns := map[string]bool{}
	for _, run := range workspaceRuns {
		status, linked := linkedRuns[run.ID]
		if linked && (status == domain.RunPending || status == domain.RunRunning || status == domain.RunWaiting || status == domain.RunPaused) {
			diagnosticRuns = append(diagnosticRuns, run)
			selectedRuns[run.ID] = true
		}
	}
	for _, run := range workspaceRuns {
		if len(diagnosticRuns) >= 12 {
			break
		}
		if _, linked := linkedRuns[run.ID]; linked && !selectedRuns[run.ID] {
			diagnosticRuns = append(diagnosticRuns, run)
			selectedRuns[run.ID] = true
		}
	}
	companionRunDiagnostics, err := a.recentRunDiagnostics(ctx, diagnosticRuns, len(diagnosticRuns))
	if err != nil {
		return CompanionLive{}, err
	}
	focus, _ := normalizeObservationPath(workspace.Path, focusPath)
	if focus == "" {
		focus = companion.LatestObservationFocus(hub.IDEObservations)
	}
	interventions := companion.MergeInterventionsWithObservations(
		hub.IDEObservations,
		companion.BuildInterventions(cfg, hub.ProjectAgents, hub.Executions, hub.ChangeSets, hub.UsageRecords, hub.Connections),
		budgetInterventions,
		companion.BuildExecutionInterventions(cfg, hub.Executions, workspaceRuns, now),
		companion.BuildRunDiagnosticInterventions(cfg, hub.Executions, companionRunDiagnostics),
		companion.BuildIDEInterventionsGated(hub.IDEObservations, cfg, companion.InterveneContext{Now: now, FocusPath: focus}),
	)
	visible, count, err := a.finalizeCompanionInterventions(ctx, workspace.ID, cfg, hub.IDEObservations, interventions, now, focus)
	if err != nil {
		return CompanionLive{}, err
	}
	return CompanionLive{Companion: &cfg, CompanionInterventions: visible, CompanionDismissedCount: count}, nil
}

func (a *App) finalizeCompanionInterventions(ctx context.Context, workspaceID string, cfg domain.CompanionConfig, observations []domain.IDEObservation, interventions []domain.CompanionIntervention, now time.Time, focusPath string) ([]domain.CompanionIntervention, int, error) {
	memory, err := a.loadCompanionGateMemory(ctx, workspaceID)
	if err != nil {
		return nil, 0, err
	}
	gated, nextMemory := companion.GateMergedInterventions(cfg, interventions, observations, companion.InterveneContext{Now: now, FocusPath: focusPath}, memory)
	if err = a.saveCompanionGateMemory(ctx, workspaceID, nextMemory); err != nil {
		return nil, 0, err
	}
	dismissed, err := a.loadDismissedCompanionInterventions(ctx, workspaceID)
	if err != nil {
		return nil, 0, err
	}
	activeOccurrences := make(map[string]bool, len(interventions))
	for _, item := range interventions {
		activeOccurrences[item.OccurrenceKey] = true
	}
	if len(dismissed) > 0 {
		activeDismissals := make(map[string]bool, len(dismissed))
		for key := range dismissed {
			if activeOccurrences[key] {
				activeDismissals[key] = true
			}
		}
		if len(activeDismissals) != len(dismissed) {
			if err = a.saveDismissedCompanionInterventions(ctx, workspaceID, activeDismissals); err != nil {
				return nil, 0, err
			}
		}
		dismissed = activeDismissals
	}
	visible, _ := companion.VisibleInterventions(gated, dismissed, companion.InitiativeVisibleLimit(cfg))
	count := 0
	for key := range dismissed {
		if activeOccurrences[key] {
			count++
		}
	}
	return visible, count, nil
}

func (a *App) loadCompanionGateMemory(ctx context.Context, workspaceID string) (companion.SpeakMemory, error) {
	memory := companion.SpeakMemory{ByID: map[string]companion.SpeakRecord{}}
	value, err := a.store.Setting(ctx, companionGateMemorySettingKey(workspaceID))
	if err != nil {
		if storage.IsNotFound(err) {
			return memory, nil
		}
		return memory, err
	}
	if strings.TrimSpace(value) == "" {
		return memory, nil
	}
	if err = json.Unmarshal([]byte(value), &memory); err != nil {
		return companion.SpeakMemory{ByID: map[string]companion.SpeakRecord{}}, nil
	}
	if memory.ByID == nil {
		memory.ByID = map[string]companion.SpeakRecord{}
	}
	return memory, nil
}

func (a *App) saveCompanionGateMemory(ctx context.Context, workspaceID string, memory companion.SpeakMemory) error {
	if memory.ByID == nil {
		memory.ByID = map[string]companion.SpeakRecord{}
	}
	payload, err := json.Marshal(memory)
	if err != nil {
		return err
	}
	return a.store.SaveSetting(ctx, companionGateMemorySettingKey(workspaceID), string(payload))
}

func companionGateMemorySettingKey(workspaceID string) string {
	return "companion.interventions.speak." + workspaceID
}

func (a *App) ClearCompanionHistory() error {
	workspace, err := a.requireWorkspace()
	if err != nil {
		return err
	}
	return a.store.DeleteCompanionMessages(context.Background(), workspace.ID)
}

func (a *App) CompanionHistory(ctx context.Context, speaker string, limit int) ([]domain.CompanionMessage, error) {
	workspace, err := a.requireWorkspace()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(speaker) != "companion-log" {
		speaker = "companion"
	}
	return a.store.ListChatMessages(ctx, workspace.ID, speaker, limit)
}

func (a *App) DismissCompanionIntervention(interventionID, occurrenceKey string) error {
	workspace, err := a.requireWorkspace()
	if err != nil {
		return err
	}
	interventionID = strings.TrimSpace(interventionID)
	occurrenceKey = strings.ToLower(strings.TrimSpace(occurrenceKey))
	if interventionID == "" || len(interventionID) > 256 {
		return errors.New("companion intervention id is required")
	}
	if !validCompanionOccurrenceKey(occurrenceKey) {
		return errors.New("invalid companion intervention occurrence key")
	}
	bootstrap, err := a.loadHubBootstrap(context.Background(), workspace.ID)
	if err != nil {
		return err
	}
	found := false
	for _, item := range bootstrap.CompanionInterventions {
		if item.ID == interventionID && item.OccurrenceKey == occurrenceKey {
			found = true
			break
		}
	}
	if !found {
		// Soft signals may already be speak-cooled while still dismissible from the last visible state.
		memory, memErr := a.loadCompanionGateMemory(context.Background(), workspace.ID)
		if memErr != nil {
			return memErr
		}
		if rec, ok := memory.ByID[interventionID]; ok && strings.ToLower(strings.TrimSpace(rec.OccurrenceKey)) == occurrenceKey {
			found = true
		}
	}
	if !found {
		return errors.New("companion intervention is no longer active")
	}
	dismissed, err := a.loadDismissedCompanionInterventions(context.Background(), workspace.ID)
	if err != nil {
		return err
	}
	dismissed[occurrenceKey] = true
	return a.saveDismissedCompanionInterventions(context.Background(), workspace.ID, dismissed)
}

func (a *App) RestoreCompanionInterventions() error {
	workspace, err := a.requireWorkspace()
	if err != nil {
		return err
	}
	ctx := context.Background()
	if err = a.store.SaveSetting(ctx, companionDismissedSettingKey(workspace.ID), "[]"); err != nil {
		return err
	}
	// Clearing dismissals should also reset speak cooldown so soft nudges can surface again.
	return a.saveCompanionGateMemory(ctx, workspace.ID, companion.SpeakMemory{ByID: map[string]companion.SpeakRecord{}})
}

func (a *App) loadDismissedCompanionInterventions(ctx context.Context, workspaceID string) (map[string]bool, error) {
	result := map[string]bool{}
	value, err := a.store.Setting(ctx, companionDismissedSettingKey(workspaceID))
	if err != nil {
		if storage.IsNotFound(err) {
			return result, nil
		}
		return nil, err
	}
	var keys []string
	if strings.TrimSpace(value) != "" {
		if err = json.Unmarshal([]byte(value), &keys); err != nil {
			return nil, fmt.Errorf("decode dismissed companion interventions: %w", err)
		}
	}
	for _, key := range keys {
		key = strings.ToLower(strings.TrimSpace(key))
		if validCompanionOccurrenceKey(key) {
			result[key] = true
		}
	}
	return result, nil
}

func (a *App) saveDismissedCompanionInterventions(ctx context.Context, workspaceID string, dismissed map[string]bool) error {
	keys := make([]string, 0, len(dismissed))
	for key := range dismissed {
		if validCompanionOccurrenceKey(key) {
			keys = append(keys, key)
		}
	}
	if len(keys) > 200 {
		keys = keys[len(keys)-200:]
	}
	payload, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	return a.store.SaveSetting(ctx, companionDismissedSettingKey(workspaceID), string(payload))
}

func companionDismissedSettingKey(workspaceID string) string {
	return "companion.interventions.dismissed." + workspaceID
}

func validCompanionOccurrenceKey(value string) bool {
	if len(value) != 16 {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}

func (a *App) CompanionPropose(req companion.RecommendRequest) (domain.QuestProposal, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.QuestProposal{}, err
	}
	if req.WorkspaceID != "" && req.WorkspaceID != ws.ID {
		return domain.QuestProposal{}, errors.New("companion workspace does not match the open project")
	}
	req.WorkspaceID = ws.ID
	svc := companion.Service{Store: a.store}
	return svc.ProposeQuest(context.Background(), req)
}

// resolveCompanionSkills — навыки, надетые на помощника, в том виде, в каком
// они уезжают в промпт.
//
// Навык, которому нужен инструмент вне доступа помощника, здесь пропускается, а
// не роняет разговор: на экипировке такой навык не принимают, и дойти сюда он
// может только правкой самого навыка задним числом. Помощник отвечает на каждую
// реплику, и молчать целиком из-за одной практики он не должен — но и молча
// применять её, не имея инструмента, тоже нельзя.
func (a *App) resolveCompanionSkills(ctx context.Context, workspaceID string) []domain.SkillRuntime {
	cfg, err := a.store.GetCompanionConfig(ctx, workspaceID)
	if err != nil || len(cfg.SkillIDs) == 0 {
		return nil
	}
	defined, err := a.store.ListSkills(ctx)
	if err != nil {
		return nil
	}
	skillByID := make(map[string]domain.SkillDefinition, len(defined))
	for _, skill := range defined {
		skillByID[skill.ID] = skill
	}
	instanceBySkill := map[string]domain.ProjectSkillInstance{}
	if instances, instanceErr := a.store.ListProjectSkills(ctx, workspaceID); instanceErr == nil {
		for _, instance := range instances {
			instanceBySkill[instance.SkillID] = instance
		}
	}
	grants := policy.CompanionGrants()
	equipped := make([]domain.SkillRuntime, 0, len(cfg.SkillIDs))
	for _, id := range cfg.SkillIDs {
		skill, known := skillByID[id]
		if !known {
			continue
		}
		instance, hasInstance := instanceBySkill[id]
		if hasInstance && !instance.Enabled {
			continue
		}
		if tool, ok := firstUngrantedTool(grants, skill.RequiredTools); !ok {
			slog.Warn("companion skill skipped", "workspace_id", workspaceID, "skill", skill.Name, "tool", tool)
			continue
		}
		equipped = append(equipped, skillRuntimeFrom(skill, instance, hasInstance))
	}
	return equipped
}

// firstUngrantedTool называет первый инструмент навыка, которого сущности не
// выдали. Пустое имя и true — навык укладывается в доступ.
func firstUngrantedTool(grants policy.Grants, required []string) (string, bool) {
	for _, tool := range required {
		if !grants.Allows(tool) {
			return tool, false
		}
	}
	return "", true
}

func (a *App) SaveCompanionConfig(cfg domain.CompanionConfig) (domain.CompanionConfig, error) {
	now := time.Now().UTC()
	ws, err := a.requireWorkspace()
	if err != nil {
		return domain.CompanionConfig{}, err
	}
	if cfg.WorkspaceID != "" && cfg.WorkspaceID != ws.ID {
		return domain.CompanionConfig{}, errors.New("companion workspace does not match the open project")
	}
	cfg.WorkspaceID = ws.ID
	// В отличие от EnsureConfig это явное сохранение от интерфейса/клиента.
	// Только оно завершает этап настройки первого запуска.
	cfg.Configured = true
	cfg.Preset = strings.TrimSpace(cfg.Preset)
	if cfg.Preset == "" {
		cfg.Preset = "balanced"
	}
	if _, ok := companion.PresetDefaults(cfg.Preset); !ok {
		return domain.CompanionConfig{}, fmt.Errorf("unsupported companion preset %q", cfg.Preset)
	}
	// Навык проверяется на экипировке — там, где человек принимает решение.
	if len(cfg.SkillIDs) > 64 {
		return domain.CompanionConfig{}, errors.New("companion skill list exceeds its size limit")
	}
	if len(cfg.SkillIDs) > 0 {
		defined, listErr := a.store.ListSkills(context.Background())
		if listErr != nil {
			return domain.CompanionConfig{}, listErr
		}
		skillByID := make(map[string]domain.SkillDefinition, len(defined))
		for _, skill := range defined {
			skillByID[skill.ID] = skill
		}
		grants := policy.CompanionGrants()
		seen := make(map[string]bool, len(cfg.SkillIDs))
		for _, id := range cfg.SkillIDs {
			if seen[id] {
				return domain.CompanionConfig{}, fmt.Errorf("duplicate companion skill %q", id)
			}
			seen[id] = true
			skill, known := skillByID[id]
			if !known {
				// Как и у агента: навык может приехать позже переносом или
				// продвижением выученного, и запрет сломал бы эти пути.
				continue
			}
			if tool, ok := firstUngrantedTool(grants, skill.RequiredTools); !ok {
				return domain.CompanionConfig{}, fmt.Errorf("skill %q requires tool %q that the companion cannot use", skill.Name, tool)
			}
		}
	}
	personality := []struct {
		name  string
		value int
	}{
		{"criticality", cfg.Criticality}, {"creativity", cfg.Creativity}, {"verbosity", cfg.Verbosity},
		{"initiative", cfg.Initiative}, {"questionStrictness", cfg.QuestionStrictness}, {"riskTolerance", cfg.RiskTolerance},
	}
	for _, setting := range personality {
		if setting.value < 0 || setting.value > 100 {
			return domain.CompanionConfig{}, fmt.Errorf("companion %s must be between 0 and 100", setting.name)
		}
	}
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.ProviderPreset = strings.TrimSpace(cfg.ProviderPreset)
	if strings.TrimSpace(cfg.ConnectionID) != "" {
		cfg, err = a.resolveCompanionConnection(cfg)
		if err != nil {
			return domain.CompanionConfig{}, err
		}
	}
	if cfg.Provider == "" && cfg.Model != "" || cfg.Provider != "" && cfg.Model == "" {
		return domain.CompanionConfig{}, errors.New("companion provider and model must be configured together")
	}
	if cfg.Provider != "" {
		if domain.IsAgentCLIProvider(cfg.Provider) {
			return domain.CompanionConfig{}, errors.New("companion CLI providers are removed; use an HTTP API connection")
		}
		if !domain.IsHTTPAPIProvider(cfg.Provider) {
			return domain.CompanionConfig{}, errors.New("companion provider is not supported")
		}
		if cfg.BaseURL == "" {
			if cfg.Provider == domain.ProviderOllama {
				cfg.BaseURL = "http://127.0.0.1:11434"
			} else if cfg.Provider == domain.ProviderAnthropic {
				cfg.BaseURL = "https://api.anthropic.com/v1"
			} else if cfg.Provider == domain.ProviderOpenAI {
				cfg.BaseURL = "https://api.openai.com/v1"
			} else {
				return domain.CompanionConfig{}, errors.New("Azure OpenAI requires a connection with resource URL and API version")
			}
		}
		if err := checkProviderURL("companion", cfg.BaseURL); err != nil {
			return domain.CompanionConfig{}, err
		}
		if cfg.Provider == domain.ProviderAzureOpenAI && strings.TrimSpace(cfg.APIVersion) == "" {
			return domain.CompanionConfig{}, errors.New("Azure OpenAI connection requires an API version")
		}
	}
	if cfg.Temperature < 0 || cfg.Temperature > 2 {
		return domain.CompanionConfig{}, errors.New("companion temperature must be between 0 and 2")
	}
	if cfg.MaxOutputTokens == 0 {
		cfg.MaxOutputTokens = domain.MinThinkingOutputTokens
	}
	// Верхняя граница выросла вместе с размышляющими моделями: у них предел
	// вывода тратится сперва на размышление, и 8192 на всё про всё означало
	// пустой ответ вместо совета.
	if cfg.MaxOutputTokens < 128 || cfg.MaxOutputTokens > 32768 {
		return domain.CompanionConfig{}, errors.New("companion max output tokens must be between 128 and 32768")
	}
	if cfg.ID == "" {
		cfg.ID = domain.NewID("companion")
		cfg.CreatedAt = now
	}
	if cfg.CreatedAt.IsZero() {
		cfg.CreatedAt = now
	}
	cfg.UpdatedAt = now
	if err := a.store.SaveCompanionConfig(context.Background(), cfg); err != nil {
		return domain.CompanionConfig{}, err
	}
	return cfg, nil
}
