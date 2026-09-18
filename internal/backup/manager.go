package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"local-agent-workbench/internal/storage"
)

const filenamePrefix = "point-"

type SnapshotStore interface {
	Backup(context.Context, string) (storage.DatabaseReport, error)
}

type Policy struct {
	Daily    int   `json:"daily"`
	Weekly   int   `json:"weekly"`
	MaxBytes int64 `json:"maxBytes"`
}

func DefaultPolicy() Policy {
	return Policy{Daily: 7, Weekly: 4, MaxBytes: 2 * 1024 * 1024 * 1024}
}

type Snapshot struct {
	ID        string                 `json:"id"`
	Reason    string                 `json:"reason"`
	CreatedAt time.Time              `json:"createdAt"`
	Database  storage.DatabaseReport `json:"database"`
}

type Manager struct {
	store  SnapshotStore
	dir    string
	policy Policy
	now    func() time.Time
	mu     sync.Mutex
}

func NewManager(store SnapshotStore, dir string, policy Policy) *Manager {
	return &Manager{store: store, dir: filepath.Clean(dir), policy: normalizePolicy(policy), now: time.Now}
}

// Create uses SQLite's online backup API and verifies the resulting database
// before applying bounded retention. Calls are serialized so two event bursts
// cannot race on pruning.
func (m *Manager) Create(ctx context.Context, reason string) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store == nil {
		return Snapshot{}, errors.New("backup store is not configured")
	}
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return Snapshot{}, err
	}
	createdAt := m.now().UTC()
	id := snapshotID(createdAt, reason)
	destination := filepath.Join(m.dir, id)
	report, err := m.store.Backup(ctx, destination)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{ID: id, Reason: safeReason(reason), CreatedAt: createdAt, Database: report}
	if err = m.pruneLocked(ctx); err != nil {
		return snapshot, fmt.Errorf("backup created but retention failed: %w", err)
	}
	return snapshot, nil
}

// Latest returns the newest owned and verified snapshot. Unknown database files
// are intentionally outside this manager's retention boundary.
func (m *Manager) Latest(ctx context.Context) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items, err := m.listLocked()
	if err != nil {
		return Snapshot{}, err
	}
	if len(items) == 0 {
		return Snapshot{}, os.ErrNotExist
	}
	item := items[0]
	report, err := storage.VerifyDatabase(ctx, item.path)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{ID: item.name, Reason: reasonFromID(item.name), CreatedAt: item.createdAt, Database: report}, nil
}

// List returns only snapshots that pass a fresh integrity and SHA-256 check.
// A corrupt snapshot is never offered as a restore source.
func (m *Manager) List(ctx context.Context) ([]Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items, err := m.listLocked()
	if err != nil {
		return nil, err
	}
	result := make([]Snapshot, 0, len(items))
	var firstInvalid error
	for _, item := range items {
		report, verifyErr := storage.VerifyDatabase(ctx, item.path)
		if verifyErr != nil {
			if firstInvalid == nil {
				firstInvalid = fmt.Errorf("verify snapshot %s: %w", item.name, verifyErr)
			}
			continue
		}
		result = append(result, Snapshot{ID: item.name, Reason: reasonFromID(item.name), CreatedAt: item.createdAt, Database: report})
	}
	if len(result) == 0 && firstInvalid != nil {
		return nil, firstInvalid
	}
	return result, nil
}

// CreatePreMigration creates a verified recovery point before storage.Open can
// apply migrations. The source remains byte-for-byte untouched.
func CreatePreMigration(ctx context.Context, databasePath, backupDir string, now time.Time) (*Snapshot, error) {
	needed, err := storage.DatabaseNeedsMigration(ctx, databasePath)
	if err != nil || !needed {
		return nil, err
	}
	if err = os.MkdirAll(backupDir, 0o700); err != nil {
		return nil, err
	}
	createdAt := now.UTC()
	id := snapshotID(createdAt, "pre-migration")
	report, err := storage.BackupDatabase(ctx, databasePath, filepath.Join(backupDir, id))
	if err != nil {
		return nil, err
	}
	return &Snapshot{ID: id, Reason: "pre-migration", CreatedAt: createdAt, Database: report}, nil
}

type backupFile struct {
	name      string
	path      string
	createdAt time.Time
	size      int64
}

func (m *Manager) listLocked() ([]backupFile, error) {
	entries, err := os.ReadDir(m.dir)
	if errors.Is(err, os.ErrNotExist) {
		return []backupFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]backupFile, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !isOwnedSnapshotName(name) || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(m.dir, name)
		info, infoErr := os.Lstat(path)
		if infoErr != nil {
			return nil, infoErr
		}
		if !info.Mode().IsRegular() {
			continue
		}
		createdAt, parseErr := createdAtFromID(name)
		if parseErr != nil {
			continue
		}
		items = append(items, backupFile{name: name, path: path, createdAt: createdAt, size: info.Size()})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].createdAt.Equal(items[j].createdAt) {
			return items[i].name > items[j].name
		}
		return items[i].createdAt.After(items[j].createdAt)
	})
	return items, nil
}

func (m *Manager) pruneLocked(ctx context.Context) error {
	items, err := m.listLocked()
	if err != nil || len(items) == 0 {
		return err
	}
	for _, item := range items {
		if _, err = storage.VerifyDatabase(ctx, item.path); err != nil {
			return fmt.Errorf("verify retained snapshot %s: %w", item.name, err)
		}
	}
	keep := retentionSet(items, m.policy)
	for _, item := range items {
		if keep[item.name] {
			continue
		}
		if err = removeOwnedSnapshot(m.dir, item.path); err != nil {
			return err
		}
	}
	items, err = m.listLocked()
	if err != nil {
		return err
	}
	var total int64
	for _, item := range items {
		total += item.size
	}
	for index := len(items) - 1; total > m.policy.MaxBytes && index > 0; index-- {
		if err = removeOwnedSnapshot(m.dir, items[index].path); err != nil {
			return err
		}
		total -= items[index].size
	}
	if total > m.policy.MaxBytes {
		return fmt.Errorf("newest snapshot exceeds retention space limit (%d > %d bytes)", total, m.policy.MaxBytes)
	}
	return nil
}

func retentionSet(items []backupFile, policy Policy) map[string]bool {
	keep := map[string]bool{}
	days, weeks := map[string]bool{}, map[string]bool{}
	for _, item := range items {
		day := item.createdAt.Format("2006-01-02")
		if len(days) < policy.Daily && !days[day] {
			days[day], keep[item.name] = true, true
		}
		year, week := item.createdAt.ISOWeek()
		weekKey := fmt.Sprintf("%04d-%02d", year, week)
		if len(weeks) < policy.Weekly && !weeks[weekKey] {
			weeks[weekKey], keep[item.name] = true, true
		}
	}
	keep[items[0].name] = true
	return keep
}

func removeOwnedSnapshot(root, path string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("refusing to remove path outside backup directory: %s", path)
	}
	info, err := os.Lstat(pathAbs)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || !isOwnedSnapshotName(filepath.Base(pathAbs)) {
		return fmt.Errorf("refusing to remove unmanaged backup path: %s", path)
	}
	return os.Remove(pathAbs)
}

func normalizePolicy(policy Policy) Policy {
	if policy.Daily < 1 {
		policy.Daily = 1
	}
	if policy.Weekly < 0 {
		policy.Weekly = 0
	}
	if policy.MaxBytes < 1 {
		policy.MaxBytes = DefaultPolicy().MaxBytes
	}
	return policy
}

func snapshotID(at time.Time, reason string) string {
	return filenamePrefix + at.UTC().Format("20060102T150405.000000000Z") + "-" + safeReason(reason) + ".db"
}

func safeReason(reason string) string {
	var result strings.Builder
	lastDash := false
	for _, char := range strings.ToLower(strings.TrimSpace(reason)) {
		allowed := char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
		if allowed {
			result.WriteRune(char)
			lastDash = false
		} else if result.Len() > 0 && !lastDash {
			result.WriteByte('-')
			lastDash = true
		}
	}
	value := strings.Trim(result.String(), "-")
	if value == "" {
		return "backup"
	}
	if len(value) > 32 {
		value = strings.TrimRight(value[:32], "-")
	}
	return value
}

func reasonFromID(name string) string {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if !strings.HasPrefix(base, filenamePrefix) {
		return "backup"
	}
	remainder := strings.TrimPrefix(base, filenamePrefix)
	separator := strings.IndexByte(remainder, '-')
	if separator < 0 || separator == len(remainder)-1 {
		return "backup"
	}
	return remainder[separator+1:]
}

func isOwnedSnapshotName(name string) bool {
	if !strings.HasPrefix(name, filenamePrefix) || !strings.EqualFold(filepath.Ext(name), ".db") {
		return false
	}
	base := strings.TrimSuffix(strings.TrimPrefix(name, filenamePrefix), filepath.Ext(name))
	separator := strings.IndexByte(base, '-')
	if separator <= 0 || separator == len(base)-1 {
		return false
	}
	_, err := createdAtFromID(name)
	return err == nil
}

func createdAtFromID(name string) (time.Time, error) {
	base := strings.TrimSuffix(strings.TrimPrefix(name, filenamePrefix), filepath.Ext(name))
	separator := strings.IndexByte(base, '-')
	if separator <= 0 {
		return time.Time{}, errors.New("snapshot timestamp is missing")
	}
	return time.Parse("20060102T150405.000000000Z", base[:separator])
}
