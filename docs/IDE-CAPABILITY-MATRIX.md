# Матрица готовности Point IDE

Актуально на 18 сентября 2026 (шапка пересмотрена; строки таблицы датированы 26 августа, их повторная проверка — отдельная работа). Статус **готово** означает не наличие кнопки, а
исполняемый пользовательский маршрут и автоматическую регрессию. Все пути
ограничены выбранным проектом, а внешние мутации проходят явное подтверждение.

| № | Поверхность | Рабочий маршрут | Автоматическая гарантия | Статус |
| --- | --- | --- | --- | --- |
| 1 | Код | IntelliSense/hover/signature, usages/implementations/type definition, Structure и breadcrumbs, Search Everywhere, Run/Debug, индекс и точечные агентские правки | `smoke-point-editor-daily.js`, `smoke-point-run-configurations.js`, тесты `internal/workspace`, `internal/agent`, `internal/verification` | Готово |
| 2 | Файлы | Explorer, создание/переименование/удаление, recent locations с позицией, scratch, compare, local history, безопасное чтение/patch/apply/rollback агентом | `test-point-files.ps1`, `smoke-point-editor-daily.js`, тесты `internal/workspace` и `internal/tools` | Готово |
| 3 | Проект | Явный активный корень, индекс и Problems только этого корня, launch/tasks/Makefile/package scripts, доверенный запуск и диагностика | `smoke-extension-backend.js`, `smoke-point-run-configurations.js`, `smoke-point-performance-controls.js` | Готово |
| 4 | Инструменты | Каталог встроенных и типизированных process-tools, dry-run, строгая JSON Schema, allowlist/policy, approval, sandbox и проверяемое завершение | `smoke-agent-studio-ui.js`, тесты `internal/tools`, `internal/policy`, `internal/sandbox` | Готово |
| 5 | Терминал | Нативная shell проекта, ленивый старт, вкладки терминала, консольные каналы в editor area, Run/Test/Build и передача ошибки Компаньону; cold-start без активного редактора не передаёт `undefined` в terminal API | `smoke-point-console-runtime.js`, `test-point-lazy-terminal.ps1`, `test-point-console-channel.ps1`, `smoke-point-daily-workbench.mjs` | Готово, cold-start E2E пройден |
| 6 | Git | Панель в левой рейке: изменения папками, отметки для коммита, отдельная группа файлов вне репозитория, коммит из отмеченного и «коммит и отправить». Репозиторий выбирается по активному файлу и самому глубокому корню; status/diff/blame/history/branches, push/pull/fetch и Change Set apply/rollback | `smoke-git-workflow.js`, `test-point-chronicle.ps1`, Change Set-сценарий в `smoke-agent-studio-ui.js` | Готово |
| 7 | Несколько проектов | Несколько roots в одном окне с одним явно выбранным проектом Point; отдельные окна получают отдельные core, один проект разделяет core через lease | `smoke-point-multi-project.js`, `test-point-split-windows.ps1`, `verify-point-project-switcher.mjs` | Готово |
| 8 | Базы данных | SQLite/PostgreSQL/MySQL: создание и редактирование профиля без сброса SecretStorage, ping, schema, bounded query; запись/DDL только после подтверждения; агентские DB tools под policy | `smoke-agent-studio-ui.js`, `test-point-connections.ps1`, тесты `internal/dbconn` и `internal/tools` | Готово |
| 9 | SSH | Создание и редактирование профиля key/agent/password без сброса SecretStorage, probe, нативный терминал, старт из настроенного remote path, переходы по каталогам, bounded UTF-8 preview; agent list/read/exec под network policy | `smoke-agent-studio-ui.js`, `smoke-point-ssh.js`, `test-point-connections.ps1`, тесты `internal/servers` и `internal/tools` | Готово |

## Повторная проверка 5 сентября

После аудита закрыты SQL read-only обход, потеря draft в диагностическом frontend и устаревший навигационный checker. Go tests, extension check и обе remote SQL-регрессии прошли. Таблица выше сохраняет продуктовый scope; отдельные старые Electron evidence не выдаются за новый полный прогон. Текущие результаты — в [PROJECT-STATUS.md](PROJECT-STATUS.md).

## Осознанные границы

- Один процесс Point Core обслуживает один активный локальный корень. Это
  изоляция проекта, а не ограничение Explorer: для параллельной работы есть
  отдельные окна, для переключения внутри multi-root — явный selector.
- SSH-поверхность подключается к серверу через системный OpenSSH, но не выдаёт
  себя за Remote-SSH workspace. Предпросмотр удалённого файла только для UTF-8,
  не более 64 КиБ и без неявной записи обратно на сервер; для правок остаются
  SSH-терминал или подтверждаемый `ssh_exec_remote`.
- Базы — рабочая IDE-поверхность для разработки и диагностики, не замена
  полнофункциональному администратору БД. Результаты ограничены, внешняя запись
  не обещает локальный rollback.

## Минимальный release-gate

Сначала обычный прогон — тот же, что и в CI:

```bash
make test
```

Состав целей живёт в `Makefile` и здесь намеренно не дублируется: копия
этого списка здесь уже отставала — в ней был один затвор документации
из трёх.

Перед выпуском сверх этого — то, чего в `make test` нет по замыслу:
чистая установка зависимостей и аудит обоих деревьев, живой запуск ядра и
проверка compose-файла:

```powershell
Push-Location frontend; npm ci; npm audit; Pop-Location
Push-Location vscode-extension; npm ci; npm audit --omit=dev; Pop-Location
node scripts/smoke-extension-backend.js
docker compose config
```

Перед выпуском portable дополнительно выполняются `smoke-vsix-runtime.ps1` и
реальные Code-OSS E2E (`test-point-files.ps1`, `test-point-console-channel.ps1`,
`test-point-chronicle.ps1`, `test-point-split-windows.ps1`,
`test-point-connections.ps1`).
