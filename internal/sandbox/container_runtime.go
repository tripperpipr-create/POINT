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

func (b *ContainerBackend) resolveRuntimeImage(ctx context.Context, requested string, runtime RuntimeRequirements) (string, string, error) {
	baseImage, baseDigest, err := b.resolveExecutionImage(ctx, requested)
	if err != nil {
		return "", "", err
	}
	commands, packages, candidates, err := normalizeRuntimeRequirements(runtime)
	if err != nil {
		return "", "", err
	}
	if len(commands) == 0 {
		return baseImage, baseDigest, nil
	}
	if b.imageProvidesCommands(ctx, immutableImage(baseImage, baseDigest), commands) == nil {
		slog.Info("sandbox runtime ready", "runtime_id", runtime.ID, "source", "base", "image", baseImage, "commands", commands)
		return baseImage, baseDigest, nil
	}
	for _, candidate := range candidates {
		image, digest, candidateErr := b.resolveExecutionImage(ctx, candidate)
		if candidateErr == nil && b.imageProvidesCommands(ctx, immutableImage(image, digest), commands) == nil {
			slog.Info("sandbox runtime ready", "runtime_id", runtime.ID, "source", "compatible_pack", "image", image, "commands", commands)
			return image, digest, nil
		}
	}
	if len(packages) == 0 {
		return "", "", fmt.Errorf("managed runtime %q is missing required commands %s and no approved packages can provide them", runtime.ID, strings.Join(commands, ", "))
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
	return b.buildRuntimeImage(ctx, baseDigest, runtime.ID, runtime.Version, commands, packages)
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
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := b.output(probeCtx,
		"run", "--rm", "--pull", "never", "--network", "none", "--read-only",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges=true",
		"--user", strings.TrimSpace(b.User), image, "/bin/sh", "-lc", strings.Join(checks, " && "),
	)
	return err
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
