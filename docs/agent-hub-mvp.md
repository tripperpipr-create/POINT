# Agent Hub model and guarantees

Compatibility and Hub model reference, reviewed for Point `1.2.3` on
2026-09-23. The filename is retained for stable links. Some numbered guarantees
below record design intent or earlier routes; [PROJECT-STATUS.md](PROJECT-STATUS.md)
lists confirmed live behavior and [PRODUCT-VISION.md](PRODUCT-VISION.md) defines
the target architecture.

Point Agent Hub manages a small persistent AI team on top of the local
`point-core` runtime. A user develops a bounded set of reusable specialists and
adapts them to projects instead of creating a separate collection of agents for
every repository.

## Model

- **AgentBlueprint** — global reusable specialist profile: role, instructions,
  Skills, Tools, model settings and permission baseline. The technical name is
  retained for API compatibility; the product UI calls it the main profile.
- **ProjectAgent** — workspace-scoped adaptation of that specialist with local
  rules, overrides, XP and project memory
- **ExecutionInstance** — one concrete task run with immutable config snapshot v2
- **Sandbox** — execution workspace selected by the route and approved plan; v2 Fast Agent requires a separate copy, while legacy or explicit live-write paths may touch the open project
- **ChangeSet** — reviewable exact-snapshot journal; Apply/Reject/Revert are separate user actions
- **Quest / Flow** — first-class work units and persisted graph runtime
- **WorkOrder** — the single immutable launch contract of v2: goal, scope,
  acceptance criteria, milestones, workspace plan, roster, model routing,
  network grants, secrets, budget and delivery policy. Master drafts it, the
  user approves an exact version/digest, and execution state stays outside the
  approved fields in the derived `runtime` object.
- **SourceSnapshot** — immutable text/image/file/HTTPS/git source a WorkOrder is
  bound to by digest; refresh creates a new snapshot and a bounded requirements diff
- **EvidenceBundle** — version 3 completion proof bound to the work order and
  source digests; the only path to a terminal v2 quest state
- **CompletionProfile** — the approved list of checks, each with its own command
  and expected exit code; it enters the approval digest, and `completed` requires
  a recorded result for every entry
- **DeliveryReceipt** — machine-verifiable proof that the checked revision
  reached the approved target (workspace revision, optional commit, optional app URL)
- **WriterLease** — durable transactional single-writer authority per workspace;
  it survives pause and process restart and is released explicitly
- **Skill / Team / Memory / Connection / UsageRecord** — supporting Hub entities
- **Companion** — IDE-wide accompanist; recommend-only; own model
- **Master / Orchestrator** — separate system agent; drafts the WorkOrder, then
  assigns the roster and starts Flows after approval; own model

`ProjectAgent` is the canonical runnable identity. `AgentBlueprint` remains global and reusable, while adding an agent to a project is always explicit. Legacy `/api/profiles` and linear workflows remain supported compatibility adapters; the codebase does not currently define a removal release. Legacy profile or Blueprint IDs are resolved to the corresponding ProjectAgent when one exists.

The user-facing rule is: create a new specialist only when responsibility,
permissions or long-lived identity differ. Add a Skill or Tool when only the
way of doing work changes. Changes can be promoted from a project adaptation to
the main profile through the reviewed Blueprint diff; project-only rules never
leak into other repositories.

## Runtime guarantees

1. File mutation mode is route-specific. The v2 Fast Agent requires an isolated worktree/snapshot, while legacy or explicitly selected live-write paths journal direct changes against an immutable baseline. Docker isolates commands and network; it does not by itself stage file writes. The selected mode must be shown before execution. See [sandbox.md](sandbox.md).
2. Persistent project entities carry `workspace_id`. Bootstrap, Runs, Journal, Statistics, patch/changeset mutations and Companion run evidence stay inside the open project world; Blueprints, Skill definitions and Connections remain global.
3. Flow runs restore from SQLite after reload.
4. Connection secrets stay in VS Code `SecretStorage` (`secretRef` only in SQLite).
5. Companion proposes quests and never mutates orchestration state without an explicit user command. Orchestrator is a different system agent: it assigns the party and starts Flow after Start.
6. A Skill cannot expand an agent's tool allowlist or permission policy; missing grants fail preflight explicitly.
7. Direct launches create a first-class Quest and correlate Quest → Execution → ProjectAgent → Run → UsageRecord.
8. Change Set Apply is a no-op (`kept`) when live content already matches the proposed hash; Revert restores exact pre-write snapshots only while the applied files are unchanged.
9. Provider-backed Companion can use its granted read-only tools (and read_skill when equipped), then returns a strict bounded JSON envelope, records usage, and falls back deterministically without auto-starting work. NDJSON `delta` lines stream a growing `reply` for UX only; they never mutate Hub state or start a Quest.
10. Blueprint synchronization always presents a complete field-by-field diff and requires explicit confirmation; project-only rules remain local.
11. Project, Agent, Companion and Quest memory records are isolated by
    workspace. Profile memory is instead owned by a global AgentBlueprint,
    follows that specialist into every project, and is injected only into runs
    of adaptations derived from the same profile. Every owner is validated;
    records support real create, edit, pin, source inspection and deletion.
12. Companion answers read-only status/usage questions without creating a QuestProposal, grounds month-over-month explanations in Usage Records, and preserves structured clarification questions across reloads.
13. Proactive Companion interventions are non-blocking and reversibly dismissible per workspace and observed occurrence; changed or recurring evidence becomes visible again.
14. Explicit chat requests to create a Flow, ProjectAgent, Team or Skill produce durable typed drafts with review previews. Modify and Ignore are non-mutating; only an explicit Apply creates the entity, and the decision cannot be replayed. A confirmed Skill is also equipped into the active project without expanding an agent's grants.
15. Editor Problems plus terminal and VS Code Task outcomes are bounded, redacted and isolated by workspace. Companion may explain them or prepare a grounded pending fix Quest, but never starts work automatically.
16. While an execution is active, Companion derives live recommendations from persisted Run diagnostics: pending approvals, repeated tool failures, truncated retrieval, context pressure, completion revisions and provider retries. Opening a run or sending a corrective message always requires an explicit user click.
17. Companion chat stays on the surface that opened it (peek / sidebar / Hub overview). Moving peek → sidebar preserves the in-flight thread; Stop freezes any streamed partial reply. Interventions refresh without stealing editor focus.

18. A configured Orchestrator model receives one bounded, no-tools planning request and may choose only listed ProjectAgent IDs plus 1–4 execution phases. Point Core validates the complete JSON contract, compiles graph IDs and node kinds itself, always appends verification, preserves user-selected parties, and adds policy-required approvals. Invalid output or provider failure produces an explained deterministic fallback instead of breaking Quest Start. The credential is read from IDE SecretStorage, sent transiently for this planning turn, never persisted, and model usage is recorded as `orchestrator_plan`.
19. **Target policy — Orchestrator supervision:** periodically inspect execution progress and pause or route repeated stalls with an explained reason. The exact runtime coverage remains to be verified. Spec: [AGENT-HUB-ORCHESTRATOR-NETWORK-POLICY.md](AGENT-HUB-ORCHESTRATOR-NETWORK-POLICY.md).
20. **Target policy — confirmed git and egress escalation:** outbound git remotes and new network hosts require the approved policy or a user decision; agents cannot widen it from the prompt. The exact runtime coverage remains to be verified. Spec: same policy doc.
21. A `requireResult` verifier understands direct agent output and nested Join/Loop aggregates. Parallel work passes only when every participating branch has a non-empty result; an empty branch fails the Flow instead of being hidden by the Join wrapper.
22. The IDE keeps a lightweight coordinator for already-authorized active Flows even when Hub and Companion surfaces are hidden. It launches newly ready cloud executions with credentials read just-in-time from SecretStorage, supports multiple active Flows, and resumes persisted pending/interrupted Flow executions after an IDE/core restart. Waiting approvals and missing credentials remain pending; the coordinator never stores a secret in Point Core.
23. Agent-to-agent handoff contains the Quest brief, upstream result and a bounded redacted review view of the exact Change Set produced by that execution (ID, status, paths, operations and unified diffs). A reviewer therefore evaluates persisted evidence instead of trusting only the previous agent's prose; under live-write mode those files are already on disk and the Change Set is the revert journal.
24. Every execution has an immutable start baseline. A node with one completed upstream execution inherits that execution's exact sandbox snapshot; root parallel branches share one Flow-start snapshot even when scheduled at different times. The stage produces an incremental Change Set with `dependsOn` lineage; Core rejects out-of-order Apply and unsafe ancestor Reject/Revert, while the IDE offers one dependency-ordered chain action.
25. A multi-parent Join uses the nearest common immutable ancestor and automatically combines disjoint files, identical results and non-overlapping line edits. Real overlaps stop before the successor starts and appear as a bounded file-by-file decision in the Flow UI. After explicit resolution, one aggregate merge Change Set supersedes the branch-local sets, preventing duplicate Apply while retaining atomic review and rollback.

26. A Connection owns the endpoint, the credential reference, the optional Azure `api-version` and the probed model catalog. Every ProjectAgent, Blueprint, Companion and Orchestrator references one by `connectionId`, and the credential is resolved from that single link. When a row predates the link it still resolves through a unique provider-preset match; two candidates produce an explicit ambiguity error naming them instead of a silent first match. A Connection cannot be deleted while anything references it, and exactly one Connection can be the default.

27. **WorkOrder approval is the single review surface (v2).** Model output may draft it, but only server-side normalization creates the contract: approval fields, workspace isolation and routing authority are derived from trusted local state, never from the model. Approval is atomic on an exact version and digest — it materializes the roster and workspace, builds the Flow and launches it. Revising an active WorkOrder first closes Flow scheduling and requests safe checkpoints; an interactive CLI that cannot pause rejects the revision instead of letting later stages run under mixed briefs.
28. **Completion requires evidence, not a successful Flow.** The v2 Flow terminal callback cannot use the legacy completion path. It persists an `EvidenceBundle` and only the evidence gate may produce `completed` or `needs_review`: the bundle must be version 3, carry the Point version, match the work order digest and the source-snapshot ledger, and include environment and versioned stack-preset evidence. Every approved completion check must appear as an executed record with the approved command and its exit code; a named check without a result, or one executed with a different command, is untrusted evidence. Structural mismatch is untrusted evidence and genuinely failed checks are retained — both yield `blocked`.
29. **Delivery is a receipt, not a side effect.** `applyMode=automatic` transfers the verified result and records changed files and commits; `applyMode=manual` leaves the verified result in `isolated_review` and deliberately does not touch the workspace. `commitMode=squash` creates exactly one commit from an explicit changed-file list and refuses unrelated workspace drift — never `git add -A`. A missing squash commit clears `deliveryVerified` instead of being ignored. Starting or stopping a delivered Docker Compose application requires a matching version, digest, DeliveryReceipt and idempotency key.
30. **Certification and retention.** Model certification history is immutable, and an ordinary capability probe stays Experimental; a connection exposes a credential-free adapter manifest with its certification status. Quest sandboxes older than 30 days are closed by a retention pass at core startup and marked closed in the journal.

## Hub UI (current)

The Hall rail has six top-level sections: Overview, Master, Decisions, Changes,
Quests and Guild. Agents, teams, flows, skills, memory and connections are Guild
sub-views or routed detail surfaces. Statistics is a separate IDE view opened
from Overview or `Point: Открыть статистику проекта`.

The quest token ceiling guards money, not diligence: a runtime that does not bill for tokens (Ollama, a local server, the `llmux` gateway) is bounded by step and active-time limits instead, and its turns never exhaust the ceiling. Those runtimes also keep their reasoning — Point raises the output allowance rather than silencing thinking for the rest of the quest.

Statistics includes project-scoped daily/monthly cost budgets. At 80% Point warns; optional hard stop blocks new launches at the known-cost limit. Providers that do not report trustworthy cost remain explicitly unknown and are never assigned a fictional estimate.

Agent, Team, Quest and Flow statistics also expose an evidence-based quality
scorecard. It does not collapse unlike signals into a synthetic rating:
coverage shows how many executions have replayable Run diagnostics, while
healthy-run rate, required-verification completion and Tool success rate remain
separate. Missing diagnostics lower visible coverage instead of being counted as
success. The scorecard is project-scoped and bounded to the latest 400 Runs (the
same bound as the immutable diagnostics memo); the UI says when that history
window is full or a diagnostic replay failed.

Agent scorecards turn repeated negative evidence into reviewed development suggestions.
Two or more verification gaps route to Skills;
repeated Tool failures route to Tools; denied approvals recurring across Runs
route to Permissions; and completion revisions recurring across Runs route to
Rules. A click opens that exact ProjectAgent and constructor step. A single
noisy Run is intentionally insufficient, and opening a recommendation never
equips a Skill, enables a Tool or grants a permission.

Separately, Agent Hub runs a Hermes-inspired autonomous procedural-learning
loop after a successful complex trajectory. A completed ProjectAgent Run is
eligible only when immutable diagnostics are healthy, at least five tool calls
finished, and required verification was recorded. A bounded no-tools reviewer
extracts a project-agnostic procedure and creates an immutable learned Skill
revision for that permanent specialist and tool sequence. The first verified
project keeps the revision as a canary candidate. The same procedure verified
in another workspace assigns that exact candidate only to the matching agent;
it does not overwrite the active definition or mutate the Blueprint. Three
candidate Runs across two workspaces must pass the visible regression gate
before the UI enables explicit user promotion into the Blueprint. A procedure whose required tools are outside the
Blueprint allowlist remains project-only. If the run provider cannot be
used headlessly or the reviewer response is invalid, a deterministic fallback
still records the verified sequence. The loop never adds a tool the agent did
not already have and always clears permission deltas, scripts and references.

Every terminal Run also produces typed, bounded `LearningSignal` evidence for
verified success, run/tool failure, user feedback, approval denial, missing
verification and completion revisions. Every Skill loaded from the immutable
Run snapshot receives a `SkillOutcome` tied to its exact SHA-256 payload digest
and revision. A schema-v2 Run also persists whole-profile and
whole-configuration digests. Statistics reports health, tool reliability,
verification, feedback and revisions per exact Skill version. Personal
benchmark sets bind explicit case criteria to real terminal Runs and compare
only the same set revision and digest before/after. These are correlations
rather than causal scores: one Run can load several Skills.

Ordinary live Run messages are never sent to the background reviewer. The user
must mark an individual message as **Lesson**; only then can its redacted,
bounded correction support a `user_feedback_after_recovery` review. That path
requires a healthy completed trajectory and verification when applicable. It
has no deterministic lesson fallback: if the model reviewer is unavailable or
rejects the evidence, Hub records `skip` instead of inventing a rule.

The same bounded review may propose one canonical role-level Memory principle.
The reviewer receives no file contents and no final Run answer. A deterministic
fallback never invents Memory. One workspace records only a candidate; the
identical redacted principle must recur in a second independent workspace of
the same Blueprint before Hub creates pinned portable `MemoryProfile` data.
New project instances then receive it through normal scoped context enrichment.

The reviewer may additionally propose one testable permanent behavioral
instruction. It follows the same two-independent-workspace gate. Promotion
appends the exact rule to the Blueprint and all existing ProjectAgents derived
from it; future ProjectAgents inherit it normally. The instruction parser
rejects secret/path-bearing text and phrases that request approval bypasses,
new tools or permission expansion. Deterministic fallback never invents rules.

Before any reviewed Skill can change persistent agent state, a deterministic
evaluation gate records visible pass/fail checks for source health and required
verification, bounded and portable instructions, existing tool boundaries,
preservation of the verification contract, and explicit feedback consent. A
failed check creates a skipped journal entry with evidence and performs no
Skill, Blueprint, permission or ProjectAgent mutation; the gate never hides
these invariants behind a synthetic quality score.

Every autonomous change is journaled with its source Run, evidence, reviewer
mode, Skill, Memory and Instruction promotion status, exact before/after Skill, Memory and Blueprint snapshots, and
the Skill IDs of every affected ProjectAgent. Statistics shows candidates,
universal Skills and project-only procedures. Only the newest applied revision
across all workspaces can be rolled back; rollback restores the exact previous
Skill, Memory and Blueprint state, including removing the promoted Skill from compatible
ProjectAgents created after promotion, without deleting the audit trail.

The Skill catalog has a deterministic read-only curator. It proposes duplicate
merge review, flags divergent definitions with the same normalized name, and
identifies old unbound Skills as deprecation candidates from global observed
outcomes. Repeated adverse outcomes on the same exact Skill digest create a
regression review only after at least two negative Runs in a three-Run sample;
a single noisy failure is ignored. It never mutates the catalog. A user can explicitly mark a reviewed
Skill `deprecated`; historical Run snapshots and existing assignments remain
valid, while new project equip and new agent assignment are rejected.

The rollout gate is separate from curator suggestions. It compares three exact
candidate outcomes with up to five exact baseline outcomes using declared
completion, health, verification and tool-failure thresholds. Insufficient
evidence stays pending. Proven regression automatically restores the newest
candidate's exact previous bindings; a healthy candidate across two projects
still requires an explicit user action before Blueprint-wide promotion. Full
rules and API contracts are in [agent-evaluation.md](agent-evaluation.md).

The Memory surface searches persisted Memory, LearningSignals, exact
SkillOutcomes and the improvement journal. Manual teaching is a two-step
operation: a side-effect-free preview shows scope and changes, then an explicit
confirmation applies either project-local or Blueprint-wide Memory/Rule data.
The confirmation fingerprint includes current agent/Blueprint timestamps, so a
stale preview cannot overwrite a newer edit. Secret-like content is rejected,
portable lessons also reject repository-specific paths, and the applied change
uses the same exact rollback journal as autonomous development.

Onboarding has two steps: Master connection (or the built-in Point engine) →
Master policy. Companion, permanent agents, skills and permissions live in the
Hub and full constructor; none of them is a first-run gate. Missing permanent
roles are proposed in the editable WorkOrder approval card.

Agent constructor: Identity → Role → Mission → Rules → Brain → Skills → Tools → Memory → Permissions → Review. The Review prompt is compiled by the Go runtime (`CompileProjectAgentPrompt` + skill enrichment + `SystemMessage`), not by a separate JS compiler.

After an Ollama, OpenAI-compatible, Anthropic or Azure OpenAI connection/model is checked in Agent Studio, Point automatically offers and starts a bounded role-specific capability probe using only the API key explicitly present in the current form. It evaluates tool calls, strict JSON/schema output, inspection-before-edit discipline, verification evidence, and context/time limits as independent signals with raw failure/token/duration metrics. Read-only roles keep editing unavailable. There is no synthetic aggregate score, no real-project mutation, and no automatic provider/model change; any suggestions remain explicit user choices.

Command Center supports Pause / Resume / Stop / Message / Forbid file / Context Inspector on active runs. Context Inspector can add a project file or the current editor selection while the agent is working; the secure immutable snapshot is shown as queued and joins the model context at the next safe step without restarting the run.

Live Companion guidance refreshes periodically during an active Run but updates the UI only when the recommendation occurrence changes. A recommendation can open the exact linked execution or send a visible predefined correction through the ordinary Run Message control; it never approves, pauses, changes permissions or edits a Flow by itself.

Companion receives current error/warning diagnostics and failed terminal/Task outcomes from the IDE. Each inline IDE warning offers an explicit **Prepare fix** or **Analyze failure** action. The click opens an ordinary Companion turn grounded in current Problems and failed commands, then returns a reviewable fix Quest with file/line and `exitCode: 0` verification criteria. Work starts only after the separate **Start** confirmation, using the ordinary Quest → Flow → Agent execution path. Companion can also create reviewed Agent/Team/Flow/Skill drafts after an explicit request. Skill review exposes instructions, required tools, scripts count and permission requirements before confirmation.

Companion Studio is a five-step in-place constructor: Mode → Connection → Personality → Boundaries → Test. It can reuse and probe a saved IDE connection without exposing its credential, or probe a new endpoint before saving its key to VS Code SecretStorage. Personality presets and six live sliders update a behavioral preview; changing a slider switches to Custom. The final step provides safe interactive examples for status analysis and reviewed Quest/Agent/Flow/Skill drafts, and can save the configuration and execute one test turn into the ordinary project-scoped Companion history.

Flow tab: node canvas + inspector; structure locked while a flow run is active. Agent nodes launch ordinary sandboxed executions. Tool nodes are limited to an explicit low-risk deterministic allowlist. Loop nodes require `maxIterations` (1-20), distinct `continue`/`done` edges and a single controlled back edge.

Quest Start records the configured Orchestrator model in the created Team and Flow descriptions. If model planning fails, Point retries once and then reports an error without creating a Team or Flow. A user-selected Flow remains an explicit alternative.

Change Sets support atomic Apply / Reject / Revert, dependency lineage, dependency-ordered chain Apply and conflict resolution strategies.

## Key APIs

- `POST /api/v2/master/turns` (+ `/{id}`, `/{id}/events`, `/{id}/cancel`) — Master-first turn over immutable source snapshots
- `POST /api/v2/work-orders` (+ `/{id}`, `/{id}/diffs`, `/{id}/revise`, `/{id}/approve`) — the immutable launch contract
- `GET /api/v2/master/quests/{id}` (+ `/evidence`, `/{action}`, `/application/{action}`) — v2 runtime, evidence and delivered app
- `POST /api/v2/sources/preview` (+ `GET /{id}`, `POST /{id}/refresh`) — immutable source snapshots
- `GET /api/v2/model-certifications`, `GET /api/v2/connections/{id}/capabilities`
- `GET /api/bootstrap` — optional Hub fields (`blueprints`, `projectAgents`, `quests`, `changeSets`, …)
- `POST /api/companion/chat`
- `POST /api/companion/actions/decide`
- `POST /api/change-sets/{id}/apply|reject|revert`
- `POST /api/connections` (metadata + `secretRef`)
- `POST /api/quests`, `/api/flows`, `/api/flow-runs`, `/api/skills`, `/api/teams`, `/api/memories`
- `DELETE /api/memories/{id}`
- `GET /api/experience/search?q=`
- `POST /api/learning/manual/preview`, `/api/learning/manual/apply`
- `GET /api/project-agents/{id}/diff` — complete Blueprint synchronization preview

## Sandbox note

The compatibility/development sandbox backend is a **filtered copy** for predictable performance on large repos. The backend contract reports its guarantees through bootstrap capabilities so UI and policy do not infer an OS boundary. The same sensitive/generated-directory filter is used for copy, baseline, diff and branch merge, so excluded files do not appear as deletions. This isolates live workspace writes, but it is not an OS security boundary: an approved command still runs as the Point process user. Agent command environments are allowlisted and known secret variables are removed; network command checks are defense in depth.

Production uses the Docker backend described in [sandbox.md](sandbox.md). It keeps the Change Set lifecycle but executes free-form commands and fixed/typed process tools with a single sandbox mount, read-only root, non-root identity, dropped capabilities and resource limits. Egress is deny-all by default. An exact TLS FQDN/port allowlist uses a per-process internal bridge and locked policy gateway with public-only pinned DNS, SNI verification and connection/byte/duration quotas; the tool has no direct outbound route. Docker version, image reference, exact local image ID and immutable egress policy digest are persisted with the run/sandbox evidence. Unrestricted, IP, wildcard and non-TLS rules fail closed. `POINT_SANDBOX_REQUIRE_STRONG=true` prevents fallback to host execution. A clean Git repository may use a detached worktree; any dirty or untracked state forces a filtered copy of the current workspace so local edits are preserved. A linked worktree's `.git` control file is never copied into a child sandbox. Every root and sequential node keeps a separate immutable baseline; copied files retain their permission bits.
