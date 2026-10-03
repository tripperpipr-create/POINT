package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var runtimeCommandPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._+-]{0,63}$`)
var runtimePackagePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._+:-]{0,127}(?:=[a-zA-Z0-9][a-zA-Z0-9._+~-]{0,127})?$`)
var runtimeVersionPattern = regexp.MustCompile(`^(?:\d+(?:\.\d+){0,2}|stable)$`)
var runtimeActualVersion = regexp.MustCompile(`\d+(?:\.\d+){0,2}`)
var ErrRuntimeVersionUnavailable = errors.New("requested sandbox tool version unavailable")

// ErrRuntimeImageChanged — образ песочницы не тот, что закреплён в наряде до
// утверждения (TODO Q17): локальный тег пересобран или заменён.
var ErrRuntimeImageChanged = errors.New("sandbox image changed after approval")

// ImageResolver разрешает образ песочницы без её создания. Наряд закрепляет
// его дайджест до утверждения.
type ImageResolver interface {
	ResolveRuntimeImage(ctx context.Context, runtime RuntimeRequirements) (image, digest string, err error)
}

var _ ImageResolver = (*ContainerBackend)(nil)

// ResolveRuntimeImage — образ, который получит песочница с этими
// требованиями. Может собрать runtime-образ, как и Create.
func (b *ContainerBackend) ResolveRuntimeImage(ctx context.Context, runtime RuntimeRequirements) (string, string, error) {
	if err := b.validate(); err != nil {
		return "", "", err
	}
	if err := b.ensureAvailable(ctx); err != nil {
		return "", "", err
	}
	runtime.PinnedImageDigest = ""
	image, digest, err := b.resolveRuntimeImage(ctx, "", runtime)
	if err != nil {
		return "", "", err
	}
	if !imageDigestPattern.MatchString(strings.ToLower(strings.TrimSpace(digest))) {
		if digest, err = b.inspectImageDigest(ctx, image); err != nil {
			return "", "", err
		}
	}
	return image, strings.ToLower(strings.TrimSpace(digest)), nil
}

// resolveRuntimeImage выбирает образ и сверяет его с закреплённым в наряде.
func (b *ContainerBackend) resolveRuntimeImage(ctx context.Context, requested string, runtime RuntimeRequirements) (string, string, error) {
	image, digest, err := b.resolveUnpinnedRuntimeImage(ctx, requested, runtime)
	if err != nil {
		return "", "", err
	}
	pinned := strings.ToLower(strings.TrimSpace(runtime.PinnedImageDigest))
	if pinned == "" {
		return image, digest, nil
	}
	actual := strings.ToLower(strings.TrimSpace(digest))
	if !imageDigestPattern.MatchString(actual) {
		if actual, err = b.inspectImageDigest(ctx, image); err != nil {
			return "", "", err
		}
	}
	if actual != pinned {
		return "", "", fmt.Errorf("%w: approved %s, now %s (%s)", ErrRuntimeImageChanged, shortImageDigest(pinned), shortImageDigest(actual), image)
	}
	return image, actual, nil
}

func shortImageDigest(digest string) string {
	digest = strings.TrimPrefix(strings.TrimSpace(digest), "sha256:")
	if len(digest) > 12 {
		digest = digest[:12]
	}
	return "sha256:" + digest
}

func (b *ContainerBackend) resolveUnpinnedRuntimeImage(ctx context.Context, requested string, runtime RuntimeRequirements) (string, string, error) {
	if len(runtime.UnsupportedTools) > 0 {
		return "", "", fmt.Errorf("%w: unsupported package managers or tools %s", ErrRuntimeVersionUnavailable, strings.Join(runtime.UnsupportedTools, ", "))
	}
	if len(runtime.VersionConflicts) > 0 {
		return "", "", fmt.Errorf("%w: project sources disagree on versions (%s); choose a version for the work order and retry", ErrRuntimeVersionUnavailable, strings.Join(runtime.VersionConflicts, "; "))
	}
	baseImage, baseDigest, err := b.resolveExecutionImage(ctx, requested)
	if err != nil {
		return "", "", err
	}
	commands, packages, candidates, err := normalizeRuntimeRequirements(runtime)
	if err != nil {
		return "", "", err
	}
	for tool, version := range runtime.ToolVersions {
		if !runtimeCommandPattern.MatchString(tool) || !runtimeVersionPattern.MatchString(version) {
			return "", "", fmt.Errorf("managed runtime version for %q is invalid", tool)
		}
	}
	if len(commands) == 0 {
		return baseImage, baseDigest, nil
	}
	if b.imageProvidesCommands(ctx, immutableImage(baseImage, baseDigest), commands) == nil && b.imageMatchesToolVersions(ctx, immutableImage(baseImage, baseDigest), runtime.ToolVersions) == nil {
		slog.Info("sandbox runtime ready", "runtime_id", runtime.ID, "source", "base", "image", baseImage, "commands", commands)
		return baseImage, baseDigest, nil
	}
	for _, candidate := range candidates {
		image, digest, candidateErr := b.resolveExecutionImage(ctx, candidate)
		if candidateErr == nil && b.imageProvidesCommands(ctx, immutableImage(image, digest), commands) == nil && b.imageMatchesToolVersions(ctx, immutableImage(image, digest), runtime.ToolVersions) == nil {
			slog.Info("sandbox runtime ready", "runtime_id", runtime.ID, "source", "compatible_pack", "image", image, "commands", commands)
			return image, digest, nil
		}
	}
	if len(packages) == 0 {
		return "", "", fmt.Errorf("%w: versions %v, commands %s; choose an available environment and retry", ErrRuntimeVersionUnavailable, runtime.ToolVersions, strings.Join(commands, ", "))
	}
	if !imageDigestPattern.MatchString(strings.ToLower(strings.TrimSpace(baseDigest))) {
		baseDigest, err = b.inspectImageDigest(ctx, baseImage)
		if err != nil {
			return "", "", err
		}
	}

	b.runtimeBuildMu.Lock()
	defer b.runtimeBuildMu.Unlock()
	if runtime.Progress != nil {
		runtime.Progress("runtime_building", "Устанавливаем системные инструменты в кэшированный sandbox: "+strings.Join(commands, ", "))
	}
	slog.Info("sandbox runtime provisioning", "runtime_id", runtime.ID, "version", runtime.Version, "commands", commands, "package_count", len(packages))
	image, digest, err := b.buildRuntimeImage(ctx, baseDigest, runtime.ID, runtime.Version, commands, packages)
	if err != nil {
		return "", "", err
	}
	if err = b.imageMatchesToolVersions(ctx, immutableImage(image, digest), runtime.ToolVersions); err != nil {
		return "", "", err
	}
	return image, digest, nil
}

func (b *ContainerBackend) imageMatchesToolVersions(ctx context.Context, image string, versions map[string]string) error {
	for tool, expected := range versions {
		command := map[string]string{"python": "python3", "rust": "rustc"}[tool]
		if command == "" {
			command = tool
		}
		if !runtimeCommandPattern.MatchString(command) || !runtimeVersionPattern.MatchString(expected) {
			return fmt.Errorf("invalid sandbox version request for %q", tool)
		}
		key := "version|" + image + "|" + command + "|" + expected
		if b.probePassed(image, key) {
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		output, err := b.output(probeCtx,
			"run", "--rm", "--pull", "never", "--network", "none", "--read-only",
			"--cap-drop", "ALL", "--security-opt", "no-new-privileges=true",
			"--user", strings.TrimSpace(b.User), image, command, "--version",
		)
		cancel()
		if err != nil {
			return fmt.Errorf("probe %s version: %w", tool, err)
		}
		if expected == "stable" {
			continue
		}
		actual := runtimeActualVersion.FindString(output)
		if actual != expected && !strings.HasPrefix(actual, expected+".") {
			return fmt.Errorf("%w: sandbox %s version %q does not match requested %q", ErrRuntimeVersionUnavailable, tool, actual, expected)
		}
		b.rememberProbe(image, key)
	}
	return nil
}

func normalizeRuntimeRequirements(runtime RuntimeRequirements) ([]string, []string, []string, error) {
	commands := uniqueSorted(runtime.RequiredCommands)
	packages := uniqueSorted(runtime.Packages)
	candidates := uniqueSorted(runtime.CandidateImages)
	for _, command := range commands {
		if !runtimeCommandPattern.MatchString(command) {
			return nil, nil, nil, fmt.Errorf("managed runtime command %q is invalid", command)
		}
	}
	for _, pkg := range packages {
		if !runtimePackagePattern.MatchString(pkg) {
			return nil, nil, nil, fmt.Errorf("managed runtime package %q is invalid", pkg)
		}
	}
	for _, image := range candidates {
		if !imageReferencePattern.MatchString(image) {
			return nil, nil, nil, fmt.Errorf("managed runtime candidate image %q is invalid", image)
		}
	}
	return commands, packages, candidates, nil
}

func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func immutableImage(image, digest string) string {
	digest = strings.ToLower(strings.TrimSpace(digest))
	if imageDigestPattern.MatchString(digest) {
		return digest
	}
	return strings.TrimSpace(image)
}

func (b *ContainerBackend) inspectImageDigest(ctx context.Context, image string) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	digest, err := b.output(probeCtx, "image", "inspect", image, "--format", "{{.Id}}")
	if err != nil {
		return "", fmt.Errorf("managed runtime base image %q unavailable locally: %w", image, err)
	}
	digest = strings.ToLower(strings.TrimSpace(digest))
	if !imageDigestPattern.MatchString(digest) {
		return "", errors.New("managed runtime base image returned an invalid digest")
	}
	return digest, nil
}

func (b *ContainerBackend) imageProvidesCommands(ctx context.Context, image string, commands []string) error {
	if strings.TrimSpace(image) == "" {
		return errors.New("runtime probe image is empty")
	}
	checks := make([]string, 0, len(commands))
	for _, command := range commands {
		if !runtimeCommandPattern.MatchString(command) {
			return fmt.Errorf("managed runtime command %q is invalid", command)
		}
		checks = append(checks, "command -v "+command+" >/dev/null")
		switch command {
		case "composer", "php", "pip", "pip3":
			checks = append(checks, command+" --version >/dev/null 2>&1")
		}
	}
	key := "commands|" + image + "|" + strings.Join(checks, " && ")
	if b.probePassed(image, key) {
		return nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := b.output(probeCtx,
		"run", "--rm", "--pull", "never", "--network", "none", "--read-only",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges=true",
		"--user", strings.TrimSpace(b.User), image, "/bin/sh", "-lc", strings.Join(checks, " && "),
	)
	if err == nil {
		b.rememberProbe(image, key)
	}
	return err
}

// Пробы образа — отдельный docker run на каждую, и раньше каждый этап квеста
// повторял их заново. Образ, названный дайджестом, неизменен: однажды
// найденные в нём команды и версии там и останутся. Запоминаются только
// удачные пробы и только для дайджестов — тег мог переехать на другой образ.
func (b *ContainerBackend) probePassed(image, key string) bool {
	if !imageDigestPattern.MatchString(image) {
		return false
	}
	_, ok := b.runtimeProbes.Load(key)
	return ok
}

func (b *ContainerBackend) rememberProbe(image, key string) {
	if imageDigestPattern.MatchString(image) {
		b.runtimeProbes.Store(key, struct{}{})
	}
}

func (b *ContainerBackend) buildRuntimeImage(ctx context.Context, baseDigest, runtimeID, runtimeVersion string, commands, packages []string) (string, string, error) {
	manifest := strings.Join([]string{baseDigest, strings.TrimSpace(runtimeID), strings.TrimSpace(runtimeVersion), strings.Join(commands, "\n"), strings.Join(packages, "\n")}, "\x00")
	sum := sha256.Sum256([]byte(manifest))
	manifestDigest := hex.EncodeToString(sum[:])
	tag := "point-runtime:" + manifestDigest[:32]
	if digest, err := b.inspectImageDigest(ctx, tag); err == nil && b.imageProvidesCommands(ctx, digest, commands) == nil {
		slog.Info("sandbox runtime cache hit", "runtime_id", runtimeID, "image", tag, "digest", digest)
		return tag, digest, nil
	}
	if b.Manager == nil || strings.TrimSpace(b.Manager.Root) == "" {
		return "", "", errors.New("managed runtime build root is unavailable")
	}
	if err := os.MkdirAll(b.Manager.Root, 0o700); err != nil {
		return "", "", fmt.Errorf("create managed runtime root: %w", err)
	}
	buildRoot, err := os.MkdirTemp(b.Manager.Root, "runtime-build-")
	if err != nil {
		return "", "", fmt.Errorf("create managed runtime build context: %w", err)
	}
	defer os.RemoveAll(buildRoot)
	dockerfile := runtimeDockerfile(baseDigest, manifestDigest, b.User, packages)
	dockerfilePath := filepath.Join(buildRoot, "Dockerfile")
	if err = os.WriteFile(dockerfilePath, []byte(dockerfile), 0o600); err != nil {
		return "", "", fmt.Errorf("write managed runtime Dockerfile: %w", err)
	}
	buildCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if _, err = b.output(buildCtx,
		"build", "--pull=false", "--network", "default", "--tag", tag,
		"--label", "io.point.runtime.digest=sha256:"+manifestDigest,
		"--file", dockerfilePath, buildRoot,
	); err != nil {
		return "", "", fmt.Errorf("build managed runtime %q: %w", runtimeID, err)
	}
	digest, err := b.inspectImageDigest(ctx, tag)
	if err != nil {
		return "", "", err
	}
	if err = b.imageProvidesCommands(ctx, digest, commands); err != nil {
		return "", "", fmt.Errorf("managed runtime %q failed its tool probe: %w", runtimeID, err)
	}
	slog.Info("sandbox runtime provisioned", "runtime_id", runtimeID, "image", tag, "digest", digest)
	return tag, digest, nil
}

func runtimeDockerfile(baseDigest, manifestDigest, user string, packages []string) string {
	return "FROM " + baseDigest + "\n" +
		"LABEL io.point.runtime.digest=sha256:" + manifestDigest + "\n" +
		"USER root\n" +
		"RUN apk add --no-cache " + strings.Join(packages, " ") + "\n" +
		"USER " + strings.TrimSpace(user) + "\n" +
		"WORKDIR /workspace\n"
}
