# Документация Point IDE

Актуальная точка входа на 23 сентября 2026 года. Версия исходников core, frontend и расширения — `1.2.3`. Датированный отчёт сохраняет состояние на дату отчёта и не переопределяет текущий контракт.

## С чего начать

| Вопрос | Документ |
| --- | --- |
| Цель продукта и порядок развития | [PRODUCT-VISION.md](PRODUCT-VISION.md) |
| Подтверждённое состояние и открытые проверки | [PROJECT-STATUS.md](PROJECT-STATUS.md) |
| Сборка и запуск | [README проекта](../README.md), [README поставки](../distribution/README.md) |
| Runtime, IDE и данные | [architecture.md](architecture.md) |
| HTTP-маршруты | [api.md](api.md) |
| Разработка и проверки | [CONTRIBUTING.md](../CONTRIBUTING.md) |

## Действующие справочники

- [agent-hub-mvp.md](agent-hub-mvp.md) — модель Hub и совместимые поверхности. Для новой работы приоритет у целевого документа и контракта v2.
- [sandbox.md](sandbox.md), [security.md](security.md), [threat-model.md](threat-model.md) — границы изоляции, доступы, сеть и угрозы.
- [agent-evaluation.md](agent-evaluation.md) — benchmark, attribution и canary для Skills.
- [operations.md](operations.md), [performance.md](performance.md), [DEPENDENCIES.md](DEPENDENCIES.md) — эксплуатация, SLO и зависимости.
- [databases.md](databases.md), [ssh.md](ssh.md), [tool-access-layer.md](tool-access-layer.md) — инструменты и данные.
- [legacy-lifecycle.md](legacy-lifecycle.md) — совместимость старых маршрутов и порядок её сокращения.
- [IDE-CAPABILITY-MATRIX.md](IDE-CAPABILITY-MATRIX.md), [IDE-DESIGN.md](IDE-DESIGN.md), [ide-workspace-controls.md](ide-workspace-controls.md), [search-window.md](search-window.md), [RPG-DESIGN-SYSTEM.md](RPG-DESIGN-SYSTEM.md), [VISUAL-LOOP.md](VISUAL-LOOP.md) — реализация и дизайн IDE.
- [js-modules.md](js-modules.md), [master-chat-sessions.md](master-chat-sessions.md) — карта расширения и разговоры Мастера.

## Исторические решения и проверки

Эти документы не являются сегодняшним обещанием продукта. Их даты обозначают срез; они сохраняются как свидетельства решений и проверок.

- [AGENT-HUB-V2-MVP.md](AGENT-HUB-V2-MVP.md) — исходный сокращённый объём v2 и критерий приёмки.
- [AGENT-HUB-ACCEPTANCE-2x10.md](AGENT-HUB-ACCEPTANCE-2x10.md), [AGENT-HUB-IMPLEMENTATION-2026-09-06.md](AGENT-HUB-IMPLEMENTATION-2026-09-06.md), [AGENT-HUB-URL-INTAKE-2026-09-10.md](AGENT-HUB-URL-INTAKE-2026-09-10.md) — живые сценарии на даты отчётов.
- [AGENT-HUB-ORCHESTRATOR-NETWORK-POLICY.md](AGENT-HUB-ORCHESTRATOR-NETWORK-POLICY.md) — целевая политика надзора, сети и git; пометки о невыполненном runtime остаются обязательными.
- [AUDIT-2026-09-05.md](AUDIT-2026-09-05.md), [UI-UX-LOOP.md](UI-UX-LOOP.md), [POINT-UI-UX-BRIEF.md](../POINT-UI-UX-BRIEF.md), [CHANGELOG расширения](../vscode-extension/CHANGELOG.md) — аудит и журнал решений.

## Источники фактов

Версии — `internal/app/app.go`, `frontend/package.json`, `vscode-extension/package.json`. API-маршруты — `internal/httpapi`. Режимы запуска — `internal/app` и `internal/sandbox`. UI-протокол — `vscode-extension/ui/client` и контроллеры расширения. Проверка ссылок, маршрутов и версий: `node scripts/check-docs.mjs`. Успешная проверка исходников не заменяет живой прогон пользователя.
