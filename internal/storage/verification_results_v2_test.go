package storage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// Приёмка берёт только целиком прошедший прогон; карточка наряда показывает
// последнюю проверку перед приёмкой с числом прошедших проверок.
func TestVerificationResultsKeepPassesReusableAndSummarizeTheLatestCheck(t *testing.T) {
	store := purgeStore(t)
	ctx := context.Background()
	evidence := func(statuses ...string) json.RawMessage {
		criteria := make([]map[string]any, 0, len(statuses)+1)
		for _, status := range statuses {
			criteria = append(criteria, map[string]any{"status": status, "check": map[string]any{"tool": "run_command"}})
		}
		// Ручной критерий команды не имеет и в счёт не идёт.
		criteria = append(criteria, map[string]any{"status": "needs_review"})
		raw, _ := json.Marshal(map[string]any{"criteria": criteria})
		return raw
	}
	now := time.Now().UTC()
	save := func(id, key string, passed bool, at time.Time, raw json.RawMessage) {
		t.Helper()
		if err := store.SaveVerificationResultV2(ctx, domain.VerificationResult{
			ID: id, WorkspaceID: "ws-1", QuestID: "q-1", FlowRunID: "flow-1", BatchKey: key, TreeDigest: "sha256:tree",
			Source: "pre_accept", AllPassed: passed, Evidence: raw, CreatedAt: at,
		}); err != nil {
			t.Fatal(err)
		}
	}
	save("v-1", "key-a", false, now, evidence("satisfied", "failed", "failed"))
	if _, found, err := store.PassedVerificationResultV2(ctx, "q-1", "key-a"); err != nil || found {
		t.Fatalf("a failed run was offered for reuse: found=%v err=%v", found, err)
	}
	if check := store.latestPreAcceptCheckV2(ctx, "flow-1"); check == nil || check.Passed != 1 || check.Total != 3 || check.AllPassed {
		t.Fatalf("summary of the failed check = %+v", check)
	}
	save("v-2", "key-b", true, now.Add(time.Second), evidence("satisfied", "satisfied", "satisfied"))
	stored, found, err := store.PassedVerificationResultV2(ctx, "q-1", "key-b")
	if err != nil || !found || stored.ID != "v-2" || stored.TreeDigest != "sha256:tree" {
		t.Fatalf("passed run not found: %+v found=%v err=%v", stored, found, err)
	}
	if _, found, _ = store.PassedVerificationResultV2(ctx, "q-other", "key-b"); found {
		t.Fatal("a result leaked into another quest")
	}
	if check := store.latestPreAcceptCheckV2(ctx, "flow-1"); check == nil || check.Passed != 3 || check.Total != 3 || !check.AllPassed {
		t.Fatalf("summary of the latest check = %+v", check)
	}
	if _, err = store.db.ExecContext(ctx, `DELETE FROM verification_results_v2 WHERE id='v-1'`); err == nil {
		t.Fatal("verification results must be immutable")
	}
}
