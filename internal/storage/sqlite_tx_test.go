package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 03.10.2026: отмена контекста посреди транзакции или запроса оставляла
// соединение modernc открытым после Close — hub-v2.db нельзя было ни
// переименовать, ни удалить (плавающая уборка тестов internal/app, TODO Q19).
// Транзакция хранилища доходит до своего Commit/Rollback, запрос — до конца,
// и Close освобождает файл.
func TestCanceledWorkDoesNotKeepDatabaseOpen(t *testing.T) {
	const heavy = `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x < 2000000) `
	cases := map[string]func(ctx context.Context, store *SQLite){
		"transaction write": func(ctx context.Context, store *SQLite) {
			tx, err := store.beginTx(ctx)
			if err != nil {
				return
			}
			_, _ = tx.ExecContext(ctx, heavy+`INSERT INTO probe SELECT x FROM n`)
			_ = tx.Rollback()
		},
		"transaction read": func(ctx context.Context, store *SQLite) {
			tx, err := store.beginTx(ctx)
			if err != nil {
				return
			}
			var count int
			_ = tx.QueryRowContext(ctx, heavy+`SELECT count(*) FROM n`).Scan(&count)
			_ = tx.Commit()
		},
		"query": func(ctx context.Context, store *SQLite) {
			var count int
			_ = store.db.QueryRowContext(ctx, heavy+`SELECT count(*) FROM n`).Scan(&count)
		},
		"write": func(ctx context.Context, store *SQLite) {
			_, _ = store.db.ExecContext(ctx, heavy+`INSERT INTO probe SELECT x FROM n`)
		},
	}
	for name, work := range cases {
		path := filepath.Join(t.TempDir(), "hub-v2.db")
		store, err := OpenShared(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.db.Exec(`CREATE TABLE probe(x INTEGER)`); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			work(ctx, store)
		}()
		time.Sleep(50 * time.Millisecond)
		cancel()
		<-done
		if err = store.Close(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err = os.Rename(path, path+".moved"); err != nil {
			t.Errorf("%s: база осталась открытой после Close: %v", name, err)
		}
	}
	// Уже отменённый контекст транзакцию не начинает.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&SQLite{}).beginTx(canceled); err == nil {
		t.Fatal("транзакция начата по отменённому контексту")
	}
}
