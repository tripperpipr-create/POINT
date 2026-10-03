package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"local-agent-workbench/internal/domain"
)

// Q13: подготовленный файл открывается в diff-редакторе IDE целиком — до и
// после; чужой мир его не получает.
func TestChangeItemContentGivesBothSidesInOwnWorldOnly(t *testing.T) {
	application := newTestApp(t)
	world := openTestWorld(t, application)
	ctx := context.Background()
	if err := application.store.SaveChangeSet(ctx, domain.ChangeSet{
		ID: "cs-prepared", WorkspaceID: world.ID, Title: "Подготовлено", Status: domain.ChangeSetPending, CreatedAt: time.Now().UTC(),
		Items: []domain.ChangeItem{{ID: "item-1", Path: "deploy.mjs", Kind: "modify", OriginalContent: "old\n", ProposedContent: "new\n", Diff: "-old\n+new\n"}},
	}); err != nil {
		t.Fatal(err)
	}
	content, err := application.ChangeItemContent("cs-prepared", "item-1")
	if err != nil || content.Original != "old\n" || content.Proposed != "new\n" || content.Path != "deploy.mjs" {
		t.Fatalf("содержимое файла: %+v %v", content, err)
	}
	if _, err = application.ChangeItemContent("cs-prepared", "missing"); err == nil {
		t.Fatal("несуществующий файл набора отдан")
	}
	if err = application.store.SaveChangeSet(ctx, domain.ChangeSet{
		ID: "cs-foreign", WorkspaceID: "ws-other", Title: "Чужое", Status: domain.ChangeSetPending, CreatedAt: time.Now().UTC(),
		Items: []domain.ChangeItem{{ID: "item-2", Path: "secret.txt", Kind: "modify", OriginalContent: "a", ProposedContent: "b"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = application.ChangeItemContent("cs-foreign", "item-2"); err == nil {
		t.Fatal("файл чужого мира отдан")
	}
}

func TestDeliverySkipReasonNamesTheCause(t *testing.T) {
	prepared := domain.EvidenceBundle{PreparedFiles: []string{"a.go"}}
	cases := []struct {
		bundle  domain.EvidenceBundle
		flow    bool
		machine bool
		want    string
	}{
		{domain.EvidenceBundle{}, false, false, ""},
		{domain.EvidenceBundle{PreparedFiles: []string{"a.go"}, DeliveryConflict: true}, true, true, "конфликтом"},
		{domain.EvidenceBundle{PreparedFiles: []string{"a.go"}, DeliveryTarget: "isolated_review"}, true, true, "ручной"},
		{prepared, false, true, "не завершилась"},
		{prepared, true, false, "проверки не подтвердили"},
	}
	for _, item := range cases {
		got := deliverySkipReasonV2(item.bundle, item.flow, item.machine)
		if (item.want == "" && got != "") || (item.want != "" && !strings.Contains(got, item.want)) {
			t.Fatalf("причина %q, ждали %q", got, item.want)
		}
	}
}
