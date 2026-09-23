package storage_test

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/masterskills"
	"local-agent-workbench/internal/storage"
)

func masterLearningStore(t *testing.T) *storage.SQLite {
	t.Helper()
	s, err := storage.Open(filepath.Join(t.TempDir(), "master.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func TestMasterSkillSeedPreservesRevisions(t *testing.T) {
	s := masterLearningStore(t)
	ctx := context.Background()
	skills := masterskills.Builtins()
	if err := s.SeedMasterSkills(ctx, skills); err != nil {
		t.Fatal(err)
	}
	r := domain.MasterSkillRevision{ID: "learned", Skill: skills[0], WorkspaceID: "a", Status: "local", CreatedAt: time.Now().UTC()}
	r.Skill.Instructions += "\nLearned methodology."
	r.Skill.Configuration = map[string]any{"revision": 2}
	r.Digest = domain.SkillDefinitionAttribution(r.Skill).Digest
	if err := s.SaveMasterRevision(ctx, r); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.SeedMasterSkills(ctx, skills); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.MasterSkillRevisions(ctx)
	if err != nil || len(all) != 8 {
		t.Fatalf("seed: %d %v", len(all), err)
	}
	if err = s.RollbackMasterRevision(ctx, r.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveMasterRevision(ctx, r); err != nil {
		t.Fatal(err)
	}
	all, _ = s.MasterSkillRevisions(ctx)
	for _, v := range all {
		if v.ID == r.ID && v.Status != "rolled_back" {
			t.Fatal("stale worker resurrected rollback")
		}
	}
}
func TestMasterLearningQueueIdempotentAndProjectScoped(t *testing.T) {
	s := masterLearningStore(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		op := domain.MasterOperation{ID: domain.NewID("op"), WorkspaceID: "a", Phase: "intake", Skills: []domain.SkillAttribution{{SkillID: masterskills.Intake}}, Replay: "{}", CreatedAt: time.Now().UTC()}
		if err := s.SaveMasterOperation(ctx, op); err != nil {
			t.Fatal(err)
		}
		_ = s.SaveMasterOperation(ctx, op)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.QueueMasterLearning(ctx, "a", "intake", masterskills.Intake, "base"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	jobs, err := s.MasterLearningJobs(ctx, "a")
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs=%v err=%v", jobs, err)
	}
	foreign, _ := s.MasterLearningJobs(ctx, "b")
	if len(foreign) != 0 {
		t.Fatal("cross-project job leak")
	}
	if _, err = s.ClaimMasterLearning(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimMasterLearning(ctx, "a"); err == nil {
		t.Fatal("two active evaluations")
	}
	if err = s.RecoverMasterLearning(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimMasterLearning(ctx, "a"); err != nil {
		t.Fatal("queue did not resume", err)
	}
}
func TestMasterLearningBudgetAtomicThirtyDaysAndCrash(t *testing.T) {
	s := masterLearningStore(t)
	ctx := context.Background()
	for _, op := range []domain.MasterOperation{
		{ID: "main", WorkspaceID: "a", InputTokens: 1000, CreatedAt: time.Now().UTC()},
		{ID: "old", WorkspaceID: "a", InputTokens: 999999, CreatedAt: time.Now().UTC().AddDate(0, 0, -31)},
		{ID: "foreign", WorkspaceID: "b", InputTokens: 999999, CreatedAt: time.Now().UTC()},
	} {
		if err := s.SaveMasterOperation(ctx, op); err != nil {
			t.Fatal(err)
		}
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.ReserveMasterLearning(ctx, "a", 30); err == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 3 {
		t.Fatalf("reservations %d", successes.Load())
	}
	b, _ := s.MasterLearningBudget(ctx, "a")
	if b.LimitTokens != 100 || b.ReservedTokens != 90 {
		t.Fatalf("budget %+v", b)
	}
	if err := s.RecoverMasterLearning(ctx); err != nil {
		t.Fatal(err)
	}
	b, _ = s.MasterLearningBudget(ctx, "a")
	if b.SpentTokens != 90 || b.ReservedTokens != 0 {
		t.Fatalf("crash charge %+v", b)
	}
	if _, err := s.ReserveMasterLearning(ctx, "a", 11); err == nil {
		t.Fatal("overspent 10% budget")
	}
}
func TestMasterProviderErrorsAndLegacyNotLearningEvidence(t *testing.T) {
	s := masterLearningStore(t)
	ctx := context.Background()
	for i := 0; i < 6; i++ {
		op := domain.MasterOperation{ID: domain.NewID("op"), WorkspaceID: "a", Phase: "intake", Replay: "{}", CreatedAt: time.Now().UTC()}
		if i < 3 {
			op.Skills = []domain.SkillAttribution{{SkillID: masterskills.Intake}}
			op.ProviderError = true
		}
		_ = s.SaveMasterOperation(ctx, op)
	}
	if err := s.QueueMasterLearning(ctx, "a", "intake", masterskills.Intake, "base"); err != nil {
		t.Fatal(err)
	}
	jobs, _ := s.MasterLearningJobs(ctx, "a")
	if len(jobs) > 0 {
		t.Fatal("legacy/provider errors became evidence")
	}
}
