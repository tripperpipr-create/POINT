package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"local-agent-workbench/internal/domain"
	"local-agent-workbench/internal/sandbox"
	"local-agent-workbench/internal/workspace"
)

// Повтор этапа продолжает с кандидата прерванной попытки (TODO Q11, E6).
//
// Новая попытка получала песочницу от того же неизменного исходника, что и
// прерванная, и всё сделанное до обрыва пропадало: в E6 интегратор начал
// синхронизацию lock-файла, связь с моделью оборвалась, и повтор начал с
// нуля. Теперь текстовые изменения прерванной попытки относительно её
// исходника перекладываются в новую песочницу. Исходник у обеих один, поэтому
// набор изменений новой попытки включает перенесённое сам. Файл, который в
// новой песочнице уже не совпадает с исходником, не трогается и называется.
// Не переносится ничего, если исходники разные, аудит прерванной песочницы
// неполон или она живая (её правки и так в проекте).

type attemptCarry struct {
	From    string   `json:"from"`
	Files   []string `json:"files,omitempty"`
	Skipped []string `json:"skipped,omitempty"`
	Reason  string   `json:"reason,omitempty"`
}

const (
	attemptCarryOutputKey = "carriedAttempt"
	attemptCarryNoteKey   = "carriedAttemptNote"
)

// carryPreviousAttemptV2 переносит изменения попытки previousExecutionID в
// песочницу execution.
func (a *App) carryPreviousAttemptV2(ctx context.Context, previousExecutionID string, execution domain.ExecutionInstance) attemptCarry {
	carry := attemptCarry{From: previousExecutionID}
	previous, err := a.store.GetExecution(ctx, previousExecutionID)
	if err != nil {
		carry.Reason = "прерванная попытка не найдена"
		return carry
	}
	from, err := a.store.GetSandbox(ctx, previous.SandboxID)
	if err != nil {
		carry.Reason = "песочница прерванной попытки не найдена"
		return carry
	}
	to, err := a.store.GetSandbox(ctx, execution.SandboxID)
	if err != nil {
		carry.Reason = "песочница новой попытки не найдена"
		return carry
	}
	if reason := attemptCarryRefusal(from, to); reason != "" {
		carry.Reason = reason
		return carry
	}
	if status, integrityErr := a.sandboxIntegrity(ctx, from); integrityErr != nil || status.Incomplete {
		carry.Reason = "аудит прерванной песочницы неполон: её исход неизвестен"
		return carry
	}
	diffs, err := a.attemptDiff(ctx, from)
	if err != nil {
		carry.Reason = "изменения прерванной попытки не прочитаны"
		return carry
	}
	fs, err := workspace.Open(to.Path)
	if err != nil {
		carry.Reason = "песочница новой попытки недоступна"
		return carry
	}
	if strings.TrimSpace(to.FileRulesVersion) != "" {
		if err = fs.UseFileRules(to.FileRulesVersion); err != nil {
			carry.Reason = "правила файлов новой песочницы неизвестны"
			return carry
		}
	}
	// В режиме тома запись помечает зеркало, и она доезжает до контейнера.
	if observer, ok := a.sandboxBackend.(interface{ WatchMirror(*workspace.FS) }); ok {
		observer.WatchMirror(fs)
	}
	for _, entry := range diffs {
		if carryErr := carryDiffEntry(fs, entry); carryErr != nil {
			carry.Skipped = append(carry.Skipped, entry.Path)
			continue
		}
		carry.Files = append(carry.Files, entry.Path)
	}
	return carry
}

// attemptCarryRefusal — почему переносить нельзя; пусто, если можно.
func attemptCarryRefusal(from, to domain.SandboxRecord) string {
	if from.Kind == "live" || to.Kind == "live" {
		return "попытки работали в самом проекте"
	}
	if strings.TrimSpace(from.BaselinePath) == "" {
		return "у прерванной песочницы нет исходника"
	}
	if info, err := os.Stat(from.Path); err != nil || !info.IsDir() {
		return "песочница прерванной попытки уже убрана"
	}
	if from.FileRulesVersion != to.FileRulesVersion || !slices.Equal(sortedParents(from), sortedParents(to)) {
		return "у попыток разные исходники"
	}
	return ""
}

func sortedParents(record domain.SandboxRecord) []string {
	parents := sandboxParentExecutionIDs(record)
	slices.Sort(parents)
	return parents
}

func (a *App) attemptDiff(ctx context.Context, from domain.SandboxRecord) ([]sandbox.DiffEntry, error) {
	if ruled, ok := a.sandboxBackend.(interface {
		DiffWithRules(context.Context, string, string, string) ([]sandbox.DiffEntry, error)
	}); ok && strings.TrimSpace(from.FileRulesVersion) != "" {
		return ruled.DiffWithRules(ctx, from.BaselinePath, from.Path, from.FileRulesVersion)
	}
	return a.sandboxBackend.Diff(ctx, from.BaselinePath, from.Path)
}

var errCarryConflict = errors.New("file differs from the shared baseline")

// carryDiffEntry кладёт одно изменение, только если файл новой песочницы
// совпадает с общим исходником.
func carryDiffEntry(fs *workspace.FS, entry sandbox.DiffEntry) error {
	abs, err := fs.Resolve(entry.Path, true)
	if err != nil {
		return err
	}
	current, readErr := os.ReadFile(abs)
	exists := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	switch entry.Kind {
	case "create":
		if exists {
			return errCarryConflict
		}
		if err = os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
			return err
		}
		_, err = fs.Write(entry.Path, entry.Proposed)
		return err
	case "modify":
		if !exists || string(current) != entry.Original {
			return errCarryConflict
		}
		_, err = fs.Write(entry.Path, entry.Proposed)
		return err
	case "delete":
		if !exists {
			return nil
		}
		return fs.RestoreAgentChange(entry.Path, entry.Original, "", false)
	}
	return errCarryConflict
}

// attemptCarryNote — что новая попытка получила от прерванной.
func attemptCarryNote(carry attemptCarry) string {
	switch {
	case len(carry.Files) > 0:
		note := "Изменения прерванной попытки уже перенесены в вашу песочницу: " + strings.Join(limitStrings(carry.Files, 20), ", ") +
			". Проверьте их и продолжайте с них, а не делайте заново."
		if len(carry.Skipped) > 0 {
			note += " Не перенесены (файл в новой песочнице иной): " + strings.Join(limitStrings(carry.Skipped, 10), ", ") + "."
		}
		return note
	case carry.Reason != "":
		return "Изменения прерванной попытки не перенесены: " + carry.Reason + ". Песочница начинается с исходного состояния этапа."
	}
	return ""
}

func limitStrings(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return append(append([]string(nil), values[:limit]...), "…")
}
