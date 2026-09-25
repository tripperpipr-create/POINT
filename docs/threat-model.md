# Threat model и privacy-аудит

Актуально для Point `1.2.2` на 10 сентября 2026 года (база threat matrix —
31 августа; добавлен T16 по Hub egress/git; 25 сентября — T17–T21 по
MCP-серверам и плагину GitLab). Этот документ задаёт
границы доверия, данные, угрозы, действующие контроли и честные остаточные риски.
Он дополняет подробные механизмы в [security.md](security.md) и
[sandbox.md](sandbox.md), но не заменяет их. Продуктовая политика Мастера/сети:
[AGENT-HUB-ORCHESTRATOR-NETWORK-POLICY.md](AGENT-HUB-ORCHESTRATOR-NETWORK-POLICY.md).

## Модель противника и границы доверия

Защищаем пользовательский workspace, локальную историю, секреты, удалённые
системы и право принимать решения от:

- недоверенного содержимого проекта, attachments, вывода команд и ответов модели;
- ошибочного или скомпрометированного provider/model;
- зависимостей и процессов, которые запускает tool;
- другого открытого проекта Point;
- web-origin без API-токена;
- случайной ошибки пользователя, upgrade или повреждения SQLite.

Не считаются изолированными друг от друга процессы того же скомпрометированного
OS-пользователя. Такой процесс обычно уже может читать workspace, профиль Point
и ввод пользователя. Loopback bearer-токен — control-plane credential против
других origin/пользователей, но не security boundary внутри полностью
скомпрометированной учётной записи. Защита от неё требует отдельной OS-account
или VM boundary.

Основные переходы доверия:

1. Code-OSS webview → extension host: CSP, nonce и типизированные сообщения.
2. Extension/frontend → Point Core: loopback или internal Docker network,
   bearer token, JSON-only mutation requests и 2 MiB body cap.
3. Core → provider: только собранный bounded context и transient credential.
4. Core → executable tool: approval, disposable workspace, затем production
   Docker process/network boundary.
5. Core → SQLite: workspace-scoped rows, immutable events и versioned migrations.
6. Core → SSH/DB: конкретный сохранённый profile, destination policy, bounded
   result и отдельное подтверждение мутаций.

## Матрица угроз и доказательств

| ID | Поверхность и угроза | Контроль | Проверяемое evidence | Остаток |
| --- | --- | --- | --- | --- |
| T01 | Tool читает/пишет за пределами проекта | Canonical path, symlink/reparse checks, sensitive denylist, disposable sandbox mount | `internal/workspace/workspace_test.go`, `internal/workspace/reparse_boundary_test.go`, `internal/workspace/hardlink_boundary_test.go`, `internal/sandbox/container_integration_test.go` | Human terminal и Cursor interactive не входят в sandbox |
| T02 | Child process получает host/сеть/секреты или обходит hostname policy | Docker non-root, read-only root, one workspace mount, limits, sanitized env; deny-all `network=none`; selective internal bridge + exact TLS FQDN/port gateway, public-only pinned DNS, SNI match, quotas и cleanup | `internal/egress/*_test.go`, `internal/sandbox/container_test.go`, `internal/sandbox/container_integration_test.go`, `.github/workflows/production-release.yml` | Local compatibility backend не даёт OS/network isolation; разрешённый публичный endpoint остаётся внешней trust boundary |
| T03 | Клиент подставляет `approved:true`, tool или args | Run broker; для manual custom tool — durable 10-minute grant с workspace/tool/args digest и atomic consume | `internal/app/tool_execution_approval_test.go`, `internal/storage/tool_execution_approvals_test.go`, `internal/httpapi/server_test.go` | Любой владелец действующего API-токена может нажать resolve API; это control-plane authority |
| T04 | Replay/TOCTOU после approval | One-time status, expiry, immutable digest, current file/tool hash, atomic SQL update before process | `internal/app/tool_execution_approval_test.go`, `internal/storage/tool_execution_approvals_test.go`, `internal/tools/patch_test.go`, `internal/agent/engine_test.go` | Необратимый внешний эффект процесса нельзя «отменить» одной транзакцией |
| T05 | Prompt injection меняет policy | Attachments/tool output помечены untrusted; tool registry и policy строятся вне prompt; exact allowlist | `internal/attachments/extractor_test.go`, `internal/app/run_enrichment_test.go`, `internal/agent/engine_test.go`, `internal/policy/policy_test.go` | Модель всё ещё может предложить плохое действие, поэтому критические effects требуют решения человека |
| T06 | Provider/API/SSH/DB secret попадает в БД или логи | SecretStorage/transient request, MemorySecrets, env filtering, redaction, response-shape tests | `internal/httpapi/server_test.go`, `internal/security/redact_test.go`, `internal/connections/secret_test.go`, `internal/app/manual_learning_test.go` | SQLite не шифруется приложением; OS volume/backup должен защищаться как чувствительный |
| T07 | Learning сохраняет prompt/source/секрет | Только redacted bounded diagnostics, явный correction/Lesson intent, reviewer без file bodies/final answer | `internal/app/run_learning_test.go`, `internal/app/manual_learning_test.go`, `internal/app/agent_learning_test.go`, `internal/app/learning_eval_test.go` | Пользовательская формулировка Lesson сама является сохраняемыми данными |
| T08 | Memory переносит данные между проектами | Project Memory workspace-scoped; portable запись требует подтверждённого одинакового сигнала из двух workspaces; delete scoped | `internal/app/agent_learning_test.go`, `internal/app/hub_test.go`, `internal/storage/hub_test.go` | Global portable Memory видима проектам сознательно; её content надо считать общим пользовательским знанием |
| T09 | Skill получает новые полномочия или тихо деградирует | Required tools проверяются отдельно, revision/digest attribution, personal benchmark gate, canary, explicit Blueprint promotion, auto rollback | `internal/app/benchmarks_test.go`, `internal/app/skill_canary_test.go`, `internal/domain/run_configuration_test.go`, `internal/app/agent_learning_test.go` | Глобальная Skill library общая; project equipment и runtime allowlist остаются scoped |
| T10 | SSH command/path injection или MITM | Option terminator, single remote argv, shell quoting, bounds, key/agent preference, network host policy, critical exec approval | `internal/servers/openssh_test.go`, `internal/tools/ssh_tools_test.go` | `accept-new` использует TOFU: первый host key надо сверять по доверенному каналу для критичных серверов |
| T11 | DB query меняет production данные | Typed driver, exact connection, read-only default, write flag plus explicit UI confirmation, row/time bounds; passwords memory-only | `internal/tools/sql_tools_test.go`, `internal/dbconn/classify_test.go`, `internal/httpapi/server_test.go` | Manual write confirmation пока не является отдельной OS identity; DB grants должны оставаться least-privilege |
| T12 | Другой проект читает history/Memory/Run/connection | Current workspace ID в application use case и SQL filters; cross-workspace saves/deletes rejected | `internal/storage/hub_test.go`, `internal/storage/compatibility_test.go`, `scripts/smoke-point-multi-project.js` | Blueprints/Skill definitions — намеренно global templates; исходные project bodies в них не копируются автоматически |
| T13 | Web CSRF или неавторизованный loopback caller | Random 256-bit token file, constant-time compare, JSON content type, CORS allow-origin, security headers, nginx same-origin proxy | `internal/httpapi/server_test.go`, `internal/httpapi/decisions_resolve_test.go`, `internal/httpapi/auth.go` | Docker dev token небезопасен при публикации за пределы `127.0.0.1`; его обязательно менять |
| T14 | SQLite corruption/upgrade destroys history | Online backup, integrity/FK check, migrate immutable copy, offline restore with pre-restore recovery point | `internal/storage/maintenance_test.go`, `cmd/point-db/main_test.go`, `scripts/test-point-database-dr.ps1`, `.github/workflows/production-release.yml` | Field corpus требует одобренных обезличенных копий; synthetic fixtures его не заменяют |
| T15 | Supply-chain/installer подмена | Pinned Code-OSS/runtime/container inputs, lockfiles, Go/npm/Trivy vuln gates, sandbox и installer CycloneDX SBOM, manifest SHA-256, Authenticode, clean Git baseline | `.github/workflows/ci.yml`, `.github/workflows/production-release.yml`, `scripts/check-release-contracts.mjs`, `scripts/check-installer-sbom.mjs`, `distribution/sandbox-image-security.json`, `distribution/publish-release.ps1` | Подписанный production artifact ещё должен быть выпущен с защищённым signing key |
| T16 | Агент тянет неподтверждённый git/remote или подозрительную загрузку; зависает без надзора | Deny-all / exact egress; product: confirmed remotes only; else escalate Orchestrator→user; Master watch before engine stall | [AGENT-HUB-ORCHESTRATOR-NETWORK-POLICY.md](AGENT-HUB-ORCHESTRATOR-NETWORK-POLICY.md), `internal/egress/*`, local identical-plan guard in `internal/agent/engine.go` | Runtime Master-watch и structured git allowlist ещё backlog; до внедрения URL-autonomy не ship |
| T17 | Локальный MCP-сервер (npx, docker) действует с правами владельца | Разовое доверие к точному отпечатку запуска, отказ обёрткам оболочки, окно доверия с программой, аргументами и переменными, метка «вне песочницы»; среда ребёнка — белый список без `POINT_*` и `VSCODE_*`; группа процессов гаснет с ядром | `internal/app/mcp_servers_test.go` (`TestMCPServerTrustProbeSnapshotAndDelete`, `TestMCPServerValidationRefusesUnsafeConfigurations`), `internal/mcpclient/stdio_test.go` (`TestChildEnvironmentKeepsOnlyAllowedAndServerValues`), `internal/osproc/*_test.go` | Одобренный сервер не изолирован: доверие — решение владельца. У npx закреплена версия пакета, транзитивные зависимости — нет |
| T18 | «Подмена» инструмента: сервер меняет описание или схему после одобрения и так внедряет инструкции в промпт | Отпечаток инструмента; изменившийся выключается до нового взгляда владельца; окно GitLab вызывает инструменты адаптером без модели | `internal/app/mcp_servers_test.go` (режим `changed` поддельного сервера) | На этапе 1 MCP-инструменты агентам не выдаются вовсе; выдача на этапе 2 — отдельное решение |
| T19 | Удалённый MCP-сервер уводит соединение во внутреннюю сеть или к metadata: SSRF, редирект, прокси | Закреплённый адрес; только публичные адреса, частный — только для узла, названного владельцем; link-local запрещён всегда; без редиректов и прокси; только https, кроме loopback | `internal/mcpclient/http_test.go` (`TestPinAddress`, `TestPinnedDialerRefusesPrivateHostThroughHTTP`, `TestHTTPAuthRedirectAndScheme`) | Разрешённый владельцем внутренний узел остаётся доверенной границей |
| T20 | Импорт чужого `mcp.json` или токен GitLab утекает в базу, журнал, stderr или вебвью | Предпросмотр отказывает обёрткам, устаревшему SSE и учётным данным в URL; секреты уходят в SecretStorage и не возвращаются в вебвью; `security.Redact` для токенов GitLab; кольцо stderr чистится по точным значениям | `internal/app/mcp_servers_test.go` (`TestPreviewMCPImport`), `internal/security/redact_test.go`, `internal/mcpclient/stdio_test.go` (`TestStdioLogIsScrubbed`), `scripts/smoke-mcp-integrations.js` | Значение видно в поле вебвью, пока владелец его вводит |
| T21 | Действие в GitLab от имени владельца без его ведома или merge не той головы | Запись — только по нажатию владельца; merge — после модального подтверждения и с `expectedSha`, GitLab откажет при ушедшей голове; журнал `integration_actions` без текста тела; агентам инструменты GitLab не выданы | `internal/app/gitlab_test.go` (`TestGitLabPluginConnectsAndShowsScreens`), `scripts/smoke-gitlab-tool-window.js` | Запись агентом через очередь решений — этап 2, ещё не построена |

## Privacy inventory и жизненный цикл

| Данные | Где живут | Передача наружу | Удаление/восстановление |
| --- | --- | --- | --- |
| Workspace files | исходный каталог; временная sandbox copy | выбранные bounded fragments — только выбранному provider | управляет пользователь; sandbox copy удаляется после run |
| Run/event/approval history | local SQLite | не отправляется целиком | сохраняется для audit; backup/restore по operations runbook |
| Provider keys | IDE SecretStorage и transient memory | только configured provider endpoint | удаляются/заменяются через IDE SecretStorage; в DB/backup не входят |
| SSH/DB passwords | SecretStorage → transient request/MemorySecrets | только выбранному destination | memory очищается при process stop/delete; SecretStorage отдельно от DB backup |
| MCP secrets (токен GitLab и др.) | SecretStorage → MemorySecrets ядра | только окружению процесса своего MCP-сервера или заголовку его запроса | удаление сервера убирает ссылки из SecretStorage и памяти ядра; в DB/backup не входят |
| Журнал действий интеграций | local SQLite (`integration_actions`) | не отправляется | хранится для аудита; текст комментария не хранится — только SHA-256 и длина |
| Memory | SQLite, project или explicit global scope | входит в model context только при применимом scope | явное scoped delete; rollback learning восстанавливает previous record |
| Skill definitions | global local library; equipment workspace-scoped | инструкции входят в exact run snapshot/provider context | immutable candidates retire/rollback; project equipment можно снять |
| Learning signals/outcomes | workspace-scoped SQLite; cross-project curator читает derived facts | background reviewer получает только bounded redacted evidence | bounded UI queries; raw workspace bodies не дублируются |
| Benchmark cases/evaluations | workspace-scoped SQLite | только если запуск benchmark вызывает обычный provider run | набор редактируется версионно; evaluations сохраняют exact attribution |
| Compatibility telemetry | SQLite aggregate counters | не экспортируется автоматически | без IDs, paths, prompts и args; используется только для removal gate |
| Backups/migration corpus | выбранный оператором путь | только по действию оператора | retention и физическое удаление — ответственность оператора |

Point не выполняет скрытую облачную телеметрию. Provider-вызов является
пользовательски настроенной обработкой данных, а не продуктовой аналитикой.

## Обязательные release-решения

Production candidate запрещён, если выполняется хотя бы одно условие:

- executable agent tools могут попасть на local compatibility backend;
- Docker integration не доказал process/filesystem boundary, deny-all и
  positive/negative controlled-egress cases;
- `approved:true` способен непосредственно запустить custom tool;
- bootstrap/API/log/SQLite test обнаружил secret-bearing field или raw secret;
- workspace isolation, migration corpus, backup/restore либо installer rollback
  не прошли;
- существует незакрытый P0/P1 из security review;
- installer не имеет valid Authenticode signature (кроме явно unsigned local
  candidate, который нельзя публиковать).

Полнота review scope и отсутствие известных незакрытых P0/P1 фиксируются без
непрозрачного score в `distribution/quality-gate.json`. CI и production workflow
запускают `scripts/check-quality-gate.mjs`; contract test отдельно доказывает,
что фиктивный открытый P0 и удалённая область review fail-closed.

## Принятые остаточные риски

- **R1, P2:** системный OpenSSH использует `StrictHostKeyChecking=accept-new`.
  Для production host оператор сверяет fingerprint отдельным доверенным каналом
  до первого Point probe; изменение уже известного ключа OpenSSH блокирует.
- **R2, P2:** SQLite и local backups не имеют собственного application-level
  encryption. Требуются OS account isolation, BitLocker/зашифрованный backup
  target и ограниченные ACL.
- **R3, P2:** один и тот же bearer token авторизует control-plane UI. Он не
  защищает от процесса уже скомпрометированного OS user; не публикуйте порт и
  data directory другим пользователям.
- **R4, P2:** selective egress защищает маршрут и назначение, но не делает
  разрешённый публичный сервис доверенным. Правила должны оставаться узкими;
  compromise разрешённого endpoint или его публичной инфраструктуры остаётся
  внешним риском. Неточные, wildcard, IP и non-TLS правила не поддерживаются.
- **R5, P2:** human-owned integrated terminal/Cursor Agent может выполнять
  команды с правами пользователя. UI и документация обязаны отличать его от
  sandboxed Agent Hub run.
- **R6, P2:** до внедрения Master-watch и confirmed-git escalation автономный
  URL-intake может упереться в identical-plan stall или крутить registry без
  раннего human ask. Это не ослабляет Docker deny-all, но блокирует ship
  claim для URL-autonomy.

Эти риски не дают скрытого P0/P1 в заявленной локальной single-user deployment
model, но любое изменение модели (общий Windows host, удалённый API, командная
установка или новый сетевой протокол) требует нового threat-model review до
релиза.
