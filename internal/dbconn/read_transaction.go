package dbconn

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"time"

	"modernc.org/sqlite"
)

// Pin SQLite's query_only to one connection; a pool-wide PRAGMA could protect
// a different connection than the one that executes the user's query.
func beginRead(ctx context.Context, db *sql.DB) (*sql.Tx, func(), error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	sqliteOnly := false
	if _, ok := db.Driver().(*sqlite.Driver); ok {
		var previous int
		if err = conn.QueryRowContext(ctx, "PRAGMA query_only").Scan(&previous); err != nil {
			conn.Close()
			return nil, nil, err
		}
		sqliteOnly = previous == 0
		if _, err = conn.ExecContext(ctx, "PRAGMA query_only=ON"); err != nil {
			conn.Close()
			return nil, nil, err
		}
	}
	release := func() {
		if sqliteOnly {
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, resetErr := conn.ExecContext(cleanup, "PRAGMA query_only=OFF")
			cancel()
			if resetErr != nil {
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			}
		}
		_ = conn.Close()
	}
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		release()
		return nil, nil, err
	}
	return tx, func() { _ = tx.Rollback(); release() }, nil
}
