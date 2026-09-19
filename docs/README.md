# Документация Point IDE

Этот каталог — входная точка в документацию. Индекс и измеряемые величины
сверялись с исходниками `1.2.2`; у каждого справочника своя дата в шапке —
когда его утверждения последний раз сверяли с кодом. Hub URL-intake и политика
Мастера/сети обновлены 10 сентября 2026. Контур v2 (карточка запуска → шлюз
доказательств → расписка доставки) описан 13 сентября 2026 в
[PROJECT-STATUS.md](PROJECT-STATUS.md), [architecture.md](architecture.md),
[agent-hub-mvp.md](agent-hub-mvp.md) и [api.md](api.md).

## С чего начать

| Задача | Документ |
| --- | --- |
| Установить или запустить проект | [Корневой README](../README.md) |
| Понять архитектуру и границы компонентов | [architecture.md](architecture.md) |
| Работать с HTTP API | [api.md](api.md) |
| Разрабатывать и проверять изменения | [CONTRIBUTING.md](../CONTRIBUTING.md) |
| Увидеть текущее состояние и технический долг | [PROJECT-STATUS.md](PROJECT-STATUS.md) |
| Проверить готовность IDE-сценариев | [IDE-CAPABILITY-MATRIX.md](IDE-CAPABILITY-MATRIX.md) |
| Понять раскладку окна и окон инструментов | [IDE-DESIGN.md](IDE-DESIGN.md) |
| Понять модель безопасности | [security.md](security.md) |
| Проверить threat model и privacy inventory | [threat-model.md](threat-model.md) |
| Настроить изоляцию исполняемых tools | [sandbox.md](sandbox.md) |
| Настроить benchmarks и понять canary gates | [agent-evaluation.md](agent-evaluation.md) |
| Проверить lifecycle legacy-слоёв и критерии удаления | [legacy-lifecycle.md](legacy-lifecycle.md) |
| Сделать backup, migrate-copy, restore или DR drill | [operations.md](operations.md) |
| Проверить большие проекты, историю и desktop SLO | [performance.md](performance.md) |
| Собрать Windows-дистрибутив | [distribution/README.md](../distribution/README.md) |

## Живые справочники

- [AGENT-HUB-IMPLEMENTATION-2026-09-06.md](AGENT-HUB-IMPLEMENTATION-2026-09-06.md) — состояние внедрения Hub (live write с 9 сентября).
- [AGENT-HUB-URL-INTAKE-2026-09-10.md](AGENT-HUB-URL-INTAKE-2026-09-10.md) — URL intake → EvidenceBundle; live PHP gate.
- [AGENT-HUB-ORCHESTRATOR-NETWORK-POLICY.md](AGENT-HUB-ORCHESTRATOR-NETWORK-POLICY.md) — надзор Мастера, git/сеть, эскалация к пользователю.
- [AGENT-HUB-ACCEPTANCE-2x10.md](AGENT-HUB-ACCEPTANCE-2x10.md) — приёмочный suite 2×10.
- [AGENT-HUB-V2-MVP.md](AGENT-HUB-V2-MVP.md) — сокращение контура v2 до MVP:
  что режем, работы по порядку и приёмка.
- [tool-access-layer.md](tool-access-layer.md) — tools/grants/Skills и открытые продуктовые решения.
- [agent-hub-mvp.md](agent-hub-mvp.md) — сущности и гарантии Agent Hub (имя файла
  стабильно; содержание — текущая версия, не только исходный MVP).
- [search-window.md](search-window.md) — ТЗ на своё окно поиска: вкладки,
  источники данных, состояния, клавиши, отклик и порядок проверки.
- [IDE-DESIGN.md](IDE-DESIGN.md) — эталон раскладки: части окна, окна инструментов,
  вкладки нижней панели, всплывающие и клавиши окон.
- [databases.md](databases.md) — SQLite, PostgreSQL и MySQL.
- [ssh.md](ssh.md) — системный OpenSSH и его ограничения.
- [RPG-DESIGN-SYSTEM.md](RPG-DESIGN-SYSTEM.md) — словарь, токены и сборка CSS.
- [VISUAL-LOOP.md](VISUAL-LOOP.md) — воспроизводимая визуальная проверка.
- [DEPENDENCIES.md](DEPENDENCIES.md) — политика и последний аудит зависимостей.

## Исторические материалы

- [AUDIT-2026-09-05.md](AUDIT-2026-09-05.md) — аудит и закрытие A01–A06 (5 сентября); evidence для quality-gate.
- [UI-UX-LOOP.md](UI-UX-LOOP.md) — журнал выполненных UI/UX-циклов. Старые записи
  не являются описанием текущего состояния.
- [POINT-UI-UX-BRIEF.md](../POINT-UI-UX-BRIEF.md) — рабочий бриф, по которому шли
  итерации. Он задаёт намерение, но не заменяет архитектуру, статус или дизайн-систему.
- [vscode-extension/CHANGELOG.md](../vscode-extension/CHANGELOG.md) — история
  релизов; утверждения внутри прошлых версий намеренно не переписываются.

## Источники истины

| Данные | Канонический источник |
| --- | --- |
| Версия Point | `internal/app/app.go`, `frontend/package.json`, `vscode-extension/package.json` |
| Версия Code-OSS и хэши сборки | `distribution/version.json` |
| HTTP-маршруты | пакет `internal/httpapi` целиком: таблица в `server.go` плюс обработчики по семействам. Затвор читает пакет, а не список файлов, — иначе новый файл маршрутов остаётся невидимым |
| Шаги онбординга | `vscode-extension/ui/client/main.js`, `ONBOARDING_STEPS` |
| Команды и хоткеи | `vscode-extension/package.json` |
| Цвета и шкалы | `vscode-extension/ui/tokens.css` |
| CSS-бюджет | `vscode-extension/ui/budget.json` |
| Performance SLO | `distribution/performance-slo.json` |
| Сборочные и smoke-команды | `Makefile`, package scripts, `scripts/` |

Не копируйте измеряемые значения в несколько документов без необходимости.
Если число всё же важно для объяснения, указывайте дату замера и источник.

Документы ссылаются на файлы в `build/` и `.tmp/` — это локальные каталоги
доказательств: отчёты затворов, снимки стендов, журналы прогонов. В репозиторий
они не входят и в свежем клоне отсутствуют. Затвор такие ссылки не проверяет
(`scripts/check-docs.mjs` требует существования только для `internal`, `cmd`,
`scripts`, `distribution`, `.github`, `vscode-extension`, `frontend`, `docs`),
поэтому ссылка на исчезнувший отчёт не упадёт — её достоверность на совести
автора записи.

## Что в `scripts/` и что из этого гоняется само

Каталог смешанный, и по имени файла это не видно. Три рода:

- **В затворах.** `check-*.mjs`, `smoke-*` из `scripts/run-hub-smokes.mjs` и
  перечисленные в `vscode-extension/package.json` → `scripts.check`. Гоняются
  на каждый прогон и на CI.
- **Операторские.** Запускает человек по надобности:
  `ship-hub-extension.ps1` (штатная выкладка VSIX в обе копии),
  `install-vscode-extension.ps1`, `rotate-point-log.js`.
- **Зонды по живому приложению.** Требуют запущенного Point с CDP-эндпоинтом и
  потому не могут быть в затворе: `probe-point-*.mjs`, `diagnose-*.mjs`,
  `verify-point-*.mjs`, `prepare-point-untrusted-workspace.mjs`, а также
  `test-point-*.ps1` и `run-hub-dogfood.mjs`, которым нужна живая модель.

  Десяток `probe-point-*.mjs` — разовые замеры оболочки: где рейка, какой
  высоты заголовок, что происходит со слоями меню. Каждый отвечает на один
  вопрос и после ответа остаётся как способ задать его снова. Запускаются они
  не сами по себе, а через `test-point-workbench-design.ps1 -Probe
  scripts/<имя>.mjs`: тот поднимает окно, ставит сценарий и передаёт зонду
  адрес CDP. Ссылок на отдельные имена в дереве нет и не будет — общее правило
  запуска важнее списка.

  Большинство `verify-point-*` зовёт свой `test-point-*.ps1` — тот поднимает
  окно и передаёт эндпоинт. Два живут без такой пары и запускаются руками
  по адресу CDP (`node scripts/<имя>.mjs http://127.0.0.1:<порт>`):

  - `verify-point-editor-watermark.mjs` — водяной знак пустого редактора и вызов
    Агента с него;
  - `verify-point-safe-mode.mjs` — безопасный режим: полоса, экран версий и то,
    что сквозь него не протекает чужой брендинг и техническая ошибка.

  Названы здесь по той же причине, по которой ведётся список смоуков: на два
  этих файла не было ни одной ссылки во всём дереве — ни из скрипта, ни из
  документа, — то есть найти их можно было только обходом каталога.

Смешение опасно ровно одним: смоук, не попавший ни в один список, гниёт молча.
Так и случилось с `smoke-master-editor-context.cjs` и `smoke-master-stream.cjs`
— оба отстали от контракта v2 и перестали запускаться вовсе. 18 сентября они
починены и внесены в `run-hub-smokes.mjs`. Новый смоук обязан попадать в этот
список сразу: иначе он не проверка, а файл.

## Автоматическая проверка

```powershell
node scripts/check-docs.mjs
node scripts/check-release-contracts.mjs
```

Проверка валидирует локальные Markdown-ссылки, упомянутые скрипты, полный паритет
таблицы API с Go-маршрутами, единую версию core/frontend/extension и число шагов
онбординга в живых документах. Release-contract check дополнительно связывает
production workflow с installer, migration, sandbox, security и performance
gates. Проверки не доказывают смысл текста, поэтому после
изменения поведения всё равно нужно обновить соответствующий справочник.
