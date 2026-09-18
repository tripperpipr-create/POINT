# Agent Hub: URL intake → EvidenceBundle

Дата среза: 10 сентября 2026.  
Канон политики надзора и сети: [AGENT-HUB-ORCHESTRATOR-NETWORK-POLICY.md](AGENT-HUB-ORCHESTRATOR-NETWORK-POLICY.md).

Утверждённый план «URL → автономный прогон → EvidenceBundle» закрыт по контрактам в исходниках; **live PHP URL-intake** — opt-in gate, сейчас **0/2 PASS**.

## Контракты в дереве

| Контракт | Состояние |
| --- | --- |
| Source Intake + SourceBundle | Create/Approve/List, Git/HTML/PDF/DOCX, managed workspace + `point/<slug>` |
| EnvironmentPlan + runtime packs | analyzer + managed images (`point-agent-sandbox:1.2.2`, PHP → `point-agent-sandbox-php:1.3.1`) |
| Host environment controller | `internal/environment/controller.go` |
| Capability coverage gate | catalog tools + runtime; agents provisionable on approve |
| QuestToolLease | issued on ApproveIntake; enforced in `run_execution` |
| Intake lifecycle | preparing/executing/integrating/verifying → completed\|needs_review\|blocked |
| EvidenceBundle | quest finalize; `GET /api/quests/{id}/evidence-bundle`, `GET /api/intakes/{id}/evidence` |
| Contract expansion | `POST /api/intakes/{id}/expand` → awaiting_approval |
| Hub UI | overview panel «Задача по URL» |
| Matrix | `acceptance/app-benchmarks.json`: `php-composer-symfony` + 9 matrix cases |
| PHP harness | structural always; live `POINT_PHP_INTAKE_LIVE=1` + `scripts/run-php-intake-benchmark.mjs` |
| PHP image | `Dockerfile.sandbox-php` → `1.3.1` (attested base — `Dockerfile.sandbox` 1.2.2) |

## Фазы

| Фаза | Статус |
| --- | --- |
| 1 Lifecycle + Evidence | сделано |
| 2 PHP sandbox pack | сделано (`1.3.1`) |
| 3 Live PHP ×2 PASS | **не закрыта** (последний прогон FAIL) |
| 4 Matrix structural | сделано |
| 5 Hub UI + expand | сделано |
| 6 Release / ship | не стартовала |
| Политика Master watch + git/сеть | **runtime v1** (см. ORCHESTRATOR-NETWORK-POLICY); live 2× ещё нет |

## Live gate (после выполнения политики сети/надзора)

```powershell
$env:POINT_PHP_INTAKE_LIVE="1"
$env:POINT_SANDBOX_BACKEND="docker"
$env:POINT_SANDBOX_REQUIRE_STRONG="true"
$env:POINT_SANDBOX_IMAGE="point-agent-sandbox-php:1.3.1"
$env:POINT_PHP_INTAKE_MODEL="Qwen3.6-35B-A3B"
$env:POINT_LLMUX_BASE_URL="https://llmux.ds3.centrofinans.ru/v1"
$env:POINT_LLMUX_API_KEY="<token from Point SecretStorage>"
$env:POINT_LIVE_WORKSPACE="0"
# Defaults: quest 2M tokens / 2h active; max_output 65k; context 128k; steps/duration at caps (100 / 3600s).
node scripts/run-php-intake-benchmark.mjs --pass=1
node scripts/run-php-intake-benchmark.mjs --pass=2
```

Ship только после **2/2** PASS (`.tmp/php-intake-pass1.json` / `pass2.json`), нулевых false completions и зелёной structural matrix.

Модели: **Qwen3.6-35B-A3B** (default) / **Qwen3.8-27B** через LLMux (`POINT_LLMUX_*`), не Ollama.

## Последний live-срез

| Поле | Значение |
| --- | --- |
| Образ | `point-agent-sandbox-php:1.3.1` |
| Модель | `Qwen3.6-35B-A3B` (LLMux) |
| Результат | **FAIL** (~8.5 мин, 2026-09-10 15:37) → ship **0/2** |
| Ошибка | шаг 1: весь `max_output` 65536 ушёл в reasoning (`finish_reason=length`); quest не сделал ни одного tool call; intake `blocked` |
| Ранее | Composer `CONNECT` к `api.github.com`; DNS `--dns 127.0.0.1`; budget 2M; LLMux 405 |
| Исправлено | pin allowlist + GitHub dist hosts; budget 5M; project run сохраняет declared fallback; truncated-reasoning → Qwen3.8; live `reasoning_effort=low` |

## Исходники

- [`internal/app/intake.go`](../internal/app/intake.go), [`intake_evidence.go`](../internal/app/intake_evidence.go), [`intake_expand.go`](../internal/app/intake_expand.go)
- [`internal/environment/`](../internal/environment/)
- [`internal/acceptance/php_intake_test.go`](../internal/acceptance/php_intake_test.go)
- [`vscode-extension/ui/client/intake-views.js`](../vscode-extension/ui/client/intake-views.js)
- [`Dockerfile.sandbox`](../Dockerfile.sandbox) · [`Dockerfile.sandbox-php`](../Dockerfile.sandbox-php)
