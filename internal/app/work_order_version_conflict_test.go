package app

import (
	"context"
	"testing"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/environment"
)

// Q04: расхождение версий между проектами ставит запуск на паузу «Песочница
// требует выбора версии». Карточка показывает конфликт рядом с полем версии,
// и сохранённая человеком версия — его выбор, даже если совпала с
// предложенной Point: иначе согласие с предложением не снимало бы паузу.
func TestWorkOrderRevisionKeepingSuggestedVersionResolvesConflict(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	openTestWorld(t, application)
	draft := managedWorkOrderV2()
	draft.Sandbox.Toolchains = map[string]string{"node": "20"}
	draft.Sandbox.VersionSources = map[string]string{"node": "cf-vue-apps/.gitlab-ci.yml"}
	draft.Sandbox.VersionConflicts = []string{"node: 22 (cf-pages/.gitlab-ci.yml) / 20 (cf-vue-apps/.gitlab-ci.yml)"}
	saved, err := application.SaveWorkOrderV2(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	// Карточка ревизует наряд в том виде, в каком его отдаёт база.
	order, err := application.store.GetWorkOrderV2(context.Background(), saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !environment.ConflictedTools(order.Sandbox)["node"] {
		t.Fatalf("сохранённый наряд потерял конфликт версий: %#v", order.Sandbox)
	}
	revision := order
	revision.Sandbox = domain.RuntimeSpec{Toolchains: map[string]string{"node": "20"}}
	revised, err := application.ReviseWorkOrderV2(context.Background(), order.ID, ReviseWorkOrderV2Request{
		ExpectedVersion: order.Version, ExpectedDigest: domain.WorkOrderDigest(order), IdempotencyKey: "keep-suggested-node", WorkOrder: revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if revised.Sandbox.VersionSources["node"] != environment.UserSelectedVersion || len(environment.ConflictedTools(revised.Sandbox)) != 0 {
		t.Fatalf("согласие с предложенной версией не сняло конфликт: %#v", revised.Sandbox)
	}
	if got := environment.RuntimeRequirementsForWorkOrder(&revised); len(got.VersionConflicts) != 0 || got.ToolVersions["node"] != "20" {
		t.Fatalf("требования песочницы после выбора: %#v", got)
	}
}

// Смена версии в карточке наряда меняла общую с текущей версией карту, и
// ревизия отказывала «work order changed» — выбрать npm 10 было нельзя.
func TestWorkOrderRevisionChangesSandboxVersion(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	application, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { application.Shutdown(context.Background()) })
	openTestWorld(t, application)
	draft := managedWorkOrderV2()
	draft.Sandbox.Toolchains = map[string]string{"node": "24", "npm": "12"}
	draft.Sandbox.VersionSources = map[string]string{"node": "Point default", "npm": "Point default"}
	saved, err := application.SaveWorkOrderV2(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	order, err := application.store.GetWorkOrderV2(context.Background(), saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	revision := order
	revision.Sandbox = domain.RuntimeSpec{Toolchains: map[string]string{"node": "20", "npm": "10"}}
	revised, err := application.ReviseWorkOrderV2(context.Background(), order.ID, ReviseWorkOrderV2Request{
		ExpectedVersion: order.Version, ExpectedDigest: domain.WorkOrderDigest(order), IdempotencyKey: "pick-node20-npm10", WorkOrder: revision,
	})
	if err != nil {
		t.Fatalf("смена версии песочницы отклонена: %v", err)
	}
	if revised.Sandbox.Toolchains["node"] != "20" || revised.Sandbox.Toolchains["npm"] != "10" || revised.Sandbox.Image != environment.Node20ManagedImage {
		t.Fatalf("выбор версии не применён: %#v", revised.Sandbox)
	}
}
