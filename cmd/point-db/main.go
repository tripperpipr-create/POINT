package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"local-agent-workbench/internal/storage"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "point-db:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "verify":
		flags := flag.NewFlagSet("verify", flag.ContinueOnError)
		database := flags.String("db", "", "SQLite database path")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		report, err := storage.VerifyDatabase(ctx, *database)
		return printReport(report, err)
	case "backup":
		flags := flag.NewFlagSet("backup", flag.ContinueOnError)
		database := flags.String("db", "", "source SQLite database path")
		output := flags.String("out", "", "new backup database path")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		report, err := storage.BackupDatabase(ctx, *database, *output)
		return printReport(report, err)
	case "migrate-copy":
		flags := flag.NewFlagSet("migrate-copy", flag.ContinueOnError)
		database := flags.String("db", "", "source SQLite database path")
		output := flags.String("out", "", "new migrated database path")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		report, err := storage.MigrateDatabaseCopy(ctx, *database, *output)
		return printReport(report, err)
	case "migrate-corpus":
		flags := flag.NewFlagSet("migrate-corpus", flag.ContinueOnError)
		source := flags.String("source-dir", "", "directory containing source .db copies")
		output := flags.String("out-dir", "", "empty/nonexistent output directory")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		reports, err := migrateCorpus(ctx, *source, *output)
		return printReport(reports, err)
	case "restore":
		flags := flag.NewFlagSet("restore", flag.ContinueOnError)
		backup := flags.String("backup", "", "verified backup database path")
		database := flags.String("db", "", "offline target SQLite database path")
		confirmed := flags.Bool("confirm-offline", false, "confirm Point Core is stopped")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if !*confirmed {
			return errors.New("restore requires --confirm-offline after Point Core is stopped")
		}
		report, err := storage.RestoreDatabaseOffline(ctx, *backup, *database)
		return printReport(report, err)
	default:
		return usageError()
	}
}

func migrateCorpus(ctx context.Context, sourceDirectory, outputDirectory string) ([]storage.MigrationCopyReport, error) {
	if strings.TrimSpace(sourceDirectory) == "" || strings.TrimSpace(outputDirectory) == "" {
		return nil, errors.New("migrate-corpus requires --source-dir and --out-dir")
	}
	source, err := filepath.Abs(sourceDirectory)
	if err != nil {
		return nil, err
	}
	output, err := filepath.Abs(outputDirectory)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(filepath.Clean(source), filepath.Clean(output)) {
		return nil, errors.New("migration corpus output must differ from source")
	}
	info, err := os.Stat(source)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("migration corpus source is not a directory")
	}
	if _, err = os.Stat(output); err == nil {
		entries, readErr := os.ReadDir(output)
		if readErr != nil {
			return nil, readErr
		}
		if len(entries) != 0 {
			return nil, errors.New("migration corpus output directory must be empty")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err = os.MkdirAll(output, 0o700); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return nil, err
	}
	files := []string{}
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.EqualFold(filepath.Ext(entry.Name()), ".db") {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, errors.New("migration corpus contains no .db files")
	}
	reports := make([]storage.MigrationCopyReport, 0, len(files))
	for _, name := range files {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		report, migrateErr := storage.MigrateDatabaseCopy(ctx, filepath.Join(source, name), filepath.Join(output, name))
		if migrateErr != nil {
			return nil, fmt.Errorf("migrate corpus item %s: %w", name, migrateErr)
		}
		reports = append(reports, report)
	}
	return reports, nil
}

func printReport(value any, err error) error {
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func usageError() error {
	return errors.New("usage: point-db verify --db PATH | backup --db PATH --out PATH | migrate-copy --db PATH --out PATH | migrate-corpus --source-dir DIR --out-dir DIR | restore --backup PATH --db PATH --confirm-offline")
}
