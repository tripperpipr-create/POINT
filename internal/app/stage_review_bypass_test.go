package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
)

// E6 (29.09): Implementation review закрылся за 10 с без модели и без единой
// проверки, хотя lock-файл расходился с манифестом. Пропуск модельного ревью
// допустим только на дереве, которое целиком прошло проверку Point.
func TestImplementationReviewBypassNeedsAPassedCheckOnTheSameTree(t *testing.T) {
	application := newTestApp(t)
	world := openTestWorld(t, application)
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"app","dependencies":{"ssh2":"^1.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	record := domain.SandboxRecord{ID: "sandbox-review", WorkspaceID: world.ID, ExecutionID: "exec-review", Kind: "isolated", Backend: "filtered-copy", Path: root, CreatedAt: time.Now().UTC()}
	if err := application.store.SaveSandbox(ctx, record); err != nil {
		t.Fatal(err)
	}
	flowRun := domain.FlowRun{ID: "flowrun-review", QuestID: "quest-review"}
	exec := domain.ExecutionInstance{ID: "exec-review", SandboxID: record.ID}
	if application.implementationCheckedOnTree(ctx, flowRun, exec) {
		t.Fatal("ревью пропущено без единой проверки — форма E6")
	}
	saved, err := application.store.GetSandbox(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := sandbox.TreeDigestWithRules(root, saved.FileRulesVersion)
	if err != nil {
		t.Fatal(err)
	}
	save := func(id, flowRunID string, passed bool) {
		t.Helper()
		if err := application.store.SaveVerificationResultV2(ctx, domain.VerificationResult{
			ID: id, WorkspaceID: world.ID, QuestID: "quest-review", FlowRunID: flowRunID, BatchKey: "key-" + id,
			TreeDigest: tree, Source: "pre_accept", AllPassed: passed,
		}); err != nil {
			t.Fatal(err)
		}
	}
	save("failed", flowRun.ID, false)
	save("other-flow", "flowrun-other", true)
	if application.implementationCheckedOnTree(ctx, flowRun, exec) {
		t.Fatal("провал или проверка чужого Flow засчитаны как пройденная проверка")
	}
	save("passed", flowRun.ID, true)
	if !application.implementationCheckedOnTree(ctx, flowRun, exec) {
		t.Fatal("прошедшая проверка того же дерева не разрешила пропуск ревью")
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"app"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if application.implementationCheckedOnTree(ctx, flowRun, exec) {
		t.Fatal("проверка старого дерева разрешила пропуск ревью изменённого")
	}
}
