# ADR: embedded execution engine selection

Status: accepted for the embedded engine choice, 2026-10-02. Moby is selected by the user's explicit decision. Performance qualification and a global default rollout remain open.

## Decision constraints

Windows 11 x64, WSL 2 permitted, no required Docker Desktop at runtime. Preserve Point's nonprivileged project processes, isolation, managed egress, resource limits, complete audit, recovery and trustworthy acceptance. Keep global `bind/shadow` defaults. Embedded execution uses Moby and is opt-in; the user's installed Point is configured to use it. No benchmark qualification is inferred from that choice.

## Selected settings

Use the pinned Moby 29.1.3 pack, Linux volumes, warm stage containers, verified download caches, 2 CPU, 2 GiB container memory, 256 PIDs and nonroot UID/GID 10001. Keep independent verification in shadow mode, complete audit and the ten-minute idle shutdown. Live Windows workspace mounts are disabled for embedded execution. These values preserve the measured configuration; increased quotas, global WSL tuning or automatic reuse are not justified by the current data.

Point settings expose `localAgent.sandboxBackend=embedded`, `embeddedRuntimeManifest`, `sandboxCPUs`, `sandboxMemoryGiB` and `verificationReuse`. Engine/manifest/resource settings are application-scoped and ignore project overrides. The bridge is resolved beside the selected core. Missing runtime readiness leaves embedded execution unavailable, without a host fallback. Podman transport/tests remain for historical comparisons and pinned-execution compatibility; the selected installed pack and application settings use only Moby.

## Candidates and current evidence

| Candidate | Transport | Current disposition |
| --- | --- | --- |
| Moby | Owned WSL 2 distro, pinned Linux engine, streaming CLI bridge | Selected; 29.1.3 installed on the user's PC without Docker Desktop; seven native parity scenarios passed on the shared Node/Go/PHP image |
| Podman | Owned WSL 2 distro, pinned Linux engine, streaming CLI bridge | Historical alternative; 4.9.3 passed all seven native parity scenarios on the shared image; not selected for delivery |
| WSL Containers | Microsoft.WSL.Containers session/container/process API | Excluded from the current security profile at the documented API gate; native execution not claimed |

Moby and Podman publish [Apache-2.0](https://github.com/moby/moby/blob/master/LICENSE) [licenses](https://github.com/containers/podman/blob/main/LICENSE). Distribution packaging must retain the applicable notices and audit all Linux dependency licenses. These project licenses alone do not certify an entire runtime pack.

The native three-project replay found that Moby's implicit tmpfs execution default rejected Go test binaries, while Podman permitted them. Point now explicitly specifies `exec,nosuid,nodev` on its bounded 512 MiB command tmpfs. Compilation remains inside the nonprivileged, resource-limited container; the root remains read-only and the supervisor still terminates descendants and clears temporary state. Profile `point-container-security-v2` invalidates earlier verification identities. Both engines passed the seven native parity scenarios again, including compiler execution and temporary-file reset; the v1 observations remain historical evidence.

The [official WSL Containers documentation](https://learn.microsoft.com/en-us/windows/wsl/wsl-container) requires WSL >=2.9.3; the PC has 2.3.24. The published [ContainerSettings API](https://wsl.dev/api-reference/csharp/settings-classes/containersettings/) does not expose per-container CPU/memory/PID limits, dropped capabilities, no-new-privileges or a read-only root. [SessionSettings](https://wsl.dev/api-reference/csharp/settings-classes/sessionsettings/) offers CPU and memory limits for an entire session. These documented controls do not establish equivalence to Point's profile. The integration decision is to exclude this API from the present comparison rather than substitute session limits or add a custom security engine. This is a public API assessment, not a live failure or a claim that equivalent protection is impossible. Reconsider when supported controls or a verified equivalent adapter are available. The [candidate assessment](../perf/runtime-security-candidates-2026-10-02.json) records this distinction.

## Measurements

The profile-v2 fixed-operation matrix completed 36/36 runs: three repetitions for each Node/TypeScript, Go and PHP cold/warm cell on both engines. Every completed run passed its commands, full-tree integrity and audit checks. The [retained observation summary](../perf/runtime-replay-2026-10-02.json) includes medians/ranges for time, CPU, peak/idle memory, IO, network and storage, with raw evidence hashes. Warm medians in seconds were Moby/Podman: Node 17.47/35.44, Go 37.57/55.97, PHP 14.39/33.52. Moby used more sampled peak memory in every warm project cell. These are fixed operations with no model or full WorkOrder delivery; they do not select the plan's winner or establish acceleration against Docker bind.

An additional [18-run Moby optimization pilot](../perf/runtime-ablation-2026-10-02.json) completed without command/audit failures. Disabling warm containers increased warm fixed-operation time in each project. A single trial per cell and one installation per replay do not establish download-cache contribution; reuse of acceptance evidence is absent. Full-model ablations remain required before asserting the target acceleration. The user chose Moby based on the local speed/memory tradeoff without waiting for that qualification.

Current Docker Desktop baseline preflight fails: Linux engine named pipe is unavailable. Whole-environment resource collection and the fixed Node/Go/PHP operation replay now run on this PC. No qualified full-model task matrix has been produced. The pinned route was initially unreachable, then became accessible and passed catalog preflight. The user subsequently requested no model-result validation; the isolated pilot was canceled, retaining its receipt and partial artifacts without independent quality proof or a completed sample. Existing Docker bind/volume microbenchmarks, local replays and one agreeing shadow run cannot select a full-task winner.

After an ordinary startup attempt, the user confirmed Docker's startup failure while removing `userAnalyticsOtlpHttp.sock`; the zero-length entry is a Windows reparse point. No reset or deletion of Docker state was attempted. A separate PC is unnecessary for embedded engine testing: owned guests and isolated projects on this PC are being used. A working Docker bind baseline is still required to qualify 30% acceleration. A fresh Windows VM/installation is needed only to establish clean-install/reboot acceptance; absence of it does not block source work or current-PC native quests.

Future performance qualification still requires three complete cold/warm quests per Node/TypeScript, Go and PHP project with alternating order, plus deterministic operation replay. Require >=30% aggregate warm acceleration relative to Docker bind, <=10% CPU/peak memory growth and <=10% per-project/cold slowdown with unchanged quality/audit/delivery. Aggregate relative time geometrically with equal project weights. Store raw evidence and exclusions before claiming these thresholds or approving a global default rollout. Model-result validation is currently reserved for the user.
