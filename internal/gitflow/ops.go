package gitflow

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

// Checkout переключает репозиторий на ветку branch от baseCommit.
//
// Идемпотентно: повтор утверждения или следующий этап наряда находит ветку уже
// открытой — это успех. `--no-track`: новая ветка не должна получить апстримом
// origin/main, иначе простой `git push` с push.default=upstream ушёл бы в main.
// created — ветку создали сейчас (её можно убрать при откате).
func Checkout(ctx context.Context, runner Runner, dir, branch, baseCommit string) (created bool, err error) {
	current, _ := run(ctx, runner, dir, "branch", "--show-current")
	if current == branch {
		return false, nil
	}
	if existing, lookupErr := run(ctx, runner, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); lookupErr == nil && existing != "" {
		if existing != baseCommit {
			return false, fmt.Errorf("ветка %s уже есть и указывает на другой коммит — выберите другое имя", branch)
		}
		_, err = run(ctx, runner, dir, "switch", branch)
		return false, err
	}
	if _, err = run(ctx, runner, dir, "switch", "--no-track", "-c", branch, baseCommit); err != nil {
		return false, err
	}
	return true, nil
}

// UndoCheckout возвращает прежнюю ветку и удаляет созданную, если на ней
// ничего нового нет.
func UndoCheckout(ctx context.Context, runner Runner, dir, previous, branch, baseCommit string) {
	if previous != "" {
		_, _ = run(ctx, runner, dir, "switch", previous)
	}
	if head, err := run(ctx, runner, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil && head == baseCommit {
		_, _ = run(ctx, runner, dir, "branch", "-D", branch)
	}
}

// ErrNothingToCommit — файлы квеста уже совпадают с HEAD.
var ErrNothingToCommit = errors.New("nothing to commit")

// Identity — автор коммита из git config человека.
func Identity(ctx context.Context, runner Runner, dir string) (string, string, error) {
	name, _ := run(ctx, runner, dir, "config", "user.name")
	email, _ := run(ctx, runner, dir, "config", "user.email")
	if name == "" || email == "" {
		return name, email, errors.New("Git не знает, кто вы: задайте git config --global user.name и user.email")
	}
	return name, email, nil
}

// Commit коммитит ровно paths (относительно dir) с сообщением message от
// имени пользователя git. Чужие изменения рабочего дерева в коммит не попадают:
// `git commit -- paths` берёт только названные пути.
func Commit(ctx context.Context, runner Runner, dir string, paths []string, message string) (string, error) {
	if len(paths) == 0 {
		return "", ErrNothingToCommit
	}
	if _, _, err := Identity(ctx, runner, dir); err != nil {
		return "", err
	}
	clean := make([]string, 0, len(paths))
	for _, item := range paths {
		value := filepath.ToSlash(filepath.Clean(strings.TrimSpace(item)))
		if value == "." || value == ".." || strings.HasPrefix(value, "../") || filepath.IsAbs(value) {
			return "", fmt.Errorf("unsafe commit path %q", item)
		}
		clean = append(clean, value)
	}
	status, err := run(ctx, runner, dir, append([]string{"status", "--porcelain=v1", "--untracked-files=all", "--"}, clean...)...)
	if err != nil {
		return "", err
	}
	if status == "" {
		return "", ErrNothingToCommit
	}
	if _, err = run(ctx, runner, dir, append([]string{"add", "-A", "--"}, clean...)...); err != nil {
		return "", err
	}
	if _, err = run(ctx, runner, dir, append([]string{"commit", "-m", message, "--"}, clean...)...); err != nil {
		_, _ = run(ctx, runner, dir, append([]string{"reset", "-q", "--"}, clean...)...)
		return "", err
	}
	return run(ctx, runner, dir, "rev-parse", "HEAD")
}

// PushResult — что сказал сервер.
type PushResult struct {
	Output   string `json:"output"`
	UpToDate bool   `json:"upToDate"`
	MRURL    string `json:"mrUrl,omitempty"`
	NewMRURL string `json:"newMrUrl,omitempty"`
}

// MergeRequest — параметры MR через push-опции GitLab.
type MergeRequest struct {
	Target      string
	Title       string
	Description string
}

// Push отправляет ветку явным refspec и ставит апстрим на одноимённую ветку
// сервера. С mr != nil просит GitLab создать MR (push-опции); если ветка уже
// была на сервере и push ничего не обновил, GitLab MR не создаёт — тогда
// вызывающий открывает NewMRURL.
func Push(ctx context.Context, runner Runner, dir, remote, branch string, mr *MergeRequest) (PushResult, error) {
	if remote == "" {
		remote = "origin"
	}
	args := []string{"push", "--porcelain", "-u"}
	if mr != nil {
		args = append(args, "-o", "merge_request.create")
		if target := strings.TrimSpace(mr.Target); target != "" {
			args = append(args, "-o", "merge_request.target="+target)
		}
		if title := oneLine(mr.Title, 250); title != "" {
			args = append(args, "-o", "merge_request.title="+title)
		}
		if description := oneLine(mr.Description, 900); description != "" {
			args = append(args, "-o", "merge_request.description="+description)
		}
	}
	args = append(args, remote, "refs/heads/"+branch+":refs/heads/"+branch)
	out, err := runner.Run(ctx, dir, args...)
	result := PushResult{Output: clip(string(out), 4000)}
	result.UpToDate = strings.Contains(result.Output, "[up to date]") || strings.Contains(result.Output, "Everything up-to-date")
	result.MRURL, result.NewMRURL = ParseMergeRequestURLs(result.Output)
	return result, err
}

var (
	mrURLPattern    = regexp.MustCompile(`https?://\S+/-/merge_requests/\d+`)
	newMRURLPattern = regexp.MustCompile(`https?://\S+/-/merge_requests/new\?\S+`)
)

// ParseMergeRequestURLs вынимает из ответа GitLab ссылку на созданный MR и
// ссылку «создать MR».
func ParseMergeRequestURLs(output string) (string, string) {
	return mrURLPattern.FindString(output), newMRURLPattern.FindString(output)
}

// WebURL — https-адрес проекта по адресу remote (scp, ssh или https).
func WebURL(remote string) (string, bool) {
	remote = StripCredentials(remote)
	var host, path string
	switch {
	case strings.Contains(remote, "://"):
		parsed, err := url.Parse(remote)
		if err != nil || parsed.Hostname() == "" {
			return "", false
		}
		host, path = parsed.Hostname(), parsed.Path
		if parsed.Scheme == "https" || parsed.Scheme == "http" {
			host = parsed.Host
		}
	default:
		at := strings.LastIndex(remote, "@")
		colon := strings.Index(remote, ":")
		if colon <= 0 {
			return "", false
		}
		host, path = remote[at+1:colon], remote[colon+1:]
	}
	path = strings.Trim(strings.TrimSuffix(strings.Trim(path, "/"), ".git"), "/")
	if host == "" || !strings.Contains(path, "/") {
		return "", false
	}
	return "https://" + host + "/" + path, true
}

// NewMergeRequestURL — страница «новый MR» GitLab для ветки.
func NewMergeRequestURL(remote, source, target string) string {
	base, ok := WebURL(remote)
	if !ok {
		return ""
	}
	query := url.Values{}
	query.Set("merge_request[source_branch]", source)
	if target != "" {
		query.Set("merge_request[target_branch]", target)
	}
	return base + "/-/merge_requests/new?" + query.Encode()
}

// LooksLikeGitLab — remote ведёт на GitLab: узел совпадает с подключённым
// GitLab Point или называется gitlab.*. Кнопку MR показывают только тогда.
func LooksLikeGitLab(remote, connectedGitLab string) bool {
	base, ok := WebURL(remote)
	if !ok {
		return false
	}
	host := strings.ToLower(strings.TrimPrefix(base, "https://"))
	host, _, _ = strings.Cut(host, "/")
	if connected, err := url.Parse(strings.TrimSpace(connectedGitLab)); err == nil && connected.Hostname() != "" && strings.EqualFold(connected.Hostname(), strings.Split(host, ":")[0]) {
		return true
	}
	return strings.HasPrefix(host, "gitlab.") || strings.Contains(host, ".gitlab.") || host == "gitlab.com"
}

// push-опции не допускают перевода строки.
func oneLine(value string, limit int) string {
	return clipRunes(strings.Join(strings.Fields(value), " "), limit)
}
