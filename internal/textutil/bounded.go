package textutil

import (
	"strings"
	"unicode/utf8"
)

// Обрезка текста жила в семи копиях, и две из них резали по байтам.
//
// `value[:limit]` на русском тексте разрывает руну пополам: в JSON уходит
// невалидный UTF-8, а получатель видит «пустой» хендофф или мусор в конце
// вывода команды. Остальные пять копий резали по рунам правильно, но каждая
// по-своему — с многоточием и без, с обрезкой пробелов и без. Правило одно:
// граница проходит по руне, а лимит означает то, что написано в имени.

// Bounded обрезает строку до limit рун и ставит многоточие, если обрезала.
// Лимит считается в рунах: «не больше limit знаков» — это обещание человеку,
// а не бюджет хранилища.
func Bounded(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "…"
}

// BoundedPlain обрезает строку до limit рун без многоточия. Нужна там, где
// текст идёт не человеку, а в поле с ограниченной длиной.
func BoundedPlain(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}

// BoundedBytes обрезает строку до limit байт, не разрывая руну. Байтовый лимит
// осмыслен там, где ограничивают размер полезной нагрузки: вывод команды,
// хендофф между узлами. Результат всегда валидный UTF-8 и всегда не длиннее
// limit байт.
func BoundedBytes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

// FirstNonEmpty возвращает первое значение, в котором есть что-то кроме
// пробелов, — и возвращает его как есть, не обрезая: вызывающий мог захотеть
// сохранить исходное форматирование вывода команды.
//
// Выбор «показать хоть что-то осмысленное» был написан пятью копиями в пяти
// пакетах, одна из них — с суффиксом V2 только потому, что имя в пакете уже
// заняли.
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
