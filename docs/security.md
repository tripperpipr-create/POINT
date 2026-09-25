# Security model

Current for Point `1.2.2` as of 2026-09-10. This document describes enforced
application guarantees and explicit non-guarantees; UI wording is not a security boundary.
Формальная матрица угроз, privacy inventory и принятые остаточные риски находятся
в [threat-model.md](threat-model.md). Требования к надзору Мастера, confirmed-only
git и эскалации сети для Agent Hub —
[AGENT-HUB-ORCHESTRATOR-NETWORK-POLICY.md](AGENT-HUB-ORCHESTRATOR-NETWORK-POLICY.md).

## Workspace boundary

All paths are relative to a canonical workspace root. Absolute paths and `..` traversal are rejected. Existing targets are evaluated through symlinks; new targets validate their nearest existing parent. Paths resolving outside the root are denied.

File trees and search skip `.git`, `node_modules`, `vendor`, symlinks, binary data and common credential files. `.env`, private keys and credential stores require a separate permission path that is not exposed to ordinary agent tools.

Docker adds a second boundary: the API only accepts paths beneath `WORKSPACE_ROOT`, and the container sees only the configured bind mount.

The VS Code extension additionally requires Workspace Trust and a local `file:` workspace. Its companion listens on an ephemeral `127.0.0.1` port, receives an exact workspace boundary, and stores state in the extension's private global-storage directory.

Context attachments use the same boundary. A file attachment contains only a relative path until the backend resolves it; traversal, external symlinks, unknown binary formats and known credential paths are rejected. Explicit text/selection attachments must be valid UTF-8. PDF, DOCX and XLSX parsing is local and bounded; ZIP entries have compressed/expanded limits, images are magic-checked and each source receives a SHA-256 digest. Sources are limited to 16 MiB each, images to 8 MiB each/16 MiB combined and all sources to 32 MiB. Extracted/redacted text is capped at 256 KiB per item and 1 MiB combined. Raw image bytes are never returned in preview or history APIs. The system prompt tells the model to treat attachments as untrusted evidence, never as authority to change policy or gain tools.

Whole-run preflight is read-only and uses the same context resolver, system-message builder, tool registry and policy engine as execution. Its fingerprint includes the application version, workspace identity/path, normalized task, complete profile, sorted custom-tool definitions and attachment metadata/digests. Content and image payloads are not duplicated into the fingerprint buffer. If a supplied fingerprint is stale, `StartRun` stops before persisting a run, creating a provider or starting the agent loop.

Workflow handoffs use the same trust model. A stage receives original attachments only when its definition enables that flag. A previous stage result is redacted, cut on a valid UTF-8 boundary, capped at 256 KiB and inserted as an explicit untrusted context item; it never changes the next profile's system prompt, allowlist or approval policy. The combined child context remains capped at 16 items and 1 MiB. Legacy sequential Workflow stages share a dedicated copy of the project rather than the live workspace; their terminal diff becomes a Change Set. Hub `execute_readonly` uses a separate disposable copy and destroys any writes after returning output.

## Changes

The model cannot write files directly. `propose_patch` records the original contents and SHA-256, and prepares a diff. The user must accept it. Immediately before writing, the current file hash must match; otherwise the operation returns a conflict. Writes use a same-directory temporary file, fsync and rename, with a rollback-safe Windows replacement path.

Before a proposal can reach approval, an existing target must have been inspected in an earlier model turn and the recorded whole-file digest must still match disk at the current Point workspace revision. Complete-content rewrites require a complete `read_file`; exact replacements may rely on bounded `search_code` fragments only when every `oldText` anchor was present in those visible fragments. A search/read and patch emitted together do not count because the model has not seen the result. Missing anchor coverage produces `inspection_scope_required` without discarding still-valid fragments, allowing the model to search for the missing region and combine evidence. New targets require an earlier workspace listing or neighboring-file read. External edits produce `inspection_stale`, discard obsolete observations and unlock a fresh search/read. The index preserves CRLF/LF bytes, attaches the whole-file SHA-256 to every chunk, refreshes on external create/delete/metadata changes, and re-hashes selected files to catch content changes that preserve metadata. The original state captured during proposal preparation is compared with accepted evidence as an additional time-of-check/time-of-use guard. Sensitive, binary and oversized files continue through their stricter existing tool errors rather than being misrepresented as inspected text.

Index retrieval is bounded independently of the model schema: `search_code` rejects empty/oversized budgets and queries above 4096 UTF-8 bytes, while the workspace layer rejects more than 64 expanded meaningful terms. Partial identifier matches contribute at most once per query term and chunk, preventing generated identifier variants from amplifying one fragment without bound. Selection is deterministic, removes overlapping ranges and respects both the requested chunk and character budgets; UTF-8 truncation never cuts a rune.

Dependency metadata is not file inspection. Import extraction is limited to supported source extensions, 256 unique specs per file, eight resolved workspace targets per spec and 20 returned relationships. Resolution operates only over canonical relative paths already present in the safe index; absolute/drive/UNC/URL paths and relative traversal above the workspace are rejected. External package names produce no relationship unless they map unambiguously to an indexed local path. Related records contain no source text and are ignored by `observationTracker`; their SHA-256 is rechecked before return only to prevent stale navigation. A model must perform a normal bounded search/read before a related file can pass the patch guard.

Existing-file patch observations use the target's whole-file SHA-256 rather than invalidating on every unrelated workspace revision. This permits an already-inspected second file to be patched after acceptance of the first file in one multi-file plan. It does not weaken target freshness: a direct edit, accepted patch or executable-tool mutation of that second file changes the digest and produces `inspection_stale` before approval.

Localized patch inputs never identify a replacement by line number alone. Every `oldText` is resolved against the exact current original, must be unique, and cannot overlap another resolved range. Unknown fields, mixed full-content/exact-edit modes, empty anchors, oversized arguments and no-op changes are rejected before the inspection or approval flow. This prevents ambiguous replacements and keeps the approved diff tied to the same hashed original as rollback. Exact-edit diff hunks are derived from resolved ranges rather than an unbounded global comparison.

Approved executable tools can mutate files themselves, so Point brackets `run_command` and user-defined command/process tools with bounded workspace snapshots. Exact small UTF-8 mutations enter the rollback journal under the approving action; binary, oversized and sensitive-file changes retain only a fingerprint and path. Snapshot and journal limits are included in `workspace.changed` evidence, and incomplete coverage raises a warning instead of silently claiming a complete history. The post-execution snapshot runs even after a nonzero exit or cancellation. If that snapshot or durable journal persistence fails after process start, the run fails and cannot produce a successful final answer. Exact rollback bodies stay in the local SQLite store; API/event objects clear the duplicate original/proposed fields and redact displayed diffs.

No code path automatically commits, pushes, deletes branches or rolls back unrelated user changes.

## Commands

Every command approval displays the command, working directory and model-supplied reason. The process starts only after `Allow once`. The working directory must resolve inside the execution sandbox. Interactive/background agent processes are outside the supported scope. Commands have a timeout, share the run context, capture stdout/stderr separately, return an exit code and duration, and cap output before it enters history or model context. The child receives an allowlisted environment; variables whose names indicate keys, tokens, secrets, passwords or credentials are removed.

The desktop filtered-copy sandbox protects the live workspace publication path; it does not change the OS identity of the child process. Command-string network detection remains defense in depth for that compatibility backend and cannot prove process-level egress isolation. The production Docker backend runs executable tools with a single sandbox bind mount, read-only root, non-root identity and resource limits. Deny-all uses no network namespace. A selective policy gives the tool only a unique internal bridge and a locked gateway that permits exact TLS FQDN/port rules after public-address DNS validation, pins the chosen address, checks ClientHello SNI and enforces connection/byte/duration quotas. Direct IP, wildcard, plain HTTP and unlisted traffic fail closed. The run snapshot records the canonical policy digest, and the child cannot mutate it. Exact controls and operating instructions are in [sandbox.md](sandbox.md).

User-created tools support a legacy fixed shell command and a preferred direct-process mode. A process definition fixes the executable and argv templates; the model can fill only declared typed fields. Unknown fields, invalid enums, oversized values, absolute/UNC/drive-relative programs and paths escaping the workspace are rejected before an approval exists. Substitution never reparses a value as shell syntax: every expanded template remains one argv element, and execution uses the OS process API directly. The approval card shows the resolved program and every exact argv element. The builder sandbox exposes the same validation and effective invocation but has no execution or storage call; tests assert a side-effect program is never started. A configured PATH program can still be an interpreter or shell with its own semantics, so all custom tools always require one-time approval and cannot be deleted while any profile, blueprint, or project agent grants them.

Manual Hub execution does not trust a request boolean. The server first stores a
pending grant bound to the current workspace, the complete saved-tool digest and
the canonical exact-arguments digest; only a separate resolve operation can mark
it allowed. The grant expires after ten minutes and is atomically changed to
`consumed` before process creation. A changed tool, substituted arguments,
different workspace, denial, expiration or replay therefore fails closed. The
durable approval keeps only redacted arguments; the caller resubmits raw
arguments at execution and they must match the recorded digest.

The companion UI also offers a direct command box. It is a user-originated terminal action rather than an agent tool: the typed command is executed immediately, still with workspace-bound working directories, timeout and output limits. The VS Code extension itself opens the native integrated terminal for direct interactive work. These human-owned terminal surfaces, including interactive Cursor Agent, are outside the Point process sandbox and must not be represented as having its OS/network guarantees.

Container mode reduces OS reach with a non-root user, `cap_drop: ALL`, `no-new-privileges`, read-only root filesystem and internal-only API/cache ports. Command approval is still mandatory because a shell remains intentionally powerful inside the mounted workspace.

## Secrets

Provider API keys are passed directly to an in-memory provider instance. They are not placed in profiles, run-configuration snapshots, events, SQLite, tool arguments or process environments. The Agent Studio connection check uses the key for one bounded model-list request and returns only model metadata. Provider URLs containing embedded user-info credentials are rejected. Known token/password forms are redacted from events, command output, run summaries and durable patch previews.

Run diagnostics are computed locally from already redacted, persisted artifacts. They do not send history to a provider, execute tools, modify SQLite or infer hidden token counts. The IDE explicitly distinguishes provider-reported usage from unavailable data and presents verification only when a recognized built-in verifier or an allowed owner-designated custom verifier succeeds at the required revision. For structured process results, successful verification requires both `exitCode: 0` and `timedOut: false`; starting a process, receiving output, running `echo`/version commands through the generic runner or masking failures with shell branches is not proof of success.

`providesVerification` is a user trust declaration, not an automatic semantic judgment. The builder warns that such a custom tool must return nonzero on failure. The flag, full definition and profile allowlist are captured together in the immutable run snapshot, and diagnostics use that snapshot rather than mutable live settings.

The agent cannot finish an explicitly verifiable quest by merely claiming that tests passed. The completion gate uses recorded tool results and workspace revision numbers, allows one bounded correction episode, and persists `completion.checked` outcomes for later audit. The correction message contains local evidence only and cannot manufacture a passing result.

Provider retries are bounded to three attempts and only occur before a successful streaming response is consumed. HTTP 408/409/429/5xx and connection-setup failures use exponential backoff with a capped `Retry-After`; authentication, permission and invalid-request errors are never retried. This avoids replaying partial model output or tool calls. Semantic duplicate-call guardrails prevent an already successful action from being executed again against the same known workspace revision.

A workflow accepts a separate transient key per referenced profile, allowing mixed OpenAI-compatible endpoints without writing credentials into the workflow definition or execution snapshot.

The HTTP layer logs method, path, duration and remote address only. It never logs request bodies or headers. It enforces request size limits, JSON content types, origin controls for development and security headers.

## MCP servers and integrations

- A stdio MCP server runs on the owner's machine **outside the sandbox**. It
  starts only after the owner trusts the exact launch digest: transport,
  command, resolved path, args, cwd, open env values and secret names. Any edit
  drops trust. Shell wrappers (`sh -c`, `cmd /c`, `powershell`) are refused, so
  the trust dialog always names the real program.
- The child environment is an allowlist (PATH, HOME/USERPROFILE, APPDATA, TEMP,
  locale, proxy) plus the server's own values; `POINT_*`, `VSCODE_*` and the core
  API token never reach it. The process group dies with the core.
- MCP secrets live in IDE SecretStorage and core memory only
  (`point.mcp.<id>.env|header.<NAME>`); the webview sees names, never values.
  Server stderr and errors pass `security.Redact` (GitLab tokens included) and
  an exact scrub of the injected secret values.
- A remote server is reached over https (http only on loopback), without
  redirects or proxy, through a pinned dialer: public addresses only; a private
  or VPN address only for the one host the owner granted; link-local and
  metadata addresses never.
- A tool whose description or schema changed after approval is disabled until
  the owner reviews it. At stage 1 no MCP tool is given to agents or the Master.
- GitLab window actions (comment, approve, merge, retry) run only on the
  owner's click and are journaled in `integration_actions` with the tool,
  target and outcome; comment bodies are stored as SHA-256 and length only.
  Merge requires a modal confirmation and the head SHA the owner saw.

## Learning and evaluation privacy

Learning signals and Skill outcomes copy only bounded diagnostic facts, redacted
feedback explicitly marked as a Lesson, IDs and digests. The background
reviewer receives neither workspace file bodies nor the final Run response and
cannot grant tools, permissions or network access. Personal benchmark records
store the user-authored task/criteria plus Run IDs, status, health, tool and
verification counters and exact configuration/Skill attribution; they do not
duplicate attachments, source files, model messages or final output.

Benchmark evaluation rejects evidence from another workspace or ProjectAgent,
task mismatches, reused Runs and legacy snapshots without exact attribution.
Canary assignment is limited to the current agent after its own eligible
verified trajectory. Background code may automatically restore the newest
candidate on proven degradation, but cannot promote a Skill across a
Blueprint. Blueprint-wide promotion is a separate modal user action and is
accepted only after a healthy exact-version gate. Rollback lineage is keyed by
the immutable Skill family so an older revision cannot overwrite a newer one.

## Limits and cancellation

Profiles bound steps, total run duration, model context and output allowance. Reads, search results, command output and HTTP bodies are bounded. Rolling context management never edits the durable chronicle and never separates an assistant tool call from its tool response. Old provider-request rounds become deterministic local evidence; only evicted read-only calls may be refreshed, while completed side effects stay deduplicated. `Stop` cancels model I/O, approval waits and child processes through the same context.

## Agent Hub network and supervision (required)

Executable tools still cannot invent egress: deny-all or exact TLS allowlist only
([sandbox.md](sandbox.md)). Product rule for autonomous Hub work: agents do not
widen that allowlist; unconfirmed git remotes and suspicious downloads stop and
escalate to the Orchestrator, which asks the user. The Orchestrator must also
periodically inspect active agents so hang loops fail closed with a human decision
rather than only after local identical-plan stall. Full contract:
[AGENT-HUB-ORCHESTRATOR-NETWORK-POLICY.md](AGENT-HUB-ORCHESTRATOR-NETWORK-POLICY.md).
Until that runtime lands, treat long unattended URL-intake as not ship-ready.
