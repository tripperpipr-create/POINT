# Architecture

Architecture inventory for Point `1.2.3`, reviewed on 2026-09-23. Runtime code
and tests remain the source of truth; see [README.md](README.md) for document
ownership and [PRODUCT-VISION.md](PRODUCT-VISION.md) for the target architecture.
Sections that describe earlier run paths are historical until revalidated by a
live end-to-end test. In particular, file isolation and process isolation must
be evaluated separately; the v2 Fast Agent uses an isolated execution copy.

## Runtime surfaces

The repository produces three runtime surfaces from one use-case layer:

1. `vscode-extension` contributes the full-page Point Agent Hub, walkthrough and commands to the Point workbench. Its Activity Bar entry is only a lightweight launcher. It starts a private `cmd/server` (`point-core`) child process for the first local workspace folder.
2. The root Wails binary embeds `frontend/dist` and injects a native directory picker plus Wails event sink. This is the official diagnostic client, not a product surface or a feature-parity copy of the Code-OSS Hub. New Agent Hub workflows target Code-OSS only unless an architecture decision explicitly changes that status.
3. `cmd/server` exposes REST and Server-Sent Events for the Docker web client. It restricts all opened workspaces to `WORKSPACE_ROOT`.

Point is Hub-first. A bare launch opens only the dedicated sessions window that hosts the Agent Hub; the previous session is not restored, and an IDE workbench window opens on demand or when an explicit path, folder/file URI or `point://` link is given. With no project bound, the Hub renders a project gallery served entirely by the extension host (shell recents merged with a `point.projects.v1` record in `globalState`), so the first screen needs no core at all. Switching projects rebinds the Hub's workspace folder in place — the sessions window is opened from a saved `.code-workspace`, so no reload occurs — and the outgoing core is detached rather than stopped, leaving at most two unreferenced warm cores for instant return. Cores leased by open IDE windows are never reaped. See [ide-workspace-controls.md](ide-workspace-controls.md).

No transport owns agent behavior, policy decisions, storage semantics, or filesystem safety. VS Code remains responsible for editing, tabs, Explorer, terminals and Workspace Trust; the extension owns only the agent-specific integration and point-core lifecycle.

## Module boundaries

The central entry files remain compatibility composition roots, but bounded
behavior no longer has to be edited inside them:

- `internal/app/app.go` is a composition root; `run_execution.go`,
  `run_views.go`, `workspace_surface.go`, `profiles.go`, `memory.go`,
  `usage_statistics.go`, `custom_tools.go`, `legacy_workflows.go` and
  `companion_hub.go` own their named use-case seams;
- storage Hub persistence is split by domain into `hub_agents.go`,
  `hub_changesets.go`, `hub_companion.go`, `hub_flow_runs.go`,
  `hub_orchestration.go`, `hub_servers.go` and `hub_state.go`;
- `internal/companion/service.go` coordinates bounded modules for model chat,
  deterministic chat/replies, proposal parsing, Quest proposals and usage
  analysis, alongside focus, interventions and configuration;
- `vscode-extension/extension.js` is the host composition root. Named families
  of `AgentViewProvider` methods live in their own controllers and are reached
  through one-line forwarders, so the class keeps its public surface while the
  bodies move out: `git-tool-controller.js` (repository reads, change lists,
  stash, push targets, tool-window snapshot), `hub-surfaces-controller.js`
  (which window to open and where to return the user), `hub-polling-controller.js`
  (the three run/workflow/flow timers and the "hidden means no polling" rule),
  `companion-thread-controller.js` (the life of one companion reply),
  `core-log.js` and `core-lease.js` (chronicle, and the warm-core lease the
  multi-window model rests on);
- host message groups are dispatched by family through
  `handleXxxMessage.call(this, message)`: `master-chat-controller.js`,
  `infra-controller.js`, `hub-runtime-controller.js`, `roster-controller.js`,
  `learning-controller.js`, `tooling-controller.js`, `cursor-controller.js`,
  `companion-chat-controller.js`. Stateful surfaces use closure factories
  instead: `companion-controller.js`, `ide-action-controller.js`,
  `ide-navigation-controller.js`, `project-index-controller.js`,
  `console-ssh-controller.js`, `point-panels.js`, `ide-observation-controller.js`;
- `vscode-extension/extension-utils.js`, `run-config-utils.js`,
  `ide-navigation-utils.js` and `ssh-utils.js` contain pure host guards,
  run discovery, fallback navigation/search and remote-path validation;
- `vscode-extension/ui/client` is bundled from source modules by esbuild into
  the single `media/main.js`. Screens render from `*-views.js` modules; clicks
  are dispatched by family through `handleXxxClickAction(...) -> boolean`
  (`companion-actions.js`, `onboarding-actions.js`, `git-actions.js`,
  `hub-actions.js`, `infra-actions.js`, `master-actions.js`, `run-actions.js`,
  `flow-actions.js`, `roster-actions.js`); incoming core messages by
  `*-inbox.js`; and every form on every surface by `form-submit.js`. Shared
  pure helpers have one home each — `html-escape.js` for escaping and
  `format-units.js` for units, plurals and truncation — because each of them
  had already drifted into copies once.

`scripts/check-release-contracts.mjs` verifies these source boundaries and the
extension gate executes their behavior contracts, including the chat-markup
XSS and bundled Git workflow smokes. It also enforces upper line-count budgets
as a ratchet on 54 named files, so extracted behavior cannot silently grow
back into them: a budget may fall, never rise, and a split that does not
lower the ceiling in the same change has not been made. Extraction stays behavior-preserving: public
routes, message names and the generated `media/main.js` protocol remain
unchanged.

## Agent Studio and run context

The backend publishes Blueprint templates, project-scoped ProjectAgents, tool capabilities and provider presets through bootstrap. All clients therefore render the same roles, risk labels and allowlist values, while prompt compilation, validation and actual tool authorization remain server-side. The full-page Code-OSS Agent Hub is the canonical Agent Hub client. It supports explicit Blueprint-to-project instantiation, editing and provider selection without persisting provider secrets. A ProjectAgent includes goals, raw instructions, rules, model tuning, context/output limits, skills and explicit tool permissions; the backend deterministically compiles the layered runtime prompt. Cursor Agent CLI is deliberately launched in an interactive terminal and remains outside the local headless engine.

The v2 first-run onboarding has two steps: Master connection (`orchestrator-brain`) and Master policy (`orchestrator-choose`). The built-in deterministic engine makes an external connection optional. Companion, permanent agents, skills, permissions and the workspace index remain available after onboarding and are not first-run gates; missing agents are proposed in the WorkOrder approval card. Completion is stored globally under `point.agentHubV2.onboardingComplete`, so switching projects does not repeat setup; the legacy workspace flag is ignored. Completing setup, opening the Hub and starting the Core all land on the Master chat rather than the overview. Setup can be restarted from the Agent Hub. Companion never starts a Quest; the Master assigns the roster only after WorkOrder approval.

The custom-tool builder stores two capability types in `custom_tools`. The compatibility `command` kind is a fixed shell command. The preferred `process` kind stores a program, an argv template and up to 16 typed model parameters (`string`, `integer`, `enum` or `workspace_path`). Placeholders expand within one argv element and execution uses the OS process API directly without an implicit shell. The registry derives a strict JSON schema, rejects unknown/invalid arguments before creating an approval and re-resolves workspace paths at invocation time. The approval preview shows the effective program, each argv element, working directory, typed values and reason. The builder's read-only sandbox calls that same preparation path for an unsaved definition and sample arguments, then returns the schema/effective invocation without executing or persisting anything. Presets and versioned JSON import/export make definitions reusable. A definition may explicitly set `providesVerification`; if that tool is also in the profile allowlist, its exit-zero/non-timeout result can satisfy the completion gate and is preserved in the immutable run snapshot. Custom tool IDs participate in the same profile allowlist as built-ins, but policy always requires approval regardless of the profile's normal safe-tool mode.

Run context is explicit rather than inferred. The extension can attach workspace files or an editor selection. `internal/attachments` resolves every path through the workspace boundary, detects its type and creates a bounded immutable snapshot. JSON/JSONL, CSV/TSV, DOCX, XLSX and text are normalized locally; text-bearing PDF files are extracted without an external service; PNG/JPEG/GIF/WebP are validated and delivered through the provider's native multimodal message shape. Extracted text is redacted, while every item records the source size, media type and SHA-256 digest. Before launch the same pipeline returns a public preview with actual text/image byte counts, warnings and a conservative text-token estimate. Raw image payloads are persisted for reproducibility but omitted from API history and preview responses.

The same boundary remains available during execution. From Context Inspector the user can add a workspace file or the current editor selection without restarting the run. The extension sends references through a dedicated endpoint; the backend resolves them into immutable snapshots and atomically validates the combined existing/queued context. Added items are queued on the active engine and visibly marked pending. At the next safe checkpoint the engine applies them, rebuilds the stable model prefix, persists the run and appends a `context.amended` audit event. A paused run keeps the addition queued until resume. Pin, unpin and remove use the same checkpoint queue.

`internal/workspace` also maintains a per-workspace in-memory lexical/symbol/dependency index. Updates are copy-on-write: `UpdateIndex` / `SearchContext` never mutate the live maps while another reader holds them. Empty or truncated SHA-256 values are treated as stale so a partial read cannot pin a file forever. It chunks safe source files without normalizing line endings, expands camelCase/PascalCase/snake_case identifiers, records lightweight declarations, bounded import specifications and the whole-file SHA-256, and exposes `project_map` plus ranked `search_code` tools. Retrieval weights rare exact terms, exact declarations and paths above partial matches; a relevance-aware per-file penalty improves coverage without displacing a much stronger second match. Overlapping ranges are removed. Results are bounded by chunk and character budgets and report matched terms, candidate/returned counts, actual character usage and truncation so an agent can refine incomplete retrieval instead of sending the whole repository. Paths with a `..` segment are rejected; filenames like `notes..md` stay allowed.

The dependency layer resolves relative TypeScript/JavaScript imports, Go module/package paths, Python modules, Rust `use` paths and Java/Kotlin/C# imports against lookup tables built once per index. It records outgoing `imports`, reverse `imported_by`, `test` and `tests` relationships. Test filenames are linked only when their production target is unambiguous. A request may return at most 20 deterministic relationship records containing only path, relation, import spec and whole-file digest; source content is never included and does not become edit evidence. Import extraction is capped at 256 unique specs per file and each spec resolves to at most eight local targets. Before returning results, Point compares indexed file metadata plus the selected chunk/relationship digests; external creates, deletes and edits trigger a rebuild even when an edit preserves size and modification time. Any managed file write, accepted patch, detected executable-tool mutation or rollback also invalidates the index.

At run creation the application constructs schema-version-2 `RunConfigurationSnapshot` (v1 remains accepted by the engine). Execution workspace selection is route-specific: WorkOrder v2 and the v2 Fast Agent use an isolated execution copy, while legacy and opt-in live-write paths retain their own contracts. The UI must show the selected mode; a Docker process boundary does not imply staged file writes. Every direct launch also creates a first-class Quest, while Flow launches reuse their existing Quest and Execution. SQLite inserts the configuration snapshot with the run and never updates it during status transitions. This makes execution history independent of mutable agent/tool catalogs while keeping API keys outside durable state.

Graph Flows are validated before persistence and again before execution: node IDs and endpoints must be valid, the graph must be reachable, ordinary cycles are rejected, and Agent/Tool references must be explicit. Tool nodes bypass the model and invoke only the deterministic low-risk allowlist in their own execution sandbox. Loop nodes use an explicit `maxIterations` limit (1-20), one `continue` edge, one `done` edge, one entry edge, and one back edge; runtime state records bounded iteration results and attempts.

Before creation, `PreviewAgentRun` can prepare the exact same profile, system safety suffix, registry definitions and typed context without constructing a provider or writing state. It also describes the exact completion policy: explicit verification, post-change verification, tool availability, accepted evidence classes and the bounded correction count. An explicit criterion without `run_command` is surfaced as a blocking configuration issue before launch. `StartRun` recomputes that policy and rejects the impossible configuration before run/event persistence or provider construction, even when a client bypasses the preview. The preview returns a canonical SHA-256 fingerprint over the application version, workspace, task, profile, sorted tool definitions and attachment digests. `StartRun` also recomputes the fingerprint from a fresh read and rejects a supplied stale value, then starts from the already prepared immutable values. This closes the time-of-check/time-of-use gap between the IDE preview and the model request.

## VS Code companion lifecycle

The extension requires a trusted local `file:` workspace, reserves a free loopback port, creates a private global-storage directory and spawns the bundled Windows server with an explicit workspace boundary. It waits for `/api/health` before enabling agent actions, streams service logs to a dedicated Output channel and terminates the child process when the extension is disposed or the workspace changes.

The Statistics surface obtains `/api/system/diagnostics` only when opened or explicitly refreshed. The report derives `READY`, `DEGRADED`, or `BLOCKED` from named Core, SQLite/migration, disk, sandbox, workspace, port, backup, and provider/model-configuration checks. `StartRun` recomputes the same lifecycle and rejects every `BLOCKED` launch with the source reasons and safe next actions; `DEGRADED` remains available for safe limited work. The report exposes source metrics and does not receive API keys or file contents. Live provider checks remain an explicit IDE action backed by SecretStorage.

IDE diagnostics and failed terminal/Task outcomes are captured as bounded, redacted, workspace-scoped observations. Deterministic Companion interventions link back to a concrete observation and may carry a `companion_prompt` action. That action only submits a visible user message to the ordinary Companion chat. It can produce a pending, grounded Quest proposal, but cannot create or start an execution. The separate proposal decision creates the Quest; the Orchestrator system agent (configured independently from Companion, including its own model) assigns the party and starts Flow through the normal orchestration path.

## Sequential agent workflows

`internal/workflows.Manager` coordinates bounded legacy workflow definitions without weakening the single-agent engine. One workflow owns a filtered staging snapshot shared by its ordered stages; each stage starts an ordinary child run against that snapshot with its own immutable profile/custom-tool snapshot and audit events. The manager waits for that child to reach a terminal state, then optionally converts its redacted result into a bounded untrusted context attachment for the next stage. Any failure stops the chain; cancellation propagates to the currently active child and leaves later stages pending. At terminal state the snapshot is diffed against its immutable baseline and only a Change Set can publish it to the live workspace.

The parent `WorkflowRun` stores its own immutable workflow snapshot, stage order and child run IDs. This makes the high-level flow reproducible while every model/tool decision remains inspectable through the existing run history. The VS Code builder supports ordering, profile selection, per-stage instructions, handoff switches, transient per-profile API keys and a live timeline.

Graph Flow execution is persisted independently of webview visibility. Point Core advances deterministic nodes and schedules sandboxed execution records; the IDE supplies cloud credentials from SecretStorage only when a ready execution launches. A serialized background coordinator watches all active FlowRuns while executions are running, so closing Hub does not strand the next agent. On restart, Core marks stale running executions interrupted and restores graph state; the IDE then relaunches only pending/interrupted executions belonging to an already active Flow. Approval nodes and executions without an available credential stay pending.

Every execution now owns an immutable baseline captured before work begins, including the first node and clean Git worktree runs. A user edit made while an agent is running therefore cannot change the meaning of the eventual Change Set. When a node has exactly one completed upstream execution, Point creates the next execution from that predecessor's sandbox. The next agent reads the exact files produced by the previous agent, while its Change Set contains only its own incremental edits. The new Change Set records `dependsOn` lineage to the most recent persisted predecessor set. Core enforces dependency-first Apply and dependent-first Reject/Revert; the IDE can apply the complete chain in topological order. A clean Git repository may use a detached worktree for the first execution, while a dirty or untracked working tree deliberately uses a filtered copy so current local files are not lost. Root branches in one FlowRun fork from one shared immutable start snapshot even if parallelism limits schedule them later.

When an agent node completes, its sandbox is diffed against that immutable baseline before the graph advances. Downstream context also includes a structured handoff and a bounded redacted projection of matching persisted Change Sets: execution/change-set identity, status, dependency IDs, file paths, operations and unified diffs. Exact snapshots remain server-only and live-workspace mutation still requires explicit Change Set Apply.

After a multi-parent Join, Point identifies the nearest common immutable ancestor and performs a deterministic three-way sandbox merge. Changes to different files, byte-identical outcomes and non-overlapping line edits in one text file merge automatically. Divergent edits to the same base range, modify/delete and incompatible create outcomes stop the successor before an execution exists. The Flow state persists only path, operation/hash and candidate execution IDs; exact content stays server-side. The IDE shows the conflicted files and accepts an explicit branch choice, deletion or bounded manual result for each path. Once resolved, Point builds one synthetic `kind=merge` Change Set, marks branch-local sets `superseded`, and seeds the successor from the merged snapshot. The aggregate set retains only the shared pre-fork dependencies, so each branch edit is published exactly once and remains atomically reviewable/revertible.

## Master-first v2 contract

The v2 path replaces "a proposal plus whatever the Flow did" with one reviewed contract and one proof of completion. Its stages are:

1. **Sources.** `/api/v2/sources/preview` turns text, PNG/JPEG/WebP images, files, public HTTPS pages and Git repositories into immutable `SourceSnapshot` records addressed by digest. Refresh creates a new snapshot and a bounded requirements diff; an active WorkOrder keeps the digest it was approved against.
2. **Draft.** A Master turn drafts a `WorkOrder`. Server-side normalization owns the result: approval fields, workspace isolation and routing authority come from trusted local state, never from model output.
3. **Approval.** `/api/v2/work-orders/{id}/approve` is atomic on an exact version and digest. It materializes the roster and workspace, compiles a project Flow and starts it. A missing runtime credential or an interactive CLI session yields `awaiting_user`; preflight failure yields `blocked` with a redacted reason. Execution state lives in the derived `runtime` object and is excluded from the approval digest, so a running quest never mutates the approved contract.
4. **Execution.** Ordinary Flow, sandbox and Change Set machinery runs underneath — v2 is a contract and acceptance layer over the existing execution engine, not a second engine. A durable `WriterLease` keeps a single writer per workspace across pause and process restart.
5. **Acceptance.** The v2 terminal callback cannot reach the legacy completion path. It persists an `EvidenceBundle` and the evidence gate alone may produce `completed` or `needs_review`. The bundle must be version 3 (`CurrentWorkOrderEvidenceVersion` in
`internal/domain/evidence_gate_v2.go`), carry the Point version, match the work order and source-snapshot digests and include environment and versioned stack-preset evidence. Anything else is `blocked`.
6. **Delivery.** A verified result produces a `DeliveryReceipt` naming the target and workspace revision. `applyMode=automatic` transfers the result; `applyMode=manual` keeps it in `isolated_review` untouched. `commitMode=squash` creates exactly one commit from an explicit changed-file list and refuses unrelated workspace drift. Starting or stopping a delivered Docker Compose application requires a matching version, digest, receipt and idempotency key.

Milestones stay coarse on purpose: a detailed Flow is compiled only when a milestone becomes current, so replanning cannot silently widen the approved product scope. The legacy quest-proposal, Change Set, workflow and flow endpoints remain available as compatibility adapters alongside this path.

## Domain and execution

`domain` defines profiles, runs, immutable events, approvals, patches, typed tool definitions and structured tool results. `agent.Engine` owns active run contexts. Each run:

1. persists `run.started`;
2. streams a provider response;
3. records text, usage and tool calls;
4. validates the requested tool against the profile allowlist;
5. evaluates policy and waits when approval is required;
6. returns structured tool output to the model;
7. checks a candidate final response against deterministic local completion evidence;
8. stops on an accepted final response, completion-gate rejection, cancellation, timeout or step limit.

Every model request uses a capability-aware execution contract generated from the profile's actual allowlist. It asks the model to inspect before editing, prefer the project index when enabled, refine `search_code` when `truncated=true`, reuse completed evidence, verify accepted changes when command execution is available, and report exact verification evidence. The contract never requires fields absent from a tool's JSON schema.

Inspection is also a deterministic runtime capability. `observationTracker` accepts a complete exact `read_file` result or one or more bounded `search_code` fragments only after those results were visible to the model. Existing-file observations carry the whole-file SHA-256 and remain valid across unrelated workspace revisions only while that target digest still matches disk. A full-content rewrite requires the complete observation; an exact edit is allowed only when every `oldText` anchor occurs in a visible current fragment. Related-file metadata never seeds an observation. Exact unredacted workspace-file attachments may seed complete evidence; truncated or transformed attachments cannot. New files still require a visible current `list_files` result or a neighboring-file observation. `propose_patch` is rejected with `inspection_required`, `inspection_scope_required` or `inspection_stale` before approval when those conditions fail. The proposal's captured original hash is checked again against the observation to close the read/preparation race, while the existing apply-time hash check closes the later approval/write race.

`propose_patch` has two mutually exclusive input modes without adding another IDE control. `content` carries a complete body for creation or a coherent rewrite. `edits` carries at most 64 exact non-overlapping replacements for an existing file. Each non-empty `oldText` must occur exactly once in the inspected original; missing, repeated, overlapping and no-op anchors return structured correction codes before approval. The backend sorts the resolved byte ranges, reconstructs the result in one bounded pass and stores the same full proposal used by rollback. Review output for exact edits is generated from those trusted ranges with three context lines per hunk, avoiding quadratic or whole-file diff expansion on repetitive sources.

A quest token ceiling is a spending guard, so it applies only to runtimes that bill for tokens. Ollama, the local OpenAI-compatible servers and the `llmux` gateway run on hardware the owner already pays for: their turns are recorded in usage and statistics but never consume the quest ceiling, in the reservation and in the identical pre-launch check alike. A cost ceiling is untouched by this, and `custom` stays billed because an arbitrary URL may front an official API. The same split decides thinking: suppressing it is an emergency retry for a turn that spent the whole output budget on reasoning, and the suppression flag lives until the end of the run, so it is reserved for billed runtimes. A free runtime keeps thinking and is told about the budget instead. Reasoning consumes the output allowance before the answer does, so `MinThinkingOutputTokens` (8192) is the floor for any turn whose model family is known to reason or whose profile asks for reasoning effort — a floor capped by the family's own documented maximum output.

The engine reserves `maxOutputTokens` from the profile's `contextWindowTokens` and applies a provider-independent rolling input budget. The system prompt, attached context and original task form a stable prefix. Older complete assistant/tool rounds are evicted only as units and converted into bounded deterministic local evidence; the newest rounds remain complete whenever they fit. A single oversized tool result is replaced in the provider request by valid JSON containing its original size, SHA-256 and a UTF-8-safe excerpt. Full events and tool results remain untouched in SQLite. Evicting or compacting a successful read-only round releases both its semantic duplicate-call key and its edit observation for a later refresh; patch, command and custom-tool completion guards remain permanent for the run. Detecting an external edit similarly releases the stale target read so the next turn can obtain current evidence.

OpenAI-compatible and Ollama adapters retry only bounded transient failures (connection setup, HTTP 408/409/429 and 5xx), at most three attempts with exponential backoff and `Retry-After` support. A response is never replayed after streaming has started. Every retry becomes an immutable `model.retrying` event. Permanent 4xx errors fail immediately.

The engine fingerprints semantic tool calls without their provider-generated IDs. A successful call is not executed again against the same workspace revision; after an accepted patch, read/verification calls may run against the new revision while the identical patch remains globally deduplicated. Three consecutive identical tool plans stop with `agent_stalled` instead of consuming the remaining step budget. Malformed OpenAI-compatible tool arguments become a structured `invalid_input` tool result so the model can correct itself, while an empty response can no longer be reported as a successful run.

The completion tracker is deliberately local and deterministic. An explicit request to run tests/build/lint requires either `run_command` or an allowed custom tool explicitly designated as verification evidence; an accepted file change requires a successful verifier after the newest workspace revision when that capability is enabled. A candidate final answer that lacks this evidence receives one `<point_completion_gate>` follow-up containing only recorded facts. The next candidate is either accepted or rejected. Built-in shell evidence must match the conservative verification catalog, preserve verifier failure status, return `exitCode` zero and not time out. Custom verifiers are an explicit owner trust decision but must still return structured exit-zero/non-timeout results. Failed tools remain retryable and never become successful semantic-deduplication entries.

Active runs exist in memory, while every durable state transition is persisted. A restart deliberately converts unfinished state to an auditable terminal state instead of attempting unsafe replay.


### Packages the rest of this document does not name

Eight packages carried no mention in any living document, and one of them
encodes a rule the release gate enforces:

- `internal/osproc` is the core's only door to foreign programs. Point starts
  without a console, and on Windows a console-less process gets a fresh console
  window for every console child — git, docker, cmd, ssh flashed black windows
  over the workbench. `osproc` sets `CREATE_NO_WINDOW`; a direct `exec.Command`
  anywhere else reopens the hole, which is why
  `scripts/check-release-contracts.mjs` rejects one. The failure is invisible in
  tests and on CI: it only shows on a live Windows desktop.
- `internal/mcp` serves Point's own tools over MCP to executors that cannot
  accept them any other way. An API model receives tools in the request body;
  a CLI executor does not.
- `internal/changesets` owns the Change Set itself — building one from a sandbox
  diff, and applying or reverting it against the workspace through a
  symlink-safe target resolver. The concept is described across
  [agent-hub-mvp.md](agent-hub-mvp.md) and this document; the package that
  implements it was never named.
- `internal/modeljson` extracts JSON from a raw model reply. The parser existed
  four times over — learning, companion, Master and task acceptance — and the
  four disagreed about what they stripped.
- `internal/cache` is a two-implementation cache: in memory by default, Redis
  when `REDIS_ADDR` is set. `FromEnvironment` picks one; nothing else chooses.
- `internal/events` is the in-process event hub plus its store: run events fan
  out to subscribers and persist for replay.
- `internal/executors` describes what an executor kind can do — capabilities,
  request and result shapes shared by the CLI and runtime paths.
- `internal/observability` carries request attributes into `slog`, so a log line
  from deep inside a run still names the workspace and the run.

`cmd/point-egress-gateway` is the sandbox's outbound proxy: `serve` listens for
container traffic and enforces the allowlist, `probe` checks a single
destination against the same rules. The policy it implements is in
[sandbox.md](sandbox.md).

## Run diagnostics

`internal/diagnostics` replays a run together with its ordered immutable events, approvals and patch records. The analyzer is deterministic for terminal runs and never calls a provider. It reports model round trips, bounded retries, provider-reported tokens, estimated input budget/peaks, context compactions, successful index searches with candidate/returned/related-file/character/truncation totals, model/tool latency, structured tool outcomes, approval wait time, patch outcomes, duplicate-plan and blind/scope/stale-edit guardrails, completion revisions/rejections and whether an accepted file change was followed by a successful verification event. Provider usage has a first-class `model.usage` event; the replay path also recognizes the legacy usage payload in `model.streamed`, so existing SQLite databases need no migration.

A finished run's diagnostics never change: events are append-only, the status is terminal and
the analyzer calls no provider. They are therefore memoized in process, keyed by run ID,
status and finish time, so a changed record produces a different key instead of a stale
answer. Only a terminal run with `finishedAt` set is eligible — without it the analyzer falls
back to the current clock and the result stops being reproducible. This matters because the
replay is not on one path: `Bootstrap` and both Companion live-intervention paths ask for it,
and the extension polls the latter on a timer.

The result is an operational health state (`active`, `healthy`, `attention`, `failed`) plus evidence codes. It is intentionally not a semantic score of the model's answer. `RunDetails` always includes current diagnostics, while bootstrap precomputes them for the 20 newest runs so the IDE can render compact history metrics and compare the latest two terminal runs without extra model work.

## Evaluation and Skill rollout

Run configuration snapshot schema v3 persists independent SHA-256 identities
for the complete profile, complete executable configuration, canonical egress
policy and each loaded Skill revision. The egress identity covers exact
FQDN/port/protocol rules and quotas, so later profile edits cannot change a
running or historical policy. `internal/app/benchmarks.go` binds versioned personal benchmark
cases to distinct real terminal Runs and rejects evidence without exact v2/v3
attribution. Comparisons require the same set revision/digest and report
per-case regressions and recoveries; they do not compute a weighted score.

Autonomous Skill revisions are immutable rollout units. An update receives a
new Skill ID with `familyId` and `supersedesSkillId`; only the source agent swaps
from baseline to candidate. A matching verified trajectory in another project
assigns that same candidate without editing the Blueprint. `SkillOutcome`
records feed a deterministic three-Run canary gate against up to five exact
baseline outcomes. Insufficient evidence remains pending. Proven degradation
invokes the same newest-revision rollback path automatically. Healthy evidence
from two workspaces only enables an explicit promotion command; background
code never performs Blueprint-wide distribution. See
[agent-evaluation.md](agent-evaluation.md) for thresholds and API contracts.

Applied patches form the file-change history shown in the Agent Hub. This includes both explicitly accepted `propose_patch` diffs and exact text mutations detected around approved `run_command` or custom process/command tools. Executable tools are snapshotted only after approval, then compared after process exit even when the command fails. One `workspace.changed` event reports the source, total, recorded, non-revertible and omitted changes plus snapshot completeness. Exact bodies are bounded and stored only in local SQLite; event/API payloads omit the duplicate bodies and redact the visible diff. Sensitive, binary, unreadable or oversized content is fingerprinted without being persisted and cannot claim rollback coverage.

A rollback is an audited compensating operation, not a destructive database rewrite: the service verifies that the current contents still equal the exact agent-produced contents, restores the recorded original contents (including an agent-deleted file) or removes a file created by the agent, marks the patch reverted and appends `patch.reverted`. Any later edit causes a conflict instead of being overwritten.

## Data model

SQLite tables:

- `settings`
- `profiles`
- `custom_tools`
- `workflows`
- `workspaces`
- `runs`
- `workflow_runs`
- `events` (append-only triggers, indexed by run and sequence)
- `approvals`
- `tool_execution_approvals` (workspace/tool/arguments-bound, expiring, one-time manual grants)
- `patches`
- `learning_signals`, `skill_outcomes`, `agent_improvements`
- `agent_benchmark_sets`, `agent_benchmark_evaluations`
- `compatibility_usage` (privacy-preserving exact-version counters; no payloads or identifiers)
- v2 contract tables: `work_order_revisions_v2`, `work_order_current_v2`, `work_order_approvals_v2`, `work_order_revision_diffs_v2`, `work_order_completion_gates_v2`, `work_order_quest_control_events_v2`
- v2 supporting tables: `source_snapshots_v2`, `evidence_bundles`, `writer_leases_v2`, `model_certifications_v2`, `delivered_app_controls_v2`

Timestamps use RFC 3339 nanosecond UTC strings. JSON arrays are used only for bounded snapshot fields such as tools used and changed files; event payloads retain their structured JSON form.

`internal/backup` owns automatic SQLite recovery points. Startup inspects an existing database read-only and creates a verified online snapshot before applying a missing migration. Runtime snapshots use SQLite's backup API, are serialized and verified with `integrity_check`, `foreign_key_check`, and SHA-256, then retain bounded daily and weekly history under a space cap. The manager only prunes its own regular `point-*.db` files; unknown files and symlinks stay outside its deletion boundary. Significant run/workspace events schedule one debounced snapshot, while shutdown cancels pending work before closing SQLite. UI restore lists only freshly verified snapshots, requires an explicit choice and modal confirmation, creates an additional recovery point, refuses other live IDE leases, stops Core, invokes `point-db restore --confirm-offline`, verifies its SHA-256/integrity report, and then restarts Core. The API itself never swaps a live database.

The `custom_tools.configuration` JSON column stores typed process definitions while retaining the legacy fixed-command columns. The `runs.context_items` JSON column stores at most 16 bounded typed context snapshots, including exact bounded image payloads when present. `runs.configuration_snapshot` stores the immutable execution configuration, including the exact custom-tool definition selected for that run. Startup migrations add these columns with compatibility defaults to databases created by earlier versions.

Migration 29 performs the final idempotent `profiles` → `agent_blueprints`
reconciliation and replaces the old startup copy loop with durable
exact-version compatibility counters. Statistics exposes raw counts and time
bounds; it never stores prompts, paths, tool arguments, agent IDs, or secrets.
Removal rules and the current inventory are specified in
[legacy-lifecycle.md](legacy-lifecycle.md).

Migration 30 replaces the manual custom-tool `approved: true` trust flag with a
durable grant. The row stores redacted arguments plus SHA-256 digests of the
canonical raw arguments and complete saved tool definition, expires after ten
minutes and is atomically consumed once before process creation.

## Cache strategy

The cache interface has `Get`, `Set`, `DeletePrefix` and `Close`. Desktop uses a concurrency-safe in-memory implementation. The headless service enables Redis through `REDIS_ADDR`.

Only derived workspace views are cached. TTLs are deliberately short (3–5 seconds), and `patch.applied` invalidates the active workspace namespace. Run state, approvals and events are never served from Redis.

## Concurrency

- Each active run has its own cancel function and protected run snapshot.
- Approval channels are buffered and single-resolution.
- SQLite is configured with one connection and WAL mode for deterministic write ordering.
- SSE subscribers are isolated by bounded channels; a slow client cannot block the agent loop.
- Workspace cache values are copied on read and write.

## Container topology

- `frontend`: unprivileged nginx, read-only root, all capabilities dropped, loopback-only published port.
- `api`: non-root Go process, read-only root, all capabilities dropped, writable `/data`, `/workspace` and tmpfs only.
- `redis`: internal network only, append-only persistence, 128 MB LRU bound.

The API container includes Go, Git and ripgrep because the default acceptance workspace is a Go repository and approved commands must be able to run its test suite.

For desktop/core agent executions, the production process boundary is the
version-attributed Docker backend described in [sandbox.md](sandbox.md).
`filtered-copy` remains an explicitly reported compatibility/development
backend and cannot satisfy the strong-isolation release gate.

## Master methodology and development

The seven embedded resources in the `masterskills` package describe
context investigation, intake, acceptance criteria, planning, Flow composition,
recovery and explanation. They reuse `SkillDefinition`/`SkillRuntime` without
tool grants, scripts or permission deltas. `MasterSkillSession` pins the library
at operation start and attributes only instructions actually loaded. Intake
loads intake/criteria, planning loads planning/Flow, other dialogue loads
explanation. Read-tool transitions add context methodology (execution inspection
also adds recovery); extra methodologies are available via `read_skill` even
without a workspace. The no-tools planner receives its methods inline.

The invariant role/authority prompt and server validators cannot be learned.
Native Ollama structured output receives a schema without a duplicated textual
schema; other requests retain a textual format contract. The agent selector
and report agent keep independent roles. Recovery methodology does not grant
new quest-editing authority.

Migration 69 adds separate Master operations, revision history, evidence
signals, learning jobs, consumed-example keys, trials and budget reservations.
Builtin seeds are insert-only by digest; application updates cannot overwrite
learned revisions. Temporary conversations are excluded. Approval, revision,
feedback and evidence-gate events supplement model validation/repair signals;
executor completion is never a quality score for the Master.

Three new attributed completed operations of a phase enable a single candidate
for one existing skill. A persistent single-worker queue uses the configured
Master HTTP model to generate methodology, replay saved inputs for baseline and
candidate, run fixed scenarios, and judge paired quality/portability. Native
server contracts are checked as well. Replay contains redacted text only: no
images, signatures, credentials, tool dispatch or quest execution. Project
examples remain in their origin world; only generic instructions can transfer.

No candidate is trialed without preserved constraints, no quality regression,
and either a demonstrated defect correction or at least 10% reported token
savings with complete usage data. Three non-regressing uses confirm the source
trial. Another world must independently compare using its own examples and pass
three uses before shared promotion. Contract errors, repairs or negative
feedback withdraw a trial/shared lineage immediately; manual withdrawal is
also available. Insufficient evidence remains experimental, not proven.

Every background call reserves a conservative upper bound for input, capped
output and up to three provider attempts within 10% of actual main Master tokens
over the origin world's trailing 30 days. Generation, both replay sides,
judging and failed/retried calls all count. Reservations are atomic. Unknown
usage and interrupted reservations are charged conservatively on recovery.
Credentials live only in the worker closure; restarted jobs wait for the next
authorized Master interaction. Background errors never replace the main reply.

Prompt measurement is reproducible with `TestMasterPromptSizeMeasurement` and
the frozen pre-change intake fixture. The initial invariant section is 1,135
characters versus 7,936 previously (85.7% shorter); required skills, catalogue,
mode and schema are measured separately. Character reduction is not a tokenizer
measurement or proof of better answers. Actual tokens and quality need live
paired evaluations in the development history. Deterministic tests exercise
gates and lifecycle, not the quality of a particular configured model.
