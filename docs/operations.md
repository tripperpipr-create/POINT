# Эксплуатация, резервное копирование и восстановление

Runbook относится к Point `1.2.2`. Канонический desktop-клиент хранит данные в
общем каталоге extension global storage:

```text
<Point User>/globalStorage/local-agent.local-agent-workbench/hub-v2.db
```

С 14 сентября 2026 ядро открывает базу Agent Hub v2 (`hub-v2.db`). Прежняя
`workbench.db` остаётся в том же каталоге нетронутой; запуск с
`POINT_AGENT_HUB_V2=0` снова открывает её. Команды ниже работают с любой из двух
баз — подставляйте тот файл, который сейчас используется.

Точный `<Point User>` зависит от `--user-data-dir`. Не подставляйте путь из
другого окна или VS Code: откройте `point-core.log` того же каталога и остановите
все процессы Point/Core перед offline restore.

В production installer рядом с `point-core.exe` поставляется `point-db.exe`.
При разработке утилиту можно вызвать как `go run ./cmd/point-db`.

## Проверка базы

```powershell
point-db.exe verify --db 'C:\path\to\workbench.db'
```

Успех означает одновременно `PRAGMA integrity_check=ok` и отсутствие строк
`foreign_key_check`. JSON также содержит размер и SHA-256 именно проверенного
файла.

## Консистентный backup

Не копируйте `workbench.db` через Explorer во время работы Point: подтверждённые
транзакции могут находиться в WAL. Используйте native SQLite online backup API:

```powershell
point-db.exe backup `
  --db 'C:\path\to\workbench.db' `
  --out 'D:\PointBackups\workbench-20260829T120000Z.db'
```

Destination обязан не существовать. Утилита пишет во временный файл рядом с
назначением, проверяет integrity/foreign keys, ставит restrictive permissions и
только затем атомарно публикует backup. Исходная база не мигрируется и не меняет
статусы Runs.

Рекомендуемая политика: backup перед установкой, ещё один после успешного
upgrade, минимум две последние подтверждённые копии на другом физическом или
защищённом сетевом носителе. Копия содержит историю, Memory и metadata
connections, поэтому имеет ту же чувствительность, что исходная база. Provider,
SSH и DB passwords в SQLite не хранятся; SecretStorage резервируется отдельно
средствами ОС.

## Проверка миграции на копии

Одна база:

```powershell
point-db.exe migrate-copy `
  --db 'D:\MigrationCorpus\from-1.1.db' `
  --out 'D:\MigrationResults\from-1.1.db'
```

Набор `.db`-копий без рекурсивного обхода:

```powershell
point-db.exe migrate-corpus `
  --source-dir 'D:\MigrationCorpus' `
  --out-dir 'D:\MigrationResults'
```

Каждый source сначала проверяется, затем snapshot мигрируется отдельно через
тот же `storage.Open`, что production core, повторно проверяется и получает
список применённых migration versions. Source hash после gate обязан совпадать с
исходным. Реальные базы допускаются в corpus только как обезличенные копии с
явным согласием владельца; CI fixtures не заменяют field corpus перед релизом.

## Offline restore и rollback

1. Закройте все окна Point и убедитесь, что `Point.exe` и `point-core.exe` не
   работают.
2. Проверьте выбранный backup командой `verify`.
3. Выполните:

```powershell
point-db.exe restore `
  --backup 'D:\PointBackups\workbench-before-upgrade.db' `
  --db 'C:\path\to\workbench.db' `
  --confirm-offline
```

Restore откажется работать при `workbench.db-wal` или `workbench.db-shm`: это
признак активного/некорректно остановленного владельца, а не повод удалять
sidecar вручную. Backup материализуется и проверяется во временной базе. Текущая
база переименовывается в `workbench.pre-restore-<UTC>.db`, новая публикуется
atomic rename и проверяется ещё раз. При ошибке публикации прежняя база
возвращается; сохранённый pre-restore файл не удаляется автоматически.

Для отката приложения сначала восстановите installer предыдущей поддерживаемой
версии в тестовом install root, затем его совместимый pre-upgrade database
backup. Нельзя запускать старый core на необратимо мигрированной рабочей базе.

## Disaster recovery drill

Перед каждым production-релизом отдельный чистый Windows runner обязан доказать:

1. clean install и Electron renderer smoke;
2. backup populated базы при открытом SQLite store;
3. upgrade installer поверх предыдущей версии;
4. миграцию копии representative corpus и сохранность source SHA-256;
5. запуск новой версии и чтение истории;
6. offline restore pre-upgrade backup;
7. rollback installer и запуск предыдущей версии;
8. сохранность пользовательского workspace — installer никогда его не удаляет;
9. целостность release manifest/hash и `distribution/quality-gate.json` с
   нулём незакрытых P0/P1 для exact product version.

Автоматические unit/integration сценарии находятся в
`internal/storage/maintenance_test.go` и `cmd/point-db/main_test.go`. Release
workflow дополнительно запускает `scripts/test-point-database-dr.ps1` именно
packaged `point-db.exe` на двух content-distinct мигрированных базах: SHA-256
восстановленной базы обязан точно совпасть с backup, а сохранённый recovery
point — с вытесненной target-базой. Corpus из одной базы или двух одинаковых
копий fail-closed; source и target fixture повторно хэшируются после drill и
обязаны остаться byte-for-byte неизменными. Эти проверки входят в gate, но не подменяют
installer/Electron drill на собранном артефакте.

Локальный drill 30 августа 2026 года прошёл все шесть фаз:
previous clean install/renderer, current upgrade/renderer и previous
rollback/renderer. Candidate installer имеет SHA-256
`29164B4814B088E38C1A57849B97FB6C5C60E42625015FA6061FA327380AB9E3`;
current extension/core fingerprints отличались от previous, после rollback
точно восстановились previous fingerprints, а test sentinel сохранился. Это
доказательство unsigned local candidate; production всё равно требует clean Git
и Authenticode. Новый exact candidate 1 сентября имеет SHA-256
`FEAEE91CEF7EF824015DF8B112B5A0F1C59EB66A7BCB6D9EAEA5AB71B0E2D5EB`;
его isolated clean install и renderer smoke зелёные. Повторный update/rollback
для этого exact hash требует сохранённый предыдущий Point installer; legacy
`LocalAgentSetup` не считается таким baseline. Рядом с exact candidate создан
CycloneDX 1.7 installer SBOM на 305 компонентов; `release.json` фиксирует его
SHA-256 `C489989903A177EC095D8F45803C9DF442E9D7DBC9F1DE5C5732699A73020A54`.
Генератор и verifier сверяют фактические packaged app/extension manifests,
транзитивные `node_modules`, installer и ключевые executable/entrypoint hashes.
Docker sandbox integration отдельно пройден на локальном Engine 27.2.0 с
пересобранным release image.

Тем же packaged `point-db.exe` повторно проверен corpus из четырёх
production-shaped SQLite-копий: каждая дошла до migrations 1–30 с
`integrity=ok` и нулём FK violations, а четыре source SHA-256 не изменились.
Отдельный DR drill создал online backup, заменил другую offline target-базу,
сохранил `workbench.pre-restore-<UTC>.db` и подтвердил, что restored SHA-256
точно равен backup SHA-256.
