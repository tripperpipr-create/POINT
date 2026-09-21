package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
)

func detectRoleGap(task string, agents []domain.ProjectAgent) *domain.RoleRequirement {
	lower := strings.ToLower(task)
	roles := []struct {
		need  []string
		role  string
		tools []string
	}{
		{[]string{"frontend", "интерфейс", "react", "ui"}, "frontend", []string{"list_files", "read_file", "search_code", "propose_patch", "run_command"}},
		{[]string{"backend", "api", "сервер", "symfony", "php", "composer", "health endpoint", "health-эндпоинт"}, "backend", []string{"list_files", "read_file", "search_code", "propose_patch", "run_command"}},
		{[]string{"database", "база данных", "sql", "миграц", "postgres", "postgresql"}, "database", []string{"list_files", "read_file", "search_code", "db_schema", "propose_patch", "run_command"}},
		{[]string{"test", "тест", "qa", "провер"}, "testing", []string{"list_files", "read_file", "search_code", "propose_patch", "run_command"}},
		{[]string{"security", "безопас"}, "security", []string{"list_files", "read_file", "search_code", "git_diff"}},
	}
	for _, candidate := range roles {
		required := false
		for _, marker := range candidate.need {
			if strings.Contains(lower, marker) {
				required = true
				break
			}
		}
		if !required {
			continue
		}
		for _, agent := range agents {
			haystack := strings.ToLower(agent.Name + " " + agent.RoleDescription + " " + agent.Mission)
			if strings.Contains(haystack, candidate.role) || roleAliasMatches(candidate.role, haystack) {
				required = false
				break
			}
		}
		if required {
			return &domain.RoleRequirement{
				Role: candidate.role, Responsibility: "Own " + candidate.role + " work for the approved project",
				RequiredTools: candidate.tools, RequiredCapabilities: []string{"tools"},
				Verification: "ProjectAgent readiness is READY and its tool/model capability probe passes",
			}
		}
	}
	if len(agents) == 0 {
		return &domain.RoleRequirement{
			Role: "developer", Responsibility: "Implement and verify the approved project",
			RequiredTools:        []string{"list_files", "read_file", "search_code", "propose_patch", "run_command"},
			RequiredCapabilities: []string{"coding", "tools"}, Verification: "ProjectAgent readiness is READY",
		}
	}
	return nil
}

type agentGapAssessment struct {
	Kind        string // missing_primary | blocked_primary | missing_subagent
	Requirement domain.RoleRequirement
	Parent      *domain.ProjectAgent
	Blocked     *domain.ProjectAgent
	Blockers    []string
}

func (a *App) assessAgentGap(ctx context.Context, brief *domain.TaskBrief, agents, runnable []domain.ProjectAgent) *agentGapAssessment {
	if brief == nil {
		return nil
	}
	task := strings.Join([]string{
		brief.Goal,
		strings.Join(brief.Scope, " "),
		acceptanceCriterionText(brief.Criteria),
	}, " ")
	requiredTools := []string{"list_files", "read_file", "search_code"}
	if brief.Permissions.WriteFiles {
		requiredTools = append(requiredTools, "propose_patch")
	}
	if brief.Permissions.ExecuteCommands {
		requiredTools = append(requiredTools, "run_command")
	}
	if len(runnable) == 0 {
		requirement := domain.RoleRequirement{
			Role: "developer", Responsibility: "Implement and verify the approved task",
			PreparationKind: "create_agent", RequiredTools: requiredTools,
			RequiredCapabilities: []string{"coding", "tools"}, Verification: "ProjectAgent readiness is READY",
		}
		if role := detectRoleGap(task, nil); role != nil {
			requirement.Role = role.Role
			requirement.Responsibility = role.Responsibility
			requirement.RequiredTools = appendUniqueStrings(requirement.RequiredTools, role.RequiredTools...)
		}
		if len(agents) == 0 {
			return &agentGapAssessment{Kind: "missing_primary", Requirement: requirement}
		}
		candidate := agents[0]
		for _, agent := range agents {
			haystack := strings.ToLower(agent.Name + " " + agent.RoleDescription + " " + agent.Mission)
			if strings.Contains(haystack, strings.ToLower(requirement.Role)) || roleAliasMatches(requirement.Role, haystack) {
				candidate = agent
				break
			}
		}
		readiness := a.projectAgentReadiness(ctx, candidate)
		requirement.PreparationKind = "reconfigure_agent"
		requirement.ParentAgentID = candidate.ID
		return &agentGapAssessment{
			Kind: "blocked_primary", Requirement: requirement, Blocked: &candidate,
			Blockers: append([]string(nil), readiness.Blocking...),
		}
	}
	gap := detectRoleGap(task, runnable)
	if gap == nil {
		return nil
	}
	parent := runnable[0]
	if primary := detectRoleGap(task, nil); primary != nil {
		for _, agent := range runnable {
			haystack := strings.ToLower(agent.Name + " " + agent.RoleDescription + " " + agent.Mission)
			if strings.Contains(haystack, strings.ToLower(primary.Role)) || roleAliasMatches(primary.Role, haystack) {
				parent = agent
				break
			}
		}
	}
	gap.PreparationKind = "subagent"
	gap.ParentAgentID = parent.ID
	return &agentGapAssessment{Kind: "missing_subagent", Requirement: *gap, Parent: &parent}
}

func acceptanceCriterionText(criteria []domain.AcceptanceCriterion) string {
	parts := make([]string, 0, len(criteria))
	for _, criterion := range criteria {
		parts = append(parts, criterion.Text)
	}
	return strings.Join(parts, " ")
}

func roleAliasMatches(role, text string) bool {
	aliases := map[string][]string{
		"frontend": {"фронтенд", "интерфейс"}, "backend": {"бэкенд", "разработчик"},
		"database": {"база", "данных"}, "testing": {"тест", "qa"}, "security": {"безопас", "review"},
	}
	for _, alias := range aliases[role] {
		if strings.Contains(text, alias) {
			return true
		}
	}
	return false
}

func (a *App) createAgentPrepChain(ctx context.Context, parent *domain.Quest, requirement domain.RoleRequirement) (domain.AgentPrepChain, error) {
	if parent == nil || parent.Brief == nil || !domain.IsTaskBriefApproved(*parent.Brief) {
		return domain.AgentPrepChain{}, errors.New("agent provisioning requires an approved parent task")
	}
	if requirement.PreparationKind == "create_agent" || requirement.PreparationKind == "reconfigure_agent" {
		return a.createUserAgentPrepChain(ctx, parent, requirement)
	}
	if !parent.Brief.Permissions.ProvisionProjectAgents || parent.Brief.Budget.MaxProjectAgents <= 0 {
		return domain.AgentPrepChain{}, errors.New("task does not authorize a temporary subagent")
	}
	if strings.TrimSpace(requirement.ParentAgentID) == "" {
		return domain.AgentPrepChain{}, errors.New("temporary subagent requires a parent agent")
	}
	taskHash := sha256.Sum256([]byte(parent.ID + "\x00" + parent.Brief.ApprovedDigest + "\x00" + requirement.Role))
	hash := hex.EncodeToString(taskHash[:])
	chains, err := a.store.ListAgentPrepChains(ctx, parent.WorkspaceID)
	if err != nil {
		return domain.AgentPrepChain{}, err
	}
	created := 0
	for _, chain := range chains {
		if chain.ParentQuestID == parent.ID {
			created++
		}
		if chain.DeferredTaskHash == hash && chain.State != "failed" {
			return chain, nil
		}
	}
	if created >= parent.Brief.Budget.MaxProjectAgents {
		return domain.AgentPrepChain{}, errors.New("project agent provisioning budget exhausted")
	}
	now := time.Now().UTC()
	prep := domain.Quest{
		ID: domain.NewID("quest"), WorkspaceID: parent.WorkspaceID, ParentID: parent.ID, Kind: "agent_provisioning",
		ControllerState: "create", Title: "Prepare temporary " + requirement.Role + " subagent",
		Description: requirement.Responsibility, Objectives: []string{"Create and verify a temporary " + requirement.Role + " subagent under the selected agent"},
		DefinitionOfDone: []string{requirement.Verification}, Importance: parent.Importance,
		Status: domain.QuestActive, BudgetTokens: parent.BudgetTokens / 10, BudgetCents: parent.BudgetCents / 10,
		CreatedAt: now, UpdatedAt: now,
	}
	if err = a.store.SaveQuest(ctx, prep); err != nil {
		return domain.AgentPrepChain{}, err
	}
	chain := domain.AgentPrepChain{
		ID: domain.NewID("agentprep"), WorkspaceID: parent.WorkspaceID, ParentQuestID: parent.ID,
		PrepQuestID: prep.ID, DeferredTaskHash: hash, Requirement: requirement,
		State: "design", Attempts: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err = a.store.SaveAgentPrepChain(ctx, chain); err != nil {
		return domain.AgentPrepChain{}, err
	}
	parent.PrerequisiteIDs = appendUniqueStrings(parent.PrerequisiteIDs, prep.ID)
	parent.ControllerState, parent.Status, parent.UpdatedAt = controllerWaitingPrerequisite, domain.QuestPaused, now
	if err = a.store.SaveQuest(ctx, *parent); err != nil {
		return domain.AgentPrepChain{}, err
	}
	chain.State = "create"
	return a.materializeAgentCandidate(ctx, chain)
}

func (a *App) createUserAgentPrepChain(ctx context.Context, parent *domain.Quest, requirement domain.RoleRequirement) (domain.AgentPrepChain, error) {
	now := time.Now().UTC()
	title := "Create the primary " + requirement.Role + " agent with user"
	objectives := []string{"Choose the agent identity, model, tools and limits with the user", "Reach READY capability state"}
	if requirement.PreparationKind == "reconfigure_agent" {
		title = "Configure the existing " + requirement.Role + " agent with user"
		objectives = []string{"Resolve the listed model, tool and limit blockers with the user", "Reach READY capability state"}
	}
	prep := domain.Quest{
		ID: domain.NewID("quest"), WorkspaceID: parent.WorkspaceID, ParentID: parent.ID, Kind: "agent_creation",
		ControllerState: "user_decision", Title: title,
		Description:      requirement.Responsibility,
		Objectives:       objectives,
		DefinitionOfDone: []string{requirement.Verification}, Importance: parent.Importance,
		Status: domain.QuestPaused, BudgetTokens: parent.BudgetTokens / 10, BudgetCents: parent.BudgetCents / 10,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := a.store.SaveQuest(ctx, prep); err != nil {
		return domain.AgentPrepChain{}, err
	}
	chain := domain.AgentPrepChain{
		ID: domain.NewID("agentprep"), WorkspaceID: parent.WorkspaceID, ParentQuestID: parent.ID,
		PrepQuestID: prep.ID, DeferredTaskHash: domain.NewID("deferred"), Requirement: requirement,
		State: "user_decision", Attempts: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := a.store.SaveAgentPrepChain(ctx, chain); err != nil {
		return domain.AgentPrepChain{}, err
	}
	parent.PrerequisiteIDs = appendUniqueStrings(parent.PrerequisiteIDs, prep.ID)
	parent.ControllerState, parent.Status, parent.UpdatedAt = controllerWaitingPrerequisite, domain.QuestPaused, now
	if err := a.store.SaveQuest(ctx, *parent); err != nil {
		return domain.AgentPrepChain{}, err
	}
	if requirement.PreparationKind == "create_agent" {
		if err := a.proposeRoleGapHire(ctx, parent.WorkspaceID, parent.Description, requirement); err != nil {
			return domain.AgentPrepChain{}, err
		}
	}
	return chain, nil
}

func (a *App) proposeRoleGapHire(ctx context.Context, workspaceID, task string, gap domain.RoleRequirement) error {
	blueprints, err := a.store.ListBlueprints(ctx)
	if err != nil || len(blueprints) == 0 {
		if err == nil {
			err = errors.New("no blueprint is available to hire a specialist")
		}
		return err
	}
	selected := blueprints[0]
	for _, blueprint := range blueprints {
		text := strings.ToLower(blueprint.Name + " " + blueprint.RoleDescription)
		if strings.Contains(text, strings.ToLower(gap.Role)) || roleAliasMatches(gap.Role, text) {
			selected = blueprint
			break
		}
	}
	agent := domain.ProjectAgentFromBlueprint(workspaceID, selected)
	agent.ID = ""
	if gap.Role != "" {
		agent.Name = strings.ToUpper(gap.Role[:1]) + gap.Role[1:] + " Candidate"
	}
	agent.RoleDescription = gap.Responsibility
	agent.Mission = gap.Responsibility
	if strings.TrimSpace(agent.PrimaryModel) == "" {
		agent.PrimaryModel = "auto"
	}
	now := time.Now().UTC()
	return a.store.SaveCompanionActionProposal(ctx, domain.CompanionActionProposal{
		ID: domain.NewID("companionaction"), WorkspaceID: workspaceID, Kind: domain.CompanionActionCreateAgent,
		Title: "Создать агента · " + agent.Name, Rationale: "Для задачи не хватает роли " + gap.Role + ". Brief не разрешает автономное provisioning — нужен review/apply.",
		Agent: &agent, Status: "pending", ContinuationPrompt: task, ContinuationLabel: "ПРОДОЛЖИТЬ ЗАДАЧУ",
		CreatedAt: now, UpdatedAt: now,
	})
}

func (a *App) materializeAgentCandidate(ctx context.Context, chain domain.AgentPrepChain) (domain.AgentPrepChain, error) {
	if chain.Requirement.PreparationKind != "subagent" || strings.TrimSpace(chain.Requirement.ParentAgentID) == "" {
		err := errors.New("full agent creation requires a user-accompanied quest")
		chain.State, chain.Error, chain.UpdatedAt = "failed", err.Error(), time.Now().UTC()
		_ = a.store.SaveAgentPrepChain(ctx, chain)
		return chain, err
	}
	parentAgent, err := a.store.GetProjectAgent(ctx, chain.Requirement.ParentAgentID)
	if err != nil {
		chain.State, chain.Error, chain.UpdatedAt = "failed", err.Error(), time.Now().UTC()
		_ = a.store.SaveAgentPrepChain(ctx, chain)
		return chain, err
	}
	if parentAgent.Temporary || (parentAgent.Status != "" && parentAgent.Status != domain.ProjectAgentActive) {
		err = errors.New("temporary subagent requires an active general parent agent")
		chain.State, chain.Error, chain.UpdatedAt = "failed", err.Error(), time.Now().UTC()
		_ = a.store.SaveAgentPrepChain(ctx, chain)
		return chain, err
	}
	agent := parentAgent
	agent.ID = ""
	agent.ParentAgentID = parentAgent.ID
	agent.OwnerQuestID = chain.ParentQuestID
	agent.Status = domain.ProjectAgentActive
	agent.Temporary = true
	agent.Name = parentAgent.Name + " · " + strings.ToUpper(chain.Requirement.Role[:1]) + chain.Requirement.Role[1:]
	agent.RoleDescription = chain.Requirement.Responsibility
	agent.Mission = chain.Requirement.Responsibility
	agent.Rules = append(agent.Rules, "Temporary subagent: report to parent agent "+parentAgent.ID+". Never create a separate permanent Blueprint.")
	agent.AllowedTools = intersectTools(agent.AllowedTools, chain.Requirement.RequiredTools)
	if len(agent.AllowedTools) == 0 {
		agent.AllowedTools = append([]string(nil), parentAgent.AllowedTools...)
	}
	saved, err := a.SaveProjectAgent(agent)
	if err != nil {
		chain.State, chain.Error, chain.UpdatedAt = "failed", err.Error(), time.Now().UTC()
		_ = a.store.SaveAgentPrepChain(ctx, chain)
		return chain, err
	}
	chain.CandidateAgentID, chain.State, chain.UpdatedAt = saved.ID, "verify", time.Now().UTC()
	capability := a.projectAgentReadiness(ctx, saved)
	if capability.State == "READY" || capability.State == "DEGRADED" {
		chain.State = "ready"
		quests, _ := a.store.ListQuests(ctx, chain.WorkspaceID)
		for _, quest := range quests {
			if quest.ID == chain.PrepQuestID {
				quest.Status, quest.ControllerState = domain.QuestCompleted, "ready"
				now := time.Now().UTC()
				quest.UpdatedAt, quest.FinishedAt = now, &now
				_ = a.store.SaveQuest(ctx, quest)
			}
		}
	}
	if err = a.store.SaveAgentPrepChain(ctx, chain); err != nil {
		return chain, err
	}
	_, _ = a.ReconcileProjectController(ctx, chain.ParentQuestID)
	return chain, nil
}

// RequestTemporarySubagent is the typed runtime boundary used by the parent
// agent's request_subagent tool. It cannot broaden task authority.
func (a *App) RequestTemporarySubagent(ctx context.Context, request domain.SubagentRequest) (domain.ProjectAgent, error) {
	request.Role, request.Mission = strings.TrimSpace(request.Role), strings.TrimSpace(request.Mission)
	if request.WorkspaceID == "" || request.QuestID == "" || request.ParentAgentID == "" || request.Role == "" || request.Mission == "" {
		return domain.ProjectAgent{}, errors.New("subagent request requires workspace, quest, parent, role and mission")
	}
	parent, err := a.store.GetProjectAgent(ctx, request.ParentAgentID)
	if err != nil {
		return domain.ProjectAgent{}, err
	}
	if parent.WorkspaceID != request.WorkspaceID || parent.Temporary || (parent.Status != "" && parent.Status != domain.ProjectAgentActive) {
		return domain.ProjectAgent{}, errors.New("subagent parent must be an active general agent in this project")
	}
	quests, err := a.store.ListQuests(ctx, request.WorkspaceID)
	if err != nil {
		return domain.ProjectAgent{}, err
	}
	var quest *domain.Quest
	for i := range quests {
		if quests[i].ID == request.QuestID {
			quest = &quests[i]
			break
		}
	}
	if quest == nil || quest.Brief == nil || !domain.IsTaskBriefApproved(*quest.Brief) || !quest.Brief.Permissions.ProvisionProjectAgents || quest.Brief.Budget.MaxProjectAgents <= 0 {
		return domain.ProjectAgent{}, errors.New("approved quest does not authorize temporary subagents")
	}
	executions, err := a.store.ListExecutions(ctx, request.WorkspaceID, 0)
	if err != nil {
		return domain.ProjectAgent{}, err
	}
	parentParticipates := false
	for _, execution := range executions {
		if execution.QuestID == request.QuestID && execution.ProjectAgentID == parent.ID {
			parentParticipates = true
			break
		}
	}
	if !parentParticipates {
		return domain.ProjectAgent{}, errors.New("only an agent participating in this quest may request a subagent")
	}
	agents, err := a.store.ListProjectAgents(ctx, request.WorkspaceID)
	if err != nil {
		return domain.ProjectAgent{}, err
	}
	count := 0
	for _, existing := range agents {
		if existing.Temporary && existing.OwnerQuestID == request.QuestID {
			count++
			if existing.ParentAgentID == parent.ID && strings.EqualFold(existing.RoleDescription, request.Role) {
				return existing, nil
			}
		}
	}
	if count >= quest.Brief.Budget.MaxProjectAgents {
		return domain.ProjectAgent{}, errors.New("temporary subagent budget exhausted")
	}
	child := parent
	child.ID = ""
	child.BlueprintID = ""
	child.Status = domain.ProjectAgentActive
	child.RoleFamily = ""
	child.ParentAgentID = parent.ID
	child.OwnerQuestID = request.QuestID
	child.Temporary = true
	child.Name = parent.Name + " · " + request.Role
	child.RoleDescription = request.Role
	child.Mission = request.Mission
	child.SystemPrompt = request.Mission
	child.Goals = []string{request.Mission}
	child.Rules = append(append([]string(nil), parent.Rules...), "Временный субагент: работай только над делегированной специализацией и отчитывайся родителю.")
	requestedTools := a.filterKnownTools(ctx, request.RequiredTools)
	if len(request.RequiredTools) == 0 {
		requestedTools = append([]string(nil), parent.AllowedTools...)
	}
	child.AllowedTools = intersectTools(parent.AllowedTools, requestedTools)
	if len(child.AllowedTools) == 0 {
		return domain.ProjectAgent{}, errors.New("requested subagent tools are not allowed for the parent")
	}
	child.ToolPolicies = intersectToolPolicies(parent.ToolPolicies, child.AllowedTools)
	child.Experience, child.TasksCompleted, child.SuccessCount = 0, 0, 0
	child.Level = 1
	return a.SaveProjectAgent(child)
}

func intersectTools(available, required []string) []string {
	have := make(map[string]bool, len(available))
	for _, tool := range available {
		have[tool] = true
	}
	var result []string
	for _, tool := range required {
		if have[tool] {
			result = append(result, tool)
		}
	}
	return result
}

func intersectToolPolicies(policies map[string]string, allowed []string) map[string]string {
	result := make(map[string]string, len(allowed))
	for _, tool := range allowed {
		if policy, ok := policies[tool]; ok {
			result[tool] = policy
		}
	}
	return result
}

func (a *App) ListAgentPrepChains() ([]domain.AgentPrepChain, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return nil, err
	}
	return a.store.ListAgentPrepChains(context.Background(), ws.ID)
}

func (a *App) reconcileUserAgentPrepChains(ctx context.Context, agent domain.ProjectAgent) error {
	readiness := a.projectAgentReadiness(ctx, agent)
	if readiness.State == "BLOCKED" {
		return nil
	}
	chains, err := a.store.ListAgentPrepChains(ctx, agent.WorkspaceID)
	if err != nil {
		return err
	}
	haystack := strings.ToLower(agent.Name + " " + agent.RoleDescription + " " + agent.Mission)
	for _, chain := range chains {
		if chain.State != "user_decision" ||
			(chain.Requirement.PreparationKind != "create_agent" && chain.Requirement.PreparationKind != "reconfigure_agent") {
			continue
		}
		if chain.Requirement.PreparationKind == "reconfigure_agent" && chain.Requirement.ParentAgentID != agent.ID {
			continue
		}
		if chain.Requirement.Role != "developer" &&
			!strings.Contains(haystack, strings.ToLower(chain.Requirement.Role)) &&
			!roleAliasMatches(chain.Requirement.Role, haystack) {
			continue
		}
		chain.CandidateAgentID, chain.State, chain.UpdatedAt = agent.ID, "ready", time.Now().UTC()
		if err = a.store.SaveAgentPrepChain(ctx, chain); err != nil {
			return err
		}
		quests, listErr := a.store.ListQuests(ctx, chain.WorkspaceID)
		if listErr != nil {
			return listErr
		}
		for _, quest := range quests {
			if quest.ID != chain.PrepQuestID {
				continue
			}
			now := time.Now().UTC()
			quest.Status, quest.ControllerState, quest.UpdatedAt, quest.FinishedAt = domain.QuestCompleted, "ready", now, &now
			if err = a.store.SaveQuest(ctx, quest); err != nil {
				return err
			}
		}
		_, _ = a.ReconcileProjectController(ctx, chain.ParentQuestID)
	}
	return nil
}
