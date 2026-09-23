package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/egress"
	"local-agent-workbench/internal/osproc"
)

const (
	defaultContainerImage  = "point-agent-sandbox:1.2.2"
	defaultContainerMemory = "2g"
	defaultContainerCPUs   = "2"
	defaultContainerPIDs   = 256
)

var resourceValuePattern = regexp.MustCompile(`^[1-9][0-9]*(?:[bkmgBKMG])?$`)
var imageDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var imageReferencePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/@:-]{0,254}$`)
var containerUserPattern = regexp.MustCompile(`^[1-9][0-9]{0,9}(?::[1-9][0-9]{0,9})?$`)

// ContainerBackend keeps immutable workspace snapshots on the host while all
// executable tools run inside a short-lived, locked-down Docker container.
// The bind mount exposes the execution root at /workspace: either the live
// open project (Kind=live) or an isolated sandbox copy. Commands and network
// are isolated; file mutation mode is decided at Create time.
type ContainerBackend struct {
	*Manager
	DockerBinary         string
	Image                string
	MemoryLimit          string
	CPULimit             string
	PIDsLimit            int
	User                 string
	DockerVersion        string
	APIVersion           string
	ImageDigest          string
	command              func(context.Context, string, ...string) *exec.Cmd
	infrastructure       func(context.Context, ...string) error
	infrastructureOutput func(context.Context, ...string) (string, error)
	runtimeBuildMu       sync.Mutex
}

func NewContainerBackend(root string) *ContainerBackend {
	return &ContainerBackend{
		Manager:      &Manager{Root: root},
		DockerBinary: "docker",
		Image:        defaultContainerImage,
		MemoryLimit:  defaultContainerMemory,
		CPULimit:     defaultContainerCPUs,
		PIDsLimit:    defaultContainerPIDs,
		User:         defaultContainerUser(),
		command:      osproc.CommandContext,
	}
}

func (b *ContainerBackend) Capabilities() Capabilities {
	return Capabilities{
		Backend:                       "docker",
		Version:                       b.DockerVersion,
		APIVersion:                    b.APIVersion,
		Image:                         b.Image,
		ImageDigest:                   b.ImageDigest,
		LiveWorkspaceIsolation:        true,
		ProcessIsolation:              true,
		NetworkIsolation:              true,
		SecretEnvironmentSanitization: true,
		SymlinkIsolation:              true,
		StrongOSBoundary:              true,
		Notes: []string{
			"Executable tools run in one short-lived container with only the execution sandbox mounted read-write.",
			"The container root is read-only; Linux capabilities are dropped and no-new-privileges is enforced.",
			"Outbound networking is deny-all by default; selective TLS egress uses an isolated internal network and policy gateway.",
		},
	}
}

func (*ContainerBackend) EnforcesControlledEgress() bool { return true }

func (b *ContainerBackend) Create(ctx context.Context, request CreateRequest) (domain.SandboxRecord, error) {
	if err := b.validate(); err != nil {
		return domain.SandboxRecord{}, err
	}
	image, digest, err := b.resolveRuntimeImage(ctx, request.Image, request.Runtime)
	if err != nil {
		return domain.SandboxRecord{}, err
	}
	record, err := b.Manager.Create(ctx, request)
	if err != nil {
		return domain.SandboxRecord{}, err
	}
	record.Backend = "docker"
	record.BackendVersion = b.DockerVersion
	record.BackendImage = image
	record.BackendImageDigest = digest
	return record, nil
}

func (b *ContainerBackend) Merge(ctx context.Context, request MergeRequest) (MergeResult, error) {
	if err := b.validate(); err != nil {
		return MergeResult{}, err
	}
	image, digest, err := b.resolveRuntimeImage(ctx, "", request.Runtime)
	if err != nil {
		return MergeResult{}, err
	}
	result, err := b.Manager.Merge(ctx, request)
	if err != nil || result.Record.ID == "" {
		return result, err
	}
	result.Record.Backend = "docker"
	result.Record.BackendVersion = b.DockerVersion
	result.Record.BackendImage = image
	result.Record.BackendImageDigest = digest
	return result, nil
}

// Probe verifies both the daemon and the exact local image. --pull=never is
// used for execution, so a missing image is a startup/configuration error and
// can never become an implicit network operation.
func (b *ContainerBackend) Probe(ctx context.Context) error {
	if err := b.validate(); err != nil {
		return err
	}
	version, err := b.output(ctx, "version", "--format", "{{.Server.Version}}")
	if err != nil {
		return fmt.Errorf("docker sandbox daemon unavailable: %w", err)
	}
	apiVersion, err := b.output(ctx, "version", "--format", "{{.Server.APIVersion}}")
	if err != nil {
		return fmt.Errorf("docker sandbox API version unavailable: %w", err)
	}
	digest, err := b.output(ctx, "image", "inspect", b.Image, "--format", "{{.Id}}")
	if err != nil {
		return fmt.Errorf("docker sandbox image %q unavailable locally: %w", b.Image, err)
	}
	b.DockerVersion = strings.TrimSpace(version)
	b.APIVersion = strings.TrimSpace(apiVersion)
	b.ImageDigest = strings.TrimSpace(digest)
	if b.DockerVersion == "" || b.APIVersion == "" || b.ImageDigest == "" {
		return errors.New("docker sandbox probe returned empty version attribution")
	}
	return nil
}

func (b *ContainerBackend) PrepareProcess(ctx context.Context, request ProcessRequest) (PreparedProcess, error) {
	if err := b.validate(); err != nil {
		return PreparedProcess{}, err
	}
	root, workdir, err := containerPaths(request.WorkspaceRoot, request.WorkingDirectory)
	if err != nil {
		return PreparedProcess{}, err
	}
	if strings.TrimSpace(request.ShellCommand) == "" && strings.TrimSpace(request.Program) == "" {
		return PreparedProcess{}, errors.New("sandbox process program or shell command is required")
	}
	executionImage, executionDigest, err := b.resolveExecutionImage(ctx, request.Image)
	if err != nil {
		return PreparedProcess{}, err
	}
	if imageDigestPattern.MatchString(executionDigest) {
		executionImage = executionDigest
	}
	policy, err := egress.Compile(request.NetworkPolicy, request.AllowedNetworkHosts, egress.Quota{})
	if err != nil {
		return PreparedProcess{}, err
	}
	identity, err := containerIdentity()
	if err != nil {
		return PreparedProcess{}, fmt.Errorf("create sandbox container identity: %w", err)
	}
	name := "point-exec-" + identity
	networkName, gatewayName := "point-egress-net-"+identity, "point-egress-gateway-"+identity
	network := "none"
	proxyEnvironment := []string{}
	gatewayAddress := ""
	if policy.Mode == "ALLOWLIST" {
		if gatewayAddress, err = b.prepareEgressGateway(ctx, networkName, gatewayName, request.RunID, policy); err != nil {
			return PreparedProcess{}, err
		}
		network = networkName
		proxyEnvironment = []string{
			"HTTP_PROXY=http://point-egress-gateway:8080", "HTTPS_PROXY=http://point-egress-gateway:8080",
			"http_proxy=http://point-egress-gateway:8080", "https_proxy=http://point-egress-gateway:8080",
			"NO_PROXY=localhost,127.0.0.1,::1", "POINT_EGRESS_POLICY_DIGEST=" + policy.Digest,
		}
	}
	args := []string{
		"run", "--rm", "--pull", "never", "--name", name,
		"--network", network, "--read-only", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges=true", "--pids-limit", strconv.Itoa(b.PIDsLimit),
		"--memory", b.MemoryLimit, "--cpus", b.CPULimit,
		"--ipc", "none", "--hostname", "point-sandbox",
		"--tmpfs", "/tmp:rw,nosuid,nodev,size=512m",
		"--volume", root + ":/workspace:rw", "--workdir", workdir,
	}
	if gatewayAddress != "" {
		args = append(args, "--add-host", "point-egress-gateway:"+gatewayAddress)
		pinned, pinErr := pinAllowlistHosts(ctx, policy)
		if pinErr != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = b.runInfrastructure(cleanupCtx, "rm", "--force", gatewayName)
			_ = b.runInfrastructure(cleanupCtx, "network", "rm", networkName)
			cancel()
			return PreparedProcess{}, pinErr
		}
		args = append(args, pinned...)
	}
	if strings.TrimSpace(b.User) != "" {
		args = append(args, "--user", strings.TrimSpace(b.User))
	}
	for _, entry := range containerEnvironment(request.Environment) {
		args = append(args, "--env", entry)
	}
	for _, entry := range proxyEnvironment {
		args = append(args, "--env", entry)
	}
	args = append(args, executionImage)
	if strings.TrimSpace(request.ShellCommand) != "" {
		args = append(args, "/bin/sh", "-lc", request.ShellCommand)
	} else {
		args = append(args, request.Program)
		args = append(args, request.Arguments...)
	}
	command := b.commandFor(ctx, args...)
	command.Env = dockerClientEnvironment()
	cleanup := func(cleanupCtx context.Context) error {
		cleanupCommand := b.commandFor(cleanupCtx, "rm", "--force", name)
		cleanupCommand.Env = dockerClientEnvironment()
		cleanupCommand.Stdout = io.Discard
		cleanupCommand.Stderr = io.Discard
		runErr := cleanupCommand.Run()
		if runErr != nil && cleanupCtx.Err() == nil {
			// docker run --rm removes successful containers before cleanup.
			runErr = nil
		}
		if policy.Mode == "ALLOWLIST" {
			gatewayErr := b.runInfrastructure(cleanupCtx, "rm", "--force", gatewayName)
			networkErr := b.runInfrastructure(cleanupCtx, "network", "rm", networkName)
			return errors.Join(cleanupCtx.Err(), runErr, gatewayErr, networkErr)
		}
		return errors.Join(cleanupCtx.Err(), runErr)
	}
	return PreparedProcess{Command: command, Cleanup: cleanup}, nil
}

func (b *ContainerBackend) validate() error {
	if b.Manager == nil || strings.TrimSpace(b.Manager.Root) == "" {
		return errors.New("docker sandbox root is required")
	}
	if strings.TrimSpace(b.DockerBinary) == "" || strings.TrimSpace(b.Image) == "" {
		return errors.New("docker sandbox binary and image are required")
	}
	if !resourceValuePattern.MatchString(strings.TrimSpace(b.MemoryLimit)) {
		return errors.New("docker sandbox memory limit is invalid")
	}
	if value, err := strconv.ParseFloat(strings.TrimSpace(b.CPULimit), 64); err != nil || value <= 0 || value > 64 {
		return errors.New("docker sandbox CPU limit must be between 0 and 64")
	}
	if b.PIDsLimit < 16 || b.PIDsLimit > 4096 {
		return errors.New("docker sandbox PID limit must be between 16 and 4096")
	}
	user := strings.TrimSpace(b.User)
	if !containerUserPattern.MatchString(user) {
		return errors.New("docker sandbox requires a numeric non-root user identity")
	}
	return nil
}

func (b *ContainerBackend) output(ctx context.Context, args ...string) (string, error) {
	command := b.commandFor(ctx, args...)
	command.Env = dockerClientEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w (%s)", err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func (b *ContainerBackend) commandFor(ctx context.Context, args ...string) *exec.Cmd {
	factory := b.command
	if factory == nil {
		factory = osproc.CommandContext
	}
	return factory(ctx, b.DockerBinary, args...)
}

func (b *ContainerBackend) prepareEgressGateway(ctx context.Context, networkName, gatewayName, runID string, policy egress.Policy) (string, error) {
	encodedPolicy, err := json.Marshal(policy)
	if err != nil {
		return "", fmt.Errorf("encode egress policy: %w", err)
	}
	policyBase64 := base64.RawURLEncoding.EncodeToString(encodedPolicy)
	if err = b.runInfrastructure(ctx, "network", "create", "--internal", "--driver", "bridge", networkName); err != nil {
		return "", err
	}
	cleanupOnFailure := func(cause error) error {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		gatewayErr := b.runInfrastructure(cleanupCtx, "rm", "--force", gatewayName)
		networkErr := b.runInfrastructure(cleanupCtx, "network", "rm", networkName)
		return errors.Join(cause, gatewayErr, networkErr)
	}
	if err = b.runInfrastructure(ctx,
		"run", "--detach", "--rm", "--pull", "never", "--name", gatewayName,
		"--network", networkName, "--network-alias", "point-egress-gateway",
		"--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true",
		"--pids-limit", "64", "--memory", "128m", "--cpus", "0.25", "--ipc", "none",
		"--tmpfs", "/tmp:rw,nosuid,nodev,size=16m", b.executionImage(),
		"/usr/local/bin/point-egress-gateway", "serve", "--listen", "0.0.0.0:8080",
		"--policy-base64", policyBase64, "--run-id", strings.TrimSpace(runID),
	); err != nil {
		return "", cleanupOnFailure(err)
	}
	if err = b.runInfrastructure(ctx, "network", "connect", "bridge", gatewayName); err != nil {
		return "", cleanupOnFailure(err)
	}
	inspectTemplate := fmt.Sprintf(`{{with index .NetworkSettings.Networks %q}}{{.IPAddress}}{{end}}`, networkName)
	gatewayAddress, err := b.runInfrastructureOutput(ctx, "inspect", "--format", inspectTemplate, gatewayName)
	gatewayIP := net.ParseIP(strings.TrimSpace(gatewayAddress))
	if err != nil || gatewayIP == nil || gatewayIP.IsLoopback() || gatewayIP.IsUnspecified() {
		if err == nil {
			err = errors.New("egress gateway has no valid internal address")
		}
		return "", cleanupOnFailure(err)
	}
	var probeErr error
	for attempt := 0; attempt < 20; attempt++ {
		probeErr = b.runInfrastructure(ctx, "exec", gatewayName, "/usr/local/bin/point-egress-gateway", "probe", "--address", "127.0.0.1:8080")
		if probeErr == nil {
			return gatewayIP.String(), nil
		}
		select {
		case <-ctx.Done():
			return "", cleanupOnFailure(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	return "", cleanupOnFailure(fmt.Errorf("egress gateway readiness failed: %w", probeErr))
}

func (b *ContainerBackend) runInfrastructure(ctx context.Context, args ...string) error {
	if b.infrastructure != nil {
		return b.infrastructure(ctx, args...)
	}
	_, err := b.runInfrastructureOutput(ctx, args...)
	return err
}

func (b *ContainerBackend) runInfrastructureOutput(ctx context.Context, args ...string) (string, error) {
	if b.infrastructureOutput != nil {
		return b.infrastructureOutput(ctx, args...)
	}
	command := b.commandFor(ctx, args...)
	command.Env = dockerClientEnvironment()
	output, err := command.CombinedOutput()
	if err == nil {
		return string(output), nil
	}
	operation := "docker"
	if len(args) > 0 {
		operation += " " + args[0]
	}
	detail := strings.TrimSpace(string(output))
	if len(detail) > 512 {
		detail = detail[:512] + "..."
	}
	if detail == "" {
		return "", fmt.Errorf("%s: %w", operation, err)
	}
	return "", fmt.Errorf("%s: %w (%s)", operation, err, detail)
}

func (b *ContainerBackend) executionImage() string {
	digest := strings.ToLower(strings.TrimSpace(b.ImageDigest))
	if imageDigestPattern.MatchString(digest) {
		return digest
	}
	return b.Image
}

// ExecutionImageForRecord keeps every process on the image selected when its
// sandbox was created, even if a local tag is later replaced.
func ExecutionImageForRecord(record domain.SandboxRecord) string {
	digest := strings.ToLower(strings.TrimSpace(record.BackendImageDigest))
	if imageDigestPattern.MatchString(digest) {
		return digest
	}
	return strings.TrimSpace(record.BackendImage)
}

func (b *ContainerBackend) resolveExecutionImage(ctx context.Context, requested string) (string, string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" || requested == b.Image {
		digest := strings.ToLower(strings.TrimSpace(b.ImageDigest))
		return b.Image, digest, nil
	}
	if !imageReferencePattern.MatchString(requested) {
		return "", "", errors.New("managed docker sandbox image reference is invalid")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	digest, err := b.output(probeCtx, "image", "inspect", requested, "--format", "{{.Id}}")
	if err != nil {
		return "", "", fmt.Errorf("managed docker sandbox image %q unavailable locally: %w", requested, err)
	}
	digest = strings.ToLower(strings.TrimSpace(digest))
	if !imageDigestPattern.MatchString(digest) {
		return "", "", errors.New("managed docker sandbox image returned an invalid digest")
	}
	return requested, digest, nil
}

func containerPaths(workspaceRoot, workingDirectory string) (string, string, error) {
	root, err := filepath.Abs(strings.TrimSpace(workspaceRoot))
	if err != nil || strings.TrimSpace(workspaceRoot) == "" {
		return "", "", errors.New("sandbox workspace root is required")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve sandbox workspace root: %w", err)
	}
	workingDirectory = strings.TrimSpace(workingDirectory)
	if workingDirectory == "" {
		workingDirectory = root
	}
	workingDirectory, err = filepath.Abs(workingDirectory)
	if err != nil {
		return "", "", fmt.Errorf("resolve sandbox working directory: %w", err)
	}
	workingDirectory, err = filepath.EvalSymlinks(workingDirectory)
	if err != nil {
		return "", "", fmt.Errorf("resolve sandbox working directory: %w", err)
	}
	relative, err := filepath.Rel(root, workingDirectory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", "", errors.New("sandbox working directory escapes its workspace root")
	}
	containerWorkdir := "/workspace"
	if relative != "." {
		containerWorkdir += "/" + filepath.ToSlash(relative)
	}
	return filepath.Clean(root), containerWorkdir, nil
}

func containerEnvironment(environment []string) []string {
	result := []string{
		"HOME=/tmp/home", "TMPDIR=/tmp", "GOCACHE=/tmp/go-cache",
		"NPM_CONFIG_CACHE=/tmp/npm-cache", "PIP_CACHE_DIR=/tmp/pip-cache",
	}
	allowed := map[string]bool{
		"CI": true, "LANG": true, "LC_ALL": true, "TZ": true, "TERM": true,
		"NO_COLOR": true, "FORCE_COLOR": true, "CGO_ENABLED": true,
		"GOFLAGS": true, "GOMAXPROCS": true, "NODE_ENV": true,
		"PYTHONUTF8": true, "PYTHONDONTWRITEBYTECODE": true, "RUST_BACKTRACE": true,
	}
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		key = strings.ToUpper(strings.TrimSpace(key))
		if !ok || !allowed[key] || strings.IndexByte(value, 0) >= 0 || len(value) > 4096 {
			continue
		}
		result = append(result, key+"="+value)
	}
	return result
}

func dockerClientEnvironment() []string {
	allowed := map[string]bool{
		"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "WINDIR": true,
		"TEMP": true, "TMP": true, "TMPDIR": true,
		"DOCKER_HOST": true, "DOCKER_CONTEXT": true, "DOCKER_TLS_VERIFY": true,
		"DOCKER_CERT_PATH": true,
	}
	result := make([]string, 0, len(allowed))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if allowed[strings.ToUpper(key)] {
			result = append(result, entry)
		}
	}
	return result
}

func containerIdentity() (string, error) {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return hex.EncodeToString(random), nil
}

// BackendFromEnvironment selects a backend once at application startup. An
// explicitly requested strong backend is probed immediately and startup fails
// instead of silently falling back to host execution.
func BackendFromEnvironment(root string) (Backend, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("POINT_SANDBOX_BACKEND")))
	switch mode {
	case "", "local", "filtered-copy":
		if environmentTrue("POINT_SANDBOX_REQUIRE_STRONG") {
			return nil, errors.New("strong sandbox is required but POINT_SANDBOX_BACKEND is not docker")
		}
		return &Manager{Root: root}, nil
	case "docker", "container":
		backend := NewContainerBackend(root)
		if value := strings.TrimSpace(os.Getenv("POINT_SANDBOX_IMAGE")); value != "" {
			backend.Image = value
		}
		if value := strings.TrimSpace(os.Getenv("POINT_SANDBOX_MEMORY")); value != "" {
			backend.MemoryLimit = value
		}
		if value := strings.TrimSpace(os.Getenv("POINT_SANDBOX_CPUS")); value != "" {
			backend.CPULimit = value
		}
		if value := strings.TrimSpace(os.Getenv("POINT_SANDBOX_PIDS")); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("POINT_SANDBOX_PIDS: %w", err)
			}
			backend.PIDsLimit = parsed
		}
		if value, present := os.LookupEnv("POINT_SANDBOX_USER"); present {
			backend.User = strings.TrimSpace(value)
		}
		probeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := backend.Probe(probeCtx); err != nil {
			return nil, err
		}
		return backend, nil
	default:
		return nil, fmt.Errorf("unsupported POINT_SANDBOX_BACKEND %q", mode)
	}
}

func environmentTrue(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// pinAllowlistHosts resolves each allowlisted FQDN to a public IP and injects
// docker --add-host entries. Tool containers intentionally omit recursive DNS
// (--dns 127.0.0.1 with nothing listening breaks getaddrinfo); pinned hosts keep
// Composer/PHP resolving while CONNECT still goes through the egress gateway.
func pinAllowlistHosts(ctx context.Context, policy egress.Policy) ([]string, error) {
	seen := map[string]bool{}
	var args []string
	for _, rule := range policy.Rules {
		fqdn := strings.ToLower(strings.TrimSpace(rule.FQDN))
		if fqdn == "" || seen[fqdn] {
			continue
		}
		seen[fqdn] = true
		ip, err := egress.ResolvePinned(ctx, nil, fqdn)
		if err != nil {
			return nil, fmt.Errorf("pin allowlisted host %s: %w", fqdn, err)
		}
		args = append(args, "--add-host", fqdn+":"+ip.String())
	}
	return args, nil
}
