package sandbox

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Расхождение версий между источниками проекта, не разрешённое человеком,
// останавливает подбор образа до обращения к Docker: ветка запуска наряда
// превращает ErrRuntimeVersionUnavailable в паузу «Песочница требует выбора
// версии», а не в молча взятую одну из версий (Q04).
func TestRuntimeVersionConflictStopsBeforeDocker(t *testing.T) {
	backend := &ContainerBackend{}
	_, _, err := backend.resolveRuntimeImage(context.Background(), "", RuntimeRequirements{
		ID: "node", RequiredCommands: []string{"node"}, ToolVersions: map[string]string{"node": "20"},
		VersionConflicts: []string{"node: 22 (cf-pages/.gitlab-ci.yml) / 20 (cf-vue-apps/.gitlab-ci.yml)"},
	})
	if !errors.Is(err, ErrRuntimeVersionUnavailable) || !strings.Contains(err.Error(), "cf-vue-apps/.gitlab-ci.yml") {
		t.Fatalf("конфликт версий не остановил подбор образа: %v", err)
	}
}
