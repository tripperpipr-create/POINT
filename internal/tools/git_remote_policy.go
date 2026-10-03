package tools

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"local-agent-workbench/internal/osproc"
)

var gitRemoteMutatingPattern = regexp.MustCompile(`(?i)` + gitCommand + `(clone|remote\s+add|submodule\s+add)\b`)
var gitRemoteTrafficPattern = regexp.MustCompile(`(?i)` + gitCommand + `(clone|fetch|pull|push|remote\s+add|submodule\s+add)\b`)
var gitSSHURLPattern = regexp.MustCompile(`(?i)\bgit@[^\s"'` + "`" + `<>]+`)

// deniedUnconfirmedGitRemoteReason blocks git remotes that the user has not confirmed.
// Package registries stay on the host allowlist; git requires an explicit remote allowlist.
// configured — remote самого репозитория: отказ называет URL, иначе Мастер не
// может завести запрос человеку и совет «escalate» ведёт в тупик.
func deniedUnconfirmedGitRemoteReason(command string, confirmedRemotes, grantedRemotes []string, configured map[string]string) string {
	if !gitRemoteTrafficPattern.MatchString(command) {
		return ""
	}
	allow := append(append([]string{}, confirmedRemotes...), grantedRemotes...)
	targets := extractGitRemoteTargets(command)
	if gitRemoteMutatingPattern.MatchString(command) {
		if len(targets) == 0 {
			return "git clone/remote add requires an explicit confirmed repository URL; escalate to the Master so the user can approve it"
		}
		for _, target := range targets {
			if !GitRemoteAllowed(target, allow) {
				return fmt.Sprintf("git remote %q is not on the confirmed repository allowlist; escalate to the Master/user", target)
			}
		}
		return ""
	}
	// fetch/pull/push without a URL uses an already-configured origin: only allowed
	// when the quest already has at least one confirmed remote (intake/source).
	if len(targets) == 0 {
		if len(allow) == 0 {
			if url := namedRemoteURL(command, configured); url != "" {
				return fmt.Sprintf("git fetch/pull/push against an unconfirmed origin %q is blocked; escalate to the Master/user", url)
			}
			return "git fetch/pull against an unconfirmed origin is blocked; escalate to the Master/user"
		}
		return ""
	}
	for _, target := range targets {
		if !GitRemoteAllowed(target, allow) {
			return fmt.Sprintf("git remote %q is not on the confirmed repository allowlist; escalate to the Master/user", target)
		}
	}
	return ""
}

func extractGitRemoteTargets(command string) []string {
	var targets []string
	seen := map[string]bool{}
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		raw = strings.TrimRight(raw, ".,);]")
		if raw == "" || seen[raw] {
			return
		}
		seen[raw] = true
		targets = append(targets, raw)
	}
	for _, match := range networkURLPattern.FindAllString(command, -1) {
		add(match)
	}
	for _, match := range gitSSHURLPattern.FindAllString(command, -1) {
		add(match)
	}
	return targets
}

var shellSegmentSeparator = regexp.MustCompile(`\|\||&&|[;|&\r\n]`)

// configuredGitRemotes читает remote, уже настроенные в репозитории: имя → URL.
// Не репозиторий или git недоступен — пустая карта, затворы остаются прежними.
func configuredGitRemotes(ctx context.Context, dir string) map[string]string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := osproc.CommandContext(ctx, "git", "-C", dir, "config", "--get-regexp", `^remote\..*\.url$`)
	cmd.Env = append(sanitizedProcessEnv(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	remotes := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || !strings.HasPrefix(key, "remote.") || !strings.HasSuffix(key, ".url") {
			continue
		}
		if name := key[len("remote.") : len(key)-len(".url")]; name != "" && strings.TrimSpace(value) != "" {
			remotes[name] = strings.TrimSpace(value)
		}
	}
	return remotes
}

// gitTrafficTarget — адресат git fetch/pull/push в одном звене команды:
// первый позиционный аргумент после глагола (имя remote или URL), "--all" или
// "" для remote по умолчанию. Флаг с URL внутри (--repo=…) не разбирается и
// возвращается как есть, чтобы не сойти за remote по умолчанию.
func gitTrafficTarget(fields []string) string {
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		// Перенаправление вывода (`> log`, `2>`) — не адресат; голый оператор
		// забирает и следующее слово, имя файла.
		if strings.ContainsAny(field, "<>") {
			if strings.HasSuffix(field, ">") || strings.HasSuffix(field, "<") {
				i++
			}
			continue
		}
		if field == "--all" {
			return field
		}
		if strings.HasPrefix(field, "-") {
			if strings.HasPrefix(strings.ToLower(field), "--repo") || len(extractGitRemoteTargets(field)) > 0 {
				return field
			}
			continue
		}
		return field
	}
	return ""
}

func ownRemoteTarget(target string, remotes map[string]string) bool {
	if target == "" || target == "--all" {
		return len(remotes) > 0
	}
	if _, ok := remotes[target]; ok {
		return true
	}
	urls := make([]string, 0, len(remotes))
	for _, url := range remotes {
		urls = append(urls, url)
	}
	return GitRemoteAllowed(target, urls)
}

// ownRemoteGitTraffic — вся сетевая часть команды есть git fetch/pull/push к
// remote, уже настроенным в репозитории человека. Только для локальной полосы:
// адресат выбрал человек, когда настраивал репозиторий, а не агент. Звено с
// чем-то ещё сетевым (curl, clone, чужой URL) делает ответ ложным целиком.
// remotesAt — remote каталога звена: "" — каталог команды, иначе итог `-C`;
// пустой ответ — своих remote там нет. Глобальная опция, подменяющая конфиг
// или репозиторий (`-c`, `--git-dir`), тоже делает ответ ложным.
func ownRemoteGitTraffic(command string, remotesAt func(dir string) map[string]string) bool {
	if remotesAt == nil || !networkCommandPattern.MatchString(command) {
		return false
	}
	for _, segment := range shellSegmentSeparator.Split(command, -1) {
		matches := networkCommandPattern.FindAllString(segment, -1)
		if len(matches) == 0 {
			continue
		}
		if len(matches) > 1 {
			return false
		}
		call, ok := parseGitCall(strings.Fields(strings.NewReplacer(`"`, "", `'`, "").Replace(segment)))
		if !ok || call.foreign {
			return false
		}
		switch call.verb {
		case "fetch", "pull", "push":
		default:
			return false
		}
		if !ownRemoteTarget(gitTrafficTarget(call.args), remotesAt(call.dir)) {
			return false
		}
	}
	return true
}

// namedRemoteURL — URL remote, к которому обращается команда: названный в ней,
// иначе origin, иначе единственный настроенный.
func namedRemoteURL(command string, remotes map[string]string) string {
	if len(remotes) == 0 {
		return ""
	}
	for _, segment := range shellSegmentSeparator.Split(command, -1) {
		call, ok := parseGitCall(strings.Fields(strings.NewReplacer(`"`, "", `'`, "").Replace(segment)))
		if !ok {
			continue
		}
		// `-C dir` или подмена конфига: remote каталога команды тут не те, и
		// названный в отказе URL ввёл бы человека в заблуждение.
		if (call.dir != "" || call.foreign) && gitRemoteTrafficPattern.MatchString(segment) {
			return ""
		}
		if url, ok := remotes[gitTrafficTarget(call.args)]; ok {
			return url
		}
	}
	if url, ok := remotes["origin"]; ok {
		return url
	}
	if len(remotes) == 1 {
		for _, url := range remotes {
			return url
		}
	}
	return ""
}
