package tools

// Глобальные опции git стоят между `git` и подкомандой: `git -c k=v pull`,
// `git -C dir fetch URL`, `git --git-dir=.. push -f`. Затворы, ждавшие глагол
// сразу после `git`, такие команды не узнавали — ни сетью, ни remote-трафиком,
// ни разрушением. Здесь один префикс для всех регулярок и один разбор звена.

import (
	"context"
	"path/filepath"
	"strings"
)

// gitShellWord — знак слова оболочки; кавычки внутри слова идут вместе с ним.
const gitShellWord = `(?:"[^"]*"|'[^']*'|[^\s"'])`

// gitCommand — `git` с глобальными опциями и пробелом перед подкомандой; к нему
// дописывают глагол. Опция со значением отдельным словом забирает его, любое
// другое слово на «-» считается флагом. Ошибается в сторону лишнего
// совпадения: каталог `-C pull` сойдёт за глагол — затвор переспросит, а не
// пропустит.
const gitCommand = `\bgit(?:\.exe)?(?:\s+(?:-c|-C|--git-dir|--work-tree|--namespace|--super-prefix|--attr-source|--shallow-file)\s+` + gitShellWord + `+|\s+-` + gitShellWord + `*)*\s+`

// gitValueOptions — глобальные опции git со значением отдельным словом.
var gitValueOptions = map[string]bool{
	"-c": true, "-C": true, "--git-dir": true, "--work-tree": true, "--namespace": true,
	"--super-prefix": true, "--attr-source": true, "--shallow-file": true,
}

// gitInertOptions — глобальные флаги, не трогающие ни репозиторий, ни конфиг,
// ни сам исполняемый git.
var gitInertOptions = map[string]bool{
	"-p": true, "--paginate": true, "-P": true, "--no-pager": true,
	"--no-optional-locks": true, "--no-advice": true, "--no-replace-objects": true,
	"--literal-pathspecs": true, "--no-literal-pathspecs": true, "--glob-pathspecs": true,
	"--noglob-pathspecs": true, "--icase-pathspecs": true,
}

// gitCall — одно звено `git [глобальные опции] подкоманда аргументы…`.
type gitCall struct {
	verb string   // подкоманда в нижнем регистре
	args []string // слова после подкоманды
	dir  string   // итог всех -C относительно каталога звена; "" — он сам
	// foreign — опция подменяет конфиг, репозиторий или сам git (-c,
	// --git-dir, --exec-path=…) либо незнакома: remote такого звена уже не
	// те, что настроил человек.
	foreign bool
}

// parseGitCall разбирает поля звена без кавычек. false — звено не вызов git
// или подкоманды в нём нет.
func parseGitCall(fields []string) (gitCall, bool) {
	if len(fields) < 2 || !(strings.EqualFold(fields[0], "git") || strings.EqualFold(fields[0], "git.exe")) {
		return gitCall{}, false
	}
	var call gitCall
	for i := 1; i < len(fields); i++ {
		field := fields[i]
		switch {
		case !strings.HasPrefix(field, "-"):
			call.verb, call.args = strings.ToLower(field), fields[i+1:]
			return call, true
		case field == "-C":
			i++
			if i < len(fields) {
				call.dir = joinGitDir(call.dir, fields[i])
			}
		case gitValueOptions[field]:
			i++
			call.foreign = true
		case !gitInertOptions[field]:
			call.foreign = true
		}
	}
	return gitCall{}, false
}

// joinGitDir повторяет git: каждый следующий -C отсчитывается от предыдущего.
func joinGitDir(base, next string) string {
	if base == "" || filepath.IsAbs(next) {
		return next
	}
	return filepath.Join(base, next)
}

// workspaceGitRemotes — remote каталога из `git -C dir` на локальной полосе.
// Каталог вне рабочей области или недоступный своих remote не имеет: звено
// уходит на общие затворы, а не на remote, которые настроил человек.
func (t RunCommand) workspaceGitRemotes(ctx context.Context, cwd, dir string) map[string]string {
	target := dir
	if !filepath.IsAbs(target) {
		target = filepath.Join(cwd, target)
	}
	rel, err := filepath.Rel(t.FS.Root(), target)
	if err != nil {
		return nil
	}
	resolved, err := t.FS.Resolve(filepath.ToSlash(rel), false)
	if err != nil {
		return nil
	}
	return configuredGitRemotes(ctx, resolved)
}
