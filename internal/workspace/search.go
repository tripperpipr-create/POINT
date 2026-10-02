package workspace

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
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
	query := options.Query
	if strings.TrimSpace(query) == "" {
		return nil, false, errors.New("search query is empty")
	}
	limit := options.MaxResults
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	contextLines := min(max(options.Context, 0), 5)
	match, err := searchMatcher(options)
	if err != nil {
		return nil, false, err
	}
	root := f.root
	if scope := strings.Trim(strings.ReplaceAll(options.Path, "\\", "/"), "/"); scope != "" {
		resolved, resolveErr := f.Resolve(scope, false)
		if resolveErr != nil {
			return nil, false, resolveErr
		}
		root = resolved
	}
	matches := make([]ContextMatch, 0)
	truncated := false
	walkErr := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if current != root && f.skipDirectory(current, false) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || IsSensitive(entry.Name()) {
			return nil
		}
		rel, _ := filepath.Rel(f.root, current)
		rel = filepath.ToSlash(rel)
		if !globMatches(options.Glob, rel) {
			return nil
		}
		content, readErr := f.Read(rel, false)
		if readErr != nil {
			return nil
		}
		lines := splitContentLines(content.Content)
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
	})
	if errors.Is(walkErr, errLimitReached) {
		walkErr = nil
	}
	return matches, truncated, walkErr
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
