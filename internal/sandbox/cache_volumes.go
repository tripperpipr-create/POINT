package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Кэши пакетных менеджеров на время квеста.
//
// Раньше GOCACHE, npm- и pip-кэш жили в tmpfs /tmp и умирали вместе с
// контейнером — то есть после каждой команды. Каждый `go build` в квесте был
// холодным, каждый `npm ci` заново качал и распаковывал пакеты. Теперь кэш
// живёт в именованном томе Docker на квест и переживает команды и этапы.
//
// Качество это не трогает только при одном условии: кэш не может подделать
// результат проверки. npm-кэш сверяет каждый пакет с integrity из lock-файла,
// модульный кэш Go — с go.sum; такой кэш безопасен и для приёмки. Кэш сборки
// Go (GOCACHE) хранит готовые объекты и результаты `go test` по ключу действия,
// и агент, способный в него писать, мог бы подложить «зелёный» тест; pip-кэш
// хэши сверяет только с --require-hashes. Поэтому авторитетные прогоны
// (приёмка, проверки Point) получают лишь проверяемые кэши, а GOCACHE и pip —
// свои, пустые, в tmpfs, как раньше.

type cacheVolume struct {
	kind, mount, env string
	// verified — содержимое сверяется менеджером пакетов с lock-файлом, и
	// запись агента не может подменить результат проверки.
	verified bool
}

var questCacheVolumes = []cacheVolume{
	{kind: "npm", mount: "/cache/npm", env: "NPM_CONFIG_CACHE", verified: true},
	{kind: "gomod", mount: "/cache/gomod", env: "GOMODCACHE", verified: true},
	{kind: "gobuild", mount: "/cache/gobuild", env: "GOCACHE"},
	{kind: "composer", mount: "/cache/composer", env: "COMPOSER_CACHE_DIR"},
	{kind: "pip", mount: "/cache/pip", env: "PIP_CACHE_DIR"},
}

// CacheScopeLabel помечает тома кэша квестом, чтобы уборщик снимал их по нему.
const CacheScopeLabel = "point.cache.scope"

func cacheVolumeName(scope, kind string) string {
	sum := sha256.Sum256([]byte(scope))
	return "point-cache-" + hex.EncodeToString(sum[:8]) + "-" + kind
}

// cacheMounts готовит тома кэша области и возвращает аргументы docker run и
// переменные окружения, заменяющие tmpfs-кэши. Пустая область — кэша нет:
// так работают команды вне квеста.
func (b *ContainerBackend) cacheMounts(ctx context.Context, scope string, authoritative bool, image string) ([]string, []string, error) {
	if b.disableDownloadCache {
		// Installed Go modules are required by later offline commands. Keep them
		// in this workspace, like node_modules/vendor, rather than the /tmp that
		// the supervisor resets. Portable clones exclude .point, so an independent
		// checker still installs its own modules; no shared cache is mounted.
		return nil, []string{"GOMODCACHE=/workspace/.point/gomod"}, nil
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return nil, nil, nil
	}
	var args, env []string
	for _, volume := range questCacheVolumes {
		if authoritative && !volume.verified {
			continue
		}
		name := cacheVolumeName(scope, volume.kind)
		if err := b.ensureCacheVolume(ctx, name, scope, image); err != nil {
			// Кэш — ускорение, а не условие: без него команда идёт как раньше,
			// с кэшем в tmpfs.
			slog.Warn("sandbox cache volume unavailable; command runs with a throwaway cache", "volume", name, "error", err)
			continue
		}
		args = append(args, "--volume", name+":"+volume.mount+":rw")
		env = append(env, volume.env+"="+volume.mount)
	}
	return args, env, nil
}

// ensureCacheVolume создаёт том один раз за жизнь ядра. Новый том принадлежит
// root, а команды идут от непривилегированного пользователя, поэтому владельца
// ставит одноразовый контейнер без сети и со всеми отнятыми правами, кроме
// CHOWN. Dockerfile песочницы не меняется — образы не переаттестуются.
func (b *ContainerBackend) ensureCacheVolume(ctx context.Context, name, scope, image string) error {
	if _, ok := b.cacheVolumes.Load(name); ok {
		return nil
	}
	setupCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := b.runInfrastructure(setupCtx, "volume", "create", "--label", CacheScopeLabel+"="+scope, "--label", "point.cache=1", name); err != nil {
		return fmt.Errorf("create sandbox cache volume: %w", err)
	}
	if err := b.runInfrastructure(setupCtx, "run", "--rm", "--pull", "never",
		"--network", "none", "--cap-drop", "ALL", "--cap-add", "CHOWN",
		"--security-opt", "no-new-privileges=true", "--user", "0:0",
		"--volume", name+":/c", image, "chown", strings.TrimSpace(b.User), "/c"); err != nil {
		return fmt.Errorf("prepare sandbox cache volume: %w", err)
	}
	b.cacheVolumes.Store(name, struct{}{})
	return nil
}

// CacheScopes — области, у которых на демоне есть тома кэша. Уборщик сверяет
// их с квестами и снимает тома законченных.
func (b *ContainerBackend) CacheScopes(ctx context.Context) ([]string, error) {
	output, err := b.runInfrastructureOutput(ctx, "volume", "ls", "--filter", "label=point.cache=1", "--format", `{{.Label "`+CacheScopeLabel+`"}}`)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var scopes []string
	for _, line := range strings.Split(output, "\n") {
		scope := strings.TrimSpace(line)
		if scope != "" && !seen[scope] {
			seen[scope] = true
			scopes = append(scopes, scope)
		}
	}
	return scopes, nil
}

// RemoveCacheVolumes снимает тома кэша области. Зовётся, когда квест
// закончен: кэш нужен только его этапам.
func (b *ContainerBackend) RemoveCacheVolumes(ctx context.Context, scope string) error {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return nil
	}
	var errs []string
	for _, volume := range questCacheVolumes {
		name := cacheVolumeName(scope, volume.kind)
		b.cacheVolumes.Delete(name)
		if err := b.runInfrastructure(ctx, "volume", "rm", "--force", name); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("remove sandbox cache volumes: %s", strings.Join(errs, "; "))
	}
	return nil
}
