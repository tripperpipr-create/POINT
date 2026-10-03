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

// Q17: образ, закреплённый в наряде до утверждения, сверяется при каждом
// создании песочницы. Пересобранный или заменённый тег под старым
// утверждением не исполняется.
func TestPinnedImageDigestRefusesAnotherImage(t *testing.T) {
	approved := "sha256:" + strings.Repeat("a", 64)
	backend := &ContainerBackend{Image: "point-agent-sandbox:1.2.2", ImageDigest: approved}
	if _, digest, err := backend.resolveRuntimeImage(context.Background(), "", RuntimeRequirements{PinnedImageDigest: approved}); err != nil || digest != approved {
		t.Fatalf("закреплённый образ не принят: %s %v", digest, err)
	}
	backend.ImageDigest = "sha256:" + strings.Repeat("b", 64)
	_, _, err := backend.resolveRuntimeImage(context.Background(), "", RuntimeRequirements{PinnedImageDigest: approved})
	if !errors.Is(err, ErrRuntimeImageChanged) || !strings.Contains(err.Error(), "sha256:aaaaaaaaaaaa") {
		t.Fatalf("другой образ исполняется под старым утверждением: %v", err)
	}
	if _, _, err = backend.resolveRuntimeImage(context.Background(), "", RuntimeRequirements{}); err != nil {
		t.Fatalf("наряд без закрепления отказал: %v", err)
	}
}
