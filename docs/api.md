# HTTP API

The headless server binds to `127.0.0.1:8080` by default. Docker overrides it to `0.0.0.0:8080` on an internal network; nginx publishes the web application on host loopback.

Current for Point `1.2.3` as of 2026-09-23. The inventory below is complete and
is checked against every `HandleFunc` registration by `node scripts/check-docs.mjs`.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/health` | Liveness and version |
| `GET` | `/api/system/diagnostics` | Unified READY/DEGRADED/BLOCKED lifecycle checks for Core, SQLite/migrations, disk, sandbox/image/API, workspace permissions, Core port, verified backup, and provider/model configuration; never accepts provider credentials |
| `GET` | `/api/system/backups` | List verified managed restore snapshots as bounded metadata without local paths |
| `POST` | `/api/system/backups` | Create a verified online SQLite snapshot, apply daily/weekly/space retention, and return only its ID, timestamp, size, SHA-256, and integrity state (never a local path) |
| `GET` | `/api/bootstrap` | Profiles, provider/template/tool catalogs, project-index status, file-change history, recent runs and current workspace |
| `GET` | `/api/state/runtime` | Narrow runtime/run/decision state snapshot used after mutations |
| `GET` | `/api/state/guild` | Narrow Hub/Guild entity snapshot used after mutations |
| `POST` | `/api/workspaces/open` | Open a path inside the configured boundary |
| `GET` | `/api/workspaces/tree` | Safe file tree |
| `GET` | `/api/files?path=` | Read a safe UTF-8 file |
| `PUT` | `/api/files` | Atomically save a safe UTF-8 file inside the workspace |
| `GET` | `/api/search?q=` | Search safe workspace text |
| `GET` | `/api/index/search?q=` | Rank the current-world code index for IDE navigation (symbols/chunks, no full text) |
| `GET` | `/api/index/status` | Current bounded index state, limits and partial-coverage flags |
| `POST` | `/api/index/update` | Incrementally reindex changed/deleted paths without walking the whole world |
| `POST` | `/api/index/invalidate` | Mark the current index stale after an external/managed mutation |
| `GET` | `/api/companion/live` | Thin companion interventions snapshot for IDE polling |
| `POST` | `/api/index/rebuild` | Rebuild the bounded local code index and return its status |
| `POST` | `/api/terminal` | Run a user-entered command inside the workspace boundary |
| `POST` | `/api/ide/observations` | Replace Problems snapshots or append bounded terminal/task outcomes for Companion analysis |
| `POST` | `/api/profiles` | Create or update a profile |
| `DELETE` | `/api/profiles/{id}` | Delete a non-default profile; existing runs keep their immutable snapshot |
| `POST` | `/api/custom-tools` | Create or update a fixed-command or typed direct-process custom tool |
| `POST` | `/api/custom-tools/preview` | Validate an unsaved process tool and return its effective schema/program/argv without executing or persisting it |
| `POST` | `/api/tools/preview` | Prepare a saved tool invocation without executing it |
| `POST` | `/api/tools/execution-approvals` | Persist a pending, ten-minute, exact-version/argument manual custom-tool approval and return its preview |
| `POST` | `/api/tools/execution-approvals/{id}/resolve` | Allow or deny one pending manual custom-tool approval |
| `POST` | `/api/tools/execute` | Atomically consume an allowed `approvalId` and execute the exact saved custom-tool invocation; read-only built-ins do not require a grant |
| `DELETE` | `/api/custom-tools/{id}` | Delete a custom tool that is not enabled by a profile, blueprint, or project agent |
| `POST` | `/api/providers/probe` | Check a provider connection and return its bounded model catalog without persisting the API key |
| `POST` | `/api/providers/capability-probe` | Run a bounded role-specific model/tool/JSON/inspection/verification/limits probe against an in-memory fixture without changing a project or persisting the API key |
| `POST` | `/api/context/preview` | Resolve typed attachments and return safe metadata, sizes, warnings and estimated text tokens without starting a run |
| `POST` | `/api/workflows` | Create or update a validated 1–12-step sequential agent workflow |
| `POST` | `/api/workflows/validate` | Validate a sequential workflow draft without persisting it |
| `DELETE` | `/api/workflows/{id}` | Delete an inactive workflow definition while preserving execution history |
| `GET` | `/api/workflow-runs` | Recent workflow executions |
| `POST` | `/api/workflow-runs` | Start a workflow with a task, per-profile in-memory API keys and optional context |
| `GET` | `/api/workflow-runs/{id}` | Workflow snapshot, current stage and child-run references |
| `POST` | `/api/workflow-runs/{id}/cancel` | Cancel the workflow and its current child run |
| `POST` | `/api/workflow-runs/{id}/steps/{stepId}/claim` | Claim a ready workflow step for execution |
| `POST` | `/api/workflow-runs/{id}/steps/{stepId}/heartbeat` | Renew the bounded claim on a running step |
| `POST` | `/api/workflow-runs/{id}/steps/{stepId}/complete` | Persist a claimed step result and advance the workflow |
| `GET` | `/api/decisions` | Everything currently waiting on a human, ordered by waiting time |
| `POST` | `/api/egress-asks/{id}/resolve` | Resolve Master→user network/git allow (`allow_once`/`allow_quest`/`deny`) or supervision (`continue`/`stop`) |
| `GET` | `/api/master/history` | Workspace-scoped Master dialogue history |
| `GET` | `/api/master/skills` | Effective Master skills, versions, learning budget and project-scoped history; excludes replay inputs |
| `GET` | `/api/master/learning` | Alias for Master development state |
| `POST` | `/api/master/learning` | Enable/disable background Master learning; fixed 10% token ceiling |
| `POST` | `/api/master/skills/{id}/rollback` | Withdraw a learned revision and descendants, retaining audit history |
| `GET` | `/api/master/directory` | Master chats across every known workspace (metadata only; foreign paths withheld) |
| `POST` | `/api/master/chat` | Send one bounded turn to the Master dispatcher |
| `POST` | `/api/reports` | Generate a user-reviewed MD, HTML, or XLSX artifact through the separate Reporter system agent using the Master's configured model |
| `POST` | `/api/master/messages/{id}/feedback` | Mark one Master reply helpful or not (`up`, `down`, empty clears) |
| `GET` | `/api/files/history?path=` | Immutable history of one file: patches and change sets that touched it |
| `GET` | `/api/runs` | Recent persistent runs |
| `POST` | `/api/runs/preview` | Prepare an exact read-only agent launch preview and SHA-256 fingerprint without calling a model or persisting a run |
| `POST` | `/api/runs` | Start one run with optional bounded typed context items |
| `GET` | `/api/runs/{id}` | Run, events, approvals, patches and locally derived diagnostics |
| `POST` | `/api/runs/{id}/cancel` | Cancel an active run |
| `POST` | `/api/runs/{id}/pause` | Pause an active run at a safe checkpoint |
| `POST` | `/api/runs/{id}/resume` | Resume a paused run |
| `POST` | `/api/runs/{id}/message` | Queue a user message before the next model turn; optional `learningIntent:"correction"` explicitly opts that message into the separate background learning review |
| `POST` | `/api/runs/{id}/forbid-file` | Forbid further access to a workspace-relative path |
| `GET` | `/api/runs/{id}/context-inspector` | Inspect immutable, attached, tool-result and queued context layers |
| `POST` | `/api/runs/{id}/context-add` | Resolve and queue new files or text selections for an active run |
| `POST` | `/api/runs/{id}/context-amend` | Queue pin, unpin or remove for an attached context item |
| `POST` | `/api/approvals/{id}/resolve` | Resolve once with `{ "allow": true/false }` |
| `POST` | `/api/patches/{id}/revert` | Safely restore one applied agent change if the file has not changed since |
| `POST` | `/api/companion/chat` | Workspace-grounded Companion Q&A with optional live IDE `focus` (file, snippet, run, debug); usage analysis or pending proposal; no direct orchestration mutation. With `Accept: application/x-ndjson` or `?stream=1`, the response is an NDJSON stream of `progress` / `delta` / `result` / `error` lines (see below). |
| `POST` | `/api/companion/propose` | Create a typed QuestProposal |
| `POST` | `/api/quest-proposals/decide` | Modify, ignore, or explicitly start a QuestProposal; Start may include a transient `orchestratorApiKey` for the separate model planner |
| `GET` | `/api/quest-proposals/{id}/brief-history` | Ordered revisions of the task brief attached to a QuestProposal; read-only |
| `POST` | `/api/companion/actions/decide` | Modify, apply or ignore a durable typed Hub action draft prepared by Companion |
| `POST` | `/api/companion/config` | Save Companion personality/mode for the workspace |
| `POST` | `/api/orchestrator/config` | Save the separate Orchestrator system agent (preset, policy, optional planner model) |
| `POST` | `/api/orchestrator/policy` | Return a normalized, capability-aware Orchestrator policy preview |
| `GET` | `/api/companion/history` | Workspace-scoped Companion dialogue history |
| `DELETE` | `/api/companion/history` | Clear Companion chat history in the current workspace |
| `POST` | `/api/companion/interventions/{id}/dismiss` | Hide one observed intervention occurrence in the current workspace |
| `DELETE` | `/api/companion/interventions/dismissed` | Restore dismissed Companion interventions for the current workspace |
| `POST` | `/api/blueprints` | Create/update AgentBlueprint (also mirrors legacy profiles) |
| `DELETE` | `/api/blueprints/{id}` | Delete an unused AgentBlueprint while preserving historical snapshots |
| `POST` | `/api/project-agents/preview-prompt` | Compile the exact ProjectAgent prompt without saving or starting a run |
| `POST` | `/api/project-agents/capability` | Evaluate the runnable capability/blockers of a ProjectAgent draft |
| `POST` | `/api/project-agents/capability-delta` | Compare capability impact before applying a draft change |
| `POST` | `/api/project-agents` | Create/update workspace-scoped ProjectAgent |
| `DELETE` | `/api/project-agents/{id}` | Delete a workspace-scoped ProjectAgent when no active reference blocks it |
| `POST` | `/api/project-agents/{id}/activate-draft` | Explicitly validate readiness and activate a draft ProjectAgent; ordinary saves preserve draft status |
| `POST` | `/api/project-agents/{id}/reject-draft` | Reject a selected draft, remove its temporary descendants, audit the decision, and return replacement agent IDs when available |
| `POST` | `/api/project-agents/{id}/apply-blueprint` | Apply Blueprint overrides into this ProjectAgent only |
| `POST` | `/api/project-agents/{id}/update-blueprint` | Push ProjectAgent overrides back into its Blueprint |
| `GET` | `/api/project-agents/{id}/diff` | Full workspace-scoped diff of all inherited ProjectAgent ↔ Blueprint fields; project-only rules are reported but never synchronized |
| `POST` | `/api/skills` | Save SkillDefinition |
| `POST` | `/api/project-skills/equip` | Equip a skill into the current workspace |
| `POST` | `/api/project-skills/preview` | Preview grants and blockers before equipping a skill |
| `POST` | `/api/teams` | Save a Team of ProjectAgents |
| `DELETE` | `/api/teams/{id}` | Disband a Team of the open workspace when no unfinished Quest holds it |
| `POST` | `/api/quests` | Save a Quest |
| `DELETE` | `/api/quests/{id}` | Delete a Quest of the open workspace when no live run, child quest or undecided Change Set holds it; run history is kept |
| `POST` | `/api/quests/{id}/purge` | Stop the quest, revert its workspace changes and delete it with every trace (flow, runs, executions, change sets, proposal, subquests); usage records and append-only journals are kept |
| `POST` | `/api/intakes` | Create a URL intake session: snapshot source, EnvironmentPlan, draft TaskBrief |
| `GET` | `/api/intakes` | List intake sessions for the current workspace |
| `GET` | `/api/intakes/{id}` | Read one intake session including source, coverage and delivery target |
| `POST` | `/api/intakes/{id}/approve` | Approve the current brief version and start autonomous execution |
| `POST` | `/api/intakes/{id}/expand` | Expand budget/network/publish permissions; returns to awaiting_approval |
| `GET` | `/api/intakes/{id}/evidence` | Immutable EvidenceBundle for an intake-backed quest |
| `GET` | `/api/agent-prep-chains` | List AgentPrepChain / RoleRequirement provisioning state |
| `GET` | `/api/flow-runs/{id}/team-events` | Persisted team feed for a FlowRun |
| `GET` | `/api/master/conversations/{id}/export` | Export one Master conversation |
| `GET` | `/api/master/conversations/{id}/messages` | List Master conversation messages |
| `GET` | `/api/master/turns/{id}` | Read one Master turn |
| `GET` | `/api/master/turns/{id}/events` | Events for one Master turn |
| `POST` | `/api/master/conversations/{id}/fork` | Fork a Master conversation |
| `POST` | `/api/master/sessions` | Create or open a Master session |
| `POST` | `/api/master/turns` | Start a Master turn |
| `POST` | `/api/master/turns/{id}/cancel` | Cancel an in-flight Master turn |
| `POST` | `/api/v2/master/turns` | Start a Master-first v2 turn with immutable `SourceSnapshot` references; an open workspace is optional |
| `GET` | `/api/v2/master/turns/{id}` | Read one v2 Master turn |
| `GET` | `/api/v2/master/turns/{id}/events` | Read the ordered event stream for one v2 Master turn |
| `POST` | `/api/v2/master/turns/{id}/cancel` | Cancel an in-flight v2 Master turn |
| `GET` | `/api/v2/master/quests/{id}` | Read the v2 quest state exposed through the Master |
| `GET` | `/api/v2/master/quests/{id}/evidence` | Read the immutable completion evidence and gate state for a v2 quest |
| `POST` | `/api/v2/master/quests/{id}/application/{action}` | Start or stop a verified delivered Docker Compose application; requires matching version, digest, DeliveryReceipt and idempotency key |
| `POST` | `/api/v2/master/quests/{id}/{action}` | Pause, resume or cancel a v2 quest, or append a redacted user message, through the Master |
| `POST` | `/api/v2/sources/preview` | Create an immutable text, PNG/JPEG/WebP image, file, public HTTPS or Git `SourceSnapshot` |
| `GET` | `/api/v2/sources/{id}` | Read safe metadata and extracted content for an immutable source snapshot; internal storage paths are omitted |
| `POST` | `/api/v2/sources/{id}/refresh` | Create a new immutable URL/Git snapshot and return a bounded requirements diff; active WorkOrders keep their approved digest |
| `GET` | `/api/v2/model-certifications?connectionId=&model=` | Read immutable certification history; an ordinary capability probe remains Experimental |
| `GET` | `/api/v2/connections/{id}/capabilities` | Read the credential-free adapter manifest, model catalog and certification status |
| `POST` | `/api/v2/work-orders` | Create immutable v1 of a normalized v2 `WorkOrder` |
| `GET` | `/api/v2/work-orders/{id}` | Read the current immutable `WorkOrder` revision |
| `DELETE` | `/api/v2/work-orders/{id}` | Delete a WorkOrder with its revisions; refused while its quest is open |
| `GET` | `/api/v2/work-orders/{id}/diffs` | Read machine-readable diffs produced after an approved work order is revised |
| `POST` | `/api/v2/work-orders/{id}/revise` | Create a new immutable `WorkOrder` revision and pause its active quest when required |
| `POST` | `/api/v2/work-orders/{id}/approve` | Atomically approve an exact version/digest, materialize its roster/workspace, build the Flow and launch it; an optional transient `apiKey` comes from desktop SecretStorage and is never persisted |

| `GET` | `/api/model-candidates` | Bounded model candidate list for routing |
| `GET` | `/api/model-capability-evidence` | Persisted model capability probe evidence |
| `GET` | `/api/workspace/model-routing` | Workspace model routing preferences |
| `PUT` | `/api/workspace/model-routing` | Update workspace model routing preferences |
| `POST` | `/api/runs/fast-agent` | Start a Fast Agent (precise daily) run |
| `POST` | `/api/runs/{id}/extend-active-time` | One-shot extend of quest ActiveSeconds |
| `POST` | `/api/runs/{id}/undo` | Undo selected patches from a run |
| `POST` | `/api/executions/{id}/resume-runtime` | Resume an external/runtime session for an execution |
| `POST` | `/api/executions/{id}/stop-runtime` | Stop an external/runtime session for an execution |
| `POST` | `/api/flows` | Save a graph Flow |
| `GET` | `/api/flows/{id}` | Read one graph Flow definition |
| `DELETE` | `/api/flows/{id}` | Delete a graph Flow; refused while a run is active or an open quest drives it |
| `POST` | `/api/flows/compile-workflow` | Compile legacy AgentWorkflow into a linear FlowGraph |
| `POST` | `/api/flow-runs` | Start a persisted graph FlowRun |
| `GET` | `/api/flow-runs/{id}/handoffs` | Read bounded persisted handoff evidence for a FlowRun |
| `POST` | `/api/flow-runs/{id}/tick` | Advance FlowRun state machine |
| `POST` | `/api/flow-runs/{id}/nodes/{nodeId}/resolve` | Resolve an approval/manual Flow node |
| `POST` | `/api/flow-runs/{id}/nodes/{nodeId}/merge/resolve` | Resolve one persisted parallel-sandbox conflict with `use_parent` or bounded `manual` content/deletion |
| `POST` | `/api/flow-runs/{id}/nodes/{nodeId}/revert` | Revert the published result associated with one Flow node |
| `POST` | `/api/executions/sandbox` | Create an ExecutionInstance with isolated sandbox |
| `POST` | `/api/executions/{id}/launch` | Launch a prepared execution with transient credentials |
| `POST` | `/api/executions/{id}/cursor/start` | Mark/start the external interactive Cursor execution path |
| `POST` | `/api/executions/{id}/cursor/complete` | Persist the externally completed Cursor execution result |
| `POST` | `/api/executions/{id}/change-set` | Build an incremental ChangeSet from the execution sandbox vs its immutable baseline |
| `POST` | `/api/executions/{id}/revert` | Revert an execution's applied publication when safe |
| `POST` | `/api/change-sets/{id}/apply` | Apply a reviewed ChangeSet into the live workspace after every `dependsOn` prerequisite is applied |
| `POST` | `/api/change-sets/{id}/reject` | Reject a ChangeSet only when no active execution or non-terminal ChangeSet depends on it |
| `POST` | `/api/change-sets/{id}/revert` | Revert an applied ChangeSet if its published files are unchanged and no later ChangeSet depends on it |
| `POST` | `/api/change-sets/{id}/resolve` | Resolve one persisted ChangeSet conflict/decision |
| `POST` | `/api/connections` | Upsert Connection metadata (`secretRef` only; secrets in SecretStorage) |
| `GET` | `/api/connections` | List saved model Connections with status, default flag and cached model catalog; never returns a secret |
| `DELETE` | `/api/connections/{id}` | Delete a Connection; refused while an agent, Companion or Orchestrator still references it |
| `POST` | `/api/connections/{id}/probe` | Re-probe a saved Connection with a transient key and store the resulting bounded model catalog |
| `POST` | `/api/connections/{id}/default` | Mark one Connection as the default; the flag is cleared on every other Connection in the same transaction |
| `POST` | `/api/memories` | Create/update a workspace-scoped MemoryRecord with validated Agent/Quest ownership |
| `DELETE` | `/api/memories/{id}` | Delete one MemoryRecord from the current workspace |
| `GET` | `/api/quests/{id}/outcome` | Aggregate persisted Quest/Flow execution outcome evidence |
| `GET` | `/api/quests/{id}/evidence-bundle` | Immutable EvidenceBundle for a quest started from URL intake |
| `GET` | `/api/quests/{id}/replans` | History of mid-flight replans for a quest |
| `POST` | `/api/quests/{id}/replan` | Replan remaining DAG stages within approved budget |
| `POST` | `/api/quests/{id}/revise-brief` | Pause runs and propose a new TaskBrief version |
| `POST` | `/api/quests/{id}/reconcile-controller` | Reconcile project controller state after fault/restart |
| `POST` | `/api/quests/{id}/revert` | Revert the safely published output chain of a Quest |
| `POST` | `/api/usage` | Append immutable UsageRecord for the **current** world. `workspaceId` is forced to the open folder; a foreign id is rejected. Client `costCents` is ignored so spend cannot be spoofed into the hard-stop budget. |
| `POST` | `/api/budget` | Save daily/monthly cost limits and hard-stop policy for the current workspace |
| `POST` | `/api/budget/pricing` | Save the current workspace's explicit per-model input/output price in integer cents per million tokens. Hard cost limits reject unpriced model calls instead of treating unknown cost as zero. |
| `GET` | `/api/statistics` | Aggregated usage/agent/quest stats for a workspace |
| `POST` | `/api/agent-improvements/{id}/rollback` | Roll back the newest applied autonomous or manually confirmed Skill/Memory/Rules revision while retaining its audit record |
| `POST` | `/api/agent-improvements/{id}/promote` | Explicitly promote an exact healthy canary Skill revision into its Blueprint; background evaluation never performs this broad mutation |
| `POST` | `/api/agent-improvements/{id}/reject` | Reject a positively evaluated temporary specialization, retain its evaluation audit, and delete the quest-scoped subagent without creating a Blueprint |
| `GET` | `/api/experience/search?q=` | Search bounded persisted Memory, learning signals, exact Skill outcomes and improvement history in the current workspace |
| `POST` | `/api/learning/manual/preview` | Validate a project/profile Memory or Rule lesson and return a side-effect-free confirmation fingerprint |
| `POST` | `/api/learning/manual/apply` | Apply only the exact fresh manual-learning preview explicitly confirmed by the user and journal its rollback snapshots |
| `GET` | `/api/benchmarks` | List personal versioned benchmark sets for the current workspace |
| `POST` | `/api/benchmarks` | Create or revise a benchmark set for one permanent ProjectAgent |
| `POST` | `/api/benchmarks/{id}/evaluate` | Evaluate one exact benchmark-set revision against one distinct terminal schema-v2 Run per case |
| `GET` | `/api/benchmark-evaluations` | List persisted benchmark evaluations with per-case evidence and exact configuration attribution |
| `POST` | `/api/benchmark-comparisons` | Compare before/after evaluations of the same set revision/digest and report regressed/recovered cases without a composite score |
| `GET` | `/api/docker` | Bounded Docker/container overview for the active project |
| `GET` | `/api/docker/logs` | Bounded logs for an allowed project container |
| `POST` | `/api/docker/containers/action` | User-originated start/stop/restart action for an allowed container |
| `POST` | `/api/servers` | Create/update an SSH server profile without persisting its password |
| `GET` | `/api/servers` | List SSH server profiles for the current storage |
| `DELETE` | `/api/servers/{id}` | Delete an SSH server profile |
| `POST` | `/api/servers/{id}/probe` | Test one SSH profile with a bounded OpenSSH probe |
| `POST` | `/api/servers/{id}/list` | List one bounded remote directory |
| `POST` | `/api/servers/{id}/read` | Read one bounded remote UTF-8 preview |
| `GET` | `/api/servers/{id}/terminal` | Return the validated native SSH terminal launch description |
| `POST` | `/api/db-connections` | Create/update database connection metadata without returning a password |
| `DELETE` | `/api/db-connections/{id}` | Delete a database connection profile |
| `POST` | `/api/db-connections/{id}/test` | Test a database connection with bounded timeout |
| `POST` | `/api/db-connections/{id}/query` | Run a bounded query; write/DDL requires an explicit decision |
| `POST` | `/api/db-connections/{id}/schema` | Read bounded table/column metadata |
| `POST` | `/api/db-connections/unlock` | Supply a secret for the current in-memory database session |
| `GET` | `/api/mcp/servers` | List the owner's MCP servers with tool snapshots, trust state, runtime status and problem/fix |
| `POST` | `/api/mcp/servers` | Create/update an MCP server; secret values are accepted by name, kept in core memory only, never persisted |
| `DELETE` | `/api/mcp/servers/{id}` | Stop and delete an MCP server, its tool snapshot and its in-memory secrets |
| `POST` | `/api/mcp/servers/{id}/trust` | Owner trusts a stdio server's exact launch configuration; the digest must equal the one shown |
| `POST` | `/api/mcp/servers/{id}/probe` | Restart the server, read `tools/list` and reconcile the snapshot (new tools off, changed tools off) |
| `POST` | `/api/mcp/servers/{id}/stop` | Stop the server process; the next use starts it again |
| `POST` | `/api/mcp/servers/{id}/tools` | Enable/disable one tool and set its risk; enabling approves the current tool digest |
| `GET` | `/api/mcp/servers/{id}/log` | Redacted tail of the server's stderr and protocol log |
| `POST` | `/api/mcp/import/preview` | Parse an `mcp.json` (Claude/Cursor `mcpServers` or VS Code `servers`) into candidates without saving |
| `POST` | `/api/mcp/secrets/unlock` | Supply MCP secret values (`point.mcp.*` refs only) for the current in-memory session |
| `GET` | `/api/integrations/gitlab/status` | GitLab plugin state: server, token owner (`whoami`), folder binding, capabilities; failures come back as `{state:"error", reason, problem, fix}` |
| `POST` | `/api/integrations/gitlab/plugin` | Connect/update the GitLab plugin: URL, optional CA path and token; builds the pinned `@zereight/mcp-gitlab` launch, which needs the owner's trust |
| `PUT` | `/api/integrations/gitlab/binding` | Bind the open folder: `auto` (git remote origin), `manual` project path, or `all` projects; optional username override |
| `GET` | `/api/integrations/gitlab/merge-requests` | Open MRs by `scope` (`mine`, `review`, `project`) for the bound project (or all projects) |
| `GET` | `/api/integrations/gitlab/merge-request` | MR card by `project` and `iid`: detail, approvals, MR pipelines, `mine` and `approvedByMe` |
| `GET` | `/api/integrations/gitlab/merge-request/discussions` | MR discussions (up to 5 pages of 100) |
| `GET` | `/api/integrations/gitlab/merge-request/changes` | Changed files of an MR |
| `GET` | `/api/integrations/gitlab/merge-request/diff` | Text diff of one MR file by `path` (fallback when file contents are unavailable) |
| `POST` | `/api/integrations/gitlab/merge-request/notes` | Owner's comment or thread reply; runs at once and is journaled with body hash and length, not the text |
| `POST` | `/api/integrations/gitlab/merge-request/approval` | Approve (with the seen head `sha`) or unapprove an MR; journaled |
| `POST` | `/api/integrations/gitlab/merge-request/merge` | Merge an MR; requires `confirmed:true` and `expectedSha`, GitLab refuses when the head moved; journaled |
| `GET` | `/api/integrations/gitlab/file` | File content at a revision (`project`, `path`, `ref`) for the IDE diff; missing, binary and too-big are flags |
| `GET` | `/api/integrations/gitlab/pipelines` | Pipelines of a `ref` (default: the folder's branch) or of an MR (`mr`) |
| `GET` | `/api/integrations/gitlab/jobs` | Jobs of a `pipeline` |
| `GET` | `/api/integrations/gitlab/job-log` | Tail of a `job` log: server headers and terminal codes stripped, secrets redacted, at most 256 KiB |
| `POST` | `/api/integrations/gitlab/jobs/retry` | Retry a job; journaled |
| `GET` | `/api/integrations/actions` | Journal of actions in external services (who, tool, target, outcome), newest first |
| `GET` | `/api/events` | SSE stream (`workbench` events) |

An approved `WorkOrder` keeps its reviewed fields immutable and exposes changing execution state only through the derived `runtime` object (`questId`, `status`, `message`, `flowId`, `flowRunId`, `updatedAt`). `runtime` is excluded from the approval digest. Initial approval reuses the transaction-created quest, opens the exact approved workspace, builds a project Flow and moves it to `running`; a missing runtime credential or interactive CLI session yields `awaiting_user`, while preflight failure yields `blocked` with a redacted reason. A v2 Flow terminal callback cannot use the legacy completion path: it persists an `EvidenceBundle` and the v2 evidence gate alone may produce `completed` or `needs_review`. The approved `completion` profile carries a command and expected exit code per check; after automatic delivery each command runs against the delivered revision, and the gate requires a matching executed record for every entry. A conflicting external change to the workspace rolls the transfer back whole and yields `needs_review`; a receipt may claim running services only when the approved service check actually started them.

Master quest controls operate on the execution runtime, not only the quest row. `pause` first closes Flow scheduling and requests a safe checkpoint from every active headless run; an active interactive CLI that cannot pause is reported instead of pretending success. `resume` reopens the Flow, restores resumable executions and schedules the next nodes. Its optional `apiKey` is transient SecretStorage material and is never added to the immutable control journal. `cancel` terminates active headless/CLI executions and marks pending executions and the Flow terminal. `message` is secret-redacted before it is injected into active runs and before it is stored in the immutable coordinator journal. Responses include `flowRunId` and `affectedRuns`.

`POST /api/v2/work-orders/{id}/revise` normalizes and validates the candidate revision before touching execution. For an active approved WorkOrder it then closes the current Flow scheduler and requests safe checkpoints before the storage transaction publishes the new immutable version and requirements diff. An interactive CLI that cannot pause rejects the revision instead of letting later stages continue under mixed briefs.

See also [agent-hub-mvp.md](agent-hub-mvp.md). Bootstrap may include optional Hub fields (`blueprints`, `projectAgents`, `quests`, `changeSets`, …) without breaking older clients. Its `sandbox` object reports the active backend and separate live-workspace, process, network, secret-environment, symlink and OS-boundary capability flags; clients must not infer stronger isolation from the word “sandbox”.

Run configuration snapshot schema v2 persists `profileDigest`,
`configurationDigest` and exact `skillAttributions`. Benchmark evaluation rejects
legacy snapshots without that attribution, task mismatches, non-terminal Runs,
cross-workspace/cross-agent evidence and reuse of one Run for multiple cases.

`ChangeSet.dependsOn` contains predecessor Change Set IDs in execution order. A client may present one chain action, but it must apply those IDs topologically before the selected set; the server independently enforces the same rule. Every sandbox has a server-only immutable `baselinePath`. Single-parent lineage uses `parentSandboxId` / `parentExecutionId`; merged lineage uses `parentSandboxIds` / `parentExecutionIds` and `baselineChangeSetIds`.

A multi-parent Join creates one deterministic three-way merge against its nearest common immutable ancestor. Disjoint paths, identical outcomes and non-overlapping text-line edits merge automatically. The successor remains uncreated while `FlowNodeState.output.waitReason` is `sandbox_merge_conflict`; `mergeConflicts` exposes only `{path,candidates:[{executionId,kind,hash}]}`. Resolve every path through the merge endpoint with `{path,strategy:"use_parent",executionId}` or `{path,strategy:"manual",content}` / `{...,delete:true}`. Manual content is limited to 1 MiB. The resulting `kind:"merge"` Change Set lists replaced branch sets in `supersedes`; each source becomes `status:"superseded"` with `supersededBy`, so it cannot be applied twice.

Companion configuration may select an Ollama, OpenAI-compatible, Anthropic or Azure OpenAI provider, model, base URL, temperature, output limit, action preference and one of the supported personality presets (`technical-lead`, `critical-architect`, `mentor`, `product-engineer`, legacy presets, or `custom`). The API key is transient and supplied by the extension from SecretStorage for a chat request only. A new Companion Studio connection is probed before its key is persisted; an existing connection is probed by ID so the credential never returns to webview state. Model output must be one strict, bounded JSON envelope and cannot include tools; invalid/provider-failed responses are recorded in usage and return a deterministic fallback. Read-only questions and usage/cost analysis must return without a proposal; cost stays explicitly partial when a provider omitted it. A validated Quest proposal is still only `pending` until the user explicitly starts it. Explicit requests to create a Flow, ProjectAgent, Team or Skill are converted into deterministic, typed, workspace-scoped Hub action drafts: the user can review and modify the draft, and only `apply` persists the entity. `modify` never creates the entity, `ignore` closes the draft, and an applied/ignored draft cannot be replayed. A confirmed Skill creates its global definition and an enabled Project Skill Instance for the active workspace; the draft cannot add scripts, references, unknown tools or hidden permission expansion. Structured clarification questions, response provenance and facts used are persisted per workspace.

When `POST /api/quest-proposals/decide` starts a proposal and the workspace Orchestrator has a provider and model, `orchestratorApiKey` is used in memory for no-tools planning turns and is never stored. The model returns only a strict party/phase plan using exact available ProjectAgent IDs. Point Core validates the allowlist, user-locked party, bounded stage/phase limits and contiguous phases, then owns graph IDs, node kinds, verification and approval gates. A rejected plan or provider error triggers one more planning attempt; if that also fails, the request returns an error without creating a Team or Flow. Budget exhaustion and cancellation do not trigger another attempt. A project task without a configured planner requires an explicitly selected Flow. Successful responses expose `orchestratorMode`, `orchestratorModel` and a short `orchestratorNote`; `plannerFallback` remains a legacy response field. Planner token usage is stored with outcome `orchestrator_plan`.

When the client requests streaming (`Accept: application/x-ndjson` or `?stream=1`), `/api/companion/chat` writes one JSON object per line:

- `{"type":"progress","step":"gather|focus|roster|quests|memory|index|search|model|local","status":"running|done|error"}` — system work chips for the IDE UI (never model text).
- `{"type":"delta","reply":"..."}` — growing assistant reply extracted from a partial model JSON envelope; emitted only when the decoded `reply` string grows. Streaming does not change recommend-only semantics and never starts a Quest.
- `{"type":"result","response":{...}}` — final `ChatResponse` (reply, questions, optional pending proposal/action draft, usage).
- `{"type":"error","error":{"message":"..."}}` — terminal failure after optional progress/delta lines.

Focused IDE questions use a lean context gather (parallel store reads, skip ProjectMap when a focus snippet is already present). Guild status, usage, and entity-creation requests still gather the full Hub snapshot.

The extension sends only workspace-file error/warning diagnostics and bounded terminal/task outcomes to `/api/ide/observations`. Point Core enforces the active workspace, normalizes paths, redacts likely secrets, caps retained observations and treats all captured text as untrusted context. Successful terminal output is not retained. Companion can explain current Problems and failed commands. The workbench also surfaces the same recommend-only paths from hover, Quick Fix, CodeLens, Problems, terminal, Shift+F10, debug call stack, SCM diff, blame/history and the status-bar inbox; current file:line, run target and debug session are shown in the Companion rail and never auto-start a Quest. IDE-derived interventions may expose a typed `companion_prompt` action linked to the source observation; its explicit click submits a visible grounded fix request to `/api/companion/chat`. The response may prepare a pending Quest with concrete file/line objectives and verification requirements, but never starts that Quest automatically.

`GET /api/decisions` merges the independent sources that can block a human — pending
tool approvals from unfinished runs, pending and conflicting change sets, flow nodes stopped
on `waiting_approval`, Quest proposals, proposed actions, and Master egress/supervision asks
(network host, confirmed git remote, hang intervention) — into one queue scoped to the
current world. Each item carries `resolve.path`, and where the decision is a field in the
body, `resolve.field` with its `accept`/`reject` values, so a client never needs its own
switch over decision kinds and a new source does not require UI changes. `blocking` marks
the items where an agent or flow is actually idling; a Quest proposal is not blocking
because the work has not started. Items are ordered oldest-first, ties broken by ID so a
selected row does not jump between polls. Tool risk is read from the same built-in catalog
the agent editor shows, never from a second scale. Egress asks resolve through
`POST /api/egress-asks/{id}/resolve` with `allow_once` / `allow_quest` / `deny`, or for
supervision hangs `continue` / `stop`.

`GET /api/files/history` turns the same immutable records around a file instead of a run.
The run chronicle answers "what did run #47 do"; a person asks "what happened to engine.go".
The `path` query parameter is resolved through the workspace boundary — absolute paths, `..`
segments, excluded directories and symlink escapes are rejected, while a filename like
`notes..md` stays valid. A missing file is allowed: an agent may have deleted it, and that
history is the most interesting one. Entries are newest-first, ties broken by ID. Each entry
states whether it can be reverted and through which path; an already reverted patch offers
nothing. Change-set items already published as a patch are not listed twice.

Bootstrap Companion interventions use typed actions. IDE observations may carry `companion_prompt`; active Run diagnostics may carry `open_run` or `message_run` linked to an Execution ID. `companion_prompt` creates only a Companion chat turn. `open_run` is navigation only. `message_run` contains a bounded corrective message and is delivered through `POST /api/runs/{id}/message` only after an explicit UI click; Companion never resolves approvals, starts a pending Quest or changes runtime policy automatically.

Errors use:

```json
{
  "error": {
    "code": "request_failed",
    "message": "human-readable detail"
  }
}
```

Mutation requests must use `Content-Type: application/json` and are limited to 2 MiB, except `/api/v2/sources/preview`, whose 24 MiB transport envelope accommodates the documented 16 MiB decoded image limit plus base64/JSON framing. SSE connections receive heartbeats and are not buffered by nginx.

The event stream and run details may contain `model.retrying` with `{ attempt, delayMs, message }`, `context.compacted` with before/after/budget and compaction counters, `completion.checked` with `revision_required`, `accepted_after_revision` or `rejected`, and `agent.guardrail` with a machine-readable `code`. Inspection guardrails use `inspection_required`, `inspection_scope_required` or `inspection_stale` plus `path`, `requiredTool`, `workspaceRevision` and a corrective `message`; they are returned before a patch approval is created. Diagnostics expose model retries, context input peaks/budget/released tokens, completion checks/revisions/rejections, guardrail counters and signals, and the terminal `agent_stalled` stop reason when three identical tool plans are detected.

`POST /api/runs` and `POST /api/context/preview` accept `contextItems`. A `workspace_file` item contains a relative `path`; the backend resolves and snapshots it through the safe workspace boundary. A `text` item contains an explicit user-provided `label` and `content`. Supported typed files are PDF, DOCX, XLSX, CSV/TSV, JSON/JSONL, UTF-8 text and PNG/JPEG/GIF/WebP images. The preview returns item format/media metadata, SHA-256 digests, source/extracted byte totals, warnings and an estimated text-token count. The server accepts at most 16 items, 256 KiB of extracted text per item and 1 MiB combined text; image and source limits are documented in the security model. Image bytes are retained only in the durable internal snapshot and are stripped from public responses. OpenAI-compatible providers receive image content parts; Ollama receives its native `images` array.

`POST /api/runs/{id}/context-add` accepts the same `{ contextItems }` envelope, but only for a currently active run in the open workspace. Workspace paths are resolved by Point Core, not trusted from the webview, and the new immutable snapshots are checked together with existing and already queued context against the same 16-item, text, image and source limits. The accepted batch is atomic: an invalid or oversized item queues nothing. Context Inspector marks the batch as pending until the engine reaches its next safe model checkpoint; then the stable model prefix is rebuilt, the updated run snapshot is persisted and a content-free `context.amended` audit event records IDs, labels, paths and digests.

Each new direct/Hub run contains `configurationSnapshot` schema version 2: application version, capture time, the complete effective agent profile, and the definitions of custom tools selected by that profile. The snapshot is inserted once and deliberately excluded from later run-state updates, so editing or deleting live profiles/tools cannot rewrite history. The engine still reads schema version 1, and legacy runs receive a schema-version-0 compatibility view with their persisted provider and model.

`POST /api/runs/preview` accepts `{ profileId, task, contextItems }` and resolves the same profile, sorted custom-tool snapshot and typed attachments used by `POST /api/runs`. It returns the exact system message, allowed tool definitions/JSON schemas, approval decisions, public context preview, context window/reserved output/available input limits, completion contract, warnings, a component token estimate and a SHA-256 fingerprint. `completion` states whether verification is explicit, whether file changes trigger it, whether any built-in/custom verifier is available, whether the configuration is blocked, the single correction episode and accepted evidence classes. Tool previews expose `providesVerification`. No provider is constructed and no run/event is written. `POST /api/runs` independently rejects a blocking completion configuration before persistence/provider construction, even without a preview. A caller may pass the fingerprint back; if the task, workspace, profile, tools or attachment digests changed, launch is rejected before model or tool execution. An immutable initial input larger than the available input budget is rejected before a run is persisted.

`diagnostics` in a run detail is schema version 1. It contains an evidence-based `health` state and `stopReason`, duration, model request/response latency and provider-reported token usage, retrieval totals (`searches`, `truncatedSearches`, `candidateChunks`, `returnedChunks`, `relatedFiles`, `usedChars`), per-tool successes/failures/duration, approval decisions/wait time, patch counts, completion-gate metrics, `guardrails` counters (`duplicatePlans`, `inspectionRequired`, `inspectionScope`, `inspectionStale`) and machine-readable signal codes such as `retrieval_truncated`, `blind_edit_prevented`, `edit_scope_prevented` and `stale_edit_prevented`. Verification requires a recognized built-in verifier or an allowed custom tool whose immutable definition has `providesVerification`; a nonzero exit code or timeout cannot satisfy either. Output-only commands and failure-masking shell expressions are rejected by the generic runner's evidence classifier. The analyzer reads only persisted artifacts and never calls a model. Token usage can be unavailable when a provider omits it; in that case `usageReported` is false instead of presenting an estimate as fact. This is an operational summary, not a semantic score of the response.

`POST /api/providers/probe` accepts `{ provider, baseUrl, apiKey, apiVersion }`. It performs a 10-second bounded `GET /api/tags` for Ollama, `GET /models` for an OpenAI-compatible or Anthropic API, or `GET /openai/models?api-version=` for Azure OpenAI, reads at most 2 MiB and returns at most 500 unique model IDs. Authorization follows the provider: `Authorization: Bearer` for OpenAI-compatible, `x-api-key` plus `anthropic-version` for Anthropic, `api-key` for Azure. A bare company host such as LLMux is normalized to `{origin}/v1`, and so is a bare Anthropic host; an explicit path is kept. An Azure resource URL keeps its root because the request path is built per deployment, and `apiVersion` therefore lives beside the URL instead of inside it. The key is used only for that request and is not written to SQLite, events, profiles, logs or process environments.

`POST /api/providers/capability-probe` accepts `{ profile, apiKey }` for an Ollama or OpenAI-compatible profile and runs at most five model rounds/25 seconds against three fixed in-memory tools. It reports separate `toolCalls`, `jsonContract`, `inspectionBeforeEdit`, `verificationEvidence`, and `withinLimits` checks plus raw tool-failure, token, context, and duration metrics. Read-only roles receive an explicit `NOT_APPLICABLE` edit check. The result deliberately has no aggregate score and never changes a model/provider/configuration automatically. Suggested remedies name user-controlled configuration choices. The fixture never reads or writes the workspace, and the transient key follows the same non-persistence rule as provider probe.

Bootstrap provider presets cover local Ollama, LM Studio, vLLM and llama.cpp endpoints; remote OpenAI-compatible services; Anthropic over the Messages API and Azure OpenAI over deployment-scoped requests; and a custom compatible endpoint. Anthropic and Azure are separate protocol adapters rather than presets: the first sends its system instruction as a dedicated field and exchanges tools as `tool_use`/`tool_result` blocks, the second authorizes with `api-key` and addresses a deployment with a mandatory `api-version`. Profiles store the selected preset, arbitrary model ID, goals, system prompt, rules, temperature, context-window and output-token limits, reasoning effort and explicit tool allowlist. Secrets are always supplied in memory at launch/probe time. Supported Companion/run providers are Ollama, OpenAI-compatible, Anthropic, and Azure OpenAI only — Claude Code, Codex, and Cursor CLI are not product paths.

The index is local, in memory and scoped to the active workspace. `project_map` returns a compact language/directory/file/symbol overview. `search_code` accepts a focused query of at most 4096 UTF-8 bytes/64 meaningful terms and returns non-overlapping diversified ranked chunks with exact line endings, `fileSha256`, `matchedTokens` and `score`. Optional `include_related: true` adds `relatedFiles`: at most 20 `{ path, relation, via, fileSha256 }` records where `relation` is `imports`, `imported_by`, `test` or `tests`; `relatedFilesTruncated` signals omitted relationships. These records contain no source and do not authorize edits. The envelope also contains `candidateChunks`, `returnedChunks`, `usedChars`, effective `maxChars`/`maxChunks`, and `truncated`; `count` remains an alias for returned chunks. Rebuild status reports file, chunk and symbol counts, duration and indexed bytes. Point invalidates after managed mutations and refreshes automatically when external file metadata or a selected/related file digest no longer matches.

`POST /api/patches/{id}/revert` accepts only an applied patch. It compares the current file to the patch's recorded result before restoring the original contents or removing an agent-created file. A mismatch returns a conflict and preserves the user's later work. Success appends a new immutable `patch.reverted` audit event.

Patch objects include `sourceTool`: `propose_patch`, `run_command`, or the immutable ID of a user-defined tool. Public patch responses deliberately clear `original` and `proposed`; `diff` remains available after secret redaction. Approved executable tools append `workspace.changed` events with bounded counts and paths (`totalChanges`, `recordedChanges`, `nonRevertibleChanges`, `omittedRevertibleChanges`, `snapshotComplete`). Run diagnostics aggregate the same facts under `workspace` and emit warnings when exact rollback or complete audit coverage is unavailable.

The `propose_patch` tool schema requires `path`, `reason`, and exactly one of `content` or `edits`. `content` is a complete UTF-8 body capped at 512 KiB. `edits` is a 1–64 item array of `{ oldText, newText }`; anchors are non-empty, resolved only against an existing current file, must occur exactly once and may not overlap. Structured failures include `edit_target_missing`, `edit_anchor_missing`, `edit_anchor_ambiguous`, `overlapping_edits`, `no_changes`, size errors and `invalid_input`. Both modes create the same review, approval, hash-conflict and rollback records.

A workflow step stores a profile ID, stage instruction and two explicit handoff flags: `includeOriginalContext` and `includePreviousResult`. Execution is strictly sequential and stops on the first failed/cancelled stage. Original context keeps the normal 16-item/1-MiB limits; a previous result is redacted, UTF-8-safe and capped at 256 KiB before becoming untrusted context for the next model. `apiKeys` is keyed by profile ID and is never persisted.

A custom tool has a display name/description, workspace-relative `cwd`, a timeout from 1 to 600 seconds, optional `providesVerification`, and a generated ID that doubles as the model-visible function name. `kind: "command"` retains a fixed shell command for compatibility. `kind: "process"` stores a fixed relative/PATH `program`, up to 64 argv templates and up to 16 typed parameters (`string`, `integer`, `enum`, `workspace_path`). Templates reference parameters as `{{name}}`; the backend generates a strict schema, expands values as single argv elements, rejects unknown or invalid input before approval and executes without an implicit shell. When both the definition flag and the profile allowlist opt in, a structured exit-zero/non-timeout result may satisfy completion evidence. Bootstrap also returns reusable process-tool presets. Profiles may allow a custom-tool ID only while the tool exists.

`POST /api/custom-tools/preview` accepts `{ tool, arguments }`, where `tool` may be an unsaved process definition and `arguments` is a sample model payload including `reason`. It runs normalization, definition validation, strict parameter validation, executable/cwd resolution and workspace-path checks through the same code used at execution time. It returns the generated tool definition, resolved program, exact argv, relative/resolved cwd and typed values. The operation does not call the process runner and does not write the draft to SQLite.

### Master skills and learning

`GET /api/master/skills` and `GET /api/master/learning` return the current
world's `{config, budget, skills, revisions, history, operations}`. No workspace
parameter is accepted. They also work before a project is opened. `skills` are
the effective `SkillDefinition` values; immutable revision identities include
revision and digest. `operations` contain attribution, token usage and links
to turns/proposals/quests/Flows, but never saved replay inputs. Other projects'
examples, jobs and provenance are not exposed through the shared library.

`POST /api/master/learning` accepts `{"enabled":true}` (or false). The default
is enabled. The same optional `learning` object is available in
`OrchestratorConfig`; omitting it preserves the saved preference. Disabling
stops further learning calls and experimental source revisions; verified
revisions remain available. The fixed 10% ceiling cannot be raised through API.

`POST /api/master/skills/{revisionId}/rollback` accepts `{}` and withdraws a
learned revision and descendants. Builtins cannot be withdrawn. The target
must be local, under trial in the current world, or shared. Existing in-flight
operations keep their pinned snapshot; subsequent operations use the previous
eligible revision. History is retained. Missing/foreign targets fail closed.

`GET /api/master/turns/{id}` (including the v2 alias) adds optional `skills`:
`[{skillId,name,revision,digest}]`. Historical turns without attribution remain
readable. Progress event `type="skill"` carries a short load message and the
attribution JSON in `detail`. Planner load events use the existing WorkOrder
launch-progress channel. Background comparisons appear only in development
history, not as executor runs or foreground tool activity.

Learning jobs use `queued`, `running`, `deferred`, `rejected`, `canary`, `local`,
`shared`, `rolled_back`; evaluations expose paired quality scores and reported
token totals. `budget` reports `mainTokens`, `limitTokens`, `spentTokens`, and
`reservedTokens` for the origin world over 30 days. Unknown usage/crash windows
retain a conservative charge. No credential field is persisted or returned.
After restart or insufficient authorization/budget, jobs resume on the next
authorized Master interaction; a deferred job never launches a real quest.

### MCP / external CLI providers

CLI providers (Claude Code, Codex, Cursor CLI) were removed from the Companion
and run product path. Models use HTTP API providers only (Ollama,
OpenAI-compatible, Anthropic, Azure OpenAI) and receive tools in the request
body. Point's own MCP server (`/mcp`) for an external CLI executor is not a
Companion path and is not documented as a supported product integration.

The opposite direction is supported: the owner's MCP servers under
`/api/mcp/*`, where Point is the MCP client (`internal/mcpclient`). A stdio
server runs on the owner's machine outside the sandbox and starts only after
the owner trusts its exact launch digest. A remote server is reached over
Streamable HTTP with https, no redirects and a pinned address; a private or
VPN address is allowed only for the host the owner granted.

The GitLab window reads through the plugin's MCP server under
`/api/integrations/gitlab/*`; no model takes part. A GitLab project path
contains `/`, so project, MR and file are query parameters. Every answer is
`{state, reason, problem, fix, data}`: a screen that could not load is `200`
with a reason (`not_configured`, `not_trusted`, `secret_locked`,
`tool_missing`, `unreachable`, `auth`, `not_found`, `refused`, `format`,
`no_project`), an invalid request is `400` with `bad_request`. See
[integrations-gitlab.md](integrations-gitlab.md).
