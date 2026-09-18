// Package modeljson достаёт JSON из сырого ответа модели.
//
// Разбор был написан четыре раза — для обучения, компаньона, Мастера и приёмки
// задачи, — и все четыре расходились в том, что именно они снимают. Ограду кода
// снимали все, а блок размышления `<think>…</think>` — только приёмка задачи.
// Для рассуждающей модели это не мелочь: разбор Мастера искал первую `{` по
// всему тексту и находил её внутри размышления, после чего «модель ответила
// не по схеме» показывалось человеку на совершенно исправном ответе.
//
// Здесь одно правило на всё ядро. Проверку самого конверта каждый вызывающий
// по-прежнему делает сам: у него своя схема и свои слова для отказа.
package modeljson

import (
	"errors"
	"strings"
)

// ErrFence — ограда кода открыта и не закрыта. Отличается от «невалидного
// JSON»: текст мог быть оборван на полуслове, и вызывающий вправе сказать об
// этом своими словами.
var ErrFence = errors.New("modeljson: unterminated code fence")

// Payload снимает с ответа модели размышление и ограду кода.
func Payload(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	// Блок размышления закрывается последним встреченным тегом: модель может
	// упомянуть `</think>` внутри рассуждения, но структура ответа идёт после
	// настоящего закрытия.
	if index := strings.LastIndex(text, "</think>"); index >= 0 {
		text = strings.TrimSpace(text[index+len("</think>"):])
	}
	if !strings.HasPrefix(text, "```") {
		return text, nil
	}
	firstBreak := strings.IndexByte(text, '\n')
	lastFence := strings.LastIndex(text, "```")
	if firstBreak < 0 || lastFence <= firstBreak {
		return text, ErrFence
	}
	return strings.TrimSpace(text[firstBreak+1 : lastFence]), nil
}

// Braces сужает текст до внешней пары фигурных скобок. Нужна там, где модель
// дописывает пояснение до или после структуры. Вызывается уже после Payload —
// иначе первая `{` найдётся внутри размышления.
func Braces(text string) (string, bool) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return text, false
	}
	return text[start : end+1], true
}
