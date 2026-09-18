package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	modernsqlite "modernc.org/sqlite"
)

type DatabaseReport struct {
	Path                 string `json:"path"`
	SizeBytes            int64  `json:"sizeBytes"`
	SHA256               string `json:"sha256"`
	Integrity            string `json:"integrity"`
	ForeignKeyViolations int    `json:"foreignKeyViolations"`
}

type MigrationCopyReport struct {
	Source            DatabaseReport `json:"source"`
	Migrated          DatabaseReport `json:"migrated"`
	MigrationVersions []int          `json:"migrationVersions"`
}

type RestoreReport struct {
	Backup       DatabaseReport `json:"backup"`
	Restored     DatabaseReport `json:"restored"`
	PreviousPath string         `json:"previousPath,omitempty"`
}

type onlineBackuper interface {
	NewBackup(string) (*modernsqlite.Backup, error)
}

// Backup writes a consistent online SQLite snapshot. It includes committed WAL
// content through SQLite's backup API instead of copying database files.
func (s *SQLite) Backup(ctx context.Context, destination string) (DatabaseReport, error) {
	destination, err := newDatabaseDestination(destination)
	if err != nil {
		return DatabaseReport{}, err
	}
	temporary, cleanup, err := temporaryDatabasePath(destination)
	if err != nil {
		return DatabaseReport{}, err
	}
	defer cleanup()
	if err = backupDatabase(ctx, s.db, temporary); err != nil {
		return DatabaseReport{}, fmt.Errorf("online sqlite backup: %w", err)
	}
	if _, err = VerifyDatabase(ctx, temporary); err != nil {
		return DatabaseReport{}, fmt.Errorf("verify sqlite backup: %w", err)
	}
	if err = publishDatabase(temporary, destination); err != nil {
		return DatabaseReport{}, err
	}
	return VerifyDatabase(ctx, destination)
}

// BackupDatabase creates a consistent snapshot without applying migrations or
// changing run state in the source. It is the safe release/operations entry
// point when Point Core is not already holding an opened SQLite store.
func BackupDatabase(ctx context.Context, source, destination string) (DatabaseReport, error) {
	source, destination, err := databaseSourceAndDestination(source, destination)
	if err != nil {
		return DatabaseReport{}, err
	}
	if _, err = VerifyDatabase(ctx, source); err != nil {
		return DatabaseReport{}, fmt.Errorf("verify source database: %w", err)
	}
	temporary, cleanup, err := temporaryDatabasePath(destination)
	if err != nil {
		return DatabaseReport{}, err
	}
	defer cleanup()
	sourceDB, err := sql.Open("sqlite", readOnlyDSN(source))
	if err != nil {
		return DatabaseReport{}, err
	}
	sourceDB.SetMaxOpenConns(1)
	if err = backupDatabase(ctx, sourceDB, temporary); err != nil {
		_ = sourceDB.Close()
		return DatabaseReport{}, fmt.Errorf("snapshot source database: %w", err)
	}
	if err = sourceDB.Close(); err != nil {
		return DatabaseReport{}, err
	}
	if _, err = VerifyDatabase(ctx, temporary); err != nil {
		return DatabaseReport{}, fmt.Errorf("verify sqlite backup: %w", err)
	}
	if err = publishDatabase(temporary, destination); err != nil {
		return DatabaseReport{}, err
	}
	return VerifyDatabase(ctx, destination)
}

// MigrateDatabaseCopy creates and migrates a snapshot while leaving the source
// byte-for-byte untouched. Release gates can therefore use production-shaped
// databases without ever opening the originals through application migrations.
func MigrateDatabaseCopy(ctx context.Context, source, destination string) (MigrationCopyReport, error) {
	source, destination, err := databaseSourceAndDestination(source, destination)
	if err != nil {
		return MigrationCopyReport{}, err
	}
	sourceReport, err := VerifyDatabase(ctx, source)
	if err != nil {
		return MigrationCopyReport{}, fmt.Errorf("verify source database: %w", err)
	}
	temporary, cleanup, err := temporaryDatabasePath(destination)
	if err != nil {
		return MigrationCopyReport{}, err
	}
	defer cleanup()
	sourceDB, err := sql.Open("sqlite", readOnlyDSN(source))
	if err != nil {
		return MigrationCopyReport{}, err
	}
	sourceDB.SetMaxOpenConns(1)
	if err = backupDatabase(ctx, sourceDB, temporary); err != nil {
		_ = sourceDB.Close()
		return MigrationCopyReport{}, fmt.Errorf("snapshot source database: %w", err)
	}
	if err = sourceDB.Close(); err != nil {
		return MigrationCopyReport{}, err
	}
	migrated, err := Open(temporary)
	if err != nil {
		return MigrationCopyReport{}, fmt.Errorf("migrate database copy: %w", err)
	}
	versions, versionErr := migrated.MigrationVersions(ctx)
	closeErr := migrated.Close()
	if versionErr != nil {
		return MigrationCopyReport{}, versionErr
	}
	if closeErr != nil {
		return MigrationCopyReport{}, closeErr
	}
	if _, err = VerifyDatabase(ctx, temporary); err != nil {
		return MigrationCopyReport{}, fmt.Errorf("verify migrated database: %w", err)
	}
	if err = publishDatabase(temporary, destination); err != nil {
		return MigrationCopyReport{}, err
	}
	migratedReport, err := VerifyDatabase(ctx, destination)
	if err != nil {
		return MigrationCopyReport{}, err
	}
	return MigrationCopyReport{Source: sourceReport, Migrated: migratedReport, MigrationVersions: versions}, nil
}

// RestoreDatabaseOffline verifies a backup, restores it into a new file, then
// atomically swaps the target. The previous target remains beside it as a
// timestamped recovery point. Point must be stopped; WAL/SHM sidecars make the
// function fail closed instead of guessing whether another process is active.
func RestoreDatabaseOffline(ctx context.Context, backupPath, targetPath string) (RestoreReport, error) {
	backupPath, err := existingDatabasePath(backupPath)
	if err != nil {
		return RestoreReport{}, err
	}
	targetPath, err = restoreTargetPath(targetPath)
	if err != nil {
		return RestoreReport{}, err
	}
	if samePath(backupPath, targetPath) {
		return RestoreReport{}, errors.New("backup and restore target paths must differ")
	}
	backupReport, err := VerifyDatabase(ctx, backupPath)
	if err != nil {
		return RestoreReport{}, fmt.Errorf("verify restore source: %w", err)
	}
	for _, sidecar := range []string{targetPath + "-wal", targetPath + "-shm"} {
		if _, statErr := os.Stat(sidecar); statErr == nil {
			return RestoreReport{}, fmt.Errorf("refusing offline restore while sqlite sidecar exists: %s", sidecar)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return RestoreReport{}, statErr
		}
	}
	temporary, cleanup, err := temporaryDatabasePath(targetPath)
	if err != nil {
		return RestoreReport{}, err
	}
	defer cleanup()
	sourceDB, err := sql.Open("sqlite", readOnlyDSN(backupPath))
	if err != nil {
		return RestoreReport{}, err
	}
	sourceDB.SetMaxOpenConns(1)
	if err = backupDatabase(ctx, sourceDB, temporary); err != nil {
		_ = sourceDB.Close()
		return RestoreReport{}, fmt.Errorf("materialize restore: %w", err)
	}
	if err = sourceDB.Close(); err != nil {
		return RestoreReport{}, err
	}
	if _, err = VerifyDatabase(ctx, temporary); err != nil {
		return RestoreReport{}, fmt.Errorf("verify materialized restore: %w", err)
	}

	previous := ""
	if _, statErr := os.Stat(targetPath); statErr == nil {
		base := strings.TrimSuffix(targetPath, filepath.Ext(targetPath))
		previous = fmt.Sprintf("%s.pre-restore-%s.db", base, time.Now().UTC().Format("20060102T150405.000000000Z"))
		if err = os.Rename(targetPath, previous); err != nil {
			return RestoreReport{}, fmt.Errorf("preserve current database: %w", err)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return RestoreReport{}, statErr
	}
	if err = os.Rename(temporary, targetPath); err != nil {
		if previous != "" {
			_ = os.Rename(previous, targetPath)
		}
		return RestoreReport{}, fmt.Errorf("publish restored database: %w", err)
	}
	restoredReport, verifyErr := VerifyDatabase(ctx, targetPath)
	if verifyErr != nil {
		failed := targetPath + ".failed-restore"
		_ = os.Rename(targetPath, failed)
		if previous != "" {
			_ = os.Rename(previous, targetPath)
		}
		return RestoreReport{}, fmt.Errorf("verify published restore: %w", verifyErr)
	}
	return RestoreReport{Backup: backupReport, Restored: restoredReport, PreviousPath: previous}, nil
}

func VerifyDatabase(ctx context.Context, path string) (DatabaseReport, error) {
	abs, err := existingDatabasePath(path)
	if err != nil {
		return DatabaseReport{}, err
	}
	db, err := sql.Open("sqlite", readOnlyDSN(abs))
	if err != nil {
		return DatabaseReport{}, err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	rows, err := db.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return DatabaseReport{}, err
	}
	integrity := []string{}
	for rows.Next() {
		var line string
		if err = rows.Scan(&line); err != nil {
			_ = rows.Close()
			return DatabaseReport{}, err
		}
		integrity = append(integrity, line)
	}
	// Ошибку обхода возвращает Err, а не Close: без неё оборванное чтение
	// выглядит как пустой ответ PRAGMA, то есть как отсутствие находок.
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return DatabaseReport{}, err
	}
	if err = rows.Close(); err != nil {
		return DatabaseReport{}, err
	}
	if len(integrity) != 1 || !strings.EqualFold(integrity[0], "ok") {
		return DatabaseReport{}, fmt.Errorf("sqlite integrity_check failed: %s", strings.Join(integrity, "; "))
	}
	foreignRows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return DatabaseReport{}, err
	}
	violations := 0
	for foreignRows.Next() {
		violations++
	}
	// Здесь молчание опаснее всего: оборванный обход дал бы «нарушений нет» и
	// проверка целостности объявила бы повреждённую базу здоровой.
	if err = foreignRows.Err(); err != nil {
		_ = foreignRows.Close()
		return DatabaseReport{}, err
	}
	if err = foreignRows.Close(); err != nil {
		return DatabaseReport{}, err
	}
	if violations != 0 {
		return DatabaseReport{}, fmt.Errorf("sqlite foreign_key_check found %d violation(s)", violations)
	}
	if err = db.Close(); err != nil {
		return DatabaseReport{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return DatabaseReport{}, err
	}
	digest, err := fileSHA256(abs)
	if err != nil {
		return DatabaseReport{}, err
	}
	return DatabaseReport{Path: abs, SizeBytes: info.Size(), SHA256: digest, Integrity: "ok", ForeignKeyViolations: 0}, nil
}

func backupDatabase(ctx context.Context, source *sql.DB, destination string) error {
	connection, err := source.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	return connection.Raw(func(driverConnection any) error {
		backuper, ok := driverConnection.(onlineBackuper)
		if !ok {
			return errors.New("sqlite driver does not expose online backup")
		}
		backup, err := backuper.NewBackup(destination)
		if err != nil {
			return err
		}
		finished := false
		defer func() {
			if !finished {
				_ = backup.Finish()
			}
		}()
		more := true
		for more {
			if err = ctx.Err(); err != nil {
				return err
			}
			more, err = backup.Step(256)
			if err != nil {
				return err
			}
		}
		finished = true
		return backup.Finish()
	})
}

func databaseSourceAndDestination(source, destination string) (string, string, error) {
	source, err := existingDatabasePath(source)
	if err != nil {
		return "", "", err
	}
	destination, err = newDatabaseDestination(destination)
	if err != nil {
		return "", "", err
	}
	if samePath(source, destination) {
		return "", "", errors.New("source and destination database paths must differ")
	}
	return source, destination, nil
}

func existingDatabasePath(path string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return "", fmt.Errorf("database must be a non-empty regular file: %s", abs)
	}
	return filepath.Clean(abs), nil
}

func newDatabaseDestination(path string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if _, err = os.Stat(abs); err == nil {
		return "", fmt.Errorf("destination already exists: %s", abs)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parent := filepath.Dir(abs)
	info, err := os.Stat(parent)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("destination parent is not a directory: %s", parent)
	}
	return abs, nil
}

func restoreTargetPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("restore target path is required")
	}
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	parent := filepath.Dir(abs)
	info, err := os.Stat(parent)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("restore target parent is not a directory: %s", parent)
	}
	if existing, statErr := os.Stat(abs); statErr == nil && !existing.Mode().IsRegular() {
		return "", fmt.Errorf("restore target is not a regular file: %s", abs)
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return "", statErr
	}
	return abs, nil
}

func temporaryDatabasePath(destination string) (string, func(), error) {
	file, err := os.CreateTemp(filepath.Dir(destination), ".point-db-*.db")
	if err != nil {
		return "", nil, err
	}
	path := file.Name()
	if err = file.Close(); err != nil {
		return "", nil, err
	}
	if err = os.Remove(path); err != nil {
		return "", nil, err
	}
	cleanup := func() {
		for _, item := range []string{path, path + "-wal", path + "-shm"} {
			_ = os.Remove(item)
		}
	}
	return path, cleanup, nil
}

func publishDatabase(temporary, destination string) error {
	if err := os.Chmod(temporary, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, destination); err != nil {
		return fmt.Errorf("publish database: %w", err)
	}
	return nil
}

func readOnlyDSN(path string) string {
	// modernc strips query parameters from a native path before sqlite3_open_v2,
	// so `path?mode=ro` does not actually set SQLite's read-only flag. Build a
	// proper file URI instead. A leading slash before a Windows drive prevents
	// the drive letter from being parsed as the URI authority.
	slashPath := filepath.ToSlash(filepath.Clean(path))
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	uri := url.URL{Scheme: "file", Path: slashPath}
	query := uri.Query()
	query.Set("mode", "ro")
	// A compact offline snapshot in WAL journal mode would otherwise create
	// fresh -wal/-shm reader sidecars merely by being verified. Mark it
	// immutable only when no WAL state exists to consume. Sources with existing
	// sidecars stay ordinary read-only connections and include committed WAL.
	if !pathExists(path+"-wal") && !pathExists(path+"-shm") {
		query.Set("immutable", "1")
	}
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "foreign_keys(1)")
	uri.RawQuery = query.Encode()
	return uri.String()
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func samePath(first, second string) bool {
	first, second = filepath.Clean(first), filepath.Clean(second)
	if strings.EqualFold(first, second) {
		return true
	}
	firstResolved, firstErr := filepath.EvalSymlinks(first)
	secondResolved, secondErr := filepath.EvalSymlinks(second)
	return firstErr == nil && secondErr == nil && strings.EqualFold(firstResolved, secondResolved)
}
