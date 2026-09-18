# Lifecycle слоёв совместимости

Документ описывает поддерживаемые compatibility-paths Point `1.2.2`, их
замены, локальную телеметрию и обязательные критерии удаления. Наличие нового
API само по себе не делает старый путь безопасным для удаления.

## Инвентарь

| Слой | Поддерживаемый сценарий | Каноническая замена | Телеметрия |
| --- | --- | --- | --- |
| API `AgentProfile` | сохранение/удаление старым клиентом; fallback запуска без `ProjectAgent` | `AgentBlueprint` + проектный `ProjectAgent` | save, delete и фактический StartRun fallback |
| Последовательный `AgentWorkflow` | сохранение, удаление и запуск упорядоченных стадий | `FlowGraph` + `FlowRun` | save, delete и фактический run |
| `CustomTool(kind=command)` | одна фиксированная shell-строка | `CustomTool(kind=process)` с фиксированным program/argv и типизированными параметрами | save и фактическое выполнение после approval |
| Run configuration `v0/v1` | просмотр сохранённой истории старого Run | immutable run configuration `v2` с exact digests | открытие Run Details; обычный background replay не считается пользовательским обращением |
| Таблица `profiles` | данные версий до Blueprint | таблица `agent_blueprints` | не является usage-событием; покрывается миграционным gate |

Канонический Code-OSS клиент пока вызывает часть профильных и workflow API.
Следовательно, эти строки ожидаемо показывают ненулевое использование и сейчас
не являются кандидатами на удаление. Wails — диагностический клиент и не задаёт
требования feature parity.

## Что сохраняется

Migration 29 создаёт `compatibility_usage` и хранит один агрегат для комбинации:

- стабильный ключ capability;
- workspace или общая библиотека;
- точная версия Point;
- версия старого формата;
- count, first seen и last seen в UTC.

В таблице конструктивно нет колонок для пути проекта, prompt/task, ID или имени
агента, ID/команды/аргументов tool, model/provider, содержимого файла и секрета.
Случайный локальный workspace key нужен только для SQL-изоляции. Публичный
Statistics удаляет его и объединяет совпавшие feature/version rows перед
возвратом ответа. Записи локальны: автоматической отправки на сервер нет.
Statistics показывает исходные счётчики и временные границы, а не синтетический
рейтинг.

Операции старого API и legacy executions работают fail-closed, если usage-факт
нельзя записать. Read-only preview не считается выполнением и не увеличивает
счётчик запуска. Межпроектная выборка возвращает только текущий workspace и
агрегаты общей библиотеки.

## Representative field validation

`distribution/legacy-field-validation.json` неизменно требует минимум пять
реальных проектов, три stack family, 200 полностью terminal Runs и два
последовательных завершённых релизных окна. Каждый проект обязан присутствовать
в обоих окнах и подтвердить upgrade, backup/restore, чтение legacy profile,
workflow и Run history, канонический Run, rollback и отсутствие потери данных.
Любой P0/P1 блокирует отчёт.

Сбор является только явным opt-in. В начале и конце окна участник сохраняет
локальный ответ `GET /api/statistics`, заполняет копию
`distribution/legacy-field-metadata.example.json` и запускает:

```powershell
node scripts/create-legacy-field-report.mjs `
  metadata.json statistics-start.json statistics-end.json `
  evidence/project-window.json
```

Collector вычисляет не cumulative total, а delta каждого точного сочетания
feature/applicationVersion/legacyVersion. В итог не копируются другие поля
Statistics. `projectNonce` — случайный 64-hex nonce для защиты от повторного
учёта, не hash пути, имени или содержимого проекта. Финальный агрегат nonce не
показывает. Report schema закрытая: неизвестное поле, включая path/name/prompt,
отклоняет весь corpus. Пример намеренно содержит `optInAggregateOnly=false` и
`realProjectAttestation=false`, поэтому не может пройти как evidence.

После двух окон approved reports складываются в отдельный каталог:

```powershell
node scripts/check-legacy-field-validation.mjs D:\PointEvidence\legacy-field
```

Проверка выводит только aggregate counts, stack families, окна, общий Run count,
SHA-256 входного набора и features с нулём обращений в обоих окнах. Последний
список лишь кандидат на removal review: он не отменяет migration, rollback,
client-contract, security и full-test gates ниже. Production workflow требует
абсолютный путь к такому corpus и повторяет проверку fail-closed.

## Миграция без потери данных

Migration 2 впервые копировала `profiles` в `agent_blueprints`. Старый бинарь
мог записать профиль уже после того, как migration 2 была отмечена выполненной.
Поэтому migration 29 повторяет сверку идемпотентно: копирует только отсутствующие
Blueprint ID, никогда не перезаписывает новый Blueprint и не удаляет строку
профиля. После этой сверки дублирующий runtime-copy на каждом startup удалён.

Таблицы и колонки старых форматов пока не удаляются. Migration gate обязан
проверять обновление на копии базы, созданной каждой поддерживаемой схемой, а
также повторный запуск migration. Оригинал базы не используется для испытания.

## Обязательный gate удаления

Compatibility-path можно удалить только когда одновременно доказаны все пункты:

1. Для capability зафиксировано ноль обращений в двух последовательных полных
   релизных окнах на представительном наборе opt-in field reports и upgrade
   fixtures. Одна локальная база и отсутствие удалённой телеметрии доказательством
   не считаются.
2. Канонический Code-OSS bundle и все поддерживаемые adapters больше не вызывают
   старый API; это закреплено contract test и Electron E2E.
3. Для данных существует идемпотентная forward migration, проверенная на копиях
   реальных обезличенных SQLite-баз, включая повторное выполнение и прерывание.
4. Backup/restore и rollback на предыдущую поддерживаемую версию проверены. Если
   downgrade не может прочитать новую схему, release notes и updater обязаны
   восстановить совместимую резервную копию автоматически.
5. В новой версии есть эквивалентный пользовательский сценарий и migration UX;
   ручное редактирование SQLite не допускается.
6. Security/privacy review подтвердил, что новый путь не расширяет tools,
   approvals, secrets, filesystem/network или межпроектные права.
7. Полный Go, extension contract/smoke, migration, installer/update/rollback и
   E2E gate зелёный; открытых P0/P1 по capability нет.

Удаление выполняется отдельным изменением после gate. Сначала отключается код
исполнения, но чтение истории сохраняется; физическое удаление таблиц/колонок
допускается только в более поздней migration после проверенного backup window.

## Автоматические проверки

- `internal/storage/compatibility_test.go` проверяет exact-version aggregation,
  workspace isolation и повторную сверку позднего legacy profile migration 29.
- `internal/app/compatibility_test.go` проверяет реальные instrumentation-paths,
  отсутствие счётчика от preview, объединение внутренних scopes без workspace
  ID и отсутствие пользовательских данных в публичном JSON.
- `scripts/create-legacy-field-report.mjs` строит только exact-version delta;
  `scripts/check-legacy-field-validation.mjs` проверяет 5×2 matrix, три стека,
  200 Runs, lifecycle, P0/P1 и закрытый privacy schema.
- `vscode-extension/ui/contracts.mjs` рендерит Statistics из живого
  `statistics-views.js`; `npm run check` проверяет bundle и UI contracts.
- `scripts/check-docs.mjs` проверяет ссылки и согласованность живых документов.
