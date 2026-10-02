package workspace

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"local-agent-workbench/internal/filepolicy"
	"local-agent-workbench/internal/osproc"
)

// SearchOptions — поиск по тексту рабочей области.
type SearchOptions struct {
	Query string
	// Regex — Query это регулярное выражение Go (RE2), иначе подстрока.
	Regex bool
	// CaseSensitive — различать регистр. По умолчанию нет.
	CaseSensitive bool
	// Context — сколько соседних строк показать до и после совпадения (0–5).
	Context int
	// Path — искать только внутри этой папки или файла.
	Path string
	// Glob — искать только в файлах, чьё имя или путь подходит под шаблон
	// (например *.go или src/**/*.ts; ** совпадает с любым числом папок).
	Glob       string
	MaxResults int
	// TimeBudget — сколько поиск может идти; 0 — defaultSearchBudget.
	TimeBudget time.Duration
}

// ContextMatch — совпадение с соседними строками.
type ContextMatch struct {
	Path   string   `json:"path"`
	Line   int      `json:"line"`
	Text   string   `json:"text"`
	Before []string `json:"before,omitempty"`
	After  []string `json:"after,omitempty"`
}

// Строка совпадения в выдаче не длиннее этого: минифицированный файл иначе
// отдавал модели мегабайт одной строкой.
const maxMatchLineRunes = 400

// SearchWith ищет по тексту и сообщает, упёрся ли поиск в предел выдачи.
func (f *FS) SearchWith(ctx context.Context, options SearchOptions) ([]ContextMatch, bool, error) {
	matches, truncated, _, err := f.SearchWithStats(ctx, options)
	return matches, truncated, err
}

// SearchStats — что поиск успел: сколько файлов прочитал и не кончилось ли
// время.
type SearchStats struct {
	Files    int  `json:"files"`
	TimedOut bool `json:"timedOut"`
}

// defaultSearchBudget — сколько поиск по тексту может идти. 02.10.2026 поиск
// «/api/documents» в cf-bitrix читал 343 тысячи файлов (14 ГБ томов Docker в
// docker/) 15 минут, съел весь срок хода Мастера и ничего не вернул.
const defaultSearchBudget = 30 * time.Second

// SearchWithStats — поиск как у ripgrep: в Git-репозитории только файлы, что
// git не игнорирует (`ls-files -co --exclude-standard`), двоичные пропускаются,
// и у поиска свой срок. Кончилось время — возвращается найденное с
// TimedOut и truncated, а не пустая ошибка.
func (f *FS) SearchWithStats(ctx context.Context, options SearchOptions) ([]ContextMatch, bool, SearchStats, error) {
	stats := SearchStats{}
	query := options.Query
	if strings.TrimSpace(query) == "" {
		return nil, false, stats, errors.New("search query is empty")
	}
	limit := options.MaxResults
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	contextLines := min(max(options.Context, 0), 5)
	match, err := searchMatcher(options)
	if err != nil {
		return nil, false, stats, err
	}
	root := f.root
	if scope := strings.Trim(strings.ReplaceAll(options.Path, "\\", "/"), "/"); scope != "" {
		resolved, resolveErr := f.Resolve(scope, false)
		if resolveErr != nil {
			return nil, false, stats, resolveErr
		}
		root = resolved
	}
	budget := options.TimeBudget
	if budget <= 0 {
		budget = defaultSearchBudget
	}
	searchCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	matches := make([]ContextMatch, 0)
	truncated := false
	safeDirs := map[string]bool{}
	visit := func(current string) error {
		if err := searchCtx.Err(); err != nil {
			return err
		}
		info, statErr := os.Lstat(current)
		if statErr != nil || !info.Mode().IsRegular() || IsSensitive(filepath.Base(current)) {
			return nil
		}
		rel, _ := filepath.Rel(f.root, current)
		rel = filepath.ToSlash(rel)
		if !globMatches(options.Glob, rel) {
			return nil
		}
		if f.FileRules == filepolicy.Current && filepolicy.ExcludedPath(f.FileRules, rel, false) {
			return nil
		}
		text, ok := f.searchContent(current, safeDirs)
		stats.Files++
		if !ok {
			return nil
		}
		lines := splitContentLines(text)
		for index, line := range lines {
			if !match(line) {
				continue
			}
			item := ContextMatch{Path: rel, Line: index + 1, Text: clipLine(line)}
			for before := max(0, index-contextLines); before < index; before++ {
				item.Before = append(item.Before, clipLine(lines[before]))
			}
			for after := index + 1; after <= min(len(lines)-1, index+contextLines); after++ {
				item.After = append(item.After, clipLine(lines[after]))
			}
			matches = append(matches, item)
			if len(matches) >= limit {
				truncated = true
				return errLimitReached
			}
		}
		return nil
	}
	var walkErr error
	if info, statErr := os.Stat(root); statErr == nil && !info.IsDir() {
		walkErr = visit(root)
	} else if files, ok := f.gitCandidates(searchCtx, root, options); ok {
		walkErr = f.visitGitFiles(root, files, visit)
	} else {
		walkErr = filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if err := searchCtx.Err(); err != nil {
				return err
			}
			if !entry.IsDir() {
				return visit(current)
			}
			if current == root {
				return nil
			}
			if f.skipDirectory(current, false) {
				return filepath.SkipDir
			}
			// Вложенный репозиторий ищется по его же .gitignore.
			if _, gitErr := os.Lstat(filepath.Join(current, ".git")); gitErr == nil {
				if files, ok := f.gitCandidates(searchCtx, current, options); ok {
					if err := f.visitGitFiles(current, files, visit); err != nil {
						return err
					}
					return filepath.SkipDir
				}
			}
			return nil
		})
	}
	if errors.Is(walkErr, errLimitReached) {
		walkErr = nil
	}
	if walkErr != nil && searchCtx.Err() != nil && ctx.Err() == nil {
		stats.TimedOut, truncated, walkErr = true, true, nil
	}
	return matches, truncated, stats, walkErr
}

// searchContent читает файл для поиска. Полный Resolve на каждый файл —
// EvalSymlinks и Lstat каждого звена — на Windows стоил ~27 мс: за 30 с поиск
// успевал прочесть тысячу файлов. Путь здесь уже взят изнутри проекта
// обходом или git; проверка, что папка не уводит наружу через ссылку или
// junction, делается раз на папку. Двоичное и не-UTF-8 пропускается, как в Read.
func (f *FS) searchContent(abs string, safeDirs map[string]bool) (string, bool) {
	dir := filepath.Dir(abs)
	safe, seen := safeDirs[dir]
	if !seen {
		safe = isWithin(f.root, abs) && !hasEscapingReparsePoint(f.root, dir)
		safeDirs[dir] = safe
	}
	if !safe {
		return "", false
	}
	file, err := os.Open(abs)
	if err != nil {
		return "", false
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, f.maxReadBytes))
	// Предел чтения мог разрезать последний символ пополам.
	for cut := 0; cut < 3 && int64(len(data)) == f.maxReadBytes && len(data) > 0 && !utf8.Valid(data); cut++ {
		data = data[:len(data)-1]
	}
	if err != nil || bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return "", false
	}
	return string(data), true
}

// gitCandidates — файлы, где стоит искать. Подстроку git находит сам
// (`git grep -l`, многопоточно, без двоичных): cf-bitrix целиком — за 10 с
// против получаса чтения. Регулярное выражение RE2 git не понимает — тогда
// список файлов без игнорируемых, а сверяет поиск сам.
func (f *FS) gitCandidates(ctx context.Context, dir string, options SearchOptions) ([]string, bool) {
	// -i у git складывает регистр только латиницы: «Документы» не нашёл бы
	// «документы». Такой запрос сверяется поиском сам по списку файлов.
	asciiFold := options.CaseSensitive || isASCII(options.Query)
	if !options.Regex && asciiFold {
		args := []string{"-C", dir, "grep", "-l", "-z", "-I", "--untracked", "-F"}
		if !options.CaseSensitive {
			args = append(args, "-i")
		}
		cmd := osproc.CommandContext(ctx, "git", append(args, "-e", options.Query)...)
		out, err := cmd.Output()
		var exitErr *exec.ExitError
		switch {
		case err == nil:
			return splitGitPaths(out), true
		case errors.As(err, &exitErr) && exitErr.ExitCode() == 1:
			return nil, true // git grep: совпадений нет
		}
	}
	return f.gitSearchFiles(ctx, dir)
}

func isASCII(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func splitGitPaths(out []byte) []string {
	var files []string
	for _, item := range strings.Split(string(out), string(rune(0))) {
		if item != "" && !strings.HasSuffix(item, "/") {
			files = append(files, item)
		}
	}
	return files
}

// gitSearchFiles — файлы под dir, которые git не игнорирует: отслеживаемые
// и новые. Вне репозитория — false, и поиск обходит папки сам.
func (f *FS) gitSearchFiles(ctx context.Context, dir string) ([]string, bool) {
	cmd := osproc.CommandContext(ctx, "git", "-C", dir, "ls-files", "-z", "-co", "--exclude-standard")
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	var files []string
	for _, item := range strings.Split(string(out), "\x00") {
		if item != "" && !strings.HasSuffix(item, "/") {
			files = append(files, item)
		}
	}
	return files, true
}

// visitGitFiles применяет к списку git те же правила папок, что и обход:
// node_modules, сборка и прочее остаются вне поиска, даже если их закоммитили.
func (f *FS) visitGitFiles(dir string, files []string, visit func(string) error) error {
	skipped := map[string]bool{}
	for _, item := range files {
		current := filepath.Join(dir, filepath.FromSlash(item))
		parent, skip := filepath.Dir(current), false
		for probe := parent; len(probe) > len(dir); probe = filepath.Dir(probe) {
			known, seen := skipped[probe]
			if !seen {
				known = f.skipDirectory(probe, false)
				skipped[probe] = known
			}
			if known {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		if err := visit(current); err != nil {
			return err
		}
	}
	return nil
}

func searchMatcher(options SearchOptions) (func(string) bool, error) {
	if options.Regex {
		pattern := options.Query
		if !options.CaseSensitive {
			pattern = "(?i)" + pattern
		}
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		return compiled.MatchString, nil
	}
	if options.CaseSensitive {
		return func(line string) bool { return strings.Contains(line, options.Query) }, nil
	}
	needle := strings.ToLower(options.Query)
	return func(line string) bool { return strings.Contains(strings.ToLower(line), needle) }, nil
}

// globMatches: шаблон без «/» сверяется с именем файла, со «/» — с путём;
// «**» совпадает с любым числом папок.
func globMatches(pattern, rel string) bool {
	pattern = strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
	if pattern == "" {
		return true
	}
	if !strings.Contains(pattern, "/") {
		ok, _ := path.Match(pattern, path.Base(rel))
		return ok
	}
	return globPathMatch(strings.Split(pattern, "/"), strings.Split(rel, "/"))
}

func globPathMatch(pattern, parts []string) bool {
	for len(pattern) > 0 {
		if pattern[0] == "**" {
			for skip := 0; skip <= len(parts); skip++ {
				if globPathMatch(pattern[1:], parts[skip:]) {
					return true
				}
			}
			return false
		}
		if len(parts) == 0 {
			return false
		}
		if ok, _ := path.Match(pattern[0], parts[0]); !ok {
			return false
		}
		pattern, parts = pattern[1:], parts[1:]
	}
	return len(parts) == 0
}

func clipLine(line string) string {
	if utf8.RuneCountInString(line) <= maxMatchLineRunes {
		return line
	}
	return string([]rune(line)[:maxMatchLineRunes]) + "…"
}
