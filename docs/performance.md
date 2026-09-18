# Производительность и SLO

Point проверяет производительность как набор понятных предельных величин. Здесь
нет сводного «рейтинга»: отчёт сохраняет каждое исходное измерение, худшее
наблюдаемое значение и соответствующий порог. Канонический machine-readable
источник — `distribution/performance-slo.json`.

## Core и большие проекты

`cmd/point-performance-probe` создаёт во временном каталоге детерминированный
проект из 50 000 текстовых файлов и отдельную SQLite-базу. Gate
проверяет:

| Метрика | SLO | Что доказывает |
| --- | ---: | --- |
| Полная сборка индекса | ≤ 60 000 ms | 50 000 файлов включены, index не partial |
| p95 точного поиска | ≤ 25 ms | 200 запросов по реально индексированным символам |
| Рост Go heap индекса | ≤ 768 MB | разница HeapAlloc после принудительного GC |
| SQLite после 100 000 событий | ≤ 128 MB и ≤ 512 B/event | ограниченный рост длительной истории |
| Повторное открытие и чтение 2 000 Flow | ≤ 5000 ms | восстановление истории после restart |
| 32 параллельные записи Execution | ≤ 3000 ms | ограниченная конкурентная нагрузка без ошибок |

Probe отказывает при пропущенном файле, partial-индексе, ошибке записи или
несовпадении числа восстановленных Flow. Fixture и база удаляются после теста.

Свежий объединённый замер 1 сентября 2026 года прошёл на полном масштабе:
50 000 файлов проиндексированы за 4 272 ms без partial-режима, search p95 —
0 ms, heap delta — 179,2 MB; SQLite после 100 000 событий — 271,7696 B/event;
2 000 Flow восстановлены за 67 ms, 32 параллельные записи завершены за 200 ms.
Raw evidence и пустой `core.failures` находятся в каноническом отчёте.

## Desktop Code-OSS

`scripts/test-point-performance-slo.ps1` выполняет пять чистых запусков
для каждого режима: idle workbench и открытие Agent Hub. Каждый запуск получает
собственные `user-data` и extensions directories. CDP-зонд ждёт штатный
Code-OSS mark `code/didStartWorkbench`, который ставится после восстановления
layout и видимых editor parts, измеряет две последовательные animation frames,
кликает каноническую Agent Hub surface и ждёт usable UI. Более позднее время
наблюдения CDP сохраняется отдельно как `workbenchDetectedMs`. Для private
memory используется пятисекундное окно стабилизации; startup/Hub latency
фиксируется в момент готовности и не включает это ожидание.

| Метрика | SLO |
| --- | ---: |
| Workbench visible после старта процесса | ≤ 3 000 ms |
| Agent Hub usable после явного клика | ≤ 6 500 ms |
| Две animation frames | ≤ 60 ms |
| Idle private memory всего дерева | ≤ 1 100 MB |
| Agent Hub private memory всего дерева | ≤ 1 450 MB |
| Процессов в дереве | ≤ 18 |
| Рост private memory после 50 Hub open/close | ≤ 5% |

Дополнительные инварианты: ровно один общий `point-core` и один extension host
как в idle, так и после открытия канонического Agent Hub в auxiliary-окне.
PID extension host берётся из workbench-лога, потому что Electron NodeService
больше не обязан содержать `extensionHost` в командной строке. Для gate берётся
худшее значение, а не среднее: один медленный или чрезмерно тяжёлый запуск не
скрывается усреднением.

Канонический полный gate 1 сентября 2026 года прошёл все пять clean samples в
каждом режиме и 50 настоящих auxiliary Hub open/close. Худшие значения:
Workbench 2 488 ms, usable Hub после Alt+4 — 671 ms, две frames — 31,1 ms,
idle/Hub private memory — 1 099,9/1 195,5 MB, 11 процессов и один extension
host. После 50 циклов private memory изменилась на −9,26%. Порог не
корректировался; `desktop.failures` пуст. Полный raw report находится в
`build/performance-slo-report.json`.

## 8/24-часовой soak

`distribution/soak-profile.json` содержит три immutable профиля: `quick` для
проверки harness, а также release-qualified `eight-hour` и
`twenty-four-hour`. Оба длительных профиля фиксируют 50 000 файлов,
100 000 событий, 5 000 Runs, 2 000 Flow executions и не менее четырёх
одновременных полноценных engine Runs через локальный scripted provider.

`cmd/point-soak` строит реальный индекс, сохраняет workload конкурентно,
циклически закрывает/открывает SQLite как Core-restart fault, проверяет
`integrity_check`/`foreign_key_check`, каждый terminal event, ссылки
Execution→Run/Flow, reopen latency, heap growth и размер БД. Отдельно выполняются
настоящая отмена Run, provider timeout, reopen с восстановлением незавершённых
Run/Flow/execution в `interrupted`, временный read-only SQLite fault с успешным
возвратом записи и bounded near-disk-limit refusal до изменения файла.
`scripts/test-point-soak.ps1` запускает Core harness отдельным процессом. В
длительных профилях он каждый час поднимает новый packaged Code-OSS, проверяет
usable Hub и две animation frames, выполняет два настоящих auxiliary Hub
open/close и измеряет рост private memory. Disposable Docker workload с
`network=none`, read-only rootfs и dropped capabilities действительно
останавливается, восстанавливается и обязательно удаляется.

Quick controller 1 сентября 2026 года прошёл end-to-end: четыре из четырёх full
Runs одновременно находились в provider, cancellation и timeout дали ровно по
одному terminal event, 40 Runs/800 events/20 Flow executions сохранились без
orphan/lost terminal events, незавершённые Run/Flow/execution восстановились как
`interrupted`, SQLite пережила read-only fault, history reopen=5 ms, heap
growth=0,33%, Core restarts=2, Code-OSS restarts=2, Hub cycles=2, Docker
stop/recovery=1 и orphan containers=0. Quick profile намеренно имеет
`releaseQualified=false`; это smoke автоматизации, не подмена 8/24-часового
evidence.

Запуск длительных профилей:

```powershell
./scripts/test-point-soak.ps1 -Profile eight-hour
./scripts/test-point-soak.ps1 -Profile twenty-four-hour
```

Production workflow принимает абсолютные пути к обоим уже завершённым JSON и
проверяет их через `scripts/check-soak-report.mjs`. Отчёт не принимается без
полной длительности, всех девяти faults, почасовых UI/Hub samples, точных cardinality/limits и
`releaseQualified=true`.

## 14-дневный dogfood

Soak доказывает машинную устойчивость под заданной нагрузкой, но не заменяет
ежедневную работу человека. `distribution/dogfood-gate.json` отдельно требует
не менее 336 часов, checkpoints минимум на 14 UTC-днях с разрывом не более
30 часов, terminal status каждого учтённого Run и ноль P0/P1, потерь данных,
повреждений БД и потерянных terminal events. Отчёт имеет закрытую схему без
project path, prompt, agent/tool IDs и содержимого задач.

```powershell
node scripts/check-dogfood-report.mjs D:\PointEvidence\dogfood-14d.json
```

`distribution/dogfood-report.example.json` заведомо непроходной: duration и
attestations в нём ложные. Production workflow принимает только внешний
завершённый отчёт и не генерирует 14 дней evidence внутри одного CI-run.

## Запуск и отчёт

Core-only probe, пригодный для быстрой проверки индексатора и SQLite:

```powershell
go run ./cmd/point-performance-probe -slo distribution/performance-slo.json
```

Полный Windows gate после сборки portable Code-OSS:

```powershell
./scripts/test-point-performance-slo.ps1
```

Полный отчёт пишется в `build/performance-slo-report.json`. Он содержит raw
samples, thresholds, худшие значения и список точных нарушений. Production
release workflow запускает этот gate после installer/Electron lifecycle; менять
порог можно только отдельным review с объяснением пользовательского эффекта, а
не для превращения красного замера в зелёный.
