package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"

	modernsqlite "modernc.org/sqlite"
)

// Запрос к встроенной SQLite не прерывается отменой контекста.
//
// modernc.org/sqlite при отмене контекста зовёт sqlite3_interrupt посреди
// выполнения. 03.10.2026 на тестах internal/app: ядро останавливалось, пока
// фоновое обучение Мастера ждало своих запросов, и после db.Close в процессе
// оставались хэндлы hub-v2.db, -wal и -shm (Close вернул nil, занятых
// соединений не было) — «плавающая уборка hub-v2.db» из TODO Q19, 5 сбоев из
// 9 на чистом HEAD. Отмена посреди транзакции — отдельная половина той же
// беды, её закрывает beginTx (sqlite_tx.go); одна она давала 5 сбоев из 8,
// вместе с этой обёрткой — 0 из 10.
//
// Запросы к локальной базе короткие, и прерывать их на середине опаснее, чем
// дождаться. Драйвер получает контекст без отмены (context.WithoutCancel):
// значения контекста сохраняются, а database/sql по-прежнему уважает отмену
// там, где она безопасна, — при ожидании соединения и закрывая Rows.

type quietConnector struct {
	dsn    string
	driver driver.Driver
}

func (c quietConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.driver.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return quietConn{conn}, nil
}

func (c quietConnector) Driver() driver.Driver { return c.driver }

// registeredSQLite — драйвер, который modernc регистрирует под именем
// «sqlite»: на нём живут пользовательские функции и хуки соединения.
var registeredSQLite = func() driver.Driver {
	db, _ := sql.Open("sqlite", "")
	defer db.Close()
	return db.Driver()
}()

func openQuietSQLite(dsn string) *sql.DB {
	return sql.OpenDB(quietConnector{dsn: dsn, driver: registeredSQLite})
}

// quietConn передаёт драйверу контекст без отмены. Остальные возможности
// соединения modernc (резервная копия, сброс сессии, проверка) пробрасываются.
type quietConn struct{ driver.Conn }

func (c quietConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return c.Conn.(driver.QueryerContext).QueryContext(context.WithoutCancel(ctx), query, args)
}

func (c quietConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(context.WithoutCancel(ctx), query, args)
}

func (c quietConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	stmt, err := c.Conn.(driver.ConnPrepareContext).PrepareContext(context.WithoutCancel(ctx), query)
	if err != nil {
		return nil, err
	}
	return quietStmt{stmt}, nil
}

func (c quietConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(context.WithoutCancel(ctx), opts)
}

func (c quietConn) Ping(ctx context.Context) error {
	return c.Conn.(driver.Pinger).Ping(context.WithoutCancel(ctx))
}

func (c quietConn) ResetSession(ctx context.Context) error {
	return c.Conn.(driver.SessionResetter).ResetSession(ctx)
}

func (c quietConn) IsValid() bool { return c.Conn.(driver.Validator).IsValid() }

// NewBackup — резервная копия идёт через Raw-соединение (maintenance.go).
func (c quietConn) NewBackup(destination string) (*modernsqlite.Backup, error) {
	return c.Conn.(interface {
		NewBackup(string) (*modernsqlite.Backup, error)
	}).NewBackup(destination)
}

type quietStmt struct{ driver.Stmt }

func (s quietStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.Stmt.(driver.StmtExecContext).ExecContext(context.WithoutCancel(ctx), args)
}

func (s quietStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.Stmt.(driver.StmtQueryContext).QueryContext(context.WithoutCancel(ctx), args)
}
