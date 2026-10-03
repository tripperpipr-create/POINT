package storage

import (
	"context"
	"database/sql"
)

// beginTx открывает транзакцию хранилища, которую отмена контекста не рвёт.
//
// database/sql, получив отмену посреди транзакции, сам откатывает её и
// выбрасывает соединение. С modernc.org/sqlite такое соединение остаётся
// открытым и после Close: хэндлы hub-v2.db, -wal и -shm живут в процессе,
// хотя Close вернул nil, а занятых соединений нет. 03.10.2026 так объяснилась
// «плавающая уборка hub-v2.db» тестов internal/app (TODO Q19): ядро
// останавливалось, пока фоновое обучение Мастера писало в базу. Запись в
// локальную базу короткая, и оборвать её посередине хуже, чем дождаться:
// транзакция доходит до Commit или Rollback своего кода.
func (s *SQLite) beginTx(ctx context.Context) (*sql.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.db.BeginTx(context.WithoutCancel(ctx), nil)
}
