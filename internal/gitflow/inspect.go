package gitflow

import (
	"context"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	workbenchtools "local-agent-workbench/internal/tools"
)

// RepoReport — что git-агент знает о репозитории перед нарядом. Только
// чтение плюс fetch: рабочее дерево человека осмотр не меняет.
type RepoReport struct {
	// Path — корень репозитория относительно папки проекта через «/»; "." —
	// репозиторий, в котором лежит сама папка (она может быть его подпапкой).
	Path string `json:"path"`
	Dir  string `json:"-"`

	Current    string `json:"current,omitempty"`
	Detached   bool   `json:"detached,omitempty"`
	HeadCommit string `json:"headCommit"`

	Upstream     string `json:"upstream,omitempty"`
	UpstreamGone bool   `json:"upstreamGone,omitempty"`
	Ahead        int    `json:"ahead,omitempty"`
	Behind       int    `json:"behind,omitempty"`

	Remote            string `json:"remote,omitempty"`
	DefaultBranch     string `json:"defaultBranch,omitempty"`
	DefaultRef        string `json:"defaultRef,omitempty"`
	DefaultCommit     string `json:"defaultCommit,omitempty"`
	BehindDefault     int    `json:"behindDefault,omitempty"`
	MergedIntoDefault bool   `json:"mergedIntoDefault,omitempty"`

	CurrentBase       string `json:"currentBase,omitempty"`
	CurrentBaseCommit string `json:"currentBaseCommit,omitempty"`

	Dirty      []string `json:"dirty,omitempty"`
	DirtyCount int      `json:"dirtyCount,omitempty"`

	IdentityName  string `json:"identityName,omitempty"`
	IdentityEmail string `json:"identityEmail,omitempty"`
	GPGSign       bool   `json:"gpgSign,omitempty"`

	RecentSubjects []string       `json:"recentSubjects,omitempty"`
	BranchPrefixes map[string]int `json:"branchPrefixes,omitempty"`
	Branches       []string       `json:"-"`

	FetchError      string `json:"fetchError,omitempty"`
	Protected       bool   `json:"protected,omitempty"`
	ProtectedSource string `json:"protectedSource,omitempty"`
	GitLab          bool   `json:"gitlab,omitempty"`
}

type InspectOptions struct {
	// Fetch — обновить сведения с сервера (`fetch --prune`). Сбой не фатален:
	// отчёт тогда описывает состояние на прошлый fetch и говорит об этом.
	Fetch        bool
	FetchTimeout time.Duration
}

// Repositories — репозитории папки проекта: тот, в котором она лежит (корень
// или подпапка), иначе вложенные по правилам DiscoverGitRepos.
func Repositories(ctx context.Context, runner Runner, root string) []RepoReport {
	var repos []RepoReport
	if top, err := run(ctx, runner, root, "rev-parse", "--show-toplevel"); err == nil && top != "" {
		repos = append(repos, RepoReport{Path: ".", Dir: filepath.Clean(filepath.FromSlash(top))})
	}
	for _, rel := range workbenchtools.DiscoverGitRepos(root, true) {
		if rel == "." {
			continue
		}
		repos = append(repos, RepoReport{Path: rel, Dir: filepath.Join(root, filepath.FromSlash(rel))})
	}
	return repos
}

// Inspect осматривает все репозитории папки.
func Inspect(ctx context.Context, runner Runner, root string, options InspectOptions) []RepoReport {
	repos := Repositories(ctx, runner, root)
	for index := range repos {
		repos[index] = InspectRepo(ctx, runner, repos[index], options)
	}
	return repos
}

var trackPattern = regexp.MustCompile(`(ahead|behind) (\d+)`)

// InspectRepo заполняет отчёт одного репозитория.
func InspectRepo(ctx context.Context, runner Runner, report RepoReport, options InspectOptions) RepoReport {
	dir := report.Dir
	if remote, err := run(ctx, runner, dir, "remote", "get-url", "origin"); err == nil {
		report.Remote = StripCredentials(remote)
	}
	if options.Fetch && report.Remote != "" {
		timeout := options.FetchTimeout
		if timeout <= 0 {
			timeout = 8 * time.Second
		}
		fetchCtx, cancel := context.WithTimeout(ctx, timeout)
		unlock := LockRepository(fetchCtx, runner, dir)
		_, fetchErr := run(fetchCtx, runner, dir, "fetch", "--prune", "--quiet", "origin")
		unlock()
		if err := fetchErr; err != nil {
			report.FetchError = clip(err.Error(), 300)
		}
		cancel()
	}
	report.HeadCommit, _ = run(ctx, runner, dir, "rev-parse", "--verify", "HEAD")
	report.Current, _ = run(ctx, runner, dir, "branch", "--show-current")
	report.Detached = report.Current == ""
	if !report.Detached {
		if line, err := run(ctx, runner, dir, "for-each-ref", "--format=%(upstream:short)|%(upstream:track)", "refs/heads/"+report.Current); err == nil {
			upstream, track, _ := strings.Cut(line, "|")
			report.Upstream = strings.TrimSpace(upstream)
			report.UpstreamGone = strings.Contains(track, "gone")
			for _, match := range trackPattern.FindAllStringSubmatch(track, -1) {
				count, _ := strconv.Atoi(match[2])
				if match[1] == "ahead" {
					report.Ahead = count
				} else {
					report.Behind = count
				}
			}
		}
	}
	report.DefaultRef, report.DefaultBranch = defaultBranch(ctx, runner, dir)
	if report.DefaultRef != "" {
		report.DefaultCommit, _ = run(ctx, runner, dir, "rev-parse", "--verify", report.DefaultRef+"^{commit}")
		if report.HeadCommit != "" && report.DefaultCommit != "" {
			if count, err := run(ctx, runner, dir, "rev-list", "--count", "HEAD.."+report.DefaultRef); err == nil {
				report.BehindDefault, _ = strconv.Atoi(count)
			}
			if report.Current != "" && report.Current != report.DefaultBranch {
				_, err := runner.Run(ctx, dir, "merge-base", "--is-ancestor", "HEAD", report.DefaultRef)
				report.MergedIntoDefault = err == nil
			}
		}
	}
	report.CurrentBase, report.CurrentBaseCommit = report.Current, report.HeadCommit
	// Локальная ветка, которая только отстаёт от своей на сервере, — старая
	// копия той же ветки: новая работа идёт от серверной. Свои неотправленные
	// коммиты (ahead) терять нельзя — тогда основа локальная.
	if report.Upstream != "" && !report.UpstreamGone && report.Behind > 0 && report.Ahead == 0 {
		if commit, err := run(ctx, runner, dir, "rev-parse", "--verify", report.Upstream+"^{commit}"); err == nil {
			report.CurrentBase, report.CurrentBaseCommit = report.Upstream, commit
		}
	}
	if status, err := run(ctx, runner, dir, "status", "--porcelain=v1", "--untracked-files=all"); err == nil && status != "" {
		lines := strings.Split(status, "\n")
		report.DirtyCount = len(lines)
		for _, line := range lines {
			if len(report.Dirty) >= 20 {
				break
			}
			if len(line) > 3 {
				report.Dirty = append(report.Dirty, strings.TrimSpace(line[3:]))
			}
		}
	}
	report.IdentityName, _ = run(ctx, runner, dir, "config", "user.name")
	report.IdentityEmail, _ = run(ctx, runner, dir, "config", "user.email")
	if value, err := run(ctx, runner, dir, "config", "--bool", "commit.gpgsign"); err == nil {
		report.GPGSign = value == "true"
	}
	historyRef := report.DefaultRef
	if historyRef == "" {
		historyRef = "HEAD"
	}
	if log, err := run(ctx, runner, dir, "log", "-15", "--no-merges", "--format=%s", historyRef); err == nil && log != "" {
		report.RecentSubjects = strings.Split(log, "\n")
	}
	if refs, err := run(ctx, runner, dir, "for-each-ref", "--count=300", "--format=%(refname:short)", "refs/heads", "refs/remotes/origin"); err == nil && refs != "" {
		report.BranchPrefixes = map[string]int{}
		for _, ref := range strings.Split(refs, "\n") {
			name := strings.TrimPrefix(strings.TrimSpace(ref), "origin/")
			if name == "" || name == "origin" || name == "HEAD" {
				continue
			}
			report.Branches = append(report.Branches, name)
			if prefix, _, ok := strings.Cut(name, "/"); ok {
				report.BranchPrefixes[strings.ToLower(prefix)]++
			}
		}
	}
	report.Protected, report.ProtectedSource = ProtectedByName(report.Current, report.DefaultBranch), ""
	if report.Protected {
		report.ProtectedSource = "name"
	}
	return report
}

// defaultBranch — основная ветка: origin/HEAD, иначе origin/main|master, иначе
// локальные main|master. Сеть здесь не нужна.
func defaultBranch(ctx context.Context, runner Runner, dir string) (string, string) {
	if ref, err := run(ctx, runner, dir, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil && strings.HasPrefix(ref, "origin/") {
		return ref, strings.TrimPrefix(ref, "origin/")
	}
	for _, candidate := range []string{"origin/main", "origin/master"} {
		if _, err := run(ctx, runner, dir, "rev-parse", "--verify", "--quiet", "refs/remotes/"+candidate); err == nil {
			return candidate, strings.TrimPrefix(candidate, "origin/")
		}
	}
	for _, candidate := range []string{"main", "master"} {
		if _, err := run(ctx, runner, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+candidate); err == nil {
			return candidate, candidate
		}
	}
	return "", ""
}

var protectedNames = map[string]bool{
	"main": true, "master": true, "develop": true, "development": true, "dev": true,
	"trunk": true, "production": true, "prod": true, "stable": true, "staging": true,
}

// ProtectedByName — защита по имени, когда GitLab не подсказал точнее:
// основная ветка, общеупотребительные долгоживущие и release/*. Отсоединённый
// HEAD тоже «защищён»: работать в нём нельзя, только ответвиться.
func ProtectedByName(branch, defaultBranch string) bool {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return true
	}
	lower := strings.ToLower(branch)
	return branch == defaultBranch || protectedNames[lower] || strings.HasPrefix(lower, "release/") || strings.HasPrefix(lower, "releases/")
}

var credentialPattern = regexp.MustCompile(`://[^/@\s]+@`)

// StripCredentials убирает логин и токен из адреса remote: его показывают.
func StripCredentials(remote string) string {
	return credentialPattern.ReplaceAllString(strings.TrimSpace(remote), "://")
}

func clip(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len([]rune(value)) <= limit {
		return value
	}
	return string([]rune(value)[:limit]) + "…"
}
