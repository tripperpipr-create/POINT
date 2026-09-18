package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestDatabaseNeedsMigrationUsesExactReadOnlyMigrationSet(t *testing.T) {
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "missing.db")
	needed, err := DatabaseNeedsMigration(ctx, missing)
	if err != nil || needed {
		t.Fatalf("missing database: needed=%v err=%v", needed, err)
	}

	path := filepath.Join(t.TempDir(), "current.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	needed, err = DatabaseNeedsMigration(ctx, path)
	if err != nil || needed {
		t.Fatalf("current database: needed=%v err=%v", needed, err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM schema_migrations WHERE version=?`, LatestMigrationVersion()); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	needed, err = DatabaseNeedsMigration(ctx, path)
	if err != nil || !needed {
		t.Fatalf("incomplete database: needed=%v err=%v", needed, err)
	}
}

func TestDatabaseNeedsMigrationRecognizesLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE legacy_data(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	needed, err := DatabaseNeedsMigration(context.Background(), path)
	if err != nil || !needed {
		t.Fatalf("legacy database: needed=%v err=%v", needed, err)
	}
}

// Реестр миграций — единственный источник порядка. Тела разъехались по файлам
// (migrations_hub, migrations_companion, migrations_v2 и другие), и ошибиться
// теперь легче: пропущенная версия, повтор номера, забытое имя. Проверка
// смотрит на реестр целиком, а не на отдельную миграцию.
func TestMigrationRegistryIsContinuousAndUnique(t *testing.T) {
	list := hubMigrations()
	if len(list) == 0 {
		t.Fatal("реестр миграций пуст — проверка прошла бы вхолостую")
	}
	seenVersion := map[int]bool{}
	seenName := map[string]bool{}
	for index, item := range list {
		if item.version != index+1 {
			t.Fatalf("версия %d стоит на месте %d — порядок применения задаётся позицией", item.version, index+1)
		}
		if seenVersion[item.version] {
			t.Fatalf("версия %d объявлена дважды", item.version)
		}
		seenVersion[item.version] = true
		if item.name == "" {
			t.Fatalf("миграция %d без имени: в журнале применения её не отличить", item.version)
		}
		if seenName[item.name] {
			t.Fatalf("имя %q занято двумя миграциями", item.name)
		}
		seenName[item.name] = true
		if item.up == nil {
			t.Fatalf("миграция %d (%s) без тела", item.version, item.name)
		}
	}
}

// Повторное открытие той же базы не должно применять ничего заново: версия
// записана, и вторая попытка обязана пройти вхолостую.
func TestReopeningDatabaseAppliesNoMigrationTwice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "twice.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatalf("повторное открытие: %v", err)
	}
	defer second.Close()
	needed, err := DatabaseNeedsMigration(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if needed {
		t.Fatal("после повторного открытия база снова требует миграции")
	}
}
