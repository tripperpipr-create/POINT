package workspace

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// projectIgnore — что не относится к проекту для индекса и поиска: правила
// .gitignore на каждом уровне, .git/info/exclude, свой .pointignore и папки с
// данными СУБД.
//
// 02.10.2026 индекс cf-bitrix — 511 файлов, из них 492 из docker/ и ни одного
// из source/: обход тратил предел в 200 тысяч записей на тома Docker раньше,
// чем доходил до кода, и search_code не знал о коде ничего. Git при этом эти
// тома игнорировал. Разбор свой, без git: правила работают и в папке без
// репозитория, где лежит только .gitignore или .pointignore.
type projectIgnore struct {
	root  string
	mu    sync.Mutex
	rules map[string][]ignoreRule // папка относительно корня ("" — корень) → её правила
}

type ignoreRule struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
}

// Файлы правил, читаемые в каждой папке. .pointignore — для того, что git
// хранит, но в индекс и поиск Point не нужно (дампы, выгрузки).
var ignoreFileNames = []string{".gitignore", ".pointignore"}

func newProjectIgnore(root string) *projectIgnore {
	return &projectIgnore{root: root, rules: map[string][]ignoreRule{}}
}

func (p *projectIgnore) load(dir string) []ignoreRule {
	p.mu.Lock()
	defer p.mu.Unlock()
	if rules, ok := p.rules[dir]; ok {
		return rules
	}
	abs := filepath.Join(p.root, filepath.FromSlash(dir))
	names := append([]string(nil), ignoreFileNames...)
	if info, err := os.Stat(filepath.Join(abs, ".git")); err == nil && info.IsDir() {
		names = append(names, filepath.Join(".git", "info", "exclude"))
	}
	var rules []ignoreRule
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(abs, name))
		if err != nil || len(data) > 1<<20 {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if rule, ok := compileIgnoreRule(line); ok {
				rules = append(rules, rule)
			}
		}
	}
	p.rules[dir] = rules
	return rules
}

// matches — правило ближайших .gitignore к пути rel (через «/»), без проверки
// родительских папок: обход их уже прошёл. Последнее совпавшее правило решает.
func (p *projectIgnore) matches(rel string, isDir bool) bool {
	rel = strings.Trim(rel, "/")
	if rel == "" || rel == "." {
		return false
	}
	parts := strings.Split(rel, "/")
	ignored := false
	for depth := 0; depth < len(parts); depth++ {
		dir := strings.Join(parts[:depth], "/")
		target := strings.Join(parts[depth:], "/")
		for _, rule := range p.load(dir) {
			if rule.dirOnly && !isDir {
				continue
			}
			if rule.re.MatchString(target) {
				ignored = !rule.negate
			}
		}
	}
	return ignored
}

// ignored — с родительскими папками: файл в игнорируемой папке игнорируется,
// и отрицание его не возвращает (как у git).
func (p *projectIgnore) ignored(rel string, isDir bool) bool {
	parts := strings.Split(strings.Trim(path.Clean("/"+filepath.ToSlash(rel)), "/"), "/")
	for index := range parts {
		if parts[index] == "" {
			continue
		}
		last := index == len(parts)-1
		if p.matches(strings.Join(parts[:index+1], "/"), !last || isDir) {
			return true
		}
		if !last && looksLikeDataDir(filepath.Join(p.root, filepath.FromSlash(strings.Join(parts[:index+1], "/")))) {
			return true
		}
	}
	return false
}

// dataDirMarkers — файлы, по которым каталог узнаётся как данные СУБД или
// кэша, примонтированные томом Docker: в них нет кода, а файлов сотни тысяч.
var dataDirMarkers = []string{"ibdata1", "aria_log_control", "mysql.ibd", "PG_VERSION", "postmaster.opts", "WiredTiger", "dump.rdb", "appendonly.aof", "nodes"}

func looksLikeDataDir(abs string) bool {
	for _, marker := range dataDirMarkers {
		info, err := os.Lstat(filepath.Join(abs, marker))
		if err != nil {
			continue
		}
		// «nodes» — каталог данных Elasticsearch; остальные — файлы.
		if marker == "nodes" {
			if info.IsDir() {
				if _, stateErr := os.Stat(filepath.Join(abs, "nodes", "0", "_state")); stateErr == nil {
					return true
				}
			}
			continue
		}
		if !info.IsDir() {
			return true
		}
	}
	return false
}

// compileIgnoreRule переводит строку .gitignore в регулярное выражение.
func compileIgnoreRule(line string) (ignoreRule, bool) {
	line = strings.TrimRight(strings.TrimSuffix(line, "\r"), " \t")
	if line == "" || strings.HasPrefix(line, "#") {
		return ignoreRule{}, false
	}
	rule := ignoreRule{}
	if strings.HasPrefix(line, "!") {
		rule.negate, line = true, line[1:]
	} else if strings.HasPrefix(line, `\`) {
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		rule.dirOnly, line = true, strings.TrimRight(line, "/")
	}
	if line == "" {
		return ignoreRule{}, false
	}
	anchored := strings.Contains(line, "/")
	line = strings.TrimPrefix(line, "/")
	var body strings.Builder
	for index := 0; index < len(line); index++ {
		switch char := line[index]; {
		case strings.HasPrefix(line[index:], "**/"):
			body.WriteString("(?:.*/)?")
			index += 2
		case strings.HasPrefix(line[index:], "/**") && index+3 == len(line):
			body.WriteString("/.*")
			index += 2
		case strings.HasPrefix(line[index:], "**"):
			body.WriteString(".*")
			index++
		case char == '*':
			body.WriteString("[^/]*")
		case char == '?':
			body.WriteString("[^/]")
		case char == '[':
			end := strings.IndexByte(line[index+1:], ']')
			if end < 0 {
				body.WriteString(regexp.QuoteMeta("["))
				continue
			}
			class := line[index+1 : index+1+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			body.WriteString("[" + strings.ReplaceAll(class, `\`, `\\`) + "]")
			index += end + 1
		default:
			body.WriteString(regexp.QuoteMeta(string(char)))
		}
	}
	prefix := "^(?:.*/)?"
	if anchored {
		prefix = "^"
	}
	compiled, err := regexp.Compile(prefix + body.String() + "$")
	if err != nil {
		return ignoreRule{}, false
	}
	rule.re = compiled
	return rule, true
}

// ignoredDir — папку обхода не смотреть: её исключают правила или в ней
// данные СУБД.
func (f *FS) ignoredDir(ignore *projectIgnore, absolute string) bool {
	rel, err := filepath.Rel(f.root, absolute)
	if err != nil {
		return false
	}
	return ignore.matches(filepath.ToSlash(rel), true) || looksLikeDataDir(absolute)
}
