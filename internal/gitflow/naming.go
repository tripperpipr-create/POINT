package gitflow

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Запасные шаблоны git-агента — когда модели нет или она ответила негодным.
// Имя ветки и сообщение коммита обычно пишет модель Архивариуса (orchestrator),
// здесь — то, что должно сработать всегда и без сети.

var fixPattern = regexp.MustCompile(`(?i)(исправ|почин|ошибк|баг|падает|сломал|не работает|\bfix|\bbug|\bhotfix|regression)`)

// BranchKind — feat или fix по формулировке задачи.
func BranchKind(goal string) string {
	if fixPattern.MatchString(goal) {
		return "fix"
	}
	return "feat"
}

// BranchPrefix — префикс в принятом в репозитории виде: feature/ вместо feat/,
// bugfix/ вместо fix/, если так уже называют ветки.
func BranchPrefix(kind string, prefixes map[string]int) string {
	candidates := map[string][]string{"feat": {"feat", "feature", "features"}, "fix": {"fix", "bugfix", "hotfix"}}[kind]
	if len(candidates) == 0 {
		return kind
	}
	best, bestCount := candidates[0], 0
	for _, candidate := range candidates {
		if count := prefixes[candidate]; count > bestCount {
			best, bestCount = candidate, count
		}
	}
	return best
}

var translit = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e", 'ж': "zh", 'з': "z", 'и': "i", 'й': "y",
	'к': "k", 'л': "l", 'м': "m", 'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u", 'ф': "f",
	'х': "h", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "sch", 'ъ': "", 'ы': "y", 'ь': "", 'э': "e", 'ю': "yu", 'я': "ya",
}

// Slug — латинский слаг из первых слов цели: транслит кириллицы, только
// [a-z0-9] и «-», не длиннее limit.
func Slug(text string, limit int) string {
	var words []string
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			words = append(words, word.String())
			word.Reset()
		}
	}
	for _, r := range strings.ToLower(text) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			word.WriteRune(r)
		case translit[r] != "" || r == 'ъ' || r == 'ь':
			word.WriteString(translit[r])
		default:
			flush()
		}
	}
	flush()
	var out strings.Builder
	for _, w := range words {
		if out.Len() > 0 && out.Len()+1+len(w) > limit {
			break
		}
		if out.Len() > 0 {
			out.WriteByte('-')
		}
		out.WriteString(w)
	}
	result := strings.Trim(out.String(), "-")
	if len(result) > limit {
		result = strings.Trim(result[:limit], "-")
	}
	return result
}

// FallbackBranchName — `<префикс>/<слаг цели>`.
func FallbackBranchName(goal string, prefixes map[string]int) string {
	slug := Slug(goal, 40)
	if slug == "" {
		slug = "point-task"
	}
	return BranchPrefix(BranchKind(goal), prefixes) + "/" + slug
}

// SanitizeBranchName приводит ответ модели к допустимому имени или "".
func SanitizeBranchName(name string) string {
	name = strings.TrimSpace(strings.Trim(strings.TrimSpace(name), "`\"'"))
	name = strings.TrimPrefix(name, "refs/heads/")
	var out strings.Builder
	for _, r := range name {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) || r == '/' || r == '-' || r == '_' || r == '.':
			out.WriteRune(r)
		case unicode.IsSpace(r):
			out.WriteByte('-')
		}
	}
	result := strings.Trim(out.String(), "-./")
	for strings.Contains(result, "//") {
		result = strings.ReplaceAll(result, "//", "/")
	}
	for strings.Contains(result, "..") {
		result = strings.ReplaceAll(result, "..", ".")
	}
	if len(result) > 80 {
		result = strings.Trim(result[:80], "-./")
	}
	return result
}

var conventionalPattern = regexp.MustCompile(`^(feat|fix|chore|docs|refactor|test|tests|perf|ci|build|style|revert)(\([^)]*\))?!?: `)

// CommitStyle — как пишут коммиты в репозитории: Conventional Commits или
// свободно, кириллицей или латиницей.
type CommitStyle struct {
	Conventional bool `json:"conventional"`
	Cyrillic     bool `json:"cyrillic"`
}

func DetectCommitStyle(subjects []string) CommitStyle {
	conventional, cyrillic, total := 0, 0, 0
	for _, subject := range subjects {
		subject = strings.TrimSpace(subject)
		if subject == "" || strings.HasPrefix(subject, "Merge ") {
			continue
		}
		total++
		if conventionalPattern.MatchString(subject) {
			conventional++
		}
		for _, r := range subject {
			if unicode.Is(unicode.Cyrillic, r) {
				cyrillic++
				break
			}
		}
	}
	if total == 0 {
		return CommitStyle{}
	}
	return CommitStyle{Conventional: conventional*10 >= total*3, Cyrillic: cyrillic*2 >= total}
}

// FallbackCommitSubject — первая фраза цели, при Conventional Commits — с
// типом. Не длиннее 72 знаков.
func FallbackCommitSubject(goal string, style CommitStyle) string {
	subject := strings.TrimSpace(strings.SplitN(strings.TrimSpace(goal), "\n", 2)[0])
	if index := strings.IndexAny(subject, ".;:"); index > 20 {
		subject = subject[:index]
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		subject = "Point task"
	}
	if style.Conventional && !conventionalPattern.MatchString(subject) {
		runes := []rune(subject)
		runes[0] = unicode.ToLower(runes[0])
		subject = BranchKind(goal) + ": " + string(runes)
	}
	return clipRunes(subject, 72)
}

// ComposeMessage — заголовок, тело и служебные строки в конце.
func ComposeMessage(subject, body string, trailers map[string]string) string {
	subject = clipRunes(strings.Join(strings.Fields(subject), " "), 100)
	var out strings.Builder
	out.WriteString(subject)
	if body = strings.TrimSpace(body); body != "" {
		out.WriteString("\n\n")
		out.WriteString(body)
	}
	keys := []string{"Point-Quest", "Point-Evidence"}
	first := true
	for _, key := range keys {
		value := strings.TrimSpace(trailers[key])
		if value == "" {
			continue
		}
		if first {
			out.WriteString("\n")
			first = false
		}
		out.WriteString(fmt.Sprintf("\n%s: %s", key, value))
	}
	return out.String()
}

func clipRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}
