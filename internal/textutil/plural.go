// Package textutil держит правила русского языка, общие для всего ядра.
//
// Склонение по числу было в двух местах сразу (orchestrator/policy.go и
// orchestrator/chat.go), а в третьем — в очереди решений — его не было вовсе:
// набор из одного файла описывался как «1 файлов». Одно правило языка не может
// жить в трёх копиях: расходятся не формулировки, а поведение.
package textutil

import "strconv"

// Plural выбирает форму слова по числу: 1 файл, 2 файла, 5 файлов.
func Plural(n int, one, few, many string) string {
	if n < 0 {
		n = -n
	}
	switch {
	case n%100 >= 11 && n%100 <= 14:
		return many
	case n%10 == 1:
		return one
	case n%10 >= 2 && n%10 <= 4:
		return few
	default:
		return many
	}
}

// Count возвращает число вместе с подходящей формой: «1 файл», «2 файла».
func Count(n int, one, few, many string) string {
	return strconv.Itoa(n) + " " + Plural(n, one, few, many)
}
