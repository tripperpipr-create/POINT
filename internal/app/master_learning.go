package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/masterskills"
	"local-agent-workbench/internal/orchestrator"
)

type MasterDevelopment struct {
	Config     domain.MasterLearningConfig  `json:"config"`
	Budget     domain.MasterLearningBudget  `json:"budget"`
	Skills     []domain.SkillDefinition     `json:"skills"`
	Revisions  []domain.MasterSkillRevision `json:"revisions"`
	History    []domain.MasterLearningJob   `json:"history"`
	Operations []domain.MasterOperation     `json:"operations"`
}

func (a *App) recordMasterEvidence(ctx context.Context, order domain.WorkOrder, quest, kind, outcome string) {
	signal := domain.MasterEvidenceSignal{ID: fmt.Sprintf("%s-%d-%s-%s", order.ID, order.Version, kind, outcome), WorkspaceID: order.WorkspaceID, ProposalID: order.ProposalID, QuestID: quest, Kind: kind, Outcome: outcome, CreatedAt: time.Now().UTC()}
	if err := a.store.SaveMasterEvidence(ctx, signal); err != nil {
		slog.Warn("save master evidence", "error", err)
	}
}

func (a *App) masterSkillDefinitions(ctx context.Context, ws string) ([]domain.SkillDefinition, error) {
	if err := a.store.SeedMasterSkills(ctx, masterskills.Builtins()); err != nil {
		return nil, err
	}
	revisions, err := a.store.MasterSkillRevisions(ctx)
	if err != nil {
		return nil, err
	}
	cfg, err := a.store.MasterLearningConfig(ctx, ws)
	if err != nil {
		return nil, err
	}
	trials, err := a.store.MasterSkillTrials(ctx, ws)
	if err != nil {
		return nil, err
	}
	result := masterskills.Builtins()
	for i, skill := range result {
		// Builtins remain the fallback. New releases do not erase learned
		// revisions; only an explicit rollback changes an active lineage.
		for _, revision := range revisions {
			if revision.Skill.ID != skill.ID {
				continue
			}
			local := revision.WorkspaceID == ws || trials[revision.ID] == "passed" || (cfg.Enabled && trials[revision.ID] == "canary")
			if revision.Status == "shared" || (local && (revision.Status == "local" || (cfg.Enabled && revision.Status == "canary"))) {
				result[i] = revision.Skill
			}
		}
	}
	return result, nil
}

func (a *App) newMasterSkillSession(ctx context.Context, ws, phase, turn, proposal, quest string) (*orchestrator.MasterSkillSession, error) {
	defs, err := a.masterSkillDefinitions(ctx, ws)
	if err != nil {
		return nil, err
	}
	s := orchestrator.NewMasterSkillSession(phase, defs)
	s.Operation.ID = domain.NewID("master-operation")
	s.Operation.WorkspaceID = ws
	s.Operation.TurnID = turn
	s.Operation.ProposalID = proposal
	s.Operation.QuestID = quest
	s.Operation.CreatedAt = time.Now().UTC()
	return s, nil
}

func (a *App) finishMasterOperation(s *orchestrator.MasterSkillSession, cfg domain.OrchestratorConfig, key string) {
	if s == nil || s.Operation.ID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if s.Operation.QuestID != "" {
		if quest, err := a.store.GetQuest(ctx, s.Operation.QuestID); err == nil && quest.WorkspaceID == s.Operation.WorkspaceID {
			s.Operation.FlowID = quest.FlowID
		}
	}
	if err := a.store.SaveMasterOperation(ctx, s.Operation); err != nil {
		slog.Warn("save master operation", "error", err)
		return
	}
	if err := a.updateMasterCanaries(ctx, s.Operation.WorkspaceID); err != nil {
		slog.Warn("evaluate master canary", "error", err)
	}
	learning, err := a.store.MasterLearningConfig(ctx, s.Operation.WorkspaceID)
	if err != nil || !learning.Enabled {
		return
	}
	revisions, err := a.store.MasterSkillRevisions(ctx)
	if err != nil {
		return
	}
	for _, used := range s.Operation.Skills {
		// A second project evaluates only generic instructions against its own
		// examples, before getting a project-scoped trial of that revision.
		for _, candidate := range revisions {
			if candidate.Skill.ID == used.SkillID && candidate.Status == "local" && candidate.WorkspaceID != s.Operation.WorkspaceID {
				for _, base := range revisions {
					if base.Digest == used.Digest && base.Skill.ID == used.SkillID {
						_ = a.store.QueueMasterLearning(ctx, s.Operation.WorkspaceID, s.Operation.Phase, used.SkillID, base.ID, candidate.ID)
					}
				}
			}
		}
		for _, revision := range revisions {
			if revision.Digest == used.Digest && revision.Skill.ID == used.SkillID && (revision.Status == "builtin" || revision.Status == "shared" || revision.Status == "local") {
				if err = a.store.QueueMasterLearning(ctx, s.Operation.WorkspaceID, s.Operation.Phase, used.SkillID, revision.ID); err != nil {
					slog.Warn("queue master learning", "error", err)
				}
			}
		}
	}
	a.wakeMasterLearning(s.Operation.WorkspaceID, cfg, key)
}

func (a *App) MasterDevelopment(ctx context.Context) (MasterDevelopment, error) {
	ws := a.currentWorldID()
	var v MasterDevelopment
	var err error
	if v.Skills, err = a.masterSkillDefinitions(ctx, ws); err != nil {
		return v, err
	}
	if v.Config, err = a.store.MasterLearningConfig(ctx, ws); err != nil {
		return v, err
	}
	if v.Budget, err = a.store.MasterLearningBudget(ctx, ws); err != nil {
		return v, err
	}
	if v.History, err = a.store.MasterLearningJobs(ctx, ws); err != nil {
		return v, err
	}
	revisions, err := a.store.MasterSkillRevisions(ctx)
	if err != nil {
		return v, err
	}
	trials, err := a.store.MasterSkillTrials(ctx, ws)
	if err != nil {
		return v, err
	}
	for _, r := range revisions {
		if r.WorkspaceID == ws || r.Status == "builtin" || r.Status == "shared" || trials[r.ID] != "" {
			// Shared methodology has no project provenance or examples in this API.
			if r.WorkspaceID != ws {
				r.WorkspaceID = ""
				r.Reason = ""
				r.ParentID = ""
			}
			v.Revisions = append(v.Revisions, r)
		}
	}
	if v.Operations, err = a.store.MasterOperations(ctx, ws); err != nil {
		return v, err
	}
	for i := range v.Operations {
		v.Operations[i].Replay = ""
	}
	return v, nil
}

func (a *App) SetMasterLearning(ctx context.Context, cfg domain.MasterLearningConfig) (MasterDevelopment, error) {
	if err := a.store.SetMasterLearningConfig(ctx, a.currentWorldID(), cfg); err != nil {
		return MasterDevelopment{}, err
	}
	return a.MasterDevelopment(ctx)
}

func (a *App) RollbackMasterSkill(ctx context.Context, id string) (MasterDevelopment, error) {
	revisions, err := a.store.MasterSkillRevisions(ctx)
	if err != nil {
		return MasterDevelopment{}, err
	}
	trials, err := a.store.MasterSkillTrials(ctx, a.currentWorldID())
	if err != nil {
		return MasterDevelopment{}, err
	}
	found := false
	for _, r := range revisions {
		if r.ID == id && r.Status != "builtin" && (r.WorkspaceID == a.currentWorldID() || r.Status == "shared" || trials[r.ID] != "") {
			found = true
		}
	}
	if !found {
		return MasterDevelopment{}, errors.New("ревизия не найдена в текущем проекте")
	}
	if err = a.store.RollbackMasterRevision(ctx, id, "Ручной откат пользователем"); err != nil {
		return MasterDevelopment{}, err
	}
	return a.MasterDevelopment(ctx)
}

func (a *App) updateMasterCanaries(ctx context.Context, ws string) error {
	revisions, err := a.store.MasterSkillRevisions(ctx)
	if err != nil {
		return err
	}
	ops, err := a.store.MasterOperations(ctx, ws)
	if err != nil {
		return err
	}
	for _, r := range revisions {
		if r.Status != "canary" && r.Status != "local" && r.Status != "shared" {
			continue
		}
		count := 0
		bad := false
		for _, op := range ops {
			if op.ProviderError {
				continue
			}
			for _, used := range op.Skills {
				if used.SkillID == r.Skill.ID && used.Digest == r.Digest {
					if op.ContractError || op.Repairs > 0 || op.Feedback == "down" {
						bad = true
					}
					count++
				}
			}
		}
		if bad {
			if err = a.store.RollbackMasterRevision(ctx, r.ID, "Нарушение контракта или отрицательная оценка при применении"); err != nil {
				return err
			}
			continue
		}
		if count >= 3 {
			if err = a.store.SetMasterSkillTrial(ctx, r.ID, ws, "passed"); err != nil {
				return err
			}
			confirmations, err := a.store.MasterSkillConfirmations(ctx, r.ID)
			if err != nil {
				return err
			}
			r.Status = "local"
			r.Reason = "Три применения прошли; ожидается независимое подтверждение другого проекта"
			if confirmations >= 2 {
				r.Status = "shared"
				r.Reason = "Подтверждено минимум тремя применениями в каждом из двух независимых проектов"
			}
			if err = a.store.SaveMasterRevision(ctx, r); err != nil {
				return err
			}
			if err = a.store.UpdateMasterCandidateJobs(ctx, r.ID, r.Status, r.Reason); err != nil {
				return err
			}
		}
	}
	return nil
}

// Generic candidates may be independently replayed in another project; source
// examples are never read there. Promotion requires both projects' evidence.
func masterCandidateInstructions(raw string) (string, error) {
	var v struct {
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return "", err
	}
	v.Instructions = strings.TrimSpace(v.Instructions)
	if len([]rune(v.Instructions)) < 80 || len([]rune(v.Instructions)) > 4000 || !safePortableInstruction(v.Instructions) {
		return "", fmt.Errorf("кандидат не прошёл проверку переносимости и границ")
	}
	return v.Instructions, nil
}
