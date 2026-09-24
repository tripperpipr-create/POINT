# Карта JS-контура

Актуально для Point `1.2.2` на 19 сентября 2026 года. Здесь — кто за что
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

**Группа сообщений** — `handleXxxMessage.call(this, message)`. Однородное
семейство `case`-веток уезжает в модуль, провайдер приходит как `this`. Так
устроены `vscode-extension/master-chat-controller.js`,
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
`vscode-extension/ide-navigation-controller.js`,
`vscode-extension/project-index-controller.js`,
`vscode-extension/console-ssh-controller.js`,
`vscode-extension/point-panels.js`,
`vscode-extension/ide-observation-controller.js`.

Чистые помощники без состояния живут отдельно:
`vscode-extension/extension-utils.js`, `vscode-extension/run-config-utils.js`,
`vscode-extension/ide-navigation-utils.js`, `vscode-extension/ssh-utils.js`.

Отдельно стоит знать про `vscode-extension/core-lease.js`: на нём держится
общее тёплое ядро между окнами Point. Дескриптор, замок запуска и аренды окон —
это то, из-за чего второе окно не поднимает второе ядро и не убивает чужое.
Правило «кого можно гасить» проверяет `scripts/smoke-core-lease.js`:
ошибка в счёте аренд дорога в обе стороны — либо бесхозные ядра никогда
не гаснут, либо у соседнего окна убьют ядро посреди работы.

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

Формы всех поверхностей — двадцать штук — разбирает один
`vscode-extension/ui/client/form-submit.js`. Экранирование живёт в одном месте
(`vscode-extension/ui/client/html-escape.js`), склонение и единицы — в другом
(`vscode-extension/ui/client/format-units.js`). Оба вынесены потому, что уже
расходились копиями, и смоуки экранировали слабее продукта.
Значки интерфейса — встроенные SVG из `vscode-extension/ui/client/ui-icons.js`
(`icon(имя)`); значок декоративен, смысл кнопке дают её `aria-label` и `title`.
Лента разговора с Мастером оформляется одним слоем
`vscode-extension/ui/layers/07c-master-feed.css`.
Полосу идущего квеста над полем ввода считает
`vscode-extension/ui/client/master-quest-strip.js` из наряда v2; подписи
состояний она берёт у карточки наряда (`runtimePresentation`).

## Куда класть новое

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
