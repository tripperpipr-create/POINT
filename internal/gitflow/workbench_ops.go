package gitflow

import (
	"context"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"strconv"
	"strings"
)

func number(value string) int                            { n, _ := strconv.Atoi(value); return n }
func copyDigest(h hash.Hash, r io.Reader) (int64, error) { return io.Copy(h, r) }

var ErrStaleRevision = errors.New("Git изменился: обновите diff перед действием")

type Command struct {
	Action    string   `json:"action"`
	Paths     []string `json:"paths,omitempty"`
	Patch     string   `json:"patch,omitempty"`
	Message   string   `json:"message,omitempty"`
	Ref       string   `json:"ref,omitempty"`
	Name      string   `json:"name,omitempty"`
	Remote    string   `json:"remote,omitempty"`
	URL       string   `json:"url,omitempty"`
	Revision  string   `json:"revision"`
	Amend     bool     `json:"amend,omitempty"`
	Confirmed bool     `json:"confirmed,omitempty"`
	Strategy  string   `json:"strategy,omitempty"`
}
type ActionResult struct {
	Snapshot Snapshot `json:"snapshot"`
	Message  string   `json:"message"`
	Commit   string   `json:"commit,omitempty"`
}

func Execute(ctx context.Context, runner Runner, dir string, c Command) (ActionResult, error) {
	unlock := LockRepository(ctx, runner, dir)
	defer unlock()
	s, err := ReadSnapshot(ctx, runner, dir)
	result := ActionResult{Snapshot: s}
	if err != nil {
		return result, err
	}
	if c.Revision == "" || c.Revision != s.Revision {
		return result, ErrStaleRevision
	}
	for _, p := range c.Paths {
		if _, err := SafePath(s.Root, p); err != nil {
			return result, err
		}
	}
	for _, ref := range []string{c.Ref, c.Name, c.Remote} {
		if strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, "\x00\r\n") {
			return result, errors.New("неверный аргумент Git")
		}
	}
	if c.Remote != "" && (c.Action == "push" || c.Action == "forcePush" || c.Action == "commitAndPush" || c.Action == "removeRemote") {
		known := false
		for _, r := range s.Remotes {
			known = known || r.Name == c.Remote
		}
		if !known {
			return result, errors.New("remote не найден")
		}
	}
	var args []string
	switch c.Action {
	case "discardTracked":
		if !c.Confirmed || len(c.Paths) == 0 {
			return result, errors.New("подтвердите отмену выбранных изменений")
		}
		if s.Head == "" {
			return result, errors.New("в новом репозитории ещё нет версии для восстановления")
		}
		for _, path := range c.Paths {
			known := false
			for _, change := range s.Changes {
				known = known || (change.Area != "untracked" && (change.Path == path || change.OriginalPath == path))
			}
			if !known {
				return result, errors.New("выбранный файл больше не изменён")
			}
		}
		args = append([]string{"restore", "--source=HEAD", "--staged", "--worktree", "--"}, c.Paths...)
	case "stage":
		if len(c.Paths) == 0 {
			return result, errors.New("выберите файлы для подготовки")
		}
		args = append([]string{"add", "-A", "--"}, c.Paths...)
	case "unstage":
		if len(c.Paths) == 0 {
			return result, errors.New("выберите подготовленные файлы")
		}
		if s.Head == "" {
			args = append([]string{"rm", "-f", "--cached", "--ignore-unmatch", "--"}, c.Paths...)
		} else {
			args = append([]string{"reset", "-q", "HEAD", "--"}, c.Paths...)
		}
	case "stagePatch", "unstagePatch", "applyPatch":
		if c.Patch == "" || len(c.Patch) > 4*1024*1024 {
			return result, errors.New("патч пуст или слишком велик")
		}
		if err = applyPatch(ctx, runner, s.Root, c.Patch, c.Action == "unstagePatch", c.Action != "applyPatch"); err != nil {
			return result, err
		}
	case "commit", "commitAndPush":
		if strings.TrimSpace(c.Message) == "" || len(c.Message) > 8192 {
			return result, errors.New("укажите сообщение коммита")
		}
		staged := false
		for _, change := range s.Changes {
			if change.Area == "conflict" {
				return result, errors.New("сначала разрешите конфликты")
			}
			staged = staged || change.Area == "staged"
		}
		if !staged && !c.Amend {
			return result, ErrNothingToCommit
		}
		if c.Amend && s.Head == "" {
			return result, errors.New("нет коммита для amend")
		}
		if c.Amend && s.Remote != "" && !c.Confirmed {
			if published, e := run(ctx, runner, s.Root, "merge-base", "--is-ancestor", "HEAD", "@{upstream}"); e == nil && published == "" {
				return result, errors.New("amend опубликованного коммита требует подтверждения")
			}
		}
		args = []string{"commit", "-m", c.Message}
		if c.Amend {
			args = append(args, "--amend")
		}
	case "fetch":
		args = []string{"fetch", "--all", "--prune"}
	case "push", "forcePush":
		if c.Remote == "" || s.Branch == "" {
			return result, errors.New("выберите remote и локальную ветку")
		}
		args = []string{"push", "--porcelain", "-u"}
		if c.Action == "forcePush" {
			if !c.Confirmed {
				return result, errors.New("переписывание истории требует подтверждения")
			}
			args = append(args, "--force-with-lease")
		}
		target := c.Ref
		if target == "" {
			target = s.Branch
		}
		if _, e := run(ctx, runner, s.Root, "check-ref-format", "--branch", target); e != nil {
			return result, e
		}
		args = append(args, c.Remote, "refs/heads/"+s.Branch+":refs/heads/"+target)
	case "pull":
		if s.Operation != "" {
			return result, errors.New("сначала завершите текущую Git-операцию")
		}
		args = []string{"pull"}
		switch c.Strategy {
		case "merge":
			args = append(args, "--no-rebase")
		case "rebase":
			args = append(args, "--rebase")
		case "ff-only":
			args = append(args, "--ff-only")
		case "":
			configured, _ := run(ctx, runner, s.Root, "config", "--get", "pull.rebase")
			ff, _ := run(ctx, runner, s.Root, "config", "--get", "pull.ff")
			branchConfig, _ := run(ctx, runner, s.Root, "config", "--get", "branch."+s.Branch+".rebase")
			if s.Ahead > 0 && s.Behind > 0 && configured == "" && branchConfig == "" && ff == "" {
				return result, errors.New("ветки разошлись: выберите merge или rebase")
			}
		default:
			return result, errors.New("неверная стратегия pull")
		}
	case "createBranch":
		if c.Name == "" {
			return result, errors.New("нужно имя ветки")
		}
		if _, e := run(ctx, runner, s.Root, "check-ref-format", "--branch", c.Name); e != nil {
			return result, e
		}
		args = []string{"switch", "--no-track", "-c", c.Name}
		if c.Ref != "" {
			args = append(args, c.Ref)
		}
	case "switchBranch":
		if c.Ref == "" {
			return result, errors.New("выберите ветку")
		}
		args = []string{"switch", c.Ref}
		for _, tag := range s.Tags {
			if "refs/tags/"+tag == c.Ref || (tag == c.Ref && !hasRef(s.LocalBranches, c.Ref)) {
				args = []string{"switch", "--detach", c.Ref}
			}
		}
		for _, remote := range s.RemoteBranches {
			if remote == c.Ref {
				args = []string{"switch", "--track", c.Ref}
			}
		}
	case "renameBranch":
		if c.Name == "" {
			return result, errors.New("нужно имя ветки")
		}
		args = []string{"branch", "-m", c.Name}
	case "deleteBranch":
		if c.Ref == "" || !c.Confirmed {
			return result, errors.New("подтвердите удаление ветки")
		}
		args = []string{"branch", "-d", c.Ref}
	case "addRemote":
		if c.Name == "" || c.URL == "" || strings.HasPrefix(c.URL, "-") || strings.Contains(c.URL, "::") {
			return result, errors.New("укажите имя и адрес remote")
		}
		args = []string{"remote", "add", c.Name, c.URL}
	case "removeRemote":
		if c.Remote == "" || !c.Confirmed {
			return result, errors.New("подтвердите удаление remote")
		}
		args = []string{"remote", "remove", c.Remote}
	case "setUpstream":
		if c.Ref == "" {
			return result, errors.New("выберите upstream")
		}
		args = []string{"branch", "--set-upstream-to=" + c.Ref}
	case "createTag":
		if c.Name == "" {
			return result, errors.New("нужно имя тега")
		}
		args = []string{"tag", c.Name}
		if c.Ref != "" {
			args = append(args, c.Ref)
		}
	case "deleteTag":
		if c.Name == "" || !c.Confirmed {
			return result, errors.New("подтвердите удаление тега")
		}
		args = []string{"tag", "-d", c.Name}
	case "stashPush":
		args = []string{"stash", "push", "--include-untracked"}
		if c.Message != "" {
			args = append(args, "-m", c.Message)
		}
	case "stashApply", "stashDrop":
		if !validStash(c.Ref) {
			return result, errors.New("выберите запись stash")
		}
		if c.Action == "stashDrop" && !c.Confirmed {
			return result, errors.New("подтвердите удаление stash")
		}
		verb := "apply"
		if c.Action == "stashDrop" {
			verb = "drop"
		}
		args = []string{"stash", verb, c.Ref}
	case "merge", "rebase", "cherry-pick", "revert":
		if c.Action == "rebase" && !c.Confirmed && c.Ref != "" {
			total, e := run(ctx, runner, s.Root, "rev-list", "--count", "HEAD", "--not", c.Ref)
			if e != nil {
				return result, e
			}
			local, e := run(ctx, runner, s.Root, "rev-list", "--count", "HEAD", "--not", c.Ref, "--remotes")
			if e != nil {
				return result, e
			}
			if total != local {
				return result, errors.New("rebase опубликованной истории требует подтверждения")
			}
		}
		if c.Ref == "" {
			return result, errors.New("выберите ревизию")
		}
		if s.Operation != "" {
			return result, errors.New("уже выполняется Git-операция")
		}
		if len(s.Changes) > 0 {
			return result, errors.New("сначала закоммитьте или отложите локальные изменения")
		}
		args = []string{c.Action, c.Ref}
	case "continue", "abort":
		if s.Operation == "" {
			return result, errors.New("нет незавершённой операции")
		}
		args = []string{s.Operation, "--" + c.Action}
		if s.Operation == "merge" && c.Action == "continue" {
			args = []string{"commit", "--no-edit"}
		}
	default:
		return result, fmt.Errorf("неизвестное Git-действие %q", c.Action)
	}
	if args != nil {
		_, err = runner.Run(ctx, s.Root, args...)
	}
	if err == nil && (c.Action == "commit" || c.Action == "commitAndPush") {
		result.Commit, _ = run(ctx, runner, s.Root, "rev-parse", "HEAD")
		result.Message = "Коммит " + result.Commit + " создан"
		if c.Action == "commitAndPush" {
			if c.Remote == "" || s.Branch == "" {
				err = errors.New("коммит создан; выберите remote для push")
			} else {
				target := c.Ref
				if target == "" {
					target = s.Branch
				}
				if _, err = runner.Run(ctx, s.Root, "check-ref-format", "--branch", target); err == nil {
					_, err = runner.Run(ctx, s.Root, "push", "--porcelain", "-u", c.Remote, "refs/heads/"+s.Branch+":refs/heads/"+target)
				}
			}
			if err != nil {
				err = fmt.Errorf("коммит %s создан; push не выполнен: %w", result.Commit, err)
			}
		}
	}
	fresh, e := ReadSnapshot(ctx, runner, s.Root)
	if e == nil {
		result.Snapshot = fresh
	}
	if result.Message == "" && err == nil {
		result.Message = "Git: " + c.Action + " выполнено"
	}
	return result, err
}
func hasRef(refs []string, value string) bool {
	for _, ref := range refs {
		if ref == value {
			return true
		}
	}
	return false
}
func validStash(ref string) bool {
	if !strings.HasPrefix(ref, "stash@{") || !strings.HasSuffix(ref, "}") {
		return false
	}
	n, e := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(ref, "stash@{"), "}"))
	return e == nil && n >= 0
}
func applyPatch(ctx context.Context, runner Runner, root, patch string, reverse, cached bool) error {
	temp, e := os.CreateTemp("", "point-index-*.patch")
	if e != nil {
		return e
	}
	name := temp.Name()
	defer os.Remove(name)
	_, e = temp.WriteString(patch)
	temp.Close()
	if e != nil {
		return e
	}
	out, e := runner.Run(ctx, root, "apply", "--numstat", "-z", name)
	if e != nil {
		return e
	}
	records := strings.Split(string(out), "\x00")
	for _, record := range records {
		if record == "" {
			continue
		}
		fields := strings.SplitN(record, "\t", 3)
		if len(fields) != 3 {
			return errors.New("патч переименования готовится целым файлом")
		}
		if _, e = SafePath(root, fields[2]); e != nil {
			return e
		}
	}
	args := []string{"apply", "--whitespace=nowarn"}
	if cached {
		args = append(args, "--cached")
	}
	if reverse {
		args = append(args, "--reverse")
	}
	if _, e = runner.Run(ctx, root, append(append([]string{}, args...), "--check", name)...); e != nil {
		return e
	}
	_, e = runner.Run(ctx, root, append(args, name)...)
	return e
}
func Diff(ctx context.Context, runner Runner, dir, path, area string) (string, error) {
	if path != "" {
		if _, e := SafePath(dir, path); e != nil {
			return "", e
		}
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--binary", "--"}
	if area == "staged" {
		args = []string{"diff", "--cached", "--no-ext-diff", "--no-textconv", "--binary", "--"}
	}
	if path != "" {
		args = append(args, path)
	}
	out, e := runner.Run(ctx, dir, args...)
	return string(out), e
}
