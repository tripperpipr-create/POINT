# Agent Hub acceptance suite (2×10)

Ten fixed tasks, two passes each on a real model. Threshold: ≥8/10 per pass,
zero false completions, brief expansions, grant bypasses, or lost checkpoints.

## Tasks

| # | Kind | Prompt (fixed) | Pass if |
|---|------|----------------|---------|
| 1 | read | Summarize the purpose of `internal/domain/task_brief.go` without editing files | Accurate summary, no Change Set |
| 2 | bug | Fix the intentional failing test in fixture `acceptance/fixture-bug` | `go test` green in sandbox |
| 3 | test | Add a unit test for `domain.WithEffectiveModel` behavior already in tree | New test passes |
| 4 | cross-file | Rename a helper used in two packages in the fixture | Both call sites updated, tests green |
| 5 | review | Review a provided patch for a secret leak; do not apply | Names the leak; no Apply |
| 6 | recover | Continue after a forced pause (`active_time_exhausted`) with one extend | Resume succeeds once |
| 7 | sequential | Two-stage flow: write file then verify | Handoff evidence present; verified |
| 8 | parallel | Two writers + merge | `mergedResultVerified` set; quest not falsely verified without merge |
| 9 | precise | Return only a Go `Clamp` function in chat, no tools | precise brief, no writeFiles |
| 10 | project | Broad personal app without requirements | Stays in discussion / asks questions; no silent start |

## Recording

For each pass write one JSONL line to `.tmp/hub-acceptance-<model>-passN.jsonl`:

```json
{"task":1,"ok":true,"seconds":12.3,"tokensIn":100,"tokensOut":40,"interventions":0,"falseComplete":false,"notes":""}
```

## Commands

```powershell
# Structural readiness (no live model):
go test ./internal/app ./internal/agent ./internal/flowruntime ./internal/orchestrator ./internal/domain -count=1
cd vscode-extension; npm run build; node ../scripts/run-hub-smokes.mjs

# Live model (opt-in; set POINT_ACCEPTANCE_MODEL or POINT_INTAKE_TEST_MODEL + Ollama):
$env:POINT_ACCEPTANCE_LIVE="1"
$env:POINT_ACCEPTANCE_MODEL="qwen3.5:9b"
node scripts/run-hub-acceptance.mjs --pass=1
node scripts/run-hub-acceptance.mjs --pass=2
```

Fixtures: [`acceptance/`](../acceptance/README.md). Live suite: `TestHubAcceptanceLiveSuite` in `internal/acceptance`.

## App-level benchmark

The 2×10 ledger is a regression suite, not evidence that Agent Hub can build an
application autonomously. Coding readiness additionally requires the two cases
in `acceptance/app-benchmarks.json`, each completed twice through Command Center:

1. `greenfield-notes`: UI + API + SQLite + migrations + tests from an empty workspace.
2. `existing-project-feature`: a cross-cutting feature in this repository with
   migration compatibility, UI, API, conflict handling and full verification.

Exported runs must record runtime/model bindings, cost/latency, interventions,
TeamEvents, worktree lineage, merge conflicts, criterion evidence, false
completion and boundary violations. Validate an exported ledger with:

```powershell
node scripts/run-app-benchmarks.mjs --ledger=.tmp/exported-run.json
```

Running the script without `--ledger` intentionally writes `status=not_tested`
and exits non-zero. A missing endpoint, model or credential is always
`NOT TESTED`, never PASS.

Local runtime probe on 2026-09-09 (historical — see the note below):

- Cursor Agent CLI: `NOT TESTED` — executable not installed.
- Codex CLI: `NOT TESTED` — executable not installed.
- Claude Code CLI: version `2.1.263` detected; app benchmark not run.
- Qwen3.6-35B-A3B: `NOT TESTED` — no endpoint/credential supplied to the harness.
- Qwen3.8-27B: `NOT TESTED` — no endpoint/credential supplied to the harness.

> Первые три строки больше не относятся к продукту. 14 сентября 2026 CLI-исполнители
> (`cursor-cli`, `codex-cli`, `claude-code-cli`) сняты: мозг помощника и агента — только
> HTTP-провайдер (`domain.IsHTTPAPIProvider`), а `scripts/run-app-benchmarks.mjs` про эти
> рантаймы уже не знает. Запись оставлена как замер своего числа, а не как описание
> сегодняшнего контура. Cursor Agent CLI как **интерактивный терминал** жив и к этому
> списку отношения не имеет — см. [sandbox.md](sandbox.md).

## Ship gate

Only after both 2×10 passes and two independent passes of both app-level cases
meet the threshold:

1. Backup installed Point user data.
2. Build VSIX / Point package from these sources.
3. Install over the local Point copy.
4. Smoke open Agent Hub window and one precise task end-to-end.
