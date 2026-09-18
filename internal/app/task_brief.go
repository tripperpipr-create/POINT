package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"local-agent-workbench/internal/domain"
)

var taskProposalLocks sync.Map

func lockTaskProposal(id string) func() {
	value, _ := taskProposalLocks.LoadOrStore(id, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// User-owned brief updates and approval never share the model draft path.
func applyTaskBriefDecision(proposal *domain.QuestProposal, decision QuestProposalDecision) error {
	if proposal.Brief == nil && decision.Brief == nil {
		return nil
	}
	if proposal.Brief == nil {
		return errors.New("создайте структурированное задание через обсуждение Мастера")
	}
	current := *proposal.Brief
	if decision.ExpectedVersion != current.Version {
		return fmt.Errorf("версия задания изменилась: ожидалась %d; обновите карточку", current.Version)
	}
	if decision.Brief != nil {
		updated := domain.NormalizeTaskBrief(*decision.Brief)
		updated.Version = current.Version
		if domain.TaskBriefDigest(updated) != domain.TaskBriefDigest(current) {
			updated.Version++
		} else {
			updated = current
		}
		if err := domain.ValidateTaskBrief(updated); err != nil {
			return err
		}
		proposal.Brief = &updated
	}
	if decision.Action == QuestProposalStart {
		if proposal.Brief.Mode == domain.TaskModeProject && decision.ApproveVersion != proposal.Brief.Version {
			return errors.New("утвердите текущую версию задания перед автономным запуском")
		}
		approved, err := domain.ApproveTaskBrief(*proposal.Brief)
		if err != nil {
			return err
		}
		proposal.Brief = &approved
	}
	syncProposalBrief(proposal)
	return nil
}
func syncProposalBrief(p *domain.QuestProposal) {
	if p.Brief == nil {
		return
	}
	b := p.Brief
	p.Task = b.Goal
	p.Objectives = append([]string(nil), b.Scope...)
	p.Constraints = append([]string(nil), b.OutOfScope...)
	p.Unknowns = append([]string(nil), b.OpenQuestions...)
	p.DefinitionOfDone = nil
	for _, c := range b.Criteria {
		p.DefinitionOfDone = append(p.DefinitionOfDone, c.Text)
	}
	p.EstimateTokens = b.Budget.Tokens
}
func cloneTaskBrief(b *domain.TaskBrief) *domain.TaskBrief {
	if b == nil {
		return nil
	}
	raw, _ := json.Marshal(b)
	var copy domain.TaskBrief
	_ = json.Unmarshal(raw, &copy)
	return &copy
}
func (a *App) taskBriefForQuest(ctx context.Context, workspaceID, questID string) (*domain.TaskBrief, error) {
	if questID == "" {
		return nil, nil
	}
	quests, err := a.store.ListQuests(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	byID := map[string]domain.Quest{}
	for _, q := range quests {
		byID[q.ID] = q
	}
	seen := map[string]bool{}
	for questID != "" {
		if seen[questID] {
			return nil, errors.New("cyclic task ancestry")
		}
		seen[questID] = true
		q, ok := byID[questID]
		if !ok {
			return nil, errors.New("task belongs to another workspace or is missing")
		}
		if q.Brief != nil {
			return cloneTaskBrief(q.Brief), nil
		}
		questID = q.ParentID
	}
	return nil, nil
}

func stageScopedBrief(brief *domain.TaskBrief, stageRole string) *domain.TaskBrief {
	if brief == nil {
		return nil
	}
	switch stageRole {
	case domain.StageRoleBootstrap, domain.StageRoleIntegrate, domain.StageRoleImplReview, domain.StageRoleImplement:
		scoped := *brief
		// Drop machine verification for delivery stages. Implement must ship
		// source without inventing phpunit harnesses; accept re-binds parent
		// criteria on the integrated tip.
		scoped.Criteria = []domain.AcceptanceCriterion{{
			ID: "stage-complete", Kind: "manual", Text: "Stage completed its bounded instruction",
		}}
		if scoped.ApprovedDigest != "" {
			scoped.ApprovedDigest = domain.TaskBriefDigest(scoped)
		}
		return &scoped
	default:
		// accept (and unknown roles) keep the parent brief.
		return brief
	}
}
func (a *App) validateTaskEnvironment(brief *domain.TaskBrief) error {
	if brief == nil {
		return nil
	}
	if !domain.IsTaskBriefApproved(*brief) {
		return errors.New("task brief is not approved")
	}
	if brief.Mode == domain.TaskModeProject && !a.sandboxCapabilities().StrongOSBoundary {
		return errors.New("автономный проект требует Docker sandbox; выполнение на Windows автоматически не включается")
	}
	return nil
}

// TaskBriefHistory uses the active workspace, never a workspace supplied by a
// client. Revisions remain available after conversation compaction.
func (a *App) TaskBriefHistory(ctx context.Context, proposalID string) ([]domain.TaskBriefRevision, error) {
	ws, err := a.requireWorkspace()
	if err != nil {
		return nil, err
	}
	return a.store.ListTaskBriefRevisions(ctx, ws.ID, proposalID)
}

// Legacy quest CRUD is not an alternative task-approval endpoint. It may keep
// an unchanged approved definition, but cannot inject or enlarge one.
func (a *App) guardTaskQuestUpdate(ctx context.Context, next *domain.Quest) error {
	quests, err := a.store.ListQuests(ctx, next.WorkspaceID)
	if err != nil {
		return err
	}
	var previous *domain.Quest
	for i := range quests {
		if quests[i].ID == next.ID {
			previous = &quests[i]
			break
		}
	}
	var inherited *domain.TaskBrief
	if next.ParentID != "" {
		inherited, err = a.taskBriefForQuest(ctx, next.WorkspaceID, next.ParentID)
		if err != nil {
			return err
		}
	}
	var priorBrief *domain.TaskBrief
	if previous != nil {
		priorBrief, err = a.taskBriefForQuest(ctx, next.WorkspaceID, previous.ID)
		if err != nil {
			return err
		}
	}
	if previous == nil && (next.Brief != nil || inherited != nil) {
		return errors.New("структурированное задание и его этапы создаются через утверждённый план")
	}
	if previous != nil && previous.Brief == nil && next.Brief != nil {
		return errors.New("структурированное задание создаётся и утверждается через карточку Мастера")
	}
	if inherited == nil && priorBrief == nil {
		return nil
	}
	sameStrings := func(x, y []string) bool {
		a, _ := json.Marshal(x)
		b, _ := json.Marshal(y)
		return string(a) == string(b)
	}
	if next.ParentID != previous.ParentID || next.Description != previous.Description || next.BudgetTokens != previous.BudgetTokens || next.BudgetCents != previous.BudgetCents || !sameStrings(next.Objectives, previous.Objectives) || !sameStrings(next.Constraints, previous.Constraints) || !sameStrings(next.DefinitionOfDone, previous.DefinitionOfDone) {
		return errors.New("изменение утверждённой цели, критериев или бюджета требует новой версии задания")
	}
	if next.Brief != nil && previous.Brief != nil && (next.Brief.Version != previous.Brief.Version || domain.TaskBriefDigest(*next.Brief) != domain.TaskBriefDigest(*previous.Brief)) {
		return errors.New("изменённое задание требует нового утверждения")
	}
	next.Brief = cloneTaskBrief(previous.Brief)
	return nil
}

func validateTaskExecutionLaunch(previous *domain.ExecutionInstance, brief *domain.TaskBrief, agentID, task string) error {
	if brief == nil {
		return nil
	}
	if previous == nil {
		return errors.New("структурированное задание запускается через утверждённый план")
	}
	if previous.Status != domain.RunPending || previous.RunID != "" {
		return errors.New("unknown_outcome: исполнение уже запускалось; сохранённая песочница требует восстановления из проверенной точки, повтор задания с начала запрещён")
	}
	if previous.ProjectAgentID != agentID || previous.Task != task {
		return errors.New("исполнитель и поручение должны соответствовать сохранённому этапу плана")
	}
	return nil
}
