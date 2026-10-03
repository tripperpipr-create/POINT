# Execution sandbox

`embedded` mode uses a Point-owned WSL 2 guest with a pinned Moby/Podman pack and Linux volumes. It refuses Windows bind/live execution, checks engine/image/security identity, coordinates active executions across windows and terminates only its owned guest after ten idle minutes. Both engines passed the seven native parity scenarios on the current PC under profile v2. Since 2026-10-02 Point ships the Moby pack, and the extension selects `embedded` when the user made no explicit choice (`vscode-extension/sandbox-settings.js`): volume workspaces, a warm stage container and the download cache. Docker remains supported; a core started without these settings still defaults to `bind`. Reuse of passed checks (`POINT_VERIFY_SERVICE`) defaults to `on` for every backend since 2026-10-03. Provisioning, measurements and pending clean Windows/installed IDE acceptance are described in [embedded runtime implementation](implementation-embedded-runtime.md).

Current for Point `1.2.3` as of 2026-10-03. This document is the operational
contract for executable agent tools. File mutation mode and operating system
isolation are separate guarantees and must not be presented as the same thing.
Product rules for Orchestrator supervision, confirmed-only git remotes and
escalation of new egress hosts are in
[AGENT-HUB-ORCHESTRATOR-NETWORK-POLICY.md](AGENT-HUB-ORCHESTRATOR-NETWORK-POLICY.md)
(required; runtime v1 implemented, live URL-intake passes pending in TODO Q21).

## File mutation modes

**WorkOrder v2 and v2 Fast Agent.** The v2 Fast Agent requires a separate
execution workspace and chooses a detached worktree for a clean Git workspace
or a snapshot otherwise. A live sandbox is rejected before the run starts.
WorkOrder execution selects the isolation declared in its approved workspace
plan. The selected mode must be visible before launch.

**System Fast Agent (`host_live`).** The system Fast Agent profile runs an
approved Fast Agent task directly in the open project on the host: no
container, worktree or copy, and the engine never hands it the Docker
executor (`domain.HostLiveFastAgent`). It holds the project writer lease and
keeps ASK/DENY tool policy, but has the OS identity of the user. It is not a
sandboxed route and is never presented as one (threat model T23).

**Sandbox image of a WorkOrder.** Before approval a ready WorkOrder pins the
digest of the image its sandbox will get, together with a fingerprint of the
image requirements (`sandbox.imageDigest`, `sandbox.imageBasis`). Approval is
refused until the image is pinned. Every sandbox creation under that approval
compares the resolved image with the pinned digest; a rebuilt or replaced tag
pauses the quest with `ErrRuntimeImageChanged` instead of running under an
approval given to another image (`internal/app/work_order_image_pin_v2.go`).

**Legacy or explicitly selected live workspace.** Where live mutation is
enabled, file tools write the open project and an immutable baseline supplies
an exact Change Set/revert journal. Apply is a no-op if the live content
already matches the proposed hash. `POINT_LIVE_WORKSPACE=0` (or
`POINT_FILE_ISOLATION=sandbox`) selects isolated copies for those paths.

**Isolated copy.** `filtered-copy` / worktree create a writable execution copy
under a temp root. The open project stays unchanged until reviewed delivery.

## Backends

### Volume workspaces

The implemented 4a–4d behavior and Windows/Docker evidence are recorded in
[the volume acceptance report](perf/sandbox-volume-2026-10-01.md). Live 4e
quests are still pending. The embedded runtime always uses volumes; for Docker
it remains opt-in.

`POINT_SANDBOX_WORKSPACE=bind|volume` selects storage for a new isolated run;
without it the core uses `bind`. A Flow records this choice at launch and its
successors inherit it. Live workspaces and migrated records retain bind mounts
and `legacy-v1` file rules. New isolated copies use `portable-v2` in either mode.
Volume mode always creates a filtered host mirror, including for clean Git
projects; the original project's Git metadata stays on the host.

The mirror supplies file tools, search, IDE, Change Sets and three-way merging.
The independent named Linux volume supplies command execution, dependencies
and build outputs. The immutable baseline contains only portable files.
Host synchronization state lives in a sibling `.point-control` directory,
which is never mounted into the stage container.

One non-root, read-only-root container serves a stage through `docker exec`.
It mounts only its workspace, a read-only versioned helper volume, and the
existing permitted quest caches. CPU, memory and PID limits, private IPC/PID
namespaces, `cap-drop ALL` and `no-new-privileges` apply. The static
`point-sandboxd` binary is shipped separately for Linux amd64 and arm64, checked
by SHA-256 and pinned in the sandbox record. Execution images and their existing
attestations are unchanged. A compatible image needs no Point software;
shell commands still require `/bin/sh`, and direct commands require their program.
The only root setup process assigns ownership of a new empty volume, has no
network or project code, and retains only `CHOWN`.

An ordinary invocation applies host edits, runs the command, kills command
descendants, clears `/tmp`, scans portable Linux files once, and returns framed
stdout, stderr, result and delta in one exec. Command output cannot become a
control frame. PID 1 reaps orphans; reset signals the available PID namespace
excluding supervisor processes. Supervisors set `PR_SET_DUMPABLE=0`. See
[kill(2)](https://man7.org/linux/man-pages/man2/kill.2.html) and
[PR_SET_DUMPABLE](https://man7.org/linux/man-pages/man2/PR_SET_DUMPABLE.2const.html).
An allowlist gateway remains warm but receives a fresh policy and quotas for
each command over host-controlled Docker stdin. End closes its connections and
revokes the grant. Failure and timeout still collect the file delta.

Portable rules exclude dependency/build directories, secrets, links/junctions,
PNG/JPEG and `.exe/.dll/.so`. Package-root `vendor` is excluded, while source
assets below `src`, such as `src/styles/vendor`, are retained. All transfer,
snapshot and digest paths share the versioned rules. Digest includes canonical
relative `/` paths, directories and SHA-256 file bytes in deterministic order;
mtime is never trusted. Case/Unicode aliases and Windows-unrepresentable paths
fail with the offending names. Full dependency clones preserve internal links
without dereferencing them.

A single-parent writer receives an independent full clone for the same image.
An image change requires a clean portable copy and dependency installation.
Review, verification and Accept always receive a clean portable volume with
only verified download caches. Join retains the existing mirror merge and
Change Set lineage, then uploads the result into a new clean volume.

Every operation is durably recorded before dispatch. Received deltas are saved
before mirror application and can be replayed without repeating commands.
An unknown command outcome is never automatically executed again. Container
loss recreates execution resources; volume loss restores the mirror and marks
dependencies lost. Independent digests are checked before handoff, verification
and delivery. A mismatch retains a conflict copy, resynchronizes the mirror and
stops advancement. Interrupted or incomplete audit requires a new clean check
and an immutable human decision for the same sandbox and digest before delivery.
The historical limitation stays in the evidence bundle.

Resources carry core-owner, workspace, quest and sandbox labels. Close removes
execution containers, networks and workspace volumes. Startup collects proven
orphans belonging to its owner; a grace period protects creation before database
registration. Shared binary volumes are retained while referenced. Low Docker
filesystem capacity (under 10% or 5 GiB) produces a warning.

Build the helpers with `node scripts/build-sandboxd.mjs`; the regular core and
distribution builds include them. Set `POINT_VOLUME_INTEGRATION=1` and
`POINT_SANDBOXD_BINARY` to the matching binary to run the separate volume tests.
`go run ./cmd/point-sandbox-bench -project <path> -revision HEAD -command true`
compares fixed sources, image, resources and policy. Heavy commands default to
five clean runs; `true` uses twenty warm calls. Setup/preparation are reported
separately. The measured audit includes snapshots, patch diff and a durable JSON
journal, and excludes model calls and the app event bus. Switching the Docker
default to volumes requires the complete parity/isolation gates, Windows
performance targets, unchanged `pack-artifact` within 600 seconds and live
Node/Go quest delivery.

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
- package-manager caches of a quest live in named Docker volumes
  (`internal/sandbox/cache_volumes.go`) and survive commands and stages: npm
  and the Go module cache are verified against the lock file and `go.sum`, so
  every run of the quest gets them; the Go build cache and pip cache can be
  written into by an agent and are not verified, so only writer stages
  (bootstrap, implement, integrate) get them, and acceptance, review and runs
  without a stage role keep a throwaway tmpfs cache. The volumes are owned by
  the sandbox identity, labelled with the quest and removed at core start once
  the quest is closed or purged;
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
For a mid-run TLS decision, `run_command` recompiles the exact gateway policy
before each process. A one-command grant is reserved atomically for one Run and
one explicit destination; a Quest grant is versioned in the approved brief and
restored from the durable decision after a restart. The tool result records the
policy digest, explicit destinations and grant scope; the gateway records actual
connection decisions without URL paths or bodies. Before removing the gateway,
the runtime copies its bounded, policy-matched decision records into the tool
result; a read failure is recorded as `unavailable` rather than an empty log.

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

Sandbox copies live under `%TEMP%\point-sandboxes` (`os.TempDir()/point-sandboxes`) shared by every core on the machine. `POINT_SANDBOX_ROOT` replaces that root; the `internal/app` and `internal/httpapi` test binaries point it at their own temporary directory and remove it after the run. Before 2 October 2026 they used the shared root and left about a thousand copies per day of test runs (23.7 GB by then).

Build the versioned runtime image before starting Point Core:

```powershell
docker build --pull -f Dockerfile.sandbox -t point-agent-sandbox:1.2.2 .
docker build --pull -f Dockerfile.sandbox-node --build-arg NODE_MAJOR=20 -t point-agent-sandbox-node20:1.0.0 .
docker build --pull -f Dockerfile.sandbox-node --build-arg NODE_MAJOR=22 -t point-agent-sandbox-node22:1.0.0 .
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

`POINT_VERIFY_SERVICE` (`off`, `shadow`, `on` — the default) controls the
verification service described in [architecture.md](architecture.md): whether
Point checks the last writing stage against the acceptance criteria and whether
acceptance may reuse a run that fully passed on the same tree and image.

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
