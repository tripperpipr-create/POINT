# Базы данных в Point IDE

Практическое взаимодействие с БД (не полноценный DBeaver).

Актуально для Point `1.2.2` на 5 сентября 2026 года. Полный список HTTP-маршрутов
поддерживается в [api.md](api.md); ниже оставлен только маршрут этой функции.

## Поддерживаемые драйверы

| Драйвер | Зависимости | Примечание |
|---------|-------------|------------|
| **SQLite** | `modernc.org/sqlite` (pure Go) | Путь к файлу относительно workspace или абсолютный |
| **PostgreSQL** | `jackc/pgx` (pure Go) | Хост/порт/БД/пользователь; пароль в SecretStorage |
| **MySQL** | `go-sql-driver/mysql` (pure Go) | То же, порт по умолчанию 3306 |

## Как пользоваться

1. Команда палитры **«Point: Открыть базы данных»** или чип **Базы** в шапке Гильдии.
2. Сохраните профиль (пароль уходит только в VS Code SecretStorage; в Point Core — `secretRef`).
3. **Проверить** — ping; **Схема** — таблицы/колонки; SQL — только чтение сразу, запись через карточку **Выполнить / Отменить**.

## Инструменты агента

По умолчанию **DENY** (как SSH/сеть). Нужно явно разрешить в `allowedTools` / `toolPolicies`:

- `db_list_connections` — список профилей (LOW)
- `db_schema` — схема (MEDIUM, ASK)
- `db_query` — только SELECT/SHOW/… (MEDIUM, ASK)
- `db_exec` — запись/DDL (CRITICAL, всегда ASK)

Для удалённого хоста: `toolPolicies["network:<host>"] = "ALLOW"` (localhost всегда разрешён). Перед запуском агента разблокируйте пароль проверкой подключения в UI.

## Безопасность

Читающие запросы выполняются в read-only транзакции. Для SQLite дополнительно включается query_only на том же соединении; для PostgreSQL/MySQL ReadOnly также задаёт default режима соединения. Классификатор не заменяет эту защиту. Принимается один SQL statement; batches, исполнительные комментарии и неоднозначные для разных диалектов формы quoting отклоняются. CTE с записью, EXPLAIN ANALYZE и изменяющие PRAGMA требуют write approval.

Регрессии: internal/dbconn/read_safety_test.go и read_integration_test.go. Последний использует POINT_TEST_POSTGRES_DSN / POINT_TEST_MYSQL_DSN только для специально созданных одноразовых БД и создаёт/удаляет собственные тестовые объекты.


- Пароли не пишутся в bootstrap/SQLite.
- Нет «тихого» destructive SQL от агента без approval.
- Лимит строк (по умолчанию 200, max 1000) и таймаут ~15 с.
- Запись в внешнюю БД **не** откатывается через Change Set sandbox.

## API

- `POST /api/db-connections`
- `DELETE /api/db-connections/{id}`
- `POST /api/db-connections/{id}/test|query|schema`
- `POST /api/db-connections/unlock`

## Проверка

```powershell
go test ./internal/dbconn ./internal/tools -count=1 -run "Classify|DBQuery|DBExec|IsLoopback"
```

В IDE: сохранить SQLite `./tmp-point.db`, выполнить `SELECT 1`, убедиться что `INSERT` требует подтверждения.
