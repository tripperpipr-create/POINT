package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

func TestModelCertificationRequiresCompleteKnownCostMatrix(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "hub-v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	base := domain.ModelCertification{
		ID: "certification", ConnectionID: "connection", Model: "model", Provider: domain.ProviderOpenAI,
		Runtime: "point", Status: "certified", MatrixVersion: "agent-hub-v2-3x3", RequiredRuns: 9,
		PassedRuns: 8, CostKnown: true, Capabilities: domain.AdapterCapabilityManifest{Tools: true, StructuredOutput: true, ContextTokens: 32768, CostVisibility: "known"},
		LastEvaluatedAt: time.Now().UTC(),
	}
	if err = store.SaveModelCertificationV2(ctx, base); err == nil {
		t.Fatal("partial matrix was marked certified")
	}
	base.PassedRuns = 9
	base.CostKnown = false
	if err = store.SaveModelCertificationV2(ctx, base); err == nil {
		t.Fatal("unknown-cost model was marked certified")
	}
	base.CostKnown = true
	if err = store.SaveModelCertificationV2(ctx, base); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListModelCertificationsV2(ctx, base.ConnectionID, base.Model)
	if err != nil || len(items) != 1 || items[0].Status != "certified" || items[0].CertifiedAt == nil {
		t.Fatalf("certification history=%#v err=%v", items, err)
	}
}

func TestCapabilityProbeCertificationRemainsExperimental(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "hub-v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	item := domain.ModelCertification{
		ID: "probe", ConnectionID: "connection", Model: "model", Status: "experimental", MatrixVersion: "capability-probe-v1",
		RequiredRuns: 9, PassedRuns: 1, LastEvaluatedAt: time.Now().UTC(),
	}
	if err = store.SaveModelCertificationV2(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListModelCertificationsV2(context.Background(), "connection", "model")
	if err != nil || len(items) != 1 || items[0].CertifiedAt != nil {
		t.Fatalf("probe certification=%#v err=%v", items, err)
	}
}
