package tools

import "strings"

// MatchFileLineEndings переводит якорь точной правки в концы строк файла,
// когда присланный вариант в файле не встречается, а переведённый встречается
// ровно один раз. Замена переводится вместе с ним, чтобы правка не оставила
// в CRLF-файле строки с LF. Файл со смешанными концами строк получает перевод
// только при однозначном совпадении — иначе якорь остаётся как есть и честно
// не находится.
func MatchFileLineEndings(content, oldText, newText string) (string, string) {
	if oldText == "" || strings.Contains(content, oldText) || !strings.Contains(oldText, "\n") {
		return oldText, newText
	}
	var convert func(string) string
	switch {
	case strings.Contains(content, "\r\n") && !strings.Contains(oldText, "\r\n"):
		convert = func(value string) string {
			return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\n", "\r\n")
		}
	case !strings.Contains(content, "\r\n") && strings.Contains(oldText, "\r\n"):
		convert = func(value string) string { return strings.ReplaceAll(value, "\r\n", "\n") }
	default:
		return oldText, newText
	}
	converted := convert(oldText)
	if strings.Count(content, converted) != 1 {
		return oldText, newText
	}
	return converted, convert(newText)
}
