// Разбор текста: токены, идентификаторы, символы, язык файла.
package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// indexedContentDigest — отпечаток прочитанного содержимого. У целого файла это
// отпечаток файла и есть; у обрезанного — отпечаток той части, которая попала
// в индекс.
func indexedContentDigest(content FileContent) string {
	if validIndexSHA256(content.SHA256) {
		return strings.ToLower(content.SHA256)
	}
	digest := sha256.Sum256([]byte(content.Content))
	return hex.EncodeToString(digest[:])
}

func validIndexSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}

func scanLines(value string) []string {
	if value == "" {
		return nil
	}
	lines := strings.SplitAfter(value, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func expandedTokens(value string) []string {
	seen := map[string]bool{}
	result := make([]string, 0)
	add := func(value string) {
		value = strings.ToLower(strings.Trim(value, "_"))
		if len([]rune(value)) < 3 || seen[value] {
			return
		}
		seen[value] = true
		result = append(result, value)
	}
	parts := strings.FieldsFunc(value, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' })
	for _, part := range parts {
		add(part)
		for _, segment := range strings.FieldsFunc(part, func(r rune) bool { return r == '_' }) {
			for _, identifierPart := range identifierParts(segment) {
				add(identifierPart)
			}
		}
	}
	return result
}

func identifierParts(value string) []string {
	runes := []rune(value)
	if len(runes) == 0 {
		return nil
	}
	result := make([]string, 0, 4)
	start := 0
	for index := 1; index < len(runes); index++ {
		previous, current := runes[index-1], runes[index]
		nextIsLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
		boundary := unicode.IsLower(previous) && unicode.IsUpper(current)
		boundary = boundary || (unicode.IsUpper(previous) && unicode.IsUpper(current) && nextIsLower)
		boundary = boundary || (unicode.IsLetter(previous) && unicode.IsDigit(current)) || (unicode.IsDigit(previous) && unicode.IsLetter(current))
		if boundary {
			result = append(result, string(runes[start:index]))
			start = index
		}
	}
	result = append(result, string(runes[start:]))
	return result
}

func compactToken(value string) string {
	var result strings.Builder
	for _, char := range strings.ToLower(value) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			result.WriteRune(char)
		}
	}
	return result.String()
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func extractSymbols(lines []string) []string {
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		prefixes := []string{"func ", "type ", "class ", "interface ", "def ", "function ", "export function ", "export class "}
		for _, prefix := range prefixes {
			if strings.HasPrefix(trimmed, prefix) {
				value := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
				if cut := strings.IndexAny(value, "({:< =\t"); cut >= 0 {
					value = value[:cut]
				}
				if value != "" {
					result = append(result, value)
				}
				break
			}
		}
	}
	return result
}

var languageByExt = map[string]string{
	".go": "Go", ".js": "JavaScript", ".mjs": "JavaScript", ".cjs": "JavaScript",
	".ts": "TypeScript", ".tsx": "TypeScript", ".jsx": "JavaScript",
	".py": "Python", ".rs": "Rust", ".java": "Java", ".kt": "Kotlin", ".cs": "C#",
	".cpp": "C++", ".c": "C", ".h": "C/C++", ".rb": "Ruby", ".php": "PHP",
	".sql": "SQL", ".md": "Markdown", ".json": "JSON", ".yaml": "YAML", ".yml": "YAML",
}

func languageFor(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if value := languageByExt[ext]; value != "" {
		return value
	}
	if ext == "" {
		return "Text"
	}
	return strings.TrimPrefix(ext, ".")
}
