package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"local-agent-workbench/internal/agent"
	"local-agent-workbench/internal/domain"
)

// Lock-файл кандидата сверяется с манифестом до доставки (Q09, E6).
//
// В E6 этап добавил `ssh2` в dependencies, а package-lock.json не тронул.
// Приёмка прогнала только критерии задания, и пайплайн проекта упал на
// `npm ci` раньше деплоя. Сверка — чистый разбор JSON без сети: `npm ci`
// отказывает ровно тогда, когда корневой пакет lock-файла расходится с
// манифестом, и это видно без установки.

// npmDependencySections — поля манифеста, которые `npm ci` сверяет с корнем
// lock-файла (`packages[""]`).
var npmDependencySections = []string{"dependencies", "devDependencies", "optionalDependencies", "peerDependencies"}

// lockfileSkipDirs — каталоги, где манифесты чужие или собранные.
var lockfileSkipDirs = map[string]bool{"node_modules": true, ".git": true, "vendor": true, "dist": true, "build": true, ".next": true, "coverage": true}

const lockfileSearchDepth = 3

type lockDrift struct {
	Manifest string   // путь package.json относительно корня кандидата
	Missing  []string // в манифесте есть, в lock-файле нет
	Extra    []string // в lock-файле есть, в манифесте нет
	Changed  []string // версия в манифесте не та, что в lock-файле
}

func (d lockDrift) summary() string {
	parts := make([]string, 0, 3)
	if len(d.Missing) > 0 {
		parts = append(parts, "нет в lock: "+strings.Join(d.Missing, ", "))
	}
	if len(d.Extra) > 0 {
		parts = append(parts, "лишние в lock: "+strings.Join(d.Extra, ", "))
	}
	if len(d.Changed) > 0 {
		parts = append(parts, "другая версия: "+strings.Join(d.Changed, ", "))
	}
	lock := filepath.ToSlash(filepath.Join(filepath.Dir(d.Manifest), "package-lock.json"))
	return fmt.Sprintf("%s не соответствует %s (%s) — `npm ci` откажет; обновите lock-файл через `npm install`", lock, filepath.ToSlash(d.Manifest), strings.Join(parts, "; "))
}

// npmLockfileDrift находит package.json кандидата, чей package-lock.json
// разошёлся с ним. baseline — проект до работы: манифест и lock-файл, которые
// кандидат не менял, не проверяются — расхождение, принесённое из проекта,
// не вина этапа. Пустой baseline — проверяются все манифесты.
func npmLockfileDrift(candidate, baseline string) []lockDrift {
	var drifts []lockDrift
	_ = filepath.WalkDir(candidate, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(candidate, path)
		if relErr != nil {
			return nil
		}
		if entry.IsDir() {
			if rel != "." && (lockfileSkipDirs[entry.Name()] || strings.Count(filepath.ToSlash(rel), "/") >= lockfileSearchDepth) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != "package.json" {
			return nil
		}
		lockRel := filepath.Join(filepath.Dir(rel), "package-lock.json")
		if baseline != "" && sameFileContent(path, filepath.Join(baseline, rel)) && sameFileContent(filepath.Join(candidate, lockRel), filepath.Join(baseline, lockRel)) {
			return nil
		}
		if drift, ok := compareNpmLock(path, filepath.Join(candidate, lockRel)); ok {
			drift.Manifest = rel
			drifts = append(drifts, drift)
		}
		return nil
	})
	return drifts
}

// compareNpmLock сверяет манифест с корнем lock-файла. Нет lock-файла, он
// старого формата (v1 без `packages`) или не разбирается — сверять нечего,
// и это не расхождение: `npm ci` без lock-файла откажет сам и честно.
func compareNpmLock(manifestPath, lockPath string) (lockDrift, bool) {
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		return lockDrift{}, false
	}
	lockRaw, err := os.ReadFile(lockPath)
	if err != nil {
		return lockDrift{}, false
	}
	var manifest map[string]json.RawMessage
	if json.Unmarshal(manifestRaw, &manifest) != nil {
		return lockDrift{}, false
	}
	var lock struct {
		LockfileVersion int                                   `json:"lockfileVersion"`
		Packages        map[string]map[string]json.RawMessage `json:"packages"`
	}
	if json.Unmarshal(lockRaw, &lock) != nil || lock.LockfileVersion < 2 {
		return lockDrift{}, false
	}
	root, ok := lock.Packages[""]
	if !ok {
		return lockDrift{}, false
	}
	var drift lockDrift
	for _, section := range npmDependencySections {
		want := dependencySection(manifest[section])
		have := dependencySection(root[section])
		for name, spec := range want {
			locked, present := have[name]
			switch {
			case !present:
				drift.Missing = append(drift.Missing, name)
			case locked != spec:
				drift.Changed = append(drift.Changed, fmt.Sprintf("%s (%s ≠ %s)", name, spec, locked))
			}
		}
		for name := range have {
			if _, present := want[name]; !present {
				drift.Extra = append(drift.Extra, name)
			}
		}
	}
	sort.Strings(drift.Missing)
	sort.Strings(drift.Extra)
	sort.Strings(drift.Changed)
	return drift, len(drift.Missing)+len(drift.Extra)+len(drift.Changed) > 0
}

func dependencySection(raw json.RawMessage) map[string]string {
	result := map[string]string{}
	if len(raw) == 0 {
		return result
	}
	_ = json.Unmarshal(raw, &result)
	return result
}

func sameFileContent(left, right string) bool {
	a, errA := os.ReadFile(left)
	b, errB := os.ReadFile(right)
	if errA != nil || errB != nil {
		return os.IsNotExist(errA) && os.IsNotExist(errB)
	}
	return bytes.Equal(a, b)
}

// lockfileSyncCriterionID — проверка, которую Point добавляет к приёмке сам:
// в задании её нет, но без неё кандидат не устанавливается.
const lockfileSyncCriterionID = "point-lockfile-sync"

// candidateLockfileDrift сверяет кандидата приёмки с проектом, из которого он
// вырос: базой служит открытый проект этого же мира. Без него проверяются все
// манифесты кандидата — расхождение `npm ci` не пропустит в любом случае.
func (a *App) candidateLockfileDrift(flowRun domain.FlowRun, candidate string) []lockDrift {
	baseline := ""
	if ws, err := a.requireWorkspace(); err == nil && ws.ID == flowRun.WorkspaceID {
		baseline = ws.Path
	}
	return npmLockfileDrift(candidate, baseline)
}

func lockfileDriftEvidence(drift lockDrift) agent.CriterionEvidence {
	return agent.CriterionEvidence{
		CriterionID: lockfileSyncCriterionID, Kind: "verification", Status: "failed",
		Text:  "package-lock.json соответствует " + filepath.ToSlash(drift.Manifest),
		Check: &agent.CheckEvidence{Tool: "point", Detail: drift.summary(), Status: "unresolved"},
	}
}
