# Карта JS-контура

`ui/client/completion-verdict.js` owns evidence-first completion status, pending manual-criterion text and reuse provenance formatting. `agent-work-transcript.js` and `quest-journal-views.js` consume it using copied payloads. Regression smoke: `scripts/smoke-completion-verdict.mjs`; [core/runtime contract](implementation-embedded-runtime.md).

`quest-git-controller.js` owns the quest git actions from the Hub (`questGitAction`, `openQuestGitUrl`): a modal confirmation naming the remote, branch and MR target before push, MR and revert, the Master key only for the git agent's commit message, and opening the GitLab "new MR" page the core returns. It also sends `localAgent.questGit` to the core before each Master turn (`syncQuestGitPolicy`). The webview side is `ui/client/quest-git-views.js`: the work order's "Ветка" section (approval waits for a required choice, `gitChoiceMissing`) and the quest card's git block, re-exported through `master-quest-views.js` because the card modules sit at their line budgets. `master-chat-branch.js` no longer offers a branch by itself; its manual offer takes the base from `GET /api/v2/git/inspect`. Regressions: `scripts/smoke-quest-git-views.mjs`, `scripts/smoke-master-chat-branch.cjs`.

The work order card's image line and launch hold come from `imagePinNote` in `ui/client/master-quest-views.js` (TODO Q17): while the core pins the sandbox image, "Запустить квест" is disabled with «Проверяем образ песочницы…»; a pin failure names `sandbox.imageError` and leaves the button, since approving again retries; a `filtered-copy` backend pins nothing. A failed roster selection shows its reason on the brief panel (`ui/client/master-brief-panel.js`) with «Подобрать снова» (`restaff-master-work-order-v2` → `restaffMasterWorkOrderV2` → `POST /api/v2/work-orders/{id}/restaff`). The Hub decision strip and summary count Flow nodes only of live runs (`runIsLive`), so a closed quest's `waiting_approval` node asks for nothing. Regression: `scripts/smoke-master-work-order-controls.mjs`.

`prepared-diff-controller.js` opens a prepared but undelivered quest file in the IDE diff editor (`openPreparedDiff`): it reads both sides from `GET /api/change-sets/{id}/items/{itemId}/content` and shows them as read-only `point-prepared:` documents. The button and its click handler (`handlePreparedAction`) live in `ui/client/quest-prepared-views.js`; `quest-app-actions.js` only delegates. Regression: `scripts/smoke-quest-run-card.mjs`.

`sandbox-settings.js` owns application-scoped execution settings and the embedded Moby launch environment. It resolves the Moby pack shipped beside the core (`bin/moby/runtime.json`), makes it the default engine when no backend is chosen explicitly, and offers a core restart when an engine setting changes (`watchSandboxSettings`). `extension.js` consumes its result when starting the core; `distribution/apply-overlay.mjs` includes it in delivery. Regression: `scripts/test-point-sandbox-settings.js` checks ignored project overrides, Moby selection, the shipped pack and stale overrides, bounded quotas and refusal of host fallback.

Актуально для Point `1.2.3` на 27 сентября 2026 года. Здесь — кто за что
отвечает в расширении и вебвью. Границы модулей охраняет
`scripts/check-release-contracts.mjs`, но до сих пор нигде не объяснялись: по
списку файлов в `npm run check` видно, что модуль есть, и не видно, зачем он.

## Две среды, одна поставка

`vscode-extension/` — это два разных мира в одном каталоге, и путать их нельзя.

**Хост** — файлы верхнего уровня `vscode-extension/*.js`, CommonJS, исполняются
в процессе расширения. У них есть `require`, доступ к `vscode`, к файловой
системе и к локальному ядру. Их никто не собирает: `distribution/apply-overlay.mjs`
копирует их в staged-расширение поимённо.

**Вебвью** — `vscode-extension/ui/client/*.js`, ESM, исполняются в песочнице
webview. У них нет ни `require`, ни `vscode` — только объект, полученный через
`acquireVsCodeApi()`. esbuild собирает их в один `vscode-extension/media/main.js`;
он и едет в VSIX, а исходное дерево — нет.

Между ними только сообщения. Вебвью шлёт `postMessage({type: …})`, хост
отвечает тем же. Ни одна функция не пересекает границу.

## Хост: три приёма

Внешний разбор сообщений вебвью — `vscode-extension/hub-message-router.js`:
`handleMessage` провайдера проверяет доверие к папке, пишет журнал и зовёт
`routeHubMessage.call(this, message)`, а ветки `switch (message.type)` живут
там. Новое сообщение вебвью требует ветки и в разборе, и в своём контроллере —
затвор `scripts/check-webview-message-routes.mjs` читает именно этот файл.

**Группа сообщений** — `handleXxxMessage.call(this, message)`. Однородное
семейство `case`-веток уезжает в модуль, провайдер приходит как `this`. Так
устроены `vscode-extension/master-chat-controller.js`,
`vscode-extension/master-chat-branch.js` (предложение ветки, Git worktree и переключение чата; в папке без своего Git — worktree каждого вложенного репозитория с откатом при частичном сбое),
`vscode-extension/infra-controller.js`,
`vscode-extension/hub-runtime-controller.js`,
`vscode-extension/roster-controller.js`,
`vscode-extension/learning-controller.js`,
`vscode-extension/tooling-controller.js`,
`vscode-extension/cursor-controller.js`,
`vscode-extension/companion-chat-controller.js`.

**Семейство методов** — провайдер первым доводом, в классе строка-переходник.
Публичная поверхность класса не меняется, поэтому вызовы `this.<метод>()`
изнутри переносить не нужно. Так вынесены
`vscode-extension/git-tool-controller.js` (репозиторий, списки изменений, полка,
цели push, снимок окна инструментов),
`vscode-extension/hub-surfaces-controller.js` (какое окно открыть и куда вернуть
человека), `vscode-extension/hub-polling-controller.js` (три таймера опроса и
правило «не видно — не спрашиваем»),
`vscode-extension/companion-thread-controller.js` (жизнь одного ответа
компаньона), `vscode-extension/core-log.js` и `vscode-extension/core-lease.js`.

**Фабрика с замыканиями** — для поверхностей со своим состоянием:
`vscode-extension/companion-controller.js`,
`vscode-extension/ide-action-controller.js`,
`vscode-extension/ide-run-controller.js`,
`vscode-extension/ide-navigation-controller.js`,
`vscode-extension/project-index-controller.js`,
`vscode-extension/console-ssh-controller.js`,
`vscode-extension/point-panels.js`,
`vscode-extension/ide-observation-controller.js`.

`ide-action-controller.js` оставляет за собой команды IDE и публичные методы
расширения. `ide-run-controller.js` ведёт поиск, выбор и запуск конфигураций,
включая Run Anything и запуск текущего файла; терминал, кэш манифестов и
текущая цель принадлежат его экземпляру.

Интеграции собраны так же, фабрикой:
`vscode-extension/integrations-controller.js` отдаёт два типа сообщений вебвью
(`mcpAction`, `gitlabAction`) своим модулям и больше о предмете не знает.
`vscode-extension/mcp-controller.js` держит секреты MCP в SecretStorage и
передаёт их каждому новому процессу ядра, показывает окно доверия и принимает
импорт `mcp.json`. `vscode-extension/gitlab-controller.js` отвечает окну GitLab
и карточкам MR, открывает diff файлов MR штатным `vscode.diff` над документами
`point-gitlab:` и подтверждает merge. Проекты — отдельный
`vscode-extension/gitlab-project-controller.js`: карточка проекта, клон через
`git.clone` и diff коммита. Ответ уходит только той поверхности,
которая спросила (поле `surface`: окно, карточка `mr:<проект>!<номер>`,
карточка проекта `project:<путь>` или Гильдия).

Чистые помощники без состояния живут отдельно:
`vscode-extension/extension-utils.js`, `vscode-extension/run-config-utils.js`,
`vscode-extension/ide-navigation-utils.js`, `vscode-extension/ssh-utils.js`.

Отдельно стоит знать про `vscode-extension/core-lease.js`: на нём держится
общее тёплое ядро между окнами Point. Дескриптор, замок запуска и аренды окон —
это то, из-за чего второе окно не поднимает второе ядро и не убивает чужое.
Правило «кого можно гасить» проверяет `scripts/smoke-core-lease.js`:
ошибка в счёте аренд дорога в обе стороны — либо бесхозные ядра никогда
не гаснут, либо у соседнего окна убьют ядро посреди работы. Там же отметка
окна `host-<pid>.json`: каждое окно Point обновляет её, пока открыто, а ядро,
запущенное расширением (`POINT_OWNER_DIR`), выходит само, когда свежих
отметок и аренд нет дольше 30 секунд (`cmd/server/owner_watch.go`). Так ядра
не переживают закрытое приложение, даже если оно вышло жёстко.

Поддержку языков по требованию — выбор и установку language server —
держит `vscode-extension/language-support.js`; таблицы языков приходят туда
из `ide-action-controller.js`.

## Вебвью: четыре роли

| Роль | Имя | Что делает |
| --- | --- | --- |
| Отрисовка | `*-views.js` | собирает разметку экрана из состояния |
| Нажатия | `*-actions.js` | `handleXxxClickAction(...) -> boolean`; вернул `true` — слушатель дальше не идёт |
| Приём | `*-inbox.js` | разбирает ответ ядра и меняет состояние |
| Правила | остальные | склонение, экранирование, единицы, композер |

Состояние живёт в `vscode-extension/ui/client/main.js` и раздаётся модулям одним
объектом-бэгом `modularUiState` — около 170 пар геттер/сеттер. Модуль получает
его как `ui` и читает `ui.имя`. Две ловушки видны только в браузере: у части
ячеек есть только геттер (это `const`-коллекции, их меняют через `.add`/`.clear`,
а присваивание бросит TypeError), и `esbuild` молча подставит `undefined`, если
имя не передали вовсе.

Боковую панель квеста и историю изменений файла собирает
`vscode-extension/ui/client/quest-history-views.js`. Карточки предложений
Компаньона — `vscode-extension/ui/client/companion-proposal-views.js`;
подготовку, проверку и сохранение его настроек —
`vscode-extension/ui/client/companion-setup-controller.js`. Эти модули получают
текущее состояние и нужные действия через параметры фабрик; черновики, статусы
запросов и сохранённое состояние панели по-прежнему принадлежат `main.js`.

Формы всех поверхностей — двадцать штук — разбирает один
`vscode-extension/ui/client/form-submit.js`; какой запрос держит какую форму
и какой раздел ждёт ответа — `vscode-extension/ui/client/request-failure-routing.js`.
Снимок фокуса, каретки и прокрутки до перерисовки и возврат после неё —
`vscode-extension/ui/client/ui-snapshot.js`; перетаскивание файлов Git и шагов
workflow — `vscode-extension/ui/client/drag-drop.js`. Экранирование живёт в одном месте
(`vscode-extension/ui/client/html-escape.js`), склонение и единицы — в другом
(`vscode-extension/ui/client/format-units.js`). Оба вынесены потому, что уже
расходились копиями, и смоуки экранировали слабее продукта.
Метку, пришедшую из ядра прописными («КОМАНДА»), к обычному регистру приводит
`sentenceLabel()` оттуда же, из `format-units.js`.
Оболочку окна Хаба — рейку разделов, шапку и строку вкладок подраздела — рисует
`shell()` в `vscode-extension/ui/client/quest-runtime-views.js`. Разделы и их
вкладки перечислены там же в `HALL_SECTIONS`; новая вкладка раздела — одна
строка в его `subtabs`, а не кнопка «перейти» на чужой странице. Название
раздела в шапке даёт `hallCrumb()` из `vscode-extension/ui/client/hall-onboarding-views.js`.
Значки интерфейса — встроенные SVG из `vscode-extension/ui/client/ui-icons.js`
(`icon(имя)`); значок декоративен, смысл кнопке дают её `aria-label` и `title`.
Лента разговора с Мастером оформляется одним слоем
`vscode-extension/ui/layers/07c-master-feed.css`.
Разметку ленты собирают несколько модулей, у каждого своя забота:

- `vscode-extension/ui/client/master-stream-view.js` — ход, который ещё идёт.
  Фазы: ожидание, след, текст, «оседание» до прихода истории, сбой. События
  хода правят один блок `[data-master-stream]`, а не всю ленту;
- `vscode-extension/ui/client/master-trail.js` — след хода: строка-сводка и
  раскрываемый перечень. Построитель общий у идущего и готового хода, поэтому
  на финише ответ не прыгает;
- `vscode-extension/ui/client/master-feed.js` — замена ленты, поиск по
  разговору и запас под карточкой ввода;
- `vscode-extension/ui/client/master-feed-motion.js` — какие узлы входят с
  движением: только новое в хвосте ленты;
- `vscode-extension/ui/client/master-compose-keys.js` — клавиши композера:
  очередь реплик во время хода, «↑» в пустом поле, команды «/» и Enter.
  Команда «/» только нажимает уже существующее действие, своей логики у неё нет.

Ответ модели разбирает `vscode-extension/ui/client/companion-markdown.js`. Вывод
модели недоверенный, и у разбора три правила:

- текст экранируется ровно один раз, до сборки тегов;
- ссылкой становится только `http(s)://`;
- служебные символы разбора вычищаются со входа, чтобы их нельзя было подделать.

Точный список тегов с атрибутами держит `scripts/lib/chat-markup.cjs`, а сверяет
`scripts/smoke-chat-markup-escaping.js`. Новый тег в разборе без правки этого
списка роняет смоук — так и задумано.
Прогон квеста в ленте Мастера собирает
`vscode-extension/ui/client/quest-run-views.js`: строку с полосой этапов,
вердикт, действия, условия с доказательствами и свёрнутые разделы итога. Журнал
идущего этапа — `vscode-extension/ui/client/quest-journal-views.js`, его отдаёт
общий `agentWorkTranscriptHtml` по просьбе `journal`. Русские имена этапов —
`vscode-extension/ui/client/stage-labels.js`, тона и счёт diff —
`vscode-extension/ui/client/diff-view.js`, оформление —
`vscode-extension/ui/layers/08b-quest-run.css`.
Блок доставленного приложения и строку итогового отчёта рисует
`vscode-extension/ui/client/quest-app-views.js`; их живое состояние (ответы
хоста `masterApplicationState` и `masterReportState`) держит
`vscode-extension/ui/client/quest-app-state.js`, нажатия разбирает
`vscode-extension/ui/client/quest-app-actions.js` (там же повтор проваленного
этапа как есть или по разрешённому предложению Мастера и кнопка
«Разобрать с Мастером»). Разобранный провал этапа — причину каждой проверки,
авто-повтор и карточку разрешения правки проверки — рисует
`vscode-extension/ui/client/stage-failure-views.js`; строку «Проверки Point
перед приёмкой: N из M прошли» из `runtime.preAcceptCheck` —
`vscode-extension/ui/client/pre-accept-views.js`; повтор без человека по
решению ядра и разбор провала Мастером запускает наблюдатель наряда
(`stageFailureAutopilot` в `vscode-extension/master-work-order-watch.js`). Вид приложения, способ
запуска и вывод `docker compose` отдаёт ядро: `GET
/api/v2/master/quests/{id}/application` (`internal/app/delivered_app_state_v2.go`).
Блок «Что Point вынес из квеста» у завершённого квеста рисует
`vscode-extension/ui/client/quest-retrospective-views.js`: по кнопке он шлёт
`loadQuestRetrospective`, ответ хоста (`questRetrospective`) принимает
`run-inbox.js`, оформление — `vscode-extension/ui/layers/08c-quest-retrospective.css`.
Разбор собирает ядро без модели: `GET /api/v2/master/quests/{id}/retrospective`
(`internal/app/quest_retrospective.go`).

Полосу идущего квеста над полем ввода считает
`vscode-extension/ui/client/master-quest-strip.js` из наряда v2. Подписи,
знаки и тона состояний (`runtimePresentation`), деление квестов на идущие,
ждущие человека и историю (`questPhase`) и отличие квеста человека от этапа
Flow (`isRootQuest`) живут в `vscode-extension/ui/client/quest-status.js`:
его читают полоса, карточка наряда, список квестов проекта, «текущий квест» и
счётчики. Короткие заголовки квестов и запусков —
`vscode-extension/ui/client/quest-titles.js`.

Поток хода Мастера (`vscode-extension/master-turn-stream.js`) и наблюдатель
наряда (`vscode-extension/master-work-order-watch.js`) принадлежат миру, в
котором начались: `projectScope` глушит их ответы после смены проекта, а
`forgetProjectFollowers` в `afterProjectSwitch` двигает эпоху. Подпись
состояния, по которой `postState` решает, рассылать ли снимок вкладкам, —
`vscode-extension/hub-state-signature.js`. Правая панель
разговора — вкладки «Квест», «Команда», «Контекст»: квест собирает
`vscode-extension/ui/client/master-brief-panel.js`, команду и контекст —
`vscode-extension/ui/client/master-inspector.js`, оформление —
`vscode-extension/ui/layers/07d-master-inspector.css`.

Интеграции в вебвью — один модуль состояния, нажатия своих MCP-серверов и
четыре вида над ними:

- `vscode-extension/ui/client/integrations-ui.js` — состояние окна GitLab,
  карточки MR, общей страницы и вкладки проекта, приём ответов хоста, нажатия
  и черновики полей. Черновик живёт в модуле, а у поля есть стабильный id,
  поэтому фоновая перерисовка не теряет набранное и фокус. Статус плагина
  (`pluginStatus`, общая страница) и статус проекта (`status`, окно и вкладка
  проекта) лежат раздельно; смена `workspacePath` в `state` сбрасывает
  GitLab-состояние прошлого мира. main.js знает о модуле пять строк;
- `vscode-extension/ui/client/mcp-server-actions.js` — нажатия по своим
  MCP-серверам и разбор их формы; состояние общее с `integrations-ui.js`;
- `vscode-extension/ui/client/integrations-views.js` — Общие настройки →
  «Интеграции и MCP»: плагин GitLab со строкой «Этот проект», свои
  MCP-серверы, инструменты с риском, форма, импорт, журнал;
- `vscode-extension/ui/client/gitlab-project-view.js` — Гильдия → «GitLab»:
  связь текущего проекта с GitLab тем же редактором, что в шапке окна;
- `vscode-extension/ui/client/gitlab-common.js` — общее для экранов GitLab:
  значки, слова статусов, время, инициалы (`glAvatar`), сбой (`problemHtml`)
  и вердикт (`verdictHtml`): каждый экран GitLab начинается с фразы «можно ли
  и что мешает», причины — словами и ссылками на свой экран;
- `vscode-extension/ui/client/gitlab-views.js` — окно GitLab в регистре окна
  Git (`nc-*`): списки MR и пайплайнов, редактор связи;
- `vscode-extension/ui/client/gitlab-mr-views.js` — карточка MR вкладкой
  редактора; описание и заметки проходят `companion-markdown.js`;
- `vscode-extension/ui/client/gitlab-project-actions.js` — состояние, ответы
  хоста и нажатия проектов: раздел «Проекты» окна и карточка проекта;
- `vscode-extension/ui/client/gitlab-projects-list.js` — список проектов
  окна с поиском, сгруппированный по доступу;
- `vscode-extension/ui/client/gitlab-project-card.js` — карточка проекта
  вкладкой редактора: вердикт о локальной копии, README, файлы, коммиты,
  ветки.

Оформление — слой `vscode-extension/ui/layers/96a-integrations.css`. Смоуки
`scripts/smoke-mcp-integrations.js`, `scripts/smoke-gitlab-tool-window.js` и
`scripts/smoke-gitlab-projects.js` гоняют собранный `media/main.js` через общий стенд
`scripts/lib/webview-harness.js`.

## Куда класть новое

### CSS вебвью

`vscode-extension/ui/build.mjs` собирает `ui/tokens.css`, затем все
`ui/layers/*.css` в алфавитном порядке. Порядок имён является порядком каскада.
`media/style.css` получается сборкой, его не правят вручную.

Чертог разделён на `05-hall.css` (каркас и разговор),
`05a-hall-controls.css` (кнопки и панели) и `05b-hall-decisions.css`
(разбор решения и адаптивная раскладка). Композер Мастера —
`07a-master-compose.css` и `07a-master-compose1-controls.css`.
Окна инструментов — `96-tool-windows.css` (общий каркас),
`96-tool-windows1-git.css` (Git) и `96-tool-windows2-layout.css`
(узкая рейка и широкая форма). Число в имени двух последних частей сохраняет
их место до `96a-integrations.css`.

Проверка пространства имён считает три части Чертога одним логическим слоем:
совпадения классов между ними существовали до разделения. Коллизии с остальными
слоями по-прежнему останавливают сборку.

- Новая ветка нажатия — в `*-actions.js` своего семейства, не в общий
  обработчик `main.js`.
- Новая группа сообщений хоста — свой контроллер плюс два списка: контракт
  границ в `scripts/check-release-contracts.mjs` и список копируемых файлов в
  `distribution/apply-overlay.mjs`. Пропустить второй — получить «Cannot find
  module» в собранной IDE, которого не покажет ни один тест. Синтаксис
  добавлять куда-либо не нужно: `scripts/check-js-syntax.mjs` обходит каталоги.
- Новый общий помощник — в существующий дом (`html-escape.js`,
  `format-units.js`, `view-runtime.js`, `ui-icons.js`), а не рядом с местом вызова.

## Чем это проверяется

`scripts/check-webview-exports.mjs` сверяет имена на стыке модулей вебвью тремя
правилами: импорт против экспорта, состояние `main.js` только через `ui.` и
вызов функции, которая не приходит ни параметром, ни импортом. Последнее
не ловит ни `node --check`, ни сама сборка: esbuild подставит `undefined`, а
падение дождётся человека в браузере.
`vscode-extension/ui/contracts.mjs` читает и вебвью, и хост целыми каталогами,
поэтому переживает перенос функции между файлами.
`scripts/check-release-contracts.mjs` держит бюджеты строк и контракт границ.

Проверки, читающие `vscode-extension/extension.js` по имени файла, при переносе
слепнут: перестают что-либо находить и падают не на дефекте, а на переезде. Для
них есть `scripts/lib/extension-host-source.js` — хост одним текстом. Часть
смоуков на него ещё не переведена.

## Master and POINT chat scope (2026-10-01)

`vscode-extension/master-scope.js` binds workspace/conversation identifiers and follows local Fast runs, posting the run with its chronicle (without `model.streamed` events). `master-fast-settings.js` edits the global system profile. `master-chat-controller.js` owns scoped session operations, chat files and explicit continuation in a project. `ui/client/master-fast-run.js` renders the local run as a `hall-work` card: localized status, Stop, pending approvals and the shared `agent-work-transcript.js` chronicle, whose tool rows show the command or path and the agent's reason (smoke: `scripts/smoke-master-fast-run.js`); `master-session-views.js` exposes auto/discuss/plan/fast modes. These host modules are included by `distribution/apply-overlay.mjs`; client modules are built into the Hub bundle.

## Unified Git workspace (2026-10-03)

`git-workbench-controller.js` sends local mutations to the core and `git-index-patches.js` builds user-selected hunk/line patches. `git-workspace-controller.js` owns the common editor tab, connection/remote dialogs and provider requests; `git-review-editors.js` owns native review diffs, comment threads and CI documents. `ui/client/git-workspace-ui.js` renders the shared history/review/checks views and stores drafts by repository and review identity. Existing `git-views.js` / `git-actions.js` render the compact index panel. Host modules are listed in the overlay manifest; UI modules and CSS layers are compiled from sources. See [git-workspace.md](git-workspace.md).
