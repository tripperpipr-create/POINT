package app

import (
	"testing"

	"local-agent-workbench/internal/domain"
)

func TestInvalidateDownstreamArtifacts(t *testing.T) {
	quest := domain.Quest{Controller: map[string]any{}}
	appendStageArtifact(&quest, domain.StageArtifact{
		Kind: domain.StageRoleImplement, StageID: "implement", Status: domain.ArtifactValid,
		ContentRef: "store:blob-1", SchemaVersion: domain.StageArtifactSchemaV1, ProducerID: "agent-a", AttemptID: "att-1",
	})
	appendStageArtifact(&quest, domain.StageArtifact{Kind: domain.StageRoleImplReview, StageID: "impl_review", Status: domain.ArtifactValid})
	appendStageArtifact(&quest, domain.StageArtifact{Kind: "acceptance", StageID: "accept", Status: domain.ArtifactValid})
	invalidateDownstreamArtifacts(&quest, "implement")
	items := stageArtifactsFromQuest(quest)
	if len(items) != 3 {
		t.Fatalf("items=%d", len(items))
	}
	if items[0].ContentRef != "store:blob-1" || items[0].SchemaVersion != domain.StageArtifactSchemaV1 {
		t.Fatalf("artifact metadata lost: %#v", items[0])
	}
	for _, item := range items {
		if item.Status != domain.ArtifactInvalidated {
			t.Fatalf("expected invalidated, got %#v", item)
		}
	}
	if validAcceptanceArtifact(quest) {
		t.Fatal("acceptance must be invalid after replan")
	}
}

func TestDeniedEgressReplay(t *testing.T) {
	quest := domain.Quest{}
	recordDeniedEgress(&quest, "evil.example")
	if !deniedEgressReplay(quest, "evil.example") {
		t.Fatal("expected recorded deny")
	}
	if deniedEgressReplay(quest, "github.com") {
		t.Fatal("unrelated host")
	}
}

func TestBumpQuestCounterDurable(t *testing.T) {
	quest := domain.Quest{}
	if bumpQuestCounter(&quest, "supervisionContinues") != 1 {
		t.Fatal("first")
	}
	if bumpQuestCounter(&quest, "supervisionContinues") != 2 {
		t.Fatal("second")
	}
	if questCounter(quest, "supervisionContinues") != 2 {
		t.Fatal("durable")
	}
}
