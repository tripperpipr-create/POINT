package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"local-agent-workbench/internal/diagnostics"
)

// Q11: та же проверка на нетронутом дереве — до правок пишущего этапа. Её
// исход хранится и не гоняется дважды за прогон Flow.
func TestBaselineChecksTheUntouchedTreeOnce(t *testing.T) {
	f := newVerificationFixture(t)
	ctx := context.Background()
	baseline := t.TempDir()
	if err := os.WriteFile(filepath.Join(baseline, "package.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.record.BaselinePath = baseline
	if err := f.app.store.SaveSandbox(ctx, f.record); err != nil {
		t.Fatal(err)
	}
	f.backend.fail.Store(true)
	before := f.backend.runs()
	if err := f.app.runBaselineVerificationV2(ctx, f.flowRun.ID, f.writer.ID, f.execution.ID); err != nil {
		t.Fatal(err)
	}
	if f.backend.runs() == before {
		t.Fatal("исходная проверка не запускалась")
	}
	f.backend.mu.Lock()
	root := f.backend.processRequests[len(f.backend.processRequests)-1].WorkspaceRoot
	f.backend.mu.Unlock()
	if root == f.root || root == baseline {
		t.Fatalf("исходная проверка шла не в чистой копии снимка: %s", root)
	}
	statuses := f.app.baselineCriterionStatuses(ctx, f.flowRun.ID)
	if statuses["verify"] != "failed" {
		t.Fatalf("исход исходной проверки по критерию: %v", statuses)
	}
	runs := f.backend.runs()
	if err := f.app.runBaselineVerificationV2(ctx, f.flowRun.ID, f.writer.ID, f.execution.ID); err != nil {
		t.Fatal(err)
	}
	if f.backend.runs() != runs {
		t.Fatal("исходная проверка прогона Flow гоняется второй раз")
	}
}

// Провал, который был и до правки, — дефект проекта или проверки, а не
// регрессия кода: разбор говорит это, и повтор без человека не идёт.
func TestDiagnosisSeparatesPreexistingFailures(t *testing.T) {
	diagnosis := StageFailureDiagnosis{}
	diagnosis.add(StageFailureCheck{CriterionID: "verify", Cause: "vue-demi не собирается", Class: diagnostics.FailureCode})
	diagnosis.add(StageFailureCheck{CriterionID: "tests", Cause: "тест упал", Class: diagnostics.FailureCode})
	diagnosis.markBaseline(map[string]string{"verify": "failed", "tests": "satisfied"})
	if diagnosis.Checks[0].Baseline != "failed" || diagnosis.Checks[0].Class != diagnostics.FailureCriterion {
		t.Fatalf("падавшая до правки проверка не отделена: %+v", diagnosis.Checks[0])
	}
	if diagnosis.Checks[1].Baseline != "passed" || diagnosis.Checks[1].Class != diagnostics.FailureCode || diagnosis.Class != diagnostics.FailureCode {
		t.Fatalf("регрессия кода потеряла свой класс: %+v class=%s", diagnosis.Checks[1], diagnosis.Class)
	}
	only := StageFailureDiagnosis{}
	only.add(StageFailureCheck{CriterionID: "verify", Class: diagnostics.FailureCode})
	only.markBaseline(map[string]string{"verify": "failed"})
	if only.Class != diagnostics.FailureCriterion {
		t.Fatalf("общий класс этапа: %s", only.Class)
	}
}

// Q11: в цепочке писателей нетронутый проект видит только первый. Исходная
// проверка идёт у него с критериями последнего писателя, а писатель, чей
// исходник — правки предыдущего этапа, её не делает.
func TestBaselineRunsAtTheFirstWriterOfAChain(t *testing.T) {
	f := newVerificationFixture(t)
	ctx := context.Background()
	baseline := t.TempDir()
	if err := os.WriteFile(filepath.Join(baseline, "package.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.record.BaselinePath = baseline
	f.record.ParentExecutionID = "execution_first"
	if err := f.app.store.SaveSandbox(ctx, f.record); err != nil {
		t.Fatal(err)
	}
	before := f.backend.runs()
	if err := f.app.runBaselineVerificationV2(ctx, f.flowRun.ID, f.writer.ID, f.execution.ID); err != nil {
		t.Fatal(err)
	}
	if f.backend.runs() != before {
		t.Fatal("исходная проверка шла на дереве после правок предыдущего этапа")
	}
	f.record.ParentExecutionID = ""
	if err := f.app.store.SaveSandbox(ctx, f.record); err != nil {
		t.Fatal(err)
	}
	if err := f.app.runBaselineVerificationV2(ctx, f.flowRun.ID, "node_first", f.execution.ID); err != nil {
		t.Fatal(err)
	}
	if f.backend.runs() == before {
		t.Fatal("первый писатель цепочки не получил исходную проверку")
	}
	if statuses := f.app.baselineCriterionStatuses(ctx, f.flowRun.ID); statuses["verify"] == "" {
		t.Fatalf("исход исходной проверки первого писателя не записан: %v", statuses)
	}
}
