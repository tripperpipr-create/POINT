package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLockfileFixture(t *testing.T, root, dir, manifest, lock string) {
	t.Helper()
	target := filepath.Join(root, dir)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if lock != "" {
		if err := os.WriteFile(filepath.Join(target, "package-lock.json"), []byte(lock), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const e6Lock = `{"name":"cf-vue-apps","lockfileVersion":3,"packages":{"":{"name":"cf-vue-apps","dependencies":{"vue":"^3.4.0"},"devDependencies":{"vite":"^5.0.0"}}}}`

// E6: этап добавил ssh2 в dependencies вложенного проекта, lock-файл не тронут.
func TestLockfileDriftFindsDependencyMissingFromLock(t *testing.T) {
	baseline, candidate := t.TempDir(), t.TempDir()
	writeLockfileFixture(t, baseline, "cf-vue-apps", `{"dependencies":{"vue":"^3.4.0"},"devDependencies":{"vite":"^5.0.0"}}`, e6Lock)
	writeLockfileFixture(t, candidate, "cf-vue-apps", `{"dependencies":{"vue":"^3.4.0","ssh2":"^1.15.0"},"devDependencies":{"vite":"^5.0.0"}}`, e6Lock)
	drifts := npmLockfileDrift(candidate, baseline)
	if len(drifts) != 1 || strings.Join(drifts[0].Missing, ",") != "ssh2" || filepath.ToSlash(drifts[0].Manifest) != "cf-vue-apps/package.json" {
		t.Fatalf("drift=%#v", drifts)
	}
	if summary := drifts[0].summary(); !strings.Contains(summary, "ssh2") || !strings.Contains(summary, "npm ci") {
		t.Fatalf("summary does not name the package: %s", summary)
	}
	evidence := lockfileDriftEvidence(drifts[0])
	if evidence.Status != "failed" || evidence.CriterionID != lockfileSyncCriterionID {
		t.Fatalf("evidence=%#v", evidence)
	}
}

func TestLockfileDriftAcceptsSyncedAndUnchangedProjects(t *testing.T) {
	baseline, candidate := t.TempDir(), t.TempDir()
	synced := `{"lockfileVersion":3,"packages":{"":{"dependencies":{"vue":"^3.4.0","ssh2":"^1.15.0"},"devDependencies":{"vite":"^5.0.0"}}}}`
	writeLockfileFixture(t, candidate, "cf-vue-apps", `{"dependencies":{"vue":"^3.4.0","ssh2":"^1.15.0"},"devDependencies":{"vite":"^5.0.0"}}`, synced)
	// Расхождение, принесённое из проекта, этапу не вменяется.
	stale := `{"dependencies":{"left-pad":"^1.0.0"}}`
	writeLockfileFixture(t, baseline, "legacy", stale, e6Lock)
	writeLockfileFixture(t, candidate, "legacy", stale, e6Lock)
	// Lock-файл v1 и манифест без lock-файла сверять не с чем.
	writeLockfileFixture(t, candidate, "old", `{"dependencies":{"a":"1"}}`, `{"lockfileVersion":1,"dependencies":{}}`)
	writeLockfileFixture(t, candidate, "nolock", `{"dependencies":{"a":"1"}}`, "")
	writeLockfileFixture(t, candidate, "node_modules/dep", `{"dependencies":{"b":"1"}}`, e6Lock)
	if drifts := npmLockfileDrift(candidate, baseline); len(drifts) != 0 {
		t.Fatalf("false drift: %#v", drifts)
	}
}

func TestLockfileDriftReportsChangedAndExtraVersions(t *testing.T) {
	candidate := t.TempDir()
	writeLockfileFixture(t, candidate, ".", `{"dependencies":{"vue":"^3.5.0"}}`, e6Lock)
	drifts := npmLockfileDrift(candidate, "")
	if len(drifts) != 1 || len(drifts[0].Changed) != 1 || strings.Join(drifts[0].Extra, ",") != "vite" {
		t.Fatalf("drift=%#v", drifts)
	}
}
