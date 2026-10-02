# Docker volume sandbox — evidence, 2026-10-01

Implementation 4a–4d is present in the working tree based on Point commit
`91ad15d47bcbde1f33cb47e5c818736261aff275`. Default remains `bind`.
Live Node/Go quests and the 4e default switch are deferred by the user.

## Fixed-input Windows benchmark

Docker Desktop engine 27.2.0, Linux amd64; identical source bytes, image,
resource limits, network policy and `portable-v2` file policy in both modes.
Limits are 2 CPUs, 2 GiB RAM and 256 processes.
Each installation/build sample starts from a clean portable copy. The npm
download cache is warm, while dependencies/build outputs are absent initially.
Before each verify sample, an unmeasured `npm ci` prepares dependencies.
The measured call includes SHA-256 snapshots, synchronization, patch diff and
an fsynced JSON audit journal; LLM and the application event bus are excluded.
The initial warm-up, workspace/container creation and preparation are separate
from command timing. File contents are hashed without trusting mtime.

CF revision: `2fad8ed804a28cc00d8250dbf973c1a5e09cfc68`.
Image `point-agent-sandbox-node20:1.0.0`, digest
`sha256:e275fe9b63da3b04b78fcc8b90036215971cc27cc7e648f87d3f79e741332874`.
Raw samples: [CF report](sandbox-cf-final-2026-10-01.json).

| Operation | Samples per mode | Bind | Volume | Target |
| --- | ---: | ---: | ---: | ---: |
| `npm ci`, median | 5 | 24.123 s | 3.862 s | ≤12 s |
| `npm run verify`, median | 5 | 301.135 s | 118.928 s | ≤120 s |
| warm `true`, p95 | 20 | 4.547 s | 0.285 s | ≤1 s |

All samples exit 0. Volume verify samples are 115.325, 118.928, 121.025,
119.402 and 118.163 seconds. The required median passes; the slowest sample
exceeds 120 seconds. These measurements do not establish a p95 bound for
the complete application event/approval path on a large project.

CF workspace creation medians are 17.574–21.913 s for bind and
29.606–35.372 s for volume, depending on the command group. These include
host portable mirror/baseline copies and initial transfer; volume setup is
slower and must be amortized over commands. Verify preparation medians are
29.670 s for bind and 7.146 s for volume.

Point frozen at the above Point commit, image `point-agent-sandbox:1.2.2`
(`sha256:cdbf49d1d93902496539b3054c3f02b565f8690972229c0fd221f9e17cb77add`):
20 warm `true` samples each; bind median/p95 0.914/0.965 s, volume
0.207/0.220 s. Setup was 30.617/43.495 s respectively.
[Raw Point report](sandbox-point-fixed-2026-10-01.json).

The historical user baseline (30 September: npm ci 40 s, verify about 349 s,
true at least 4.5 s) is context, not the denominator of this controlled
comparison. [Existing point-perf-report before](baseline-2026-10-01.json)
covers ten production Flow runs; there is no after-live report yet.

## Original pack-artifact

The verification criterion was read from the existing Point WorkOrder/quest
data. It requires the nested `cf-vue-apps` directory and this exact command:

```sh
cd cf-vue-apps && npm run verify && npm pack && tar -tzf cf-cf-vue-apps-*.tgz | grep -q '^package/dist/embed/' && rm -f cf-cf-vue-apps-*.tgz
```

The benchmark supports `-source-subdir cf-vue-apps` to reproduce that directory
without editing the command. This is a deterministic command check, separate
from a live model quest.

Result: exit 0 in **191.021 s**, below the unchanged 600 s limit.
Clean workspace setup was measured separately, as was the preparatory npm ci.
[Exact command, timings and resulting digest](sandbox-cf-pack-artifact-2026-10-01.json).

## Source and Docker checks

Source checks: focused package tests, `go vet ./...`, `go mod verify`,
staticcheck v0.8.1, frontend build, current core and static Linux amd64/arm64
helper builds pass. The complete extension check sequence passes when the
separately compared release-contracts step is omitted, including 85/85 Hub
smokes. This does not claim an unmodified `make test` pass.

The final full Windows Go run reports three failures in `internal/app`:
`TestMasterChatAnswersQuestionsWithoutCreatingWork`,
`TestMasterWorksWithAnEmptyRoster` (SQLite file held during TempDir cleanup),
and `TestQuestPlanningStopsWithTheHTTPContext` (cancelled-request wait and
SQLite cleanup). Baseline repetitions reproduce the same cleanup/cancellation
causes. The initial full run also encountered the pre-existing SQLite cleanup
flake in `TestRetryFailedStageContinuesTheSameFlow`; 20 baseline repetitions
fail cleanup 4 times, current repetitions 7 times, without logical assertion
failures in that test. These failures remain unresolved and are not hidden.

Release-contracts has exactly the same five red items as the initial commit:
21 expected threats / 22 found; pre-existing attested test drift; pre-existing
go.mod provenance drift; diff-view 112 lines / budget 60; master-inbox
196 lines / budget 191. Images, image attestations, go.mod and the existing
attested integration test were not changed by this implementation.

Docker tests pass for full inheritance with internal symlinks and an independent
parent, clean Join, deny-all/allowlist/quota/grant reset, read-only non-root
execution, PID 1 and protected descriptors, detached process cleanup, timeout
delta, custom process cwd, lost-volume recovery and owner-scoped orphan cleanup.
The existing `TestDockerSandboxIntegration` also passes.

The stub-model parity test compares exact portable bytes, patch bodies/diffs,
workspace audit events and significant run_command/custom-tool results.
It exercises creation, edit, delete, rename, CRLF, Unicode, binary and 1 MiB
files, exit 7 and forged result-looking stderr. Only explicit IDs/timestamps
and timing fields are removed from comparison. Timeout, inheritance and Join
are additionally exercised by separate Docker tests.

The real Docker Desktop daemon interruption test passes (219.84 s): dispatch
occurs once, the unknown-outcome command is not replayed, repeated recovery
keeps the same digest and no duplicated changes, and the audit remains
incomplete pending a fresh clean check and a digest-bound manual decision.
All 14 previously running user containers were restored after the restart.
GC tests preserve foreign resources, live sandboxes and recent creation grace.

Local raw logs are retained under build: `volume-go-final.jsonl`,
`volume-three-current.log`, `volume-baseline-91ad15d/volume-three-baseline.log`,
`volume-retry-current.log`, `volume-retry-baseline.log`,
`volume-extension-full.log`, `volume-docker-acceptance.log`,
`volume-parity-final.log`, `volume-daemon.log`, and `volume-pack-artifact.log`.
They are distinct from the tracked machine-readable benchmark reports.
[Validation ledger](sandbox-validation-2026-10-01.json) records source failures,
Docker passes, helper hashes and SHA-256 hashes of these local logs separately.

The early [vendor-policy failure](sandbox-cf-verify-failure-2026-10-01.json)
is retained as a diagnostic: blanket vendor exclusion removed
src/styles/vendor/suggestions.min.pcss. The agreed source-resource exception
fixes it; the [single exploratory verify](sandbox-cf-verify-2026-10-01.json)
is not used in the five-sample aggregates.

## Remaining acceptance

Two live model quests from request through verified delivery, their after
point-perf-report, actual Linux/macOS compatibility runs and live IDE acceptance
remain unconfirmed. The live harness is opt-in and has not run. No default
switch or release is claimed. The 4e switch is allowed only after its live gates.
