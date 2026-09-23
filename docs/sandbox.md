# Execution sandbox

Current for Point `1.2.3` as of 2026-09-23. This document is the operational
contract for executable agent tools. File mutation mode and operating system
isolation are separate guarantees and must not be presented as the same thing.
Product rules for Orchestrator supervision, confirmed-only git remotes and
escalation of new egress hosts are in
[AGENT-HUB-ORCHESTRATOR-NETWORK-POLICY.md](AGENT-HUB-ORCHESTRATOR-NETWORK-POLICY.md)
(required; runtime still catching up).

## File mutation modes

**WorkOrder v2 and v2 Fast Agent.** The v2 Fast Agent requires a separate
execution workspace and chooses a detached worktree for a clean Git workspace
or a snapshot otherwise. A live sandbox is rejected before the run starts.
WorkOrder execution selects the isolation declared in its approved workspace
plan. The selected mode must be visible before launch.

**Legacy or explicitly selected live workspace.** Where live mutation is
enabled, file tools write the open project and an immutable baseline supplies
an exact Change Set/revert journal. Apply is a no-op if the live content
already matches the proposed hash. `POINT_LIVE_WORKSPACE=0` (or
`POINT_FILE_ISOLATION=sandbox`) selects isolated copies for those paths.

**Isolated copy.** `filtered-copy` / worktree create a writable execution copy
under a temp root. The open project stays unchanged until reviewed delivery.

## Backends

`filtered-copy` is the compatibility backend for isolated copies. It creates an
immutable baseline plus a writable execution copy (or a detached Git worktree)
and filters secrets and symlinks. It does **not** isolate the process identity
or kernel and does not enforce network egress. Its API capabilities therefore
report `processIsolation=false`, `networkIsolation=false` and
`strongOsBoundary=false`.

`docker` isolates **commands and network**. With live file mutation it bind-mounts
the open project (or the isolated sandbox path when file isolation is on). The
minimal glibc-based Wolfi base in `Dockerfile.sandbox` is pinned by registry
SHA-256 digest, and every direct Node 24, Go 1.26, compiler, Python, Git and
utility package is version-pinned. A moved tag or package upgrade therefore
cannot silently change a release image; an unavailable pin fails the build.
The runtime container contract is:

- the execution root (live workspace or isolated sandbox) is mounted at `/workspace`, read-write;
- the image root is read-only and temporary files use a bounded tmpfs;
- all Linux capabilities are dropped, privilege escalation is disabled, IPC is
  private and the process count, memory and CPU are bounded;
- the process runs as a non-root numeric identity;
- host provider keys, tokens, credentials, home paths and toolchain caches are
  not inherited; only a small explicit environment allowlist crosses the
  boundary;
- Docker uses `--pull=never`; both daemon version and the exact local image ID
  are probed at startup and persisted on each sandbox record. Every later tool
  and gateway container is launched by that exact `sha256:` identity rather
  than the mutable configured tag;
- deny-all networking uses `--network=none`;
- a non-empty allowlist creates a unique internal Docker bridge for that one
  process. The tool container is attached only to that bridge. A separate
  non-root, read-only, capability-free gateway container is attached to the
  internal bridge and the Docker outbound bridge; the tool has no direct route
  to the latter. The tool receives only a validated `/etc/hosts` entry for the
  gateway and an unusable loopback DNS setting, so arbitrary Docker-DNS lookups
  are not a side channel;
- the gateway accepts only HTTP `CONNECT` carrying TLS. Rules are exact
  `tls://FQDN:port` values (a bare FQDN means port 443); IP literals, wildcards,
  user info, paths and non-TLS schemes are rejected at profile save and again
  before process creation;
- every DNS answer must be public. Mixed public/private, loopback, link-local,
  documentation and special ranges fail closed. The gateway then dials the
  selected IP directly and requires ClientHello SNI to equal the declared FQDN,
  preventing a second resolution or hostname/SNI substitution;
- the default per-process gateway quota is 32 connection attempts, 256 MiB in
  both directions combined and 15 minutes. Decisions log only run ID, policy
  digest, FQDN, port, protocol, decision, reason, bytes and duration—never URL
  paths, headers or bodies;
- the gateway container and its internal network are removed on normal exit,
  command failure, timeout and cancellation. Partial setup is rolled back.

Command-string detection remains defense in depth for `filtered-copy`. With the
strong Docker backend, package managers with implicit registries reach the
gateway, which enforces the same exact rule independently of child behavior.
The model cannot expand a running policy: profile changes are human control-plane
actions and only a new run receives the new immutable schema-v3 snapshot and
policy digest. Unrestricted `ALLOW` always fails closed.

Purpose-built SSH and database tools do not execute arbitrary child programs;
they retain their own explicit approval, destination and read/write policy.
Their threat model is separate from the process sandbox.

## Execution-path contract

Every headless execution path stages files before an executable tool can run:

- ordinary Agent and graph Flow executions own an immutable baseline and a
  writable sandbox, then expose edits as a Change Set;
- a legacy sequential Workflow shares one isolated snapshot across its ordered
  child stages, so handoffs observe preceding stage edits without publishing
  them; its terminal diff is one reviewable Change Set;
- Hub `execute_readonly` creates a disposable filtered snapshot, gives that
  path (never the live project path) to the configured process backend, and
  deletes it after the command. Writes are intentionally discarded;
- deterministic graph Tool nodes already reject command/custom-process tools
  because those tools require approval and are not low risk.

The external interactive Cursor Agent terminal is not a headless Point
execution and is therefore not inside this Docker boundary. It retains
Cursor's own confirmation flow and can act on the open project. Production
operators requiring the Point strong boundary must use headless Agent/Flow or
legacy Workflow runs for executable automation; the UI must not label an
interactive Cursor session as sandboxed by Point.

## Production configuration

Build the versioned runtime image before starting Point Core:

```powershell
docker build --pull -f Dockerfile.sandbox -t point-agent-sandbox:1.2.2 .
```

PHP/Composer Agent Hub vertical:

```powershell
docker build --pull -f Dockerfile.sandbox-php -t point-agent-sandbox-php:1.3.1 .
```

Set the following environment for a production core process:

```text
POINT_SANDBOX_BACKEND=docker
POINT_SANDBOX_REQUIRE_STRONG=true
POINT_SANDBOX_IMAGE=point-agent-sandbox:1.2.2
POINT_SANDBOX_MEMORY=2g
POINT_SANDBOX_CPUS=2
POINT_SANDBOX_PIDS=256
```

`POINT_SANDBOX_REQUIRE_STRONG=true` is the downgrade guard. If the backend is
missing, Docker is unavailable, the image is absent, the container user is
root, a resource limit is invalid or the image cannot be attributed, Point
Core refuses to start. It does not fall back to host execution.

`POINT_SANDBOX_USER` may override the numeric container identity for bind-mount
permissions. Root identities are rejected. On Linux the default is the current
non-root UID/GID; on Windows Docker Desktop the image identity `10001:10001` is
used.

## Verification

The unit contract checks the exact Docker arguments, single bind mount,
resource controls, environment filtering, path containment, version
attribution, fail-closed network behavior, disposable Hub execution and legacy
Workflow staging. CI additionally builds the image and runs
`TestDockerSandboxIntegration`, which proves that all pinned toolchain binaries
are available through the login-shell PATH and that an executable tool can write
its sandbox, runs as UID/GID 10001 with a read-only root mount, zero effective
Linux capabilities, `NoNewPrivs=1`, an isolated PID namespace and no host-secret
environment, and receives the exact memory/CPU/PID cgroup limits. It also proves
that the tool cannot see an arbitrary host path or establish an outbound socket
with deny-all egress. The live gate also proves a positive exact TLS destination
and rejects an unlisted FQDN, plain HTTP and a direct-IP bypass. Unit tests cover
mixed/private DNS answers, pinned dialing, literal/wildcard rejection, exact
port/protocol matching, SNI mismatch and connection/byte quotas. Both cgroup v1
and v2 layouts are accepted; the numeric limits must match exactly.

The Docker integration gate is intentionally not skipped in the CI `sandbox`
job. A local developer without a Docker daemon can run the remaining suite; the
integration test activates only with `POINT_SANDBOX_DOCKER_TEST=1`.

## Managed language packs

Attested production base remains `Dockerfile.sandbox` → `point-agent-sandbox:1.2.2`
(no PHP). Before an execution sandbox is created, Point derives required system
commands from the approved stack, setup plan and completion profile. Only names
from the built-in toolchain catalog are accepted; model-authored package names,
images and Dockerfile instructions are never executed.

The resolver probes the base image and compatible local packs first. If commands
are still missing, it builds a non-root derived image from the base image ID and
the catalog's version-pinned package set. The tag is content-addressed from the
base digest, toolchain version, commands and packages (`point-runtime:<digest>`),
the resulting image is probed, attributed by immutable image ID and reused by
later runs. Builds are serialized so concurrent quests cannot duplicate the same
provisioning work. Execution still uses `--pull=never`, a read-only root and the
normal sandbox limits.

Provisioning uses Docker's configured package repository network during the
build; it is a host-side maintenance operation with a fixed package allowlist.
The per-command controlled-egress policy applies to execution containers and
project package installation, not to the Docker build. Operators who prohibit
host-side package downloads should prebuild the required packs locally.

PHP 8.3 + Composer also remain available as the compatible prebuilt image
`Dockerfile.sandbox-php` → `point-agent-sandbox-php:1.3.1`; when absent, the same
pack can be produced automatically from the catalog. The resolved image is
persisted on the sandbox record and is used consistently by deterministic
bootstrap commands, agent tools and checkpoint resume.

System toolchain provisioning and project dependency installation are separate:
the former creates/reuses the derived image, while approved setup commands such
as `composer install`, `npm install` or `pip install` write only to the execution
workspace. Package-registry hosts required by project setup still need an
explicit controlled-egress allowlist; missing DNS/egress must escalate per
[AGENT-HUB-ORCHESTRATOR-NETWORK-POLICY.md](AGENT-HUB-ORCHESTRATOR-NETWORK-POLICY.md)
instead of silent mirror hopping.
