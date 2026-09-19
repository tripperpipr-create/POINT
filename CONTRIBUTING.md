# Contributing to Point IDE

## Layout

| Path | Owns |
| --- | --- |
| `internal/` | Local core: workspace sandbox, index, agent loop, Hub use cases, HTTP |
| `cmd/server` | `point-core` entry point |
| `vscode-extension/` | Hub webview, companion routing, IDE commands, core lifecycle |
| `distribution/` | Code-OSS overlay, Point Dark, installer scripts |
| `scripts/` | Gate checks, Hub smokes, operator PowerShell and live CDP probes — four kinds, see [`docs/README.md`](docs/README.md) |
| `vscode-extension/ui/` | Design-system sources: tokens, CSS layers, build |
| `docs/` | Living references, status, dependency policy and historical UI/UX log |
| `frontend/` | Wails diagnostic client (not the canonical UI) |
| `cmd/` | `point-core`, `point-db`, the egress gateway and the performance/soak probes |
| `acceptance/`, `examples/` | Fixture workspaces for the 2×10 suite and language probes |

The JavaScript side has its own map: which host controller owns what, which
webview module renders what, and which lists a new module must be added to —
[`docs/js-modules.md`](docs/js-modules.md).

Start documentation work from [`docs/README.md`](docs/README.md). Historical
changelog and UI/UX journal entries are append-only evidence; update living
references instead of rewriting past observations.

## What the repository stores

Sources only. Everything a build can recreate stays out of Git — see
[`.gitignore`](.gitignore) for the exact rules.

| Not stored (path relative to the component) | Recreated by |
| --- | --- |
| extension: media/main.js | `npm run build:js`, from `vscode-extension/ui/client/` |
| extension: media/style.css, media/rpg-tokens.css | `npm run build:css`, from `vscode-extension/ui/tokens.css` and `vscode-extension/ui/layers/` |
| extension: dist/ (packaged Cursor runtime) | `npm run build:runtime`, from node_modules |
| extension: bin/ (point-core, point-db) | `go build ./cmd/server`, `go build ./cmd/point-db` |
| frontend: dist/, node_modules/ | `npm ci`, `npm run build` |
| repository: build/, .cache/, .gocache/, .tmp/ | build scripts and gate runs |
| repository: .env, data/, *.db | local environment; `.env.example` is the template |

Two consequences worth stating once. A fresh clone has no webview bundle under
`vscode-extension/media/`, so every smoke that reads one runs after
`npm run build`. And a direct edit of a generated file disappears at the next
build — change the source under `vscode-extension/ui/` instead.

Line endings are mixed on purpose and `.gitattributes` disables every
conversion (`* -text`). Release contracts store the sha256 of build materials,
so a checkout that rewrote CRLF would fail the gate on a clean clone.

One consequence: `gofmt -l` is not a gate here. It rewrites CRLF to LF, so it
lists all 33 CRLF Go files whether or not their formatting has drifted — on
19 September exactly one of them had. Format a file through a pipe and put its
own endings back (`gofmt < file`, then restore CRLF if it had CRLF); never run
`gofmt -w` across the tree.

Secrets never enter the tree. `POINT_API_TOKEN` in `.env.example` is a
placeholder for loopback development and must be replaced in any shared
environment.

## Contracts that tests lock

- Компаньон and Мастер are two different system agents. Never merge them.
  Компаньон lives in the IDE, watches diagnostics and terminal outcomes, and only
  recommends — structurally it cannot start work. Мастер is the Orchestrator's name:
  it hands tasks to the party and runs the quest. A blanket rename of one into the
  other has already broken this product once.
- Companion recommends only. Start / Modify / Ignore (or Apply) is required for
  Quest, Agent, Flow and Skill mutations — the guarantee is structural, not a UI prompt.
- Each role keeps its own config and its own model: `CompanionConfig` and
  `OrchestratorConfig`. Onboarding introduces them as two chapters, and Overview
  shows two system-agent cards.
- The Hall rail has six sections: ОБЗОР, МАСТЕР, РЕШЕНИЯ, ИЗМЕНЕНИЯ, КВЕСТЫ, ГИЛЬДИЯ.
  Configuration collapses into ГИЛЬДИЯ; the freed space belongs to supervision.
  Older tab ids still route as sub-views of a section — the nav changed, the screens did not.
- Statistics stays `point.statistics`, a separate IDE window, never a seventh rail section.
- `GET /api/decisions` owns the decision queue and ships each item's own resolution path.
  The webview forwards what the server gave it; it must not build routes itself, and
  `extension.js` validates every path against a closed allowlist before calling core.
- Компаньон is embedded in the IDE — sidebar, dock and peek — and its chat lives there.
  The Guild belongs to Мастер: no companion rail, no companion chat on Hub surfaces.
  The companion's *settings* may open from the Guild; its *dialogue* may not. Both
  render from `companionChatHtml`, so the boundary is asserted by rendering the setup
  route in `smoke-agent-studio-ui.js`, not by reading the call site.
- Core names the step that fixes a blocker (`block("tools", …)`); the webview never
  infers it from the wording of the reason. `ui/contracts.mjs` checks that every step
  the core can emit exists as a real form step, because neither language's tests can
  see that seam alone.
- An unconfigured Мастер is a state (`configured: false`), never an error. A red
  «request_failed» tells the user something broke; what they need is an invitation
  to configure the dispatcher.
- Opening the Мастер dialogue is a focused mode: the rail and the other switchers
  collapse to a single way back, because a conversation with the dispatcher is work,
  not another tab.
- Smoke-critical Russian strings stay intact: «Думает…», «КОМПАНЬОН», «Подготовить исправление», «Открыть файл», «Так компаньон будет вести себя», «Сейчас:», «запуск:», «отладка:», «Журнал изменений», «СИСТЕМНЫЕ АГЕНТЫ», «Подробная статистика», «ГРАНИЦА SANDBOX».
- Action buttons use `data-action`, not `data-actions`.
- Worlds are isolated by folder. Bootstrap, usage, change sets and runs must not leak across `workspaceId`.

- The onboarding wizard keeps its own height: `.hall-body` is a flex column, the
  step panel scrolls, and «Назад/Дальше» is a footer row that never leaves the
  screen. A step that renders without reachable controls is a bug, not a long step.
- A locked onboarding step must say why it is locked when clicked. Silence reads
  as breakage — `scripts/smoke-onboarding-wizard.js` runs this on a clean state,
  because the shared UI smoke has already finished half the wizard by then.
- Panel width is not window width: at a 1100px window the step panel is 636px and
  its left column 347px. Viewport media queries do not see that, so size controls
  with flexible minima rather than breakpoints.

- Данные, запрашиваемые во время отрисовки, кэшируются по ключу сущности, а не в
  одной ячейке. Одна ячейка на несколько профилей дала бесконечный цикл
  «отрисовка → запрос → ответ → отрисовка»: интерфейс был занят собой и не
  отвечал на нажатия, а каждый агент показывал годность последнего ответившего.
  Ответ обязан возвращать ключ запроса (`extension.js` его эхо-отправляет).
- Установленное приложение живёт в `%LOCALAPPDATA%\Programs\Point`, а не в
  `.cache/VSCode-win32-x64`. Выкладка не в ту копию выглядит как «починка не
  помогла».

- Несостоявшаяся связь с провайдером — состояние (`connected: false` плюс
  `problem` и `fix`), а не ошибка запроса. «provider connection failed: dial tcp»
  — сообщение про сокет, из которого не следует ни одного действия. Ответ 200 с
  `connected:false` обязывает вызывающего проверять флаг: иначе подключение
  сохранится как рабочее при неудачной проверке.
- Пустой ростер не заканчивает разговор с Мастером. Он предлагает роль под задачу
  (`hire`) и объясняет выбор; найм остаётся решением человека. Одинаковый ответ
  на разные сообщения — признак того, что ветка снова стала заглушкой.

- Первый запуск — два шага: подключение (или локальный движок Point) и правила
  Мастера. Companion, постоянные агенты, навыки, разрешения и индекс доступны
  после запуска, но не блокируют первый запрос пользователя.
- Шаг настройки задаёт один вопрос. Второй шаг сохраняет конфигурацию атомарно
  и сразу открывает основной чат Мастера.

- Панель компаньона справа объявляется ключом `secondarySidebar`. Оболочка
  принимает ровно три ключа `viewsContainers` и объявляет
  `additionalProperties: false`: неизвестный ключ не даёт ошибки, он молча
  отбрасывается. Проверяется в `ui/contracts.mjs`, потому что ни один тест
  расширения такого не увидит.

- Хаб обязан держаться на неполных данных: квест без названия и отряда, ссылка на
  несуществующего агента, `null` вместо списка. `scripts/smoke-hub-degenerate-data.js`
  прогоняет девять разделов на таких состояниях и падает, если в разметку попало
  `undefined`. Аккуратные фикстуры этого не показывают.
- Сравнение вычисленных стилей до/после делается только на прогретых страницах:
  первая загрузка систематически даёт размеры на 1–2px меньше. Порядок замера
  описан в `scripts/build-css-diff-pair.js`; без замера уровня шума ложные
  расхождения неотличимы от настоящих.

- Очередь решений обязана включать статус `waiting_approval` — и для прогонов
  агента, и для прогонов флоу. Движок выставляет его ровно тогда, когда просит
  разрешения и блокируется; отбор только по `running`/`paused` пропускал именно
  ждущих человека, и работа замирала молча при пустом экране очереди.

- Русское склонение по числу живёт в `internal/textutil` — одно правило на всё
  ядро. Оно было в двух копиях и отсутствовало в третьем месте: очередь решений
  описывала набор из одного файла как «1 файлов». Текст, собранный в ядре,
  интерфейс уже не исправит.

- Ошибки внешних систем — недоверенный текст. `LastError` подключений, баз и SSH
  сохраняется и показывается на экране «Связи», где обещано, что секрет наружу не
  уходит: перед сохранением он проходит `security.Redact`. Правило вычистки
  покрывает и учётные данные внутри адреса (`postgres://user:пароль@host`).
  Оговорка: настоящей утечки продемонстрировать не удалось — драйвер в проверенном
  сценарии пароль в ошибку не кладёт. Это защита незакрытого пути, а не починка
  доказанного дефекта.

## Local checks

PowerShell does not accept `&&`. Use `; if ($LASTEXITCODE -eq 0)`.

`media/style.css` and `media/rpg-tokens.css` are build artifacts. Edit `ui/tokens.css`
or a file under `ui/layers/`, then rebuild — direct edits are overwritten.

Команды живут в `Makefile`, и цели там названы по job'ам CI — по красному job'у
сразу видно, что запускать у себя:

| Цель | Job | Что внутри |
| --- | --- | --- |
| `make test-docs` | `docs` | три затвора документации и реестр качества |
| `make test-go` | `go` | `go vet`, `go mod verify`, `go test ./... -count=1` |
| `make test-frontend` | `frontend` | сборка диагностического клиента |
| `make test-extension` | `extension` | весь JS-контур: сборка ядра, CSS/JS/runtime, смоуки Хаба |
| `make test` | все четыре | то же, что прогон CI целиком |
| `make test-race` | отдельный шаг job `go` | детектор гонок; требует CGO и gcc в PATH |
| `make loop` | нет | живой прогон: лёгкая правка в настоящем проекте от реплики до зелёных тестов |

Раньше этот список был здесь копией и успел разойтись и с `Makefile`, и с
`npm run check`: в нём перечислялись поимённо смоуки, которые давно входят в
общий прогон. Список команд в двух местах — это два ответа на вопрос «чем
проверить», и один из них всегда устаревает.

`make test-race` в `make test` не входит намеренно: без gcc в PATH он падает на
сборке, и этот отказ читался бы как отказ тестов. В CI он есть — ubuntu-раннер
gcc несёт.

`make loop` не входит ни в `make test`, ни в CI, и не должен: он требует живой
модели, а затвор обязан работать в чистом клоне без сети. Это полигон
повседневной доводки, а не проверка — круг стоит полминуты, и на нём видно то,
чего не видит ни один тест: сколько шагов агент тратит впустую, где встаёт цикл,
что человек читает вместо объяснения. Без ключа он проверяет только правила
вердикта и выходит с нулём; чтобы включить живой прогон, нужен `.env.local`
(закрыт правилом `.env.*` в `.gitignore`):

```
POINT_LIVE_LOOP=1
POINT_LLMUX_API_KEY=<ключ>
```

`make loop RUN=2` гоняет второй круг. Полигон закрыт не успехом, а
воспроизводимостью: пока два прогона подряд дают разные ответы, чинить нечего —
вы будете гоняться за шумом. Устройство и находки — в
[`docs/PROJECT-STATUS.md`](docs/PROJECT-STATUS.md).

Before a release also run production npm audits and `docker compose config`;
the canonical list is in
[`docs/IDE-CAPABILITY-MATRIX.md`](docs/IDE-CAPABILITY-MATRIX.md).

## Documentation contracts

- Do not hand-copy application versions: core, frontend and extension must agree.
- `docs/api.md` lists every route registered by `internal/httpapi`; adding a
  handler requires adding its table row in the same change.
- Onboarding counts come from `ONBOARDING_STEPS` in
  `vscode-extension/ui/client/main.js`.
- Measured line/file/route counts belong in `docs/PROJECT-STATUS.md`, include a
  date, and must be remeasured when the status document is refreshed. Route
  counts cover the whole `internal/httpapi` package, not `server.go` alone.
- A change to the v2 contract (WorkOrder fields, evidence gate, delivery,
  source snapshots) updates `docs/api.md`, `docs/architecture.md` and the
  guarantee list in `docs/agent-hub-mvp.md` in the same change.
- `node scripts/check-docs.mjs` is required before merge.

## Installer

Only rebuild when asked. Portable binary: `.cache/VSCode-win32-x64/Point.exe`.

```powershell
.\distribution\build-code-oss.ps1 -SkipInstall -InstallerOnly -PythonPath C:\Python313\python.exe
```

Use the actual local Python path; do not commit a user-profile-specific path.

## Security defaults

- Paths: reject `..` segments; keep filenames like `notes..md`. Resolve apply targets through `workspace.FS` (symlink-safe).
- Custom tools inherit the profile `network` policy. Empty policy is `DENY`.
- `POST /api/usage` forces the current world and ignores client `costCents`.
- Index rebuild after Stop must pass `allowStart: false` so a dirty-file watcher cannot restart the core.
